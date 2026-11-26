package kvm

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// Frames of different sizes and a text message in between all arrive whole
// through the one reused read buffer.
func TestWebSocketIngestReusesBuffer(t *testing.T) {
	saved := virtualVideo
	defer func() { virtualVideo = saved }()
	virtualVideo = newVirtualVideoManager()
	virtualVideo.config.Enabled = true
	virtualVideo.device = &readVideoOutput{}

	engine := gin.New()
	engine.GET(virtualVideoWebSocketPath, handleVirtualVideoWebSocket)
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+virtualVideoWebSocketPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	jpeg := func(n int) []byte { return append([]byte{0xff, 0xd8}, make([]byte, n)...) }
	for _, msg := range []struct {
		kind websocket.MessageType
		data []byte
	}{
		{websocket.MessageBinary, makeVirtualVideoTestFrame(jpeg(5000))},
		{websocket.MessageText, []byte("ignored")},
		{websocket.MessageBinary, makeVirtualVideoTestFrame(jpeg(10))},
	} {
		if err := conn.Write(ctx, msg.kind, msg.data); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if state := virtualVideo.State(); state.FramesWritten == 2 {
			if state.BytesWritten != 5002+12 || state.LastError != "" {
				t.Fatalf("bytes %d, error %q", state.BytesWritten, state.LastError)
			}
			return
		}
	}
	t.Fatalf("state %+v", virtualVideo.State())
}
