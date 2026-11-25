package kvm

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestNetbirdDaemonAddrOf(t *testing.T) {
	assert.Equal(t, "unix:///run/netbird.sock", netbirdDaemonAddrOf([]string{"netbird", "service", "run"}))
	assert.Equal(t, "unix:///x", netbirdDaemonAddrOf([]string{"netbird", "service", "run", "--daemon-addr=unix:///x"}))
}

// E2: with no daemon wanted the manager loop sleeps until a nudge.
func TestNetbirdIdleWaitsForNudge(t *testing.T) {
	m := &netbirdManager{}
	assert.Zero(t, m.interval(), "idle")
	m.wantRunning = true
	assert.Equal(t, netbirdPollFast, m.interval(), "daemon wanted")
}
