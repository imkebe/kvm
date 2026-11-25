package hostconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// fakeKVM serves the terminal API as kvm/os_services_terminal_ws.go does,
// with a real shell in a pty for each WebSocket, as the agent runs one.
type fakeKVM struct {
	t       *testing.T
	shell   []string
	home    string
	token   string
	expires string // X-JetKVM-Token-Expires to send, if any
	refuse  string // a reason to refuse every open
	mu      sync.Mutex
	open    int
}

func (f *fakeKVM) authorized(r *http.Request) bool {
	if f.token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+f.token
}

func (f *fakeKVM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"the API token is not valid"}`)
		return
	}
	if f.expires != "" {
		w.Header().Set(tokenExpiresHeader, f.expires)
	}
	switch r.URL.Path {
	case "/os-services/terminal/status":
		f.mu.Lock()
		n := f.open
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(terminalStatus{APIEnabled: true, Enabled: true, TerminalEnabled: true,
			Connected: true, Available: true, OS: "linux", Sessions: n, APISessions: n, Max: 4})
	case "/os-services/terminal/ws":
		f.serveTerminal(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeKVM) serveTerminal(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := context.Background()
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		_ = conn.Write(ctx, websocket.MessageText, b)
	}
	if f.refuse != "" {
		send(map[string]any{"type": "closed", "reason": f.refuse})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	cmd := exec.Command(f.shell[0], f.shell[1:]...)
	cmd.Env = []string{"HOME=" + f.home, "TERM=xterm-256color", "PATH=" + os.Getenv("PATH"), "USER=test", "LANG=C.UTF-8"}
	cmd.Dir = f.home
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		send(map[string]any{"type": "closed", "reason": "could not start a shell"})
		return
	}
	f.mu.Lock()
	f.open++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.open--
		f.mu.Unlock()
	}()
	send(map[string]any{"type": "opened", "id": 1, "os": "linux"})
	var wmu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				wmu.Lock()
				werr := conn.Write(ctx, websocket.MessageBinary, buf[:n])
				wmu.Unlock()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				_ = cmd.Process.Kill()
				return
			}
			if kind == websocket.MessageBinary {
				_, _ = ptmx.Write(data)
				continue
			}
			var m struct {
				Type       string `json:"type"`
				Rows, Cols uint16
			}
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: m.Rows, Cols: m.Cols})
			}
		}
	}()
	_ = cmd.Wait()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
	ptmx.Close()
	wmu.Lock()
	send(map[string]any{"type": "closed", "reason": "The shell exited"})
	wmu.Unlock()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func startFake(t *testing.T, f *fakeKVM) *kvmClient {
	t.Helper()
	f.t = t
	if f.home == "" {
		f.home = t.TempDir()
	}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	return testKVMClient(u.Host, f.token)
}

// testKVMClient is the client New builds, over plain HTTP to host.
func testKVMClient(host, token string) *kvmClient {
	return &kvmClient{
		base:  url.URL{Scheme: "http", Host: host},
		token: token,
		http:  &http.Client{Timeout: 20 * time.Second},
		ws:    &http.Client{},
	}
}

type shellCase struct {
	name    string
	argv    []string
	dialect string
	rc      map[string]string // files in HOME
}

// A coloured prompt with a right prompt, as people's shells have.
const zshrc = `PROMPT='%F{green}%n@%m%f %F{blue}%~%f %# '
RPROMPT='%F{yellow}[%T]%f'
setopt PROMPT_SUBST
`

const fishConfig = `function fish_prompt; set_color green; echo -n (whoami)'@host '; set_color normal; echo -n '> '; end
function fish_right_prompt; set_color yellow; date +%T; set_color normal; end
`

func shells(t *testing.T) []shellCase {
	all := []shellCase{
		{name: "bash", argv: []string{"bash", "-l"}, dialect: "posix", rc: map[string]string{".bash_profile": "PS1='\\[\\e[32m\\]\\u@\\h\\[\\e[0m\\] \\w \\$ '\n"}},
		{name: "zsh", argv: []string{"zsh", "-l"}, dialect: "posix", rc: map[string]string{".zshrc": zshrc}},
		{name: "dash", argv: []string{"dash", "-l"}, dialect: "posix"},
		{name: "fish", argv: []string{"fish", "-l"}, dialect: "fish", rc: map[string]string{".config/fish/config.fish": fishConfig}},
		{name: "pwsh", argv: []string{"pwsh", "-NoLogo"}, dialect: "powershell"},
	}
	var found []shellCase
	for _, c := range all {
		if _, err := exec.LookPath(c.argv[0]); err == nil {
			found = append(found, c)
		} else {
			t.Logf("%s not installed: skipped", c.argv[0])
		}
	}
	return found
}

func openTest(t *testing.T, c shellCase) *session {
	t.Helper()
	home := t.TempDir()
	for name, content := range c.rc {
		p := filepath.Join(home, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k := startFake(t, &fakeKVM{shell: c.argv, home: home})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := openSession(ctx, k, "s1", 30, 120)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.close)
	if s.shell.Name() != c.dialect {
		t.Fatalf("detected %s, want %s", s.shell.Name(), c.dialect)
	}
	return s
}

func mustRun(t *testing.T, s *session, cmd string, timeout time.Duration) runResult {
	t.Helper()
	r, err := s.run(context.Background(), cmd, timeout, true)
	if err != nil {
		t.Fatalf("run %q: %v", cmd, err)
	}
	return r
}

func code(r runResult) string {
	if r.ExitCode == nil {
		return "none"
	}
	return strconv.Itoa(*r.ExitCode)
}

func TestRunInEachShell(t *testing.T) {
	for _, c := range shells(t) {
		t.Run(c.name, func(t *testing.T) {
			s := openTest(t, c)
			echo := "echo hello world"
			fail := "false"
			multi := "echo one\necho two"
			stdin := "cat"
			if c.dialect == "powershell" {
				fail = "cmd /c exit 3"
				stdin = "$input | Out-Null"
			}

			r := mustRun(t, s, echo, 10*time.Second)
			if r.Output != "hello world" || code(r) != "0" {
				t.Fatalf("echo: output %q, exit %s", r.Output, code(r))
			}

			r = mustRun(t, s, fail, 10*time.Second)
			if code(r) == "0" || code(r) == "none" {
				t.Fatalf("false: exit %s, want non-zero; output %q", code(r), r.Output)
			}

			r = mustRun(t, s, multi, 10*time.Second)
			if r.Output != "one\ntwo" || code(r) != "0" {
				t.Fatalf("two lines: output %q, exit %s", r.Output, code(r))
			}

			if c.dialect != "powershell" {
				// A comment ends at its line; a command reading standard
				// input does not take the end marker.
				r = mustRun(t, s, "echo kept # a comment", 10*time.Second)
				if r.Output != "kept" {
					t.Fatalf("comment: output %q", r.Output)
				}
				r = mustRun(t, s, "seq 1 3 | sed 's/^/n/'", 10*time.Second)
				if r.Output != "n1\nn2\nn3" {
					t.Fatalf("pipe: output %q", r.Output)
				}
				r = mustRun(t, s, "printf 'no newline'", 10*time.Second)
				if r.Output != "no newline" || code(r) != "0" {
					t.Fatalf("no trailing newline: output %q, exit %s", r.Output, code(r))
				}
			}

			// A command that waits on standard input times out and blocks the
			// session until it is stopped.
			r = mustRun(t, s, stdin, 2*time.Second)
			if !r.TimedOut {
				t.Fatalf("%s: want a timeout, got exit %s output %q", stdin, code(r), r.Output)
			}
			if _, err := s.run(context.Background(), echo, 5*time.Second, true); err == nil {
				t.Fatal("a second command while the first runs must be refused")
			}
			if err := s.send(context.Background(), "\x03"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				r, err := s.run(context.Background(), echo, 10*time.Second, true)
				if err == nil {
					if r.Output != "hello world" {
						raw := s.snapshot(0).data
						t.Fatalf("after ctrl-c: %+v\nraw tail: %q", r, raw[max(0, len(raw)-600):])
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("session still busy after ctrl-c: %v", err)
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}
}

func TestLargeOutputIsKeptWhole(t *testing.T) {
	c := shellCase{name: "bash", argv: []string{"bash", "--norc", "--noprofile"}, dialect: "posix"}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	s := openTest(t, c)
	r := mustRun(t, s, "seq 1 20000", 30*time.Second)
	lines := strings.Split(r.Output, "\n")
	if len(lines) != 20000 || lines[0] != "1" || lines[19999] != "20000" || code(r) != "0" {
		t.Fatalf("got %d lines, first %q last %q, exit %s", len(lines), lines[0], lines[len(lines)-1], code(r))
	}
	// More than the session keeps: the end and the exit code still arrive.
	r = mustRun(t, s, "yes 0123456789abcdef | head -c 3000000; echo; echo END", 60*time.Second)
	if !r.Lost || !strings.HasSuffix(r.Output, "END") || code(r) != "0" {
		t.Fatalf("3 MB: lost %v, exit %s, tail %q", r.Lost, code(r), r.Output[max(0, len(r.Output)-40):])
	}
}

func TestReadAndScreen(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	s := openTest(t, shellCase{name: "bash", argv: []string{"bash", "--norc", "--noprofile"}, dialect: "posix"})
	ctx := context.Background()
	if err := s.send(ctx, "for i in 1 2 3; do echo tick$i; sleep 0.3; done\r"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := s.read(ctx, 5*time.Second, mustRegexp(t, `tick3`))
	if !strings.Contains(out, "tick1") || !strings.Contains(out, "tick3") {
		t.Fatalf("read: %q", out)
	}
	out, _, _ = s.read(ctx, 300*time.Millisecond, nil)
	if strings.Contains(out, "tick1") {
		t.Fatalf("read repeated old output: %q", out)
	}
	// The echoed command does not contain the awaited text, so the read ends
	// only after printf ran; the screen is still polled, as rendering lags.
	if err := s.send(ctx, "clear; printf 'top %s\\n' line\r"); err != nil {
		t.Fatal(err)
	}
	s.read(ctx, 3*time.Second, mustRegexp(t, `top line`))
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		screen, _, _ := s.screenText()
		first := strings.SplitN(screen, "\n", 2)[0]
		if first == "top line" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen's first line %q, screen:\n%s", first, screen)
		}
	}
}

func TestShellExitEndsTheSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	s := openTest(t, shellCase{name: "bash", argv: []string{"bash", "--norc", "--noprofile"}, dialect: "posix"})
	r := mustRun(t, s, "exit 0", 10*time.Second)
	if r.Closed == "" {
		t.Fatalf("exit: want the session ended, got %+v", r)
	}
	if _, err := s.run(context.Background(), "echo hi", 5*time.Second, true); err == nil {
		t.Fatal("run on an ended session must fail")
	}
}

func TestRefusalAndAuth(t *testing.T) {
	k := startFake(t, &fakeKVM{shell: []string{"sh"}, refuse: "The target computer runs four terminals"})
	_, err := openSession(context.Background(), k, "s1", 24, 80)
	if err == nil || !strings.Contains(err.Error(), "four terminals") {
		t.Fatalf("refusal: %v", err)
	}

	soon := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	f := &fakeKVM{shell: []string{"sh"}, token: "jkvm_12345678_secret", expires: soon}
	k = startFake(t, f)
	k.token = ""
	if _, err := k.status(context.Background()); err == nil || !strings.Contains(err.Error(), "no API token") {
		t.Fatalf("no token: %v", err)
	}
	k.token = "jkvm_12345678_wrong"
	if _, err := k.status(context.Background()); err == nil || !strings.Contains(err.Error(), "refused the API token") {
		t.Fatalf("wrong token: %v", err)
	}
	k.token = "jkvm_12345678_secret"
	if st, err := k.status(context.Background()); err != nil || !st.Available {
		t.Fatalf("right token: %+v %v", st, err)
	}
	if h := k.header().Get("Authorization"); h != "Bearer jkvm_12345678_secret" {
		t.Fatalf("header %q", h)
	}
	if note := k.expiryNote(time.Now()); !strings.Contains(note, "expires") {
		t.Fatalf("expiry note: %q", note)
	}
	if note := k.expiryNote(time.Now().Add(72 * time.Hour)); !strings.Contains(note, "has expired") {
		t.Fatalf("expired note: %q", note)
	}
}

func TestPlainTextAndClip(t *testing.T) {
	cases := map[string]string{
		"\x1b[32mgreen\x1b[0m\r\n":        "green\n",
		"\x1b]0;title\x07text":            "text",
		"progress 10%\rprogress 100%\r\n": "progress 100%\n",
		"abc\b\bX":                        "aX",
		"\x1b[?2004hprompt$ ":             "prompt$ ",
	}
	for in, want := range cases {
		if got := plainText([]byte(in)); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 1000) + strings.Repeat("z", 1000)
	got, cut := clip(long, 400)
	if !cut || !strings.HasPrefix(got, strings.Repeat("a", 100)) || !strings.HasSuffix(got, strings.Repeat("z", 300)) {
		t.Fatalf("clip: %q", got)
	}
	if k, err := keyBytes("ctrl-c"); err != nil || k != "\x03" {
		t.Fatalf("ctrl-c: %q %v", k, err)
	}
	if _, err := keyBytes("hyper-x"); err == nil {
		t.Fatal("an unknown key must be refused")
	}
}

func TestExtract(t *testing.T) {
	n := "abc123"
	text := "prompt$ printf '%s_%s\\n' __MCPS abc123; { ls\n}; printf ... __MCPE abc123 $?\n__MCPS_abc123\nfile1\nfile2\n__MCPE_abc123_2\nprompt$ "
	r := extract(text, n, false)
	if r.Output != "file1\nfile2" || r.ExitCode == nil || *r.ExitCode != 2 {
		t.Fatalf("extract: %+v", r)
	}
	r = extract("echo only", n, false)
	if r.Output != "" || r.ExitCode != nil {
		t.Fatalf("not started: %+v", r)
	}
	r = extract("tail of output\n__MCPE_abc123_0\n", n, true)
	if r.Output != "tail of output" || !r.Lost {
		t.Fatalf("lost start: %+v", r)
	}
	_ = fmt.Sprint()
}

func mustRegexp(t *testing.T, s string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return re
}

// A line the shell refuses (fish: $$ is a syntax error) or one that waits
// for more input (an open quote) is reported at once, and the session is
// usable afterwards.
func TestRejectedCommand(t *testing.T) {
	previous := rejectQuiet
	rejectQuiet = time.Second
	t.Cleanup(func() { rejectQuiet = previous })
	for _, c := range shells(t) {
		if c.dialect == "powershell" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			s := openTest(t, c)
			bad := "echo 'unclosed"
			if c.dialect == "fish" {
				bad = "echo $$"
			}
			began := time.Now()
			r := mustRun(t, s, bad, 20*time.Second)
			if !r.Rejected || r.TimedOut || r.ExitCode != nil {
				t.Fatalf("%q: %+v", bad, r)
			}
			if time.Since(began) > 8*time.Second {
				t.Fatalf("rejection took %s", time.Since(began))
			}
			r = mustRun(t, s, "echo after", 10*time.Second)
			if r.Output != "after" || code(r) != "0" {
				t.Fatalf("after a rejected line: %+v", r)
			}
		})
	}
}
