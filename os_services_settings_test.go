package kvm

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

func TestMergeOSServicesConfigSwitch(t *testing.T) {
	base := *defaultOSServicesConfig()
	if base.Enabled {
		t.Fatalf("OS Services is on by default")
	}

	merged, err := mergeOSServicesConfig(base, map[string]any{"enabled": true})
	if err != nil || !merged.Enabled {
		t.Fatalf("enabled true: %+v, %v", merged, err)
	}
	if _, err := mergeOSServicesConfig(base, map[string]any{"enabled": "yes"}); err == nil {
		t.Fatalf("a string was taken for the switch")
	}
}

// The browser sends back the settings it got, with the record of the last
// sender in them. Only the KVM writes that record.
func TestMergeOSServicesConfigKeepsLastSender(t *testing.T) {
	base := *defaultOSServicesConfig()
	base.LastSenderVersion = "0.4.6"
	base.LastSenderAt = "2026-11-17T20:00:00Z"

	merged, err := mergeOSServicesConfig(base, map[string]any{
		"enabled":             true,
		"last_sender_version": "9.9.9",
		"last_sender_at":      "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.LastSenderVersion != base.LastSenderVersion || merged.LastSenderAt != base.LastSenderAt {
		t.Fatalf("a settings request changed the last sender: %+v", merged)
	}
}

// R4: a repository URL saved under an older rule does not block saving other
// settings; a new value is still checked.
func TestMergeOSServicesConfigKeepsSavedRepository(t *testing.T) {
	base := *defaultOSServicesConfig()
	base.RepositoryURL = "http://repo.example.com"
	merged, err := mergeOSServicesConfig(base, map[string]any{"video_enabled": true, "repository_url": base.RepositoryURL})
	if err != nil || merged.RepositoryURL != base.RepositoryURL {
		t.Fatalf("saving with the saved URL: %+v, %v", merged, err)
	}
	if _, err := mergeOSServicesConfig(base, map[string]any{"repository_url": "http://other.example.com"}); err == nil {
		t.Fatalf("a new plain-http host name was accepted")
	}
}

// Host info is off until switched on: by default, and for settings saved
// before host_info_enabled existed. A stored true stays true.
func TestOSServicesConfigHostInfoDefault(t *testing.T) {
	if defaultOSServicesConfig().HostInfoEnabled {
		t.Fatalf("host info is on by default")
	}
	var loaded struct {
		OSServices *OSServicesConfig `json:"os_services"`
	}
	old := `{"os_services":{"enabled":true,"terminal_enabled":true,"resolution":"1920x1080","last_sender_version":"0.4.6"}}`
	if err := json.Unmarshal([]byte(old), &loaded); err != nil {
		t.Fatal(err)
	}
	c := loaded.OSServices
	if c.HostInfoEnabled || !c.Enabled || !c.TerminalEnabled || c.FilesEnabled || c.Resolution != "1920x1080" ||
		c.LastSenderVersion != "0.4.6" {
		t.Fatalf("settings without host_info_enabled: %+v", c)
	}
	var on OSServicesConfig
	if err := json.Unmarshal([]byte(`{"host_info_enabled":true,"enabled":true}`), &on); err != nil || !on.HostInfoEnabled || !on.Enabled {
		t.Fatalf("stored true: %+v, %v", on, err)
	}
	if !on.sessionSettings(0).HostInfoEnabled {
		t.Fatalf("the session settings lack host info")
	}
	// What is written is read back the same.
	for _, enabled := range []bool{true, false} {
		c := *defaultOSServicesConfig()
		c.HostInfoEnabled = enabled
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back OSServicesConfig
		if err := json.Unmarshal(data, &back); err != nil || back != c {
			t.Fatalf("round trip of %s: %+v, %v", data, back, err)
		}
	}
	merged, err := mergeOSServicesConfig(*defaultOSServicesConfig(), map[string]any{"host_info_enabled": true})
	if err != nil || !merged.HostInfoEnabled {
		t.Fatalf("host_info_enabled true: %+v, %v", merged, err)
	}
}

// The busiest-process poll runs only while host info is on, and toggling it
// leaves no goroutine behind.
func TestHostInfoTopLoopFollowsSetting(t *testing.T) {
	s := &osServicesService{}
	s.hostInfo.init()
	base := runtime.NumGoroutine()
	for range 50 {
		s.hostInfoTopRun(true)
		s.hostInfoTopRun(true)
		s.hostInfoTopRun(false)
		s.hostInfoTopRun(false)
	}
	s.hostInfoTopRun(true)
	if s.hostInfo.topStop == nil {
		t.Fatal("the poll does not run while host info is on")
	}
	s.hostInfoTopRun(false)
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("%d goroutines after toggling, %d before", n, base)
	}
}

