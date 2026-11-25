package kvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer adds a token to every request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func setupMCPServerTest(t *testing.T, enabled bool) (*httptest.Server, map[string]string) {
	t.Helper()
	previousConfig := config
	previousPath := configPath
	config = &Config{LocalAuthMode: "noPassword", DeviceAPIEnabled: true, MCPServerEnabled: enabled}
	configPath = t.TempDir() + "/kvm_config.json"
	t.Cleanup(func() {
		mcpDropSessions()
		config = previousConfig
		configPath = previousPath
	})
	tokens := map[string]string{}
	for name, scopes := range map[string][]string{
		"device":   {APIScopeDevice},
		"device2":  {APIScopeDevice},
		"metrics":  {APIScopeMetrics},
		"terminal": {APIScopeTerminal},
	} {
		created, err := rpcCreateAPIToken(name, scopes, 0)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = created.Token
	}
	srv := httptest.NewServer(setupRouter())
	t.Cleanup(srv.Close)
	return srv, tokens
}

func connectMCP(t *testing.T, url, token string) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   url + mcpServerPath,
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
	}, nil)
}

func TestMCPServerToolsThroughTheDeviceAPI(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, true)
	cs, err := connectMCP(t, srv.URL, tokens["device"])
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"kvm_status", "kvm_rpc", "console_run", "host_status"} {
		if !names[want] {
			t.Fatalf("tool %s missing: %v", want, names)
		}
	}

	// ping goes through the in-memory connection to POST /api/rpc.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "kvm_rpc", Arguments: map[string]any{"method": "ping"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(toolText(res), "pong") {
		t.Fatalf("ping: error %v, %q", res.IsError, toolText(res))
	}

	// The device API's own refusals apply.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "kvm_rpc", Arguments: map[string]any{"method": "setMCPServerEnabled", "params": map[string]any{"enabled": false}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(toolText(res), "does not run") {
		t.Fatalf("refused method: error %v, %q", res.IsError, toolText(res))
	}

	// A device token has no terminal scope: the terminal API says so.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "host_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(toolText(res), "terminal scope") {
		t.Fatalf("host_status: error %v, %q", res.IsError, toolText(res))
	}

	// The Device API switch still decides.
	config.DeviceAPIEnabled = false
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "kvm_rpc", Arguments: map[string]any{"method": "ping"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(toolText(res), "device API is off") {
		t.Fatalf("device API off: error %v, %q", res.IsError, toolText(res))
	}
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A terminal token reaches the terminal API in memory; without OS Services it
// says so as a tool error.
func TestMCPServerTerminalStatus(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, true)
	cs, err := connectMCP(t, srv.URL, tokens["terminal"])
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "host_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host_status: error %v, %q", res.IsError, toolText(res))
	if strings.Contains(toolText(res), "scope") || strings.Contains(toolText(res), "refused the API token") {
		t.Fatalf("the token was refused: %q", toolText(res))
	}
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "console_run", Arguments: map[string]any{"command": "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("console_run: error %v, %q", res.IsError, toolText(res))
	if !res.IsError {
		t.Fatal("console_run worked without OS Services")
	}
}

func TestMCPServerRefusals(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, true)
	post := func(token, sessionID string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+mcpServerPath, strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if resp := post("", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp := post("jkvm_abcdefgh_wrong", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp := post(tokens["metrics"], ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics-only token: %d", resp.StatusCode)
	}

	// A session belongs to the token that opened it.
	cs, err := connectMCP(t, srv.URL, tokens["device"])
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if resp := post(tokens["device2"], cs.ID()); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other token on the session: %d", resp.StatusCode)
	}

	// Switched off: refused, and the sessions are gone.
	if err := rpcSetMCPServerEnabled(false); err != nil {
		t.Fatal(err)
	}
	if resp := post(tokens["device"], ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("server off: %d", resp.StatusCode)
	}
	if err := rpcSetMCPServerEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(context.Background(), nil); err == nil {
		t.Fatal("a session survived switching the server off")
	}
}

func TestMCPServerIsOffByDefault(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, false)
	if _, err := connectMCP(t, srv.URL, tokens["device"]); err == nil {
		t.Fatal("connected to a server that is off")
	}
}

