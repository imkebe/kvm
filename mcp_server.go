package kvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jetkvm/kvm/internal/hostconsole"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The KVM's own MCP server: streamable HTTP at /mcp, with the tools of
// tools/hostconsole-mcp (internal/hostconsole). An MCP client needs only the
// URL and an API token; nothing runs on the client's computer.
//
// Off by default (Settings > MCP). A request needs an API token with the
// device or the terminal scope as a bearer token. Each MCP session gets its
// own server, bound to its token (the SDK refuses the session to another
// token), and its own shells, which end with the session. The tools call the
// device API and the terminal API through an in-memory connection to a small
// router with those routes, sending the client's token, so the token's scopes
// and every refusal of those APIs apply unchanged; the APIs' own switches
// (Device API, Terminal API) still decide what the tools may do.

const (
	mcpServerPath   = "/mcp"
	mcpServerOff    = "The MCP server is off (Settings > MCP)"
	mcpSessionIdle  = 30 * time.Minute
	mcpRequestLimit = 4 << 20
	// mcpSessionMax bounds the memory open sessions (and their shells) take;
	// a new session above it closes the least recently used one.
	mcpSessionMax = 16
)

var (
	mcpMu      sync.Mutex
	mcpHandler http.Handler
	// mcpClosers end the shells of the MCP sessions that are open: one per
	// session that has handled a request (the status popover counts them).
	mcpClosers = map[*mcpSessionShells]struct{}{}
	// mcpProbe answers the SDK's per-request server lookup for requests of
	// existing sessions (it only reads the protocol versions).
	mcpProbeOnce sync.Once
	mcpProbe     *mcp.Server

	mcpLoopOnce sync.Once
	mcpLoop     *memListener
)

// ss and used are set and read under mcpMu.
type mcpSessionShells struct {
	close func()
	ss    *mcp.ServerSession
	used  time.Time
}

func rpcGetMCPServerEnabled() (bool, error) {
	return config.MCPServerEnabled, nil
}

func rpcSetMCPServerEnabled(enabled bool) error {
	if config.MCPServerEnabled == enabled {
		return nil
	}
	config.MCPServerEnabled = enabled
	if err := SaveConfig(); err != nil {
		config.MCPServerEnabled = !enabled
		return fmt.Errorf("failed to save config: %w", err)
	}
	if !enabled {
		mcpDropSessions()
	}
	return nil
}

// mcpDropSessions forgets every MCP session and ends their shells.
func mcpDropSessions() {
	mcpMu.Lock()
	closers := mcpClosers
	mcpClosers = map[*mcpSessionShells]struct{}{}
	mcpHandler = nil
	mcpMu.Unlock()
	for c := range closers {
		c.close()
	}
}

// mcpVerifyToken accepts an API token with the device or the terminal scope.
func mcpVerifyToken(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	t, err := checkAPIToken(token)
	if err != nil {
		warnAPITokenRefused(r.RemoteAddr, token, err)
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}
	if !slices.Contains(t.Scopes, APIScopeDevice) && !slices.Contains(t.Scopes, APIScopeTerminal) {
		return nil, fmt.Errorf("%w: the API token %q has neither the device nor the terminal scope", auth.ErrInvalidToken, t.Name)
	}
	info := &auth.TokenInfo{UserID: t.ID, Scopes: t.Scopes, Extra: map[string]any{"token": token}}
	if t.ExpiresAt != 0 {
		info.Expiration = time.Unix(t.ExpiresAt, 0)
	}
	return info, nil
}

// mcpServerFor makes the server of a new MCP session. The SDK calls it for
// every request, also to read the protocol versions before it looks up an
// existing session, so it must not count anything itself: a session is
// counted when it handles its first request (mcpTrackSession). Requests of
// existing sessions get one shared server for that lookup.
func mcpServerFor(r *http.Request) *mcp.Server {
	info := auth.TokenInfoFromContext(r.Context())
	if info == nil {
		return nil
	}
	if r.Header.Get("Mcp-Session-Id") != "" {
		mcpProbeOnce.Do(func() {
			mcpProbe, _ = hostconsole.New(hostconsole.Options{Version: builtAppVersion})
		})
		return mcpProbe
	}
	token, _ := info.Extra["token"].(string)
	m, closeShells := hostconsole.New(hostconsole.Options{
		Host:      r.Host,
		Token:     token,
		Transport: mcpLoopTransport(r.RemoteAddr),
		Version:   builtAppVersion,
	})
	m.AddReceivingMiddleware(mcpTrackSession(&mcpSessionShells{close: closeShells}))
	return m
}

