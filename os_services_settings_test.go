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
