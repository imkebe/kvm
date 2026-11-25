// Package hostconsole is the KVM's own MCP server, served over streamable
// HTTP at /mcp (kvm/mcp_server.go). It started as tools/hostconsole-mcp, the
// stdio server for a computer next to the KVM: kvm_* tools use the device
// API (POST /api/rpc), console_* tools the OS Services terminal API
// (GET /os-services/terminal/ws). Inside the KVM those
// requests go through an in-memory connection to the KVM's own web server,
// with the client's API token, so the same checks apply as for any program.
package hostconsole

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultRows    = 50
	defaultCols    = 200
	defaultTimeout = 30
	maxTimeout     = 600
	defaultOutput  = 32 << 10
	maxOutput      = 512 << 10

	// A shell's screen costs about 32 bytes a cell, twice with the alternate screen.
	maxRows = 100
	maxCols = 300
)

var version = "0.1.0"

type server struct {
	kvm *kvmClient

	mu       sync.Mutex
	sessions map[string]*session
	next     int

	// The last kvm_screenshot's geometry (screen.go).
	shotMu sync.Mutex
	shot   *shotGeometry
	// Where this session last put the pointer (absolute coordinates) and
	// the buttons it holds.
	mouseMu        sync.Mutex
	mouseX, mouseY int
	mouseKnown     bool
	buttons        uint8
}

// Options configures one MCP server: one per MCP session on the KVM, so each
// client has its own shells and its own API token.
type Options struct {
	// Host is the KVM's address as the client reached it; it names the KVM
	// in answers. Requests go through Transport, whatever the host.
	Host string
	// Token is the client's API token, sent with every device and terminal
	// API call, so the KVM checks its scopes there as for any program.
	Token string
	// Transport carries the HTTP and WebSocket requests to the KVM's APIs.
	Transport http.RoundTripper
	// Header is added to every request (e.g. X-Forwarded-For for the logs).
	Header http.Header
	// Version is the KVM app's version, reported to the client.
	Version string
}

const instructions = "Manage a JetKVM and the target computer behind it. kvm_* tools act on the KVM: status, power, virtual media, keyboard, USB functions, clipboard, host info. " +
	"For computer use (BIOS, installers, desktop apps): kvm_screenshot to see the screen, kvm_mouse to point and click, kvm_keyboard to type. " +
	"console_* tools are shells on the target computer, through IP-KVM OS Services. " +
	"Each shell runs as the user of the target's desktop session. Start with host_status. " +
	"Prefer console_run for commands; use console_send, console_read and console_screen for interactive programs. " +
	"Never use sudo, runas or other elevation, and never type passwords."

// New makes an MCP server with the kvm_* and console_* tools. Close ends its
// shells; call it when the MCP session ends.
func New(opts Options) (m *mcp.Server, closeShells func()) {
	kvm := &kvmClient{
		base:  url.URL{Scheme: "http", Host: opts.Host},
		token: strings.TrimSpace(opts.Token),
		http:  &http.Client{Transport: opts.Transport, Timeout: 20 * time.Second},
		ws:    &http.Client{Transport: opts.Transport},
		extra: opts.Header.Clone(),
	}
	srv := &server{kvm: kvm, sessions: map[string]*session{}}
	v := opts.Version
	if v == "" {
		v = version
	}
	m = mcp.NewServer(&mcp.Implementation{Name: "jetkvm", Version: v}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	srv.register(m)
	return m, srv.closeAll
}

func (s *server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		sess.close()
		delete(s.sessions, id)
	}
}

func (s *server) get(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		switch len(s.sessions) {
		case 0:
			return nil, errors.New("no session is open; call console_open")
		case 1:
			for _, sess := range s.sessions {
				return sess, nil
			}
		}
		return nil, errors.New("several sessions are open; name one")
	}
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("no session %q; open sessions: %s", id, s.listLocked())
	}
	return sess, nil
}