// mcpTrackSession counts the session from its first request and ends its
// shells when it closes: on DELETE, after mcpSessionIdle without requests, or
// when the client goes.
func mcpTrackSession(shells *mcpSessionShells) mcp.Middleware {
	var once sync.Once
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if ss, ok := req.GetSession().(*mcp.ServerSession); ok {
				mcpMu.Lock()
				shells.used = time.Now()
				mcpMu.Unlock()
				once.Do(func() {
					// A newer client's server/discover gets a session of
					// its own that ends at once: not counted.
					counted := method == "initialize"
					if counted {
						mcpMu.Lock()
						// Clients that die without a DELETE hold a place until
						// mcpSessionIdle: make room instead of refusing.
						var oldest *mcpSessionShells
						if len(mcpClosers) >= mcpSessionMax {
							for c := range mcpClosers {
								if oldest == nil || c.used.Before(oldest.used) {
									oldest = c
								}
							}
							delete(mcpClosers, oldest)
						}
						shells.ss = ss
						mcpClosers[shells] = struct{}{}
						mcpMu.Unlock()
						if oldest != nil {
							go oldest.ss.Close()
						}
					}
					go func() {
						_ = ss.Wait()
						if counted {
							mcpMu.Lock()
							delete(mcpClosers, shells)
							mcpMu.Unlock()
						}
						shells.close()
					}()
				})
			}
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			started := time.Now()
			result, err := next(ctx, method, req)
			entry := APICallEntry{At: started.UnixMilli(), Source: apiCallSourceMCP, DurationMs: sinceMs(started), OK: err == nil}
			if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil {
				entry.Name = params.Name
				entry.Args = summarizeAPIArgs(params.Arguments)
			}
			if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
				entry.Token = apiTokenName(extra.TokenInfo.UserID)
			}
			if err != nil {
				entry.Error = shorten(err.Error(), apiCallErrorMax)
			} else if res, ok := result.(*mcp.CallToolResult); ok && res != nil && res.IsError {
				entry.OK = false
				for _, content := range res.Content {
					if text, ok := content.(*mcp.TextContent); ok {
						entry.Error = shorten(text.Text, apiCallErrorMax)
						break
					}
				}
			}
			recordAPICall(entry)
			return result, err
		}
	}
}

func mcpCurrentHandler() http.Handler {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	if mcpHandler == nil {
		h := mcp.NewStreamableHTTPHandler(mcpServerFor, &mcp.StreamableHTTPOptions{
			SessionTimeout:      mcpSessionIdle,
			MaxRequestBodyBytes: mcpRequestLimit,
		})
		mcpHandler = auth.RequireBearerToken(mcpVerifyToken, &auth.RequireBearerTokenOptions{
			AllowMissingExpiration: true,
		})(h)
	}
	return mcpHandler
}

func handleMCPServer(c *gin.Context) {
	if !config.MCPServerEnabled {
		sendErrorJsonThenAbort(c, http.StatusForbidden, mcpServerOff)
		return
	}
	// A web page of another site must not drive the KVM through a browser
	// that holds no token anyway; programs send no Origin.
	if !apiOriginAllowed(c.Request) {
		sendErrorJsonThenAbort(c, http.StatusForbidden, "Requests from other origins are refused")
		return
	}
	mcpCurrentHandler().ServeHTTP(c.Writer, c.Request)
}

// --- In-memory connection to the APIs -------------------------------------

// mcpLoopRouter has the routes the tools call, with the same middleware as
// the web server's.
func mcpLoopRouter() *gin.Engine {
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())
	// Calls here come from MCP tools, for the API call log.
	r.Use(func(c *gin.Context) { c.Set(viaMCPKey, true) })
	terminalAuth := apiAuthMiddleware(APIScopeTerminal, "JetKVM terminal API")
	r.GET(osServicesTerminalWSPath, terminalAuth, handleOSServicesTerminalWebSocket)
	r.GET(osServicesTerminalStatusPath, terminalAuth, handleOSServicesTerminalStatus)
	r.POST(deviceAPIPath, apiAuthMiddleware(APIScopeDevice, "JetKVM device API"), handleDeviceAPI)
	return r
}

// mcpLoopTransport reaches mcpLoopRouter in memory. The router sees the MCP
// client's address as the remote address, for its logs.
func mcpLoopTransport(remote string) http.RoundTripper {
	mcpLoopOnce.Do(func() {
		mcpLoop = newMemListener()
		srv := &http.Server{Handler: mcpLoopRouter(), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(mcpLoop) }()
	})
	return mcpLoopRemote{remoteAddr(remote)}
}

type mcpLoopRemoteKey struct{}

// mcpLoopShared is the one transport of every session. Each request dials its
// own pipe, which carries its client's address, so no connection stays idle.
var mcpLoopShared = &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		addr, ok := ctx.Value(mcpLoopRemoteKey{}).(net.Addr)
		if !ok {
			addr = memAddr("mcp-loop")
		}
		return mcpLoop.dial(ctx, addr)
	},
	DisableKeepAlives: true,
}

// mcpLoopRemote sends a session's requests through mcpLoopShared.
type mcpLoopRemote struct{ addr net.Addr }

func (m mcpLoopRemote) RoundTrip(r *http.Request) (*http.Response, error) {
	return mcpLoopShared.RoundTrip(r.WithContext(context.WithValue(r.Context(), mcpLoopRemoteKey{}, m.addr)))
}

type memListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr("mcp-loop") }

func (l *memListener) dial(ctx context.Context, remote net.Addr) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- &memConn{Conn: server, remote: remote}:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-l.closed:
		client.Close()
		server.Close()
		return nil, errors.New("the MCP loop is closed")
	}
}

type memAddr string

func (a memAddr) Network() string { return "memory" }
func (a memAddr) String() string  { return string(a) }

// memConn is the router's side of a pipe, with the MCP client's address.
type memConn struct {
	net.Conn
	remote net.Addr
}

func (c *memConn) RemoteAddr() net.Addr { return c.remote }

func remoteAddr(hostport string) net.Addr {
	if a, err := net.ResolveTCPAddr("tcp", hostport); err == nil {
		return a
	}
	return memAddr("mcp-loop")
}
