package kvm

import "testing"

func TestDeviceStatsAndMcpActivity(t *testing.T) {
	setupAPITokenTest(t, "noPassword")
	if _, err := rpcGetDeviceStats(); err != nil {
		t.Fatal(err)
	}
	stats, err := rpcGetDeviceStats()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := rpcGetDeviceStats()
	if again.CPUPercent != stats.CPUPercent {
		t.Fatalf("calls a moment apart changed the CPU share: %v then %v", stats.CPUPercent, again.CPUPercent)
	}
	if stats.CPUPercent < 0 || stats.CPUPercent > 100 || stats.MemTotalKB == 0 || stats.MemAvailKB > stats.MemTotalKB {
		t.Fatalf("stats %+v", stats)
	}
}
