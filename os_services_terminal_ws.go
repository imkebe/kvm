package kvm

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jetkvm/kvm/internal/osservices"
)

// The terminal API: a shell on the target computer over a WebSocket, for
// programs such as the hostconsole MCP server. Unlike the browser's
// "guest-terminal" data channel it needs no WebRTC session, so it does not
// take the browser's place. It is off until the OS Services setting
// terminal_api_enabled is on, and it shares the sender's four shells with
// the browser.
//
// One WebSocket is one terminal session:
//
//   - server to client: text {"type":"opened","id":N,"os":"windows|macos|linux|unknown"}
//     first; then binary messages of shell output; at the end text
//     {"type":"closed","reason":"…"}, then the close.
//   - client to server: binary messages are keystrokes; text
//     {"type":"resize","rows":R,"cols":C} resizes; closing the WebSocket
//     closes the terminal.
//
// GET /os-services/terminal/ws?rows=R&cols=C opens it (rows and cols
// optional), and GET /os-services/terminal/status reports whether it can.
// Both take the session cookie or the KVM password as HTTP basic auth
// (web.go); signing in would replace the browser's token.
const (
	osServicesTerminalWSPath     = "/os-services/terminal/ws"
	osServicesTerminalStatusPath = "/os-services/terminal/status"
	// terminalWSReadLimit bounds one keystroke message.
	terminalWSReadLimit = 1 << 20
	// terminalWSWriteTimeout: a client that takes no output for this long
	// is dropped.
	terminalWSWriteTimeout = 15 * time.Second
	terminalAPIOff         = "The terminal API is off in the OS Services settings"
)

// The largest terminal a client of the terminal API may ask for.
const terminalAPIRowsMax, terminalAPIColsMax = 500, 1000

type terminalStatusJSON struct {
	// APIEnabled: the terminal_api_enabled setting.
	APIEnabled bool `json:"apiEnabled"`
	// Enabled: OS Services is on.
	Enabled         bool   `json:"enabled"`
	TerminalEnabled bool   `json:"terminalEnabled"`
	Connected       bool   `json:"connected"`
	ApprovalPending bool   `json:"approvalPending"`
	Available       bool   `json:"available"`
	OS              string `json:"os"`
	SenderVersion   string `json:"senderVersion,omitempty"`
	// Sessions open on this KVM, the browser's included, of Max.
	Sessions    int `json:"sessions"`
	APISessions int `json:"apiSessions"`
	Max         int `json:"max"`
}

func handleOSServicesTerminalStatus(c *gin.Context) {
	s := osServices
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "This KVM has no OS Services"})
		return
	}
	cfg := s.config()
	status := terminalStatusJSON{
		APIEnabled:      cfg.TerminalAPIEnabled,
		Enabled:         cfg.Enabled,
		TerminalEnabled: cfg.TerminalEnabled,
		Max:             osservices.TerminalSessionsMax,
	}
	s.do(func(session *osservices.Session) {
		st := session.State()
		status.Connected = st.Connected
		status.ApprovalPending = st.ApprovalPending
		status.Available = st.Terminal
		status.OS = st.ClipboardOS.String()
		if st.SenderInfo {
			status.SenderVersion = st.SenderVersion
		}
		status.Sessions = len(s.terminals)
		for _, t := range s.terminals {
			if t.api {
				status.APISessions++
			}
		}
	})
	c.JSON(http.StatusOK, status)
}

