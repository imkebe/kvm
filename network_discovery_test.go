package kvm

import (
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanAddresses(t *testing.T) {
	cases := []struct {
		prefix      string
		count       int
		first, last string
	}{
		{"192.168.1.0/24", 254, "192.168.1.1", "192.168.1.254"},
		{"10.0.0.0/30", 2, "10.0.0.1", "10.0.0.2"},
		{"10.0.0.4/31", 2, "10.0.0.4", "10.0.0.5"},
		{"10.0.0.9/32", 1, "10.0.0.9", "10.0.0.9"},
		{"172.16.0.0/22", 1022, "172.16.0.1", "172.16.3.254"},
	}
	for _, c := range cases {
		addrs, err := scanAddresses(netip.MustParsePrefix(c.prefix))
		if err != nil {
			t.Fatalf("%s: %v", c.prefix, err)
		}
		if len(addrs) != c.count || addrs[0].String() != c.first || addrs[len(addrs)-1].String() != c.last {
			t.Errorf("%s: got %d addresses %s..%s", c.prefix, len(addrs), addrs[0], addrs[len(addrs)-1])
		}
	}
	if _, err := scanAddresses(netip.MustParsePrefix("10.0.0.0/21")); err == nil {
		t.Error("a /21 should be refused")
	}
}

type failingARP struct{ reads atomic.Int32 }

func (f *failingARP) request(netip.Addr, netip.Addr) error { return errors.New("network is down") }

func (f *failingARP) replies(deadline time.Time, _ func(netip.Addr) bool, _ func(netip.Addr, net.HardwareAddr)) {
	f.reads.Add(1)
	time.Sleep(time.Until(deadline))
}

// E1: a failed send must stop the reader before the socket is closed.
func TestARPScanReturnsOnSendError(t *testing.T) {
	sock := &failingARP{}
	start := time.Now()
	if _, err := arpScanOn(sock, &net.Interface{}, []netip.Addr{netip.MustParseAddr("192.0.2.1")}); err == nil {
		t.Fatal("expected the send error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("returned after %v", time.Since(start))
	}
	after := sock.reads.Load()
	time.Sleep(500 * time.Millisecond)
	if n := sock.reads.Load(); n != after {
		t.Fatalf("reader still running after return: %d reads, then %d", after, n)
	}
}

// scanNetwork is in the device API: only own networks and private ranges.
func TestScanTargetAllowed(t *testing.T) {
	own := []netip.Prefix{netip.MustParsePrefix("100.111.29.244/16"), netip.MustParsePrefix("203.0.113.7/24")}
	for target, want := range map[string]bool{
		"192.168.1.0/24": true,
		"10.20.0.0/22":   true,
		"172.16.4.9/32":  true,
		"203.0.113.0/24": true,
		"203.0.113.9/32": true,
		"100.111.4.0/24": true,
		"203.0.112.0/22": false, // wider than the own /24
		"8.8.8.0/24":     false,
		"100.64.0.0/24":  false,
		"172.32.0.0/24":  false,
		"192.169.0.0/24": false,
		"1.1.1.1/32":     false,
	} {
		if got := scanTargetAllowed(netip.MustParsePrefix(target).Masked(), own); got != want {
			t.Errorf("%s: allowed %v, want %v", target, got, want)
		}
	}
}
