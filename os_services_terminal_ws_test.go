package kvm

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsSinkPair serves one WebSocket whose server side is a wsTerminalSink,
// and returns the sink and the client side.
func wsSinkPair(t *testing.T) (*wsTerminalSink, *websocket.Conn, context.CancelFunc) {
	t.Helper()
	sinks := make(chan *wsTerminalSink, 1)
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		sink := newWSTerminalSink(conn)
		sinks <- sink
		sink.run(ctx)
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client.SetReadLimit(1 << 22)
	t.Cleanup(func() { client.CloseNow() })
	return <-sinks, client, cancel
}

// A deleted token ends the shells opened with it, and only those.
func TestTerminalAPIEndsWithItsToken(t *testing.T) {
	previousConfig, previousPath := config, configPath
	config, configPath = &Config{}, t.TempDir()+"/kvm_config.json"
	t.Cleanup(func() { config, configPath = previousConfig, previousPath })
	s := useTestOSServices(t)
	created, err := rpcCreateAPIToken("x", []string{APIScopeTerminal}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gone, client, cancel := wsSinkPair(t)
	defer cancel()
	kept, _, cancelKept := wsSinkPair(t)
	defer cancelKept()
	s.terminals[1] = &guestTerminal{id: 1, channel: gone, api: true, token: created.Info.ID}
	s.terminals[2] = &guestTerminal{id: 2, channel: kept, api: true, token: "other"}

	if err := rpcDeleteAPIToken(created.Info.ID); err != nil {
		t.Fatal(err)
	}
	if _, data := read(t, client); !bytes.Contains(data, []byte("token was deleted")) {
		t.Fatalf("closing message %q", data)
	}
	if _, ok := s.terminals[1]; ok {
		t.Fatal("the deleted token's shell is still open")
	}
	if _, ok := s.terminals[2]; !ok {
		t.Fatal("another token's shell was closed")
	}
}

func read(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return kind, data
}

func TestWSTerminalSinkKeepsOrderAndEndsWithTheReason(t *testing.T) {
	sink, client, cancel := wsSinkPair(t)
	defer cancel()
	sink.sendText([]byte(`{"type":"opened","id":1}`))
	buf := []byte("hello")
	if !sink.Send(buf) {
		t.Fatal("an empty sink takes output")
	}
	copy(buf, "XXXXX") // the session reuses its buffer
	sink.Close("The shell exited")
	sink.Send([]byte("after close")) // dropped

	if kind, data := read(t, client); kind != websocket.MessageText || !bytes.Contains(data, []byte("opened")) {
		t.Fatalf("first message %v %q, want the opened text", kind, data)
	}
	if kind, data := read(t, client); kind != websocket.MessageBinary || string(data) != "hello" {
		t.Fatalf("second message %v %q, want the output as it was sent", kind, data)
	}
	if kind, data := read(t, client); kind != websocket.MessageText || string(data) != `{"reason":"The shell exited","type":"closed"}` {
		t.Fatalf("third message %v %q, want the closed text", kind, data)
	}
	ctx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	if _, _, err := client.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("after closed: %v, want a normal close", err)
	}
	<-sink.done
}

func TestWSTerminalSinkHoldsOutputWhileTheClientIsSlow(t *testing.T) {
	sink, client, cancel := wsSinkPair(t)
	defer cancel()
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	// The client reads nothing, so the queue grows past the limit once the
	// socket's own buffers are full.
	held := false
	sent := 0
	for i := 0; i < 1000 && !held; i++ {
		if sink.Send(chunk) {
			sent += len(chunk)
		} else {
			held = true
		}
	}
	if !held {
		t.Fatal("a client that reads nothing never held the output")
	}
	// The writer may take one more message off the queue meanwhile.
	if b := sink.Buffered(); b < guestTerminalBuffered-len(chunk) {
		t.Fatalf("held at %d buffered, want about %d", b, guestTerminalBuffered)
	}
	// Reading everything empties the queue, and the output arrives whole.
	got := 0
	for got < sent {
		_, data := read(t, client)
		got += len(data)
	}
	if got != sent {
		t.Fatalf("read %d bytes, want %d", got, sent)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sink.Buffered() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if b := sink.Buffered(); b != 0 {
		t.Fatalf("%d still buffered after the client read everything", b)
	}
	if !sink.Send(chunk) {
		t.Fatal("a drained sink takes output again")
	}
}

func TestWSTerminalSinkStopsWhenTheClientLeaves(t *testing.T) {
	sink, client, cancel := wsSinkPair(t)
	defer cancel()
	client.CloseNow()
	cancel()
	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer did not stop")
	}
	if !sink.Send([]byte("late")) {
		// A closed sink takes and drops output, so the session never holds it.
		t.Fatal("a stopped sink refused output")
	}
}
