package hostconsole

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hinshun/vt10x"
)

const (
	// outputKeep is how much output a session keeps for console_read.
	outputKeep = 1 << 20
	// readyQuiet: the shell is taken as ready once its first output has
	// been followed by this much silence.
	readyQuiet = 600 * time.Millisecond
	readyMax   = 8 * time.Second
	writeWait  = 10 * time.Second
)

var errClosed = errors.New("the terminal session has ended")

// session is one shell on the target computer: one terminal API WebSocket.
type session struct {
	id         string
	conn       *websocket.Conn
	terminalID uint32
	os         string
	shell      dialect

	writeMu sync.Mutex
	runMu   sync.Mutex // one console_run at a time
	pending *pendingRun
	// interrupted: an interrupt key was sent since the last run, which ends
	// a pending run that will never print its end marker.
	interrupted atomic.Bool

	mu      sync.Mutex
	buf     []byte // the last outputKeep bytes of output
	base    int64  // stream offset of buf[0]
	total   int64  // stream offset after the last byte
	readPos int64  // console_read's cursor
	changed chan struct{}
	closed  bool
	reason  string
	rows    int
	cols    int
	screen  vt10x.Terminal
	// replies: the terminal's answers to queries in the output, sent after
	// the lock is released; tail: the end of the last output, for a query
	// split between two messages.
	replies []byte
	tail    []byte
}

// replyWriter takes vt10x's answers (cursor position, colours); it runs with
// s.mu held, from append.
type replyWriter struct{ s *session }

func (w replyWriter) Write(b []byte) (int, error) {
	w.s.replies = append(w.s.replies, b...)
	return len(b), nil
}

// Device attributes queries, which vt10x leaves unanswered. Programs such as
// fish 4 wait for the answer before they print anything; xterm.js in the
// browser gives it, so this does too.
var daQueries = []struct{ query, answer string }{
	{"\x1b[c", "\x1b[?62;22c"},
	{"\x1b[0c", "\x1b[?62;22c"},
	{"\x1b[>c", "\x1b[>0;10;1c"},
	{"\x1b[>0c", "\x1b[>0;10;1c"},
}

// answerQueriesLocked queues the answers to the queries in data.
func (s *session) answerQueriesLocked(data []byte) {
	scan := append(append([]byte(nil), s.tail...), data...)
	for _, q := range daQueries {
		// A query wholly inside the previous tail was answered then.
		for off := 0; ; {
			i := bytes.Index(scan[off:], []byte(q.query))
			if i < 0 {
				break
			}
			if off+i+len(q.query) > len(s.tail) {
				s.replies = append(s.replies, q.answer...)
			}
			off += i + len(q.query)
		}
	}
	keep := min(len(scan), 4)
	s.tail = append(s.tail[:0], scan[len(scan)-keep:]...)
}

// pendingRun is a console_run that timed out while its command still runs.
type pendingRun struct {
	nonce string
	start int64
}

// openSession opens a terminal and waits for the shell's first prompt.
func openSession(ctx context.Context, k *kvmClient, id string, rows, cols int) (*session, error) {
	conn, err := k.dial(ctx, rows, cols)
	if err != nil {
		return nil, err
	}
	hello, err := readControl(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if hello.Type == "closed" {
		conn.CloseNow()
		return nil, fmt.Errorf("the KVM did not open a terminal: %s", orUnknown(hello.Reason))
	}
	if hello.Type != "opened" {
		conn.CloseNow()
		return nil, fmt.Errorf("unexpected first message %q", hello.Type)
	}
	s := &session{
		id:         id,
		conn:       conn,
		terminalID: hello.ID,
		os:         hello.OS,
		changed:    make(chan struct{}),
		rows:       rows,
		cols:       cols,
	}
	s.screen = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(replyWriter{s}))
	go s.reader()
	if err := s.waitReady(ctx); err != nil {
		s.close()
		return nil, err
	}
	shell, err := s.probe(ctx)
	if err != nil {
		s.close()
		return nil, err
	}
	s.shell = shell
	if setup := shell.setup(); setup != "" {
		if _, err := s.run(ctx, setup, 15*time.Second, false); err != nil {
			s.close()
			return nil, fmt.Errorf("session setup failed: %w", err)
		}
	}
	s.mu.Lock()
	s.readPos = s.total
	s.mu.Unlock()
	return s, nil
}

