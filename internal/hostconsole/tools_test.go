package hostconsole

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolsOverMCP drives the tools through an MCP client, as Claude Code
// would, against the fake KVM with a bash shell.
func TestToolsOverMCP(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	k := startFake(t, &fakeKVM{shell: []string{"bash", "--norc", "--noprofile"}})
	srv := &server{kvm: k, sessions: map[string]*session{}}
	t.Cleanup(srv.closeAll)
	m := mcp.NewServer(&mcp.Implementation{Name: "hostconsole", Version: version}, nil)
	srv.register(m)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := m.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "console_run") || !strings.Contains(got, "host_status") {
		t.Fatalf("tools: %s", got)
	}

	call := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String(), res.IsError
	}

	if out, isErr := call("host_status", nil); isErr || !strings.Contains(out, "ready: true") {
		t.Fatalf("host_status: %q", out)
	}
	if out, isErr := call("console_read", nil); !isErr || !strings.Contains(out, "no session") {
		t.Fatalf("read without a session: %q", out)
	}
	// console_run opens a session when none is open.
	out, isErr := call("console_run", map[string]any{"command": "echo $((6*7)); (exit 3)"})
	if isErr || !strings.Contains(out, "exit code 3") || !strings.Contains(out, "42") {
		t.Fatalf("console_run: %q", out)
	}
	if out, isErr := call("console_send", map[string]any{"text": "echo typed", "keys": []string{"enter"}}); isErr {
		t.Fatalf("console_send: %q", out)
	}
	if out, isErr := call("console_read", map[string]any{"until": "typed\\n", "wait_ms": 5000}); isErr || !strings.Contains(out, "typed") {
		t.Fatalf("console_read: %q", out)
	}
	if out, isErr := call("console_screen", nil); isErr || !strings.Contains(out, "typed") {
		t.Fatalf("console_screen: %q", out)
	}
	if out, isErr := call("console_send", map[string]any{"keys": []string{"hyper-q"}}); !isErr {
		t.Fatalf("unknown key accepted: %q", out)
	}
	if out, isErr := call("console_close", nil); isErr || !strings.Contains(out, "closed") {
		t.Fatalf("console_close: %q", out)
	}
	if out, isErr := call("console_run", map[string]any{"command": " "}); !isErr {
		t.Fatalf("empty command accepted: %q", out)
	}
}

// PowerShell takes $? right after the command, inside the block: after the
// block it stays true when a cmdlet had a non-terminating error.
func TestPowerShellWrapTakesStatusInsideTheBlock(t *testing.T) {
	w := powerShell{}.wrap("Get-Item C:\\missing", "n1")
	inner := strings.Index(w, "Get-Item C:\\missing\r$__mcpOk=$?\r}")
	if inner < 0 {
		t.Fatalf("status not taken inside the block: %q", w)
	}
	if !strings.Contains(w[:inner], "$__mcpOk=$true; . {") {
		t.Fatalf("status not reset before the block: %q", w)
	}
	if strings.Contains(w[inner:], "}; $__mcpOk=$?") {
		t.Fatalf("status still taken after the block: %q", w)
	}
}

// A shell's screen is bounded before anything reaches the KVM.
func TestShellSizeIsBounded(t *testing.T) {
	srv := &server{kvm: testKVMClient("127.0.0.1:1", ""), sessions: map[string]*session{}}
	for _, size := range [][2]int{{maxRows + 1, 80}, {24, maxCols + 1}} {
		if _, err := srv.open(context.Background(), size[0], size[1]); err == nil || !strings.Contains(err.Error(), "at most") {
			t.Fatalf("%v: %v", size, err)
		}
	}
}