// The in-memory connection carries a WebSocket (the terminal API's) and
// shows the client's address to the handler.
func TestMCPLoopWebSocket(t *testing.T) {
	l := newMemListener()
	defer l.Close()
	var remote string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote = r.RemoteAddr
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, append([]byte("echo "), data...))
		}
	})}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()

	// Through the transport every MCP session shares.
	mcpLoopTransport("")
	previous := mcpLoop
	mcpLoop = l
	defer func() { mcpLoop = previous }()
	client := &http.Client{Transport: mcpLoopRemote{remoteAddr("192.0.2.7:4242")}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://192.168.2.16/os-services/terminal/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "echo hi" {
		t.Fatalf("read %q %v", data, err)
	}
	if remote != "192.0.2.7:4242" {
		t.Fatalf("remote address %q", remote)
	}
}

// At most mcpSessionMax sessions are open: a new one closes the least recently
// used, and a closed one frees its place. The
// shared loop transport still shows each client's address to the device API.
func TestMCPSessionCap(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, true)
	count := func() int {
		mcpMu.Lock()
		defer mcpMu.Unlock()
		return len(mcpClosers)
	}
	sessions := make([]*mcp.ClientSession, 0, mcpSessionMax)
	for range mcpSessionMax {
		cs, err := connectMCP(t, srv.URL, tokens["device"])
		if err != nil {
			t.Fatalf("session %d: %v", len(sessions)+1, err)
		}
		defer cs.Close()
		sessions = append(sessions, cs)
	}
	// Touch every session but the first, so the first is the least recently used.
	ctx := context.Background()
	for _, cs := range sessions[1:] {
		if _, err := cs.ListTools(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	extra, err := connectMCP(t, srv.URL, tokens["device"])
	if err != nil {
		t.Fatalf("session %d was refused: %v", mcpSessionMax+1, err)
	}
	defer extra.Close()
	if n := count(); n > mcpSessionMax {
		t.Fatalf("%d sessions counted, the cap is %d", n, mcpSessionMax)
	}

	res, err := extra.CallTool(ctx, &mcp.CallToolParams{Name: "kvm_rpc", Arguments: map[string]any{"method": "ping"}})
	if err != nil || res.IsError {
		t.Fatalf("ping: %v %q", err, toolText(res))
	}
	remote := ""
	for _, e := range rpcGetApiCallLog().Entries {
		if e.Source == apiCallSourceAPI && e.Name == "ping" {
			remote = e.Remote
		}
	}
	if remote != "127.0.0.1" {
		t.Fatalf("the device API saw ping from %q, want 127.0.0.1", remote)
	}
	if _, err := sessions[1].ListTools(ctx, nil); err != nil {
		t.Fatalf("a recently used session was closed: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := sessions[0].ListTools(ctx, nil); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the least recently used session was not closed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = extra.Close()
	for count() != mcpSessionMax-1 {
		if time.Now().After(deadline) {
			t.Fatalf("a closed session did not free its place: %d counted", count())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// One session counts as one, however many requests it makes; the SDK asks
// for a server on every request, which once counted each of them.
func TestMCPSessionCount(t *testing.T) {
	srv, tokens := setupMCPServerTest(t, true)
	count := func() int {
		mcpMu.Lock()
		defer mcpMu.Unlock()
		return len(mcpClosers)
	}
	cs, err := connectMCP(t, srv.URL, tokens["device"])
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 5 {
		if _, err := cs.ListTools(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := cs.Ping(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(); got != 1 {
		t.Fatalf("sessions = %d after 10 requests of one session, want 1", got)
	}

	cs2, err := connectMCP(t, srv.URL, tokens["device"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs2.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 2 {
		t.Fatalf("sessions = %d with two clients, want 2", got)
	}

	_ = cs.Close()
	_ = cs2.Close()
	deadline := time.Now().Add(5 * time.Second)
	for count() != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := count(); got != 0 {
		t.Fatalf("sessions = %d after both closed, want 0", got)
	}
}