func TestMergeOSServicesConfigTerminalMux(t *testing.T) {
	base := *defaultOSServicesConfig()
	merged, err := mergeOSServicesConfig(base, map[string]any{
		"terminal_mux_enabled": true,
		"terminal_mux_start":   "  tmux new-session -s jetkvm ",
		"terminal_mux_restore": "tmux attach-session -t jetkvm",
	})
	if err != nil || !merged.TerminalMuxEnabled || merged.TerminalMuxStart != "tmux new-session -s jetkvm" {
		t.Fatalf("set: %+v, %v", merged, err)
	}
	if lines := terminalStartupLines(merged, "Ubuntu"); len(lines) != 1 || lines[0] != "tmux attach-session -t jetkvm || tmux new-session -s jetkvm" {
		t.Fatalf("lines %q", lines)
	}
	// Left out or null keeps a command; an empty string clears it.
	kept, err := mergeOSServicesConfig(merged, map[string]any{"terminal_mux_start": nil})
	if err != nil || kept.TerminalMuxStart != merged.TerminalMuxStart {
		t.Fatalf("null: %+v, %v", kept, err)
	}
	onlyStart, err := mergeOSServicesConfig(merged, map[string]any{"terminal_mux_restore": ""})
	if err != nil || terminalStartupLines(onlyStart, "")[0] != "tmux new-session -s jetkvm" {
		t.Fatalf("start only: %+v, %v", onlyStart, err)
	}
	onlyRestore, _ := mergeOSServicesConfig(merged, map[string]any{"terminal_mux_start": ""})
	if got := terminalStartupLines(onlyRestore, ""); got[0] != "tmux attach-session -t jetkvm" {
		t.Fatalf("restore only: %q", got)
	}
	// Turned off, the commands stay saved and nothing is typed.
	off, _ := mergeOSServicesConfig(merged, map[string]any{"terminal_mux_enabled": false})
	if len(terminalStartupLines(off, "")) != 0 || off.TerminalMuxStart == "" {
		t.Fatalf("off: %+v", off)
	}
	for _, bad := range []any{"tmux\nrm -rf x", "a\rb", "a\x1bb", string(make([]byte, 300)), 7} {
		for _, name := range []string{"terminal_mux_start", "terminal_mux_restore"} {
			if _, err := mergeOSServicesConfig(base, map[string]any{name: bad}); err == nil {
				t.Fatalf("%s accepted %q", name, bad)
			}
		}
	}
}

func TestTerminalShellChoice(t *testing.T) {
	c := *defaultOSServicesConfig()
	cases := []struct {
		setting, command, os, kind, source string
	}{
		{"", "", "Windows 11 Pro", terminalShellPowerShell, "os"},
		{"auto", "", "macOS", terminalShellPosix, "os"},
		{"", "", "", terminalShellPosix, "unknown"},
		{"", "pwsh -NoLogo", "Windows 11 Pro", terminalShellPwsh, "command"},
		{"", `C:\Windows\System32\cmd.exe`, "Windows 10", terminalShellCmd, "command"},
		{"", "exec /usr/bin/fish -l", "Ubuntu", terminalShellFish, "command"},
		{"", "mytool", "Windows 10", terminalShellPowerShell, "os"},
		{"fish", "pwsh", "Windows 10", terminalShellFish, "setting"},
	}
	for _, tc := range cases {
		c.TerminalShell, c.TerminalShellCommand = tc.setting, tc.command
		got := chooseTerminalShell(c, tc.os)
		if got.Kind != tc.kind || got.Source != tc.source {
			t.Errorf("%+v: got %+v", tc, got)
		}
	}
	if _, err := mergeOSServicesConfig(c, map[string]any{"terminal_shell": "tcsh"}); err == nil {
		t.Fatalf("an unknown shell was taken")
	}
}

func TestExpandTerminalMacros(t *testing.T) {
	line := "tmux attach -t x %QUIET% %OR% tmux new -s x"
	want := map[string]string{
		terminalShellPosix:      "tmux attach -t x 2>/dev/null || tmux new -s x",
		terminalShellFish:       "tmux attach -t x 2>/dev/null ; or tmux new -s x",
		terminalShellPwsh:       "tmux attach -t x 2>$null || tmux new -s x",
		terminalShellPowerShell: "tmux attach -t x 2>$null ; if (-not $?) { tmux new -s x }",
		terminalShellCmd:        "tmux attach -t x 2>NUL || tmux new -s x",
	}
	for kind, w := range want {
		if got := expandTerminalMacros(line, kind); got != w {
			t.Errorf("%s: %q, want %q", kind, got, w)
		}
	}
	if got := expandTerminalMacros("a %AND% b %OR% c %SEP% d %NULL% %HOME% %NOPE%", terminalShellPowerShell); got != "a ; if ($?) { b ; if (-not $?) { c ; d $null $HOME %NOPE% } }" {
		t.Errorf("powershell blocks: %q", got)
	}
	if got := expandTerminalMacros("cd %HOME% %SEP% x", terminalShellCmd); got != "cd %USERPROFILE% & x" {
		t.Errorf("cmd: %q", got)
	}
	// The custom shell command goes to the OS's default shell, the
	// multiplexer to the shell it starts.
	c := *defaultOSServicesConfig()
	c.TerminalShellCommand = "pwsh -NoLogo"
	c.TerminalMuxEnabled, c.TerminalMuxStart, c.TerminalMuxRestore = true, "s", "r"
	if got := terminalStartupLines(c, "Windows 11"); len(got) != 2 || got[0] != "pwsh -NoLogo" || got[1] != "r || s" {
		t.Errorf("lines %q", got)
	}
	c.TerminalShellCommand = ""
	if got := terminalStartupLines(c, "Windows 11"); len(got) != 1 || got[0] != "r ; if (-not $?) { s }" {
		t.Errorf("windows default: %q", got)
	}
}