type controlMessage struct {
	Type   string `json:"type"`
	ID     uint32 `json:"id"`
	OS     string `json:"os"`
	Reason string `json:"reason"`
}

func readControl(ctx context.Context, conn *websocket.Conn) (controlMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		return controlMessage{}, fmt.Errorf("no answer from the terminal API: %w", err)
	}
	var m controlMessage
	if kind != websocket.MessageText || json.Unmarshal(data, &m) != nil {
		return controlMessage{}, errors.New("the terminal API sent an unexpected message")
	}
	return m, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

// reader takes the KVM's messages until the WebSocket ends.
func (s *session) reader() {
	for {
		kind, data, err := s.conn.Read(context.Background())
		if err != nil {
			reason := "the connection to the KVM ended"
			if status := websocket.CloseStatus(err); status == -1 {
				reason += ": " + err.Error()
			}
			s.end(reason)
			return
		}
		if kind == websocket.MessageText {
			var m controlMessage
			if json.Unmarshal(data, &m) == nil && m.Type == "closed" {
				s.end(orUnknown(m.Reason))
			}
			continue
		}
		s.append(data)
	}
}

func (s *session) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *session) append(data []byte) {
	s.mu.Lock()
	s.appendLocked(data)
	replies := s.replies
	s.replies = nil
	s.mu.Unlock()
	if len(replies) > 0 {
		go func() { _ = s.send(context.Background(), string(replies)) }()
	}
}

func (s *session) appendLocked(data []byte) {
	s.answerQueriesLocked(data)
	s.buf = append(s.buf, data...)
	s.total += int64(len(data))
	if over := len(s.buf) - outputKeep; over > 0 {
		s.buf = append(s.buf[:0], s.buf[over:]...)
		s.base += int64(over)
	}
	func() {
		defer func() { _ = recover() }() // a parser fault must not end the session
		_, _ = s.screen.Write(data)
	}()
	s.notifyLocked()
}

func (s *session) end(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.reason = reason
	s.notifyLocked()
}

func (s *session) close() {
	s.end("closed by the MCP server")
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
}

// state is a snapshot: output from offset from on, whether some of it is
// no longer kept, the end offset, and the session's end.
type state struct {
	data    []byte
	lost    bool
	total   int64
	closed  bool
	reason  string
	changed <-chan struct{}
}

func (s *session) snapshot(from int64) state {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := state{total: s.total, closed: s.closed, reason: s.reason, changed: s.changed}
	if from < s.base {
		st.lost = true
		from = s.base
	}
	if from < s.total {
		st.data = append([]byte(nil), s.buf[from-s.base:]...)
	}
	return st
}

func (s *session) send(ctx context.Context, data string) error {
	s.mu.Lock()
	closed, reason := s.closed, s.reason
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("%w: %s", errClosed, reason)
	}
	if strings.ContainsAny(data, "\x03\x04\x1c") {
		s.interrupted.Store(true)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	b := []byte(data)
	for len(b) > 0 {
		n := min(len(b), 32<<10)
		if err := s.conn.Write(ctx, websocket.MessageBinary, b[:n]); err != nil {
			return fmt.Errorf("cannot send to the terminal: %w", err)
		}
		b = b[n:]
	}
	return nil
}

func (s *session) resize(ctx context.Context, rows, cols int) error {
	msg, _ := json.Marshal(map[string]any{"type": "resize", "rows": rows, "cols": cols})
	s.writeMu.Lock()
	err := s.conn.Write(ctx, websocket.MessageText, msg)
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rows, s.cols = rows, cols
	s.screen.Resize(cols, rows)
	s.mu.Unlock()
	return nil
}