func handleOSServicesTerminalWebSocket(c *gin.Context) {
	s := osServices
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "This KVM has no OS Services"})
		return
	}
	if !s.config().TerminalAPIEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": terminalAPIOff})
		return
	}
	rows, _ := strconv.ParseUint(c.Query("rows"), 10, 16)
	cols, _ := strconv.ParseUint(c.Query("cols"), 10, 16)
	rows, cols = min(rows, terminalAPIRowsMax), min(cols, terminalAPIColsMax)
	// Accept refuses a browser page of another origin, so a web page cannot
	// open a shell through a KVM without a password. Programs send no Origin.
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		osServicesLogger.Warn().Err(err).Msg("failed to accept terminal API websocket")
		return
	}
	conn.SetReadLimit(terminalWSReadLimit)
	remote := c.ClientIP()
	started := time.Now()
	logShell := func(errText string) {
		recordAPICall(APICallEntry{
			At: started.UnixMilli(), Source: apiCallSourceTerminal, Name: "shell",
			DurationMs: sinceMs(started), OK: errText == "", Error: shorten(errText, apiCallErrorMax),
			Token: apiTokenName(c.GetString("apiToken")), Remote: remote, ViaMCP: c.GetBool(viaMCPKey),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if expires := c.GetInt64(apiTokenExpiresKey); expires != 0 {
		// The session ends when its token expires.
		var stop context.CancelFunc
		ctx, stop = context.WithDeadline(ctx, time.Unix(expires, 0))
		defer stop()
	}
	sink := newWSTerminalSink(conn)
	go sink.run(ctx)

	var terminal *guestTerminal
	var refusal string
	s.do(func(session *osservices.Session) {
		id, reason := session.TerminalOpen(uint32(rows), uint32(cols))
		if id == 0 {
			refusal = reason
			return
		}
		opened, _ := json.Marshal(map[string]any{
			"type": "opened",
			"id":   id,
			"os":   session.State().ClipboardOS.String(),
		})
		// First in the queue: before any output.
		sink.sendText(opened)
		terminal = &guestTerminal{id: id, channel: sink, api: true, token: c.GetString("apiToken")}
		s.terminals[id] = terminal
	})
	s.link.Wake()
	if terminal == nil {
		logShell(orDefault(refusal, "refused"))
		sink.Close(refusal)
		<-sink.done
		return
	}
	osServicesLogger.Info().Uint32("terminal", terminal.id).Str("remote", remote).Msg("terminal API session opened")

	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		s.do(func(session *osservices.Session) {
			if s.terminals[terminal.id] != terminal {
				return
			}
			if kind == websocket.MessageBinary {
				session.TerminalInput(terminal.id, data)
				return
			}
			var message struct {
				Type string `json:"type"`
				Rows uint32 `json:"rows"`
				Cols uint32 `json:"cols"`
			}
			if json.Unmarshal(data, &message) == nil && message.Type == "resize" {
				session.TerminalResize(terminal.id, min(message.Rows, terminalAPIRowsMax), min(message.Cols, terminalAPIColsMax))
			}
		})
		s.link.Wake()
	}

	s.do(func(session *osservices.Session) {
		if s.terminals[terminal.id] == terminal {
			delete(s.terminals, terminal.id)
			session.TerminalClose(terminal.id)
		}
	})
	s.link.Wake()
	// The client left, or the sink closed the WebSocket after the terminal
	// ended; either way nothing more is written.
	cancel()
	<-sink.done
	osServicesLogger.Info().Uint32("terminal", terminal.id).Str("remote", remote).Msg("terminal API session closed")
	logShell("")
}

// closeAPITerminals ends the terminal API sessions, when the setting goes off.
func (s *osServicesService) closeAPITerminals() {
	s.closeTerminals(func(t *guestTerminal) bool { return t.api }, terminalAPIOff)
}

// closeTokenTerminals ends the terminal API sessions opened with a token, when
// the token is deleted.
func (s *osServicesService) closeTokenTerminals(tokenID string) {
	s.closeTerminals(func(t *guestTerminal) bool { return t.api && t.token == tokenID }, "The API token was deleted")
}

func (s *osServicesService) closeTerminals(match func(*guestTerminal) bool, reason string) {
	s.do(func(session *osservices.Session) {
		for id, t := range s.terminals {
			if match(t) {
				delete(s.terminals, id)
				session.TerminalClose(id)
				t.channel.Close(reason)
			}
		}
	})
	s.link.Wake()
}

type wsTerminalMessage struct {
	text bool
	data []byte
}

// wsTerminalSink queues output for one WebSocket; a goroutine writes it, so
// Send never blocks the session. Buffered counts what is queued and not yet
// written, which holds the sender back as a slow browser does.
type wsTerminalSink struct {
	conn *websocket.Conn
	wake chan struct{}
	done chan struct{}

	mu      sync.Mutex
	queue   []wsTerminalMessage
	queued  int
	closing bool
	reason  string
}

func newWSTerminalSink(conn *websocket.Conn) *wsTerminalSink {
	return &wsTerminalSink{conn: conn, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (w *wsTerminalSink) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *wsTerminalSink) push(m wsTerminalMessage) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		return true
	}
	if !m.text && w.queued > guestTerminalBuffered {
		return false
	}
	w.queue = append(w.queue, m)
	w.queued += len(m.data)
	w.signal()
	return true
}

func (w *wsTerminalSink) sendText(data []byte) { w.push(wsTerminalMessage{text: true, data: data}) }

// Send copies data: the session reuses its buffer.
func (w *wsTerminalSink) Send(data []byte) bool {
	return w.push(wsTerminalMessage{data: append([]byte(nil), data...)})
}

func (w *wsTerminalSink) Buffered() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.queued
}

func (w *wsTerminalSink) Close(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		return
	}
	w.closing = true
	w.reason = reason
	w.signal()
}

// run writes the queue until the sink closes or ctx ends, then closes the
// WebSocket.
func (w *wsTerminalSink) run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.closing = true
			w.queue = nil
			w.queued = 0
			w.mu.Unlock()
			_ = w.conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			if len(w.queue) == 0 {
				closing, reason := w.closing, w.reason
				w.mu.Unlock()
				if closing {
					closed, _ := json.Marshal(map[string]string{"type": "closed", "reason": reason})
					wctx, cancel := context.WithTimeout(ctx, time.Second)
					_ = w.conn.Write(wctx, websocket.MessageText, closed)
					cancel()
					_ = w.conn.Close(websocket.StatusNormalClosure, "")
					return
				}
				break
			}
			m := w.queue[0]
			w.queue[0] = wsTerminalMessage{}
			w.queue = w.queue[1:]
			w.mu.Unlock()

			kind := websocket.MessageBinary
			if m.text {
				kind = websocket.MessageText
			}
			wctx, cancel := context.WithTimeout(ctx, terminalWSWriteTimeout)
			err := w.conn.Write(wctx, kind, m.data)
			cancel()

			w.mu.Lock()
			w.queued -= len(m.data)
			w.mu.Unlock()
			if err != nil {
				w.mu.Lock()
				w.closing = true
				w.queue = nil
				w.queued = 0
				w.mu.Unlock()
				_ = w.conn.CloseNow()
				return
			}
		}
	}
}