func (s *server) listLocked() string {
	if len(s.sessions) == 0 {
		return "none"
	}
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

func (s *server) open(ctx context.Context, rows, cols int) (*session, error) {
	if rows <= 0 {
		rows = defaultRows
	}
	if cols <= 0 {
		cols = defaultCols
	}
	if rows > maxRows || cols > maxCols {
		return nil, fmt.Errorf("rows must be at most %d and cols at most %d", maxRows, maxCols)
	}
	s.mu.Lock()
	s.next++
	id := fmt.Sprintf("s%d", s.next)
	s.mu.Unlock()
	sess, err := openSession(ctx, s.kvm, id, rows, cols)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess, nil
}

func text(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

func ptr[T any](v T) *T { return &v }

type statusIn struct{}

type openIn struct {
	Rows int `json:"rows,omitempty" jsonschema:"terminal rows, default 50"`
	Cols int `json:"cols,omitempty" jsonschema:"terminal columns, default 200"`
}

type runIn struct {
	Command   string `json:"command" jsonschema:"the command line; several lines are allowed"`
	Session   string `json:"session,omitempty" jsonschema:"session id from console_open; empty opens or reuses the only session"`
	TimeoutS  int    `json:"timeout_s,omitempty" jsonschema:"seconds to wait for the command to finish, default 30, at most 600"`
	MaxOutput int    `json:"max_output,omitempty" jsonschema:"most bytes of output to return, default 32768; the middle is left out beyond it"`
}

type sendIn struct {
	Session string   `json:"session,omitempty" jsonschema:"session id; empty for the only session"`
	Text    string   `json:"text,omitempty" jsonschema:"text to type as is; a line break is not Enter unless it is in the text"`
	Keys    []string `json:"keys,omitempty" jsonschema:"keys pressed after the text, e.g. enter, ctrl-c, up, tab, esc, q"`
}

type readIn struct {
	Session   string `json:"session,omitempty" jsonschema:"session id; empty for the only session"`
	WaitMS    int    `json:"wait_ms,omitempty" jsonschema:"longest wait for output in milliseconds, default 2000, at most 600000"`
	Until     string `json:"until,omitempty" jsonschema:"regular expression; wait until the new output matches it, up to wait_ms"`
	MaxOutput int    `json:"max_output,omitempty" jsonschema:"most bytes to return, default 32768"`
}

type sessionIn struct {
	Session string `json:"session,omitempty" jsonschema:"session id; empty for the only session"`
}

type resizeIn struct {
	Session string `json:"session,omitempty" jsonschema:"session id; empty for the only session"`
	Rows    int    `json:"rows" jsonschema:"terminal rows"`
	Cols    int    `json:"cols" jsonschema:"terminal columns"`
}

func (s *server) register(m *mcp.Server) {
	s.registerDevice(m)
	s.registerScreen(m)
	mcp.AddTool(m, &mcp.Tool{
		Name:        "host_status",
		Description: "Whether the KVM can open a shell on its target computer: OS Services connected and approved, the terminal and the terminal API on, the target's OS, and shells in use (the browser's included) of the maximum.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult, any, error) {
		st, err := s.kvm.status(ctx)
		if err != nil {
			return nil, nil, err
		}
		var notes []string
		switch {
		case !st.Enabled:
			notes = append(notes, "OS Services is off on the KVM.")
		case !st.Connected:
			notes = append(notes, "IP-KVM OS Services is not connected: it is not running on the target, or the target is off.")
		case st.ApprovalPending:
			notes = append(notes, "The target computer has not approved this KVM yet (\"Allow this KVM?\").")
		}
		if !st.TerminalEnabled {
			notes = append(notes, "Terminal access is off in the KVM's OS Services settings.")
		}
		if !st.APIEnabled {
			notes = append(notes, "The Terminal API is off in the KVM's OS Services settings; console tools cannot work.")
		}
		s.mu.Lock()
		mine := s.listLocked()
		s.mu.Unlock()
		res := fmt.Sprintf("host %s\nready: %v\nos: %s\nsender version: %s\nshells in use: %d of %d (%d through the API)\nsessions of this server: %s",
			s.kvm.base.Host, st.Available && st.APIEnabled, st.OS, orNone(st.SenderVersion), st.Sessions, st.Max, st.APISessions, mine)
		if note := s.kvm.expiryNote(time.Now()); note != "" {
			notes = append(notes, note)
		}
		if len(notes) > 0 {
			res += "\n" + strings.Join(notes, "\n")
		}
		return text("%s", res), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_open",
		Description: "Open a shell on the target computer and return its session id. The target allows four shells at once, the browser's terminal tabs included. The shell's language (posix, fish, powershell) is detected.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false), DestructiveHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in openIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.open(ctx, in.Rows, in.Cols)
		if err != nil {
			return nil, nil, err
		}
		return text("session %s open: os %s, shell %s, %d×%d", sess.id, sess.os, sess.shell.Name(), sess.cols, sess.rows), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "console_run",
		Description: "Run a command in a shell on the target computer and return its output and exit code. " +
			"Without a session it uses the only open session or opens one. A command that is still running at the timeout keeps running: " +
			"follow it with console_read or stop it with console_send keys [\"ctrl-c\"]. Do not run commands that ask for passwords or elevation.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Command) == "" {
			return nil, nil, errors.New("command is empty")
		}
		timeout := in.TimeoutS
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		if timeout > maxTimeout {
			timeout = maxTimeout
		}
		sess, err := s.get(in.Session)
		if err != nil && in.Session == "" && strings.HasPrefix(err.Error(), "no session") {
			sess, err = s.open(ctx, 0, 0)
		}
		if err != nil {
			return nil, nil, err
		}
		r, err := sess.run(ctx, in.Command, time.Duration(timeout)*time.Second, true)
		if err != nil {
			return nil, nil, err
		}
		out, truncated := clip(r.Output, outputLimit(in.MaxOutput))
		var head []string
		head = append(head, "session "+sess.id)
		switch {
		case r.ExitCode != nil:
			head = append(head, fmt.Sprintf("exit code %d", *r.ExitCode))
		case r.Rejected:
			head = append(head, "the shell did not run the command (syntax error, or it waited for more input); Ctrl-C was pressed, so the shell is back at its prompt; its answer before that follows")
		case r.TimedOut:
			head = append(head, fmt.Sprintf("still running after %d s", timeout))
		case r.Closed != "":
			head = append(head, "session ended: "+r.Closed)
		}
		if r.Lost {
			head = append(head, "the start of the output is no longer kept")
		}
		if truncated {
			head = append(head, "output shortened")
		}
		return text("[%s]\n%s", strings.Join(head, "; "), out), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_send",
		Description: "Type text and press keys in a shell session, for interactive programs, prompts and stopping a command (keys [\"ctrl-c\"]). Then use console_read or console_screen to see the effect.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.get(in.Session)
		if err != nil {
			return nil, nil, err
		}
		data := in.Text
		for _, k := range in.Keys {
			b, err := keyBytes(k)
			if err != nil {
				return nil, nil, err
			}
			data += b
		}
		if data == "" {
			return nil, nil, errors.New("nothing to send: give text or keys")
		}
		if err := sess.send(ctx, data); err != nil {
			return nil, nil, err
		}
		return text("sent %d bytes to session %s", len(data), sess.id), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_read",
		Description: "Return the output of a shell session since the last console_read or console_run, as plain text. Waits up to wait_ms for output, or until the output matches until.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.get(in.Session)
		if err != nil {
			return nil, nil, err
		}
		var until *regexp.Regexp
		if in.Until != "" {
			if until, err = regexp.Compile(in.Until); err != nil {
				return nil, nil, fmt.Errorf("until: %w", err)
			}
		}
		waitMS := in.WaitMS
		if waitMS <= 0 {
			waitMS = 2000
		}
		waitMS = min(waitMS, maxTimeout*1000)
		out, lost, closed := sess.read(ctx, time.Duration(waitMS)*time.Millisecond, until)
		out, truncated := clip(out, outputLimit(in.MaxOutput))
		head := []string{"session " + sess.id}
		if until != nil && !until.MatchString(out) {
			head = append(head, "until did not match")
		}
		if out == "" {
			head = append(head, "no new output")
		}
		if lost {
			head = append(head, "some output was no longer kept")
		}
		if truncated {
			head = append(head, "output shortened")
		}
		if closed != "" {
			head = append(head, "session ended: "+closed)
		}
		return text("[%s]\n%s", strings.Join(head, "; "), out), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_screen",
		Description: "Return the session's terminal screen as text, as a terminal would show it now: for full-screen programs such as top, editors and installers.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.get(in.Session)
		if err != nil {
			return nil, nil, err
		}
		screen, row, col := sess.screenText()
		return text("[session %s; %d×%d; cursor row %d col %d]\n%s", sess.id, sess.cols, sess.rows, row+1, col+1, screen), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_resize",
		Description: "Change a session's terminal size.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false), IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in resizeIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.get(in.Session)
		if err != nil {
			return nil, nil, err
		}
		if in.Rows <= 0 || in.Cols <= 0 || in.Rows > maxRows || in.Cols > maxCols {
			return nil, nil, fmt.Errorf("rows 1 to %d and cols 1 to %d", maxRows, maxCols)
		}
		if err := sess.resize(ctx, in.Rows, in.Cols); err != nil {
			return nil, nil, err
		}
		return text("session %s is %d×%d", sess.id, in.Cols, in.Rows), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "console_close",
		Description: "Close a shell session; the shell and what runs in it end.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false), IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, any, error) {
		sess, err := s.get(in.Session)
		if err != nil {
			return nil, nil, err
		}
		sess.close()
		s.mu.Lock()
		delete(s.sessions, sess.id)
		s.mu.Unlock()
		return text("session %s closed", sess.id), nil, nil
	})
}

func outputLimit(n int) int {
	if n <= 0 {
		return defaultOutput
	}
	return min(n, maxOutput)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