// wait returns when the output changes, the deadline passes or ctx ends.
func wait(ctx context.Context, changed <-chan struct{}, deadline time.Time) bool {
	d := time.Until(deadline)
	if d <= 0 {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-changed:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// waitReady waits for the shell's first output and a pause after it.
func (s *session) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(readyMax)
	var lastTotal int64 = -1
	lastChange := time.Now()
	for {
		st := s.snapshot(0)
		if st.closed {
			return fmt.Errorf("%w: %s", errClosed, st.reason)
		}
		if st.total != lastTotal {
			lastTotal, lastChange = st.total, time.Now()
		}
		if st.total > 0 && time.Since(lastChange) >= readyQuiet {
			return nil
		}
		if time.Now().After(deadline) {
			return nil // a shell with a silent prompt still takes commands
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		wait(ctx, st.changed, time.Now().Add(100*time.Millisecond))
	}
}

// waitQuiet waits until no output came for quiet, at most max.
func (s *session) waitQuiet(ctx context.Context, quiet, max time.Duration) {
	deadline := time.Now().Add(max)
	for {
		st := s.now()
		if st.closed || time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		if !wait(ctx, st.changed, time.Now().Add(quiet)) {
			return // quiet for long enough
		}
	}
}

var probePattern = regexp.MustCompile(`__MCPP_(\S*)`)

// probe finds the shell's language. The echoed probe line holds
// "__MCPP_$FISH_VERSION…", which the answers never start with.
func (s *session) probe(ctx context.Context) (dialect, error) {
	start := s.now().total
	if err := s.send(ctx, ` echo "__MCPP_$FISH_VERSION.$PSVersionTable"`+"\r"); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := s.snapshot(start)
		for _, m := range probePattern.FindAllStringSubmatch(plainText(st.data), -1) {
			answer := m[1]
			switch {
			case strings.HasPrefix(answer, "$"):
				continue
			case answer == ".":
				return posixShell{}, nil
			case strings.HasPrefix(answer, ".S"):
				return powerShell{}, nil
			case answer != "" && answer[0] >= '0' && answer[0] <= '9':
				return fishShell{}, nil
			}
		}
		if st.closed {
			return nil, fmt.Errorf("%w: %s", errClosed, st.reason)
		}
		if !wait(ctx, st.changed, deadline) && (time.Now().After(deadline) || ctx.Err() != nil) {
			return nil, errors.New("the shell did not answer the probe in 10 s; its language is unknown")
		}
	}
}

// runResult is what console_run reports.
type runResult struct {
	Output   string
	ExitCode *int
	TimedOut bool
	Lost     bool
	Closed   string
	// Rejected: the shell never started the command (a syntax error, or a
	// line that waits for more input); Output is what the shell printed.
	Rejected bool
}

// rejectQuiet is how long the output stays quiet, without the start marker,
// before run decides the shell did not take the command. The start marker
// is the first thing a command prints, so a running command shows it at once.
var rejectQuiet = 3 * time.Second

func newNonce() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// run types command wrapped in markers and waits for its end marker.
func (s *session) run(ctx context.Context, command string, timeout time.Duration, moveCursor bool) (runResult, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	interrupted := s.interrupted.Swap(false)
	if p := s.pending; p != nil {
		st := s.snapshot(p.start)
		if !interrupted && !bytes.Contains(st.data, []byte(endMarker(p.nonce))) {
			return runResult{}, errors.New("an earlier command in this session is still running; " +
				"use console_read to follow it, console_send with keys [\"ctrl-c\"] to stop it, or open another session")
		}
		s.pending = nil
	}
	if interrupted {
		// A shell that was interrupted may discard what is typed before it
		// is back at its prompt.
		s.waitQuiet(ctx, 400*time.Millisecond, 3*time.Second)
	}
	nonce := newNonce()
	start := s.now().total
	if err := s.send(ctx, s.shell.wrap(command, nonce)); err != nil {
		return runResult{}, err
	}
	end := []byte(endMarker(nonce))
	begin := []byte(startMarker(nonce))
	deadline := time.Now().Add(timeout)
	searchFrom := start
	started := false
	lastTotal, lastChange := start, time.Now()
	for {
		found, sr := s.search(searchFrom, end)
		if sr.total != lastTotal {
			lastTotal, lastChange = sr.total, time.Now()
		}
		if !started {
			started, _ = s.search(start, begin)
		}
		if !found && !started && !sr.closed && time.Since(lastChange) >= rejectQuiet {
			// The shell is quiet and never ran the line: it refused it, or
			// waits for more input. Ctrl-C leaves the shell at a fresh prompt.
			st := s.snapshot(start)
			if bytes.Contains(st.data, begin) {
				started = true
				continue
			}
			_ = s.send(ctx, "\x03")
			s.interrupted.Store(false)
			s.waitQuiet(ctx, 300*time.Millisecond, 2*time.Second)
			if moveCursor {
				s.mu.Lock()
				s.readPos = s.total
				s.mu.Unlock()
			}
			return runResult{Output: strings.TrimSpace(plainText(st.data)), Rejected: true, Lost: st.lost}, nil
		}
		if found || sr.closed || time.Now().After(deadline) || ctx.Err() != nil {
			st := s.snapshot(start)
			r := extract(plainText(st.data), nonce, st.lost)
			switch {
			case bytes.Contains(st.data, end):
			case st.closed:
				r.Closed = st.reason
			default:
				r.TimedOut = true
				s.pending = &pendingRun{nonce: nonce, start: start}
			}
			if moveCursor {
				s.mu.Lock()
				s.readPos = st.total
				s.mu.Unlock()
			}
			return r, nil
		}
		// The marker may straddle what was searched and what comes next.
		searchFrom = max(start, sr.total-int64(len(end)))
		next := deadline
		if !started {
			next = minTime(deadline, lastChange.Add(rejectQuiet))
		}
		wait(ctx, sr.changed, next)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// now is the session's state without its output.
func (s *session) now() state {
	_, st := s.search(1<<62, nil)
	return st
}

// search looks for needle in the output from offset from on, without
// copying it.
func (s *session) search(from int64, needle []byte) (bool, state) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := state{total: s.total, closed: s.closed, reason: s.reason, changed: s.changed}
	from = max(from, s.base)
	if from >= s.total {
		return false, st
	}
	return bytes.Contains(s.buf[from-s.base:], needle), st
}

func startMarker(nonce string) string { return "__MCPS_" + nonce }
func endMarker(nonce string) string   { return "__MCPE_" + nonce + "_" }

// extract cuts a command's output out of the plain text: after the start
// marker's line, up to the end marker, whose number is the exit code.
// lost: the start of the output is no longer kept, so the start marker
// may be gone with it.
func extract(text, nonce string, lost bool) runResult {
	r := runResult{Lost: lost}
	body := text
	if i := strings.Index(text, startMarker(nonce)); i >= 0 {
		body = text[i+len(startMarker(nonce)):]
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			body = body[nl+1:]
		} else {
			body = ""
		}
	} else if !lost {
		// The shell has not run the line yet.
		body = ""
	}
	if j := strings.Index(body, endMarker(nonce)); j >= 0 {
		rest := body[j+len(endMarker(nonce)):]
		k := 0
		for k < len(rest) && (rest[k] == '-' || rest[k] >= '0' && rest[k] <= '9') {
			k++
		}
		if code, err := strconv.Atoi(rest[:k]); err == nil {
			r.ExitCode = &code
		}
		body = body[:j]
	}
	r.Output = strings.TrimSuffix(body, "\n")
	return r
}

// read returns the output after console_read's cursor and moves it.
func (s *session) read(ctx context.Context, waitFor time.Duration, until *regexp.Regexp) (string, bool, string) {
	s.mu.Lock()
	from := s.readPos
	s.mu.Unlock()
	deadline := time.Now().Add(waitFor)
	var lastTotal int64 = -1
	lastChange := time.Now()
	for {
		st := s.snapshot(from)
		text := plainText(st.data)
		done := st.closed || time.Now().After(deadline) || ctx.Err() != nil
		if until != nil {
			done = done || until.MatchString(text)
		} else {
			if st.total != lastTotal {
				lastTotal, lastChange = st.total, time.Now()
			}
			// Some output, then a short pause: what a command printed so far.
			done = done || len(st.data) > 0 && time.Since(lastChange) >= 300*time.Millisecond
		}
		if done {
			s.mu.Lock()
			s.readPos = st.total
			s.mu.Unlock()
			closed := ""
			if st.closed {
				closed = st.reason
			}
			return text, st.lost, closed
		}
		next := deadline
		if until == nil && len(st.data) > 0 {
			next = time.Now().Add(100 * time.Millisecond)
		}
		wait(ctx, st.changed, next)
	}
}

// screenText is the terminal screen as an xterm would show it.
func (s *session) screenText() (string, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := strings.Split(s.screen.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \x00")
	}
	text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	cur := s.screen.Cursor()
	return text, cur.Y, cur.X
}
