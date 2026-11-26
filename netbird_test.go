package kvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jetkvm/kvm/internal/netbird"
	"github.com/jetkvm/kvm/internal/tailscale"
	"github.com/stretchr/testify/assert"
)

func TestUSBNetworkDHCPArgsWithoutNetbird(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	args := usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, usbNetbirdPeer{})
	assert.Contains(t, args, "--dhcp-range=10.77.0.10,10.77.0.200,255.255.255.0,12h")
	assert.Contains(t, args, "--dhcp-option=option:router,10.77.0.1")
	for _, arg := range args {
		assert.NotContains(t, arg, "--dhcp-option=121")
		assert.NotContains(t, arg, "--dhcp-option=249")
	}
}

func TestTailscaleUplink(t *testing.T) {
	data := []byte(`{"BackendState":"Running","TUN":true,"Peer":{
		"a":{"PrimaryRoutes":["192.168.50.0/24","0.0.0.0/0"]},
		"b":{"PrimaryRoutes":["10.9.0.0/16","fd7a::/64"]},
		"c":{}}}`)
	info, err := tailscale.ParseStatus(data, "")
	assert.NoError(t, err)
	exists := func(string) bool { return true }
	up, routes := tailscaleUplink(info, exists)
	assert.True(t, up)
	assert.Equal(t, []string{"100.64.0.0/10", "10.9.0.0/16", "192.168.50.0/24"}, routes)

	up, _ = tailscaleUplink(info, func(string) bool { return false })
	assert.False(t, up, "no tailscale0 interface")
	info.TUN = false
	up, _ = tailscaleUplink(info, exists)
	assert.False(t, up, "userspace networking")
	info, _ = tailscale.ParseStatus([]byte(`{"BackendState":"NeedsLogin","TUN":true}`), "")
	up, _ = tailscaleUplink(info, exists)
	assert.False(t, up)
}

func TestUSBNetworkDHCPArgsNetbirdMode(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	cfg.Mode = usbUplinkNetbird
	args := usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", []string{"100.64.0.0/10", "10.20.0.0/24"}, usbNetbirdPeer{})
	assert.Contains(t, args, "--dhcp-range=10.77.0.10,10.77.0.200,255.255.255.0,1h")
	// Only the VPN's networks: no default route, no DNS server.
	assert.Contains(t, args, "--dhcp-option=option:router")
	assert.Contains(t, args, "--dhcp-option=option:dns-server")
	list := "100.64.0.0/10,10.77.0.1,10.20.0.0/24,10.77.0.1"
	assert.Contains(t, args, "--dhcp-option=121,"+list)
	assert.Contains(t, args, "--dhcp-option=249,"+list)
	for _, arg := range args {
		assert.NotContains(t, arg, "0.0.0.0/0")
		assert.NotContains(t, arg, "router,")
	}

	// NetBird not connected: no routes yet, and a lease short enough that
	// the target gets them soon after NetBird connects.
	args = usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, usbNetbirdPeer{})
	assert.Contains(t, args, "--dhcp-range=10.77.0.10,10.77.0.200,255.255.255.0,2m")
	for _, arg := range args {
		assert.NotContains(t, arg, "--dhcp-option=121")
	}
}

func TestUSBNetworkModeValidation(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	cfg.Mode = usbUplinkTailscale
	assert.NoError(t, validateUSBNetworkConfig(normalizeUSBNetworkConfig(cfg)))
	cfg.Mode = "vpn"
	assert.Error(t, validateUSBNetworkConfig(normalizeUSBNetworkConfig(cfg)))
}

func TestUSBNetworkNATUpstreams(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	up := map[string]usbNetworkUplink{
		usbUplinkNetbird:   {Interface: "wt0", Up: true},
		usbUplinkTailscale: {Interface: "tailscale0", Up: true},
	}
	assert.Equal(t, []string{"eth0"}, usbNetworkNATUpstreams(cfg, up))
	assert.Equal(t, "", usbNetworkVPNInterface(cfg))
	cfg.Mode = "routed"
	assert.Empty(t, usbNetworkNATUpstreams(cfg, up))
	// NetBird mode: the target has its own peer address, nothing is NATed.
	cfg.Mode = usbUplinkNetbird
	assert.Empty(t, usbNetworkNATUpstreams(cfg, up))
	assert.Equal(t, "wt1", usbNetworkVPNInterface(cfg))
	cfg.Mode = usbUplinkTailscale
	assert.Equal(t, []string{"tailscale0"}, usbNetworkNATUpstreams(cfg, up))
	assert.Equal(t, "tailscale0", usbNetworkVPNInterface(cfg))
}

func TestUSBNetworkTargetRoutes(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	up := map[string]usbNetworkUplink{
		usbUplinkNetbird: {Interface: "wt0", Up: true,
			Routes: []string{"100.64.0.0/10", "10.77.0.0/24", "10.20.0.0/24", "10.77.0.128/25", "10.0.0.0/8", "10.20.0.0/24"}},
		usbUplinkTailscale: {Interface: "tailscale0", Up: true, Routes: []string{"100.64.0.0/10", "192.168.50.0/24"}},
	}
	assert.Nil(t, usbNetworkTargetRoutes(cfg, up))
	cfg.Mode = usbUplinkNetbird
	// The USB subnet itself (the KVM is its routing peer), overlapping
	// routes and duplicates are not handed to the target.
	assert.Equal(t, []string{"100.64.0.0/10", "10.20.0.0/24"}, usbNetworkTargetRoutes(cfg, up))
	cfg.Mode = usbUplinkTailscale
	assert.Equal(t, []string{"100.64.0.0/10", "192.168.50.0/24"}, usbNetworkTargetRoutes(cfg, up))
	up[usbUplinkTailscale] = usbNetworkUplink{Interface: "tailscale0", Routes: []string{"100.64.0.0/10"}}
	assert.Nil(t, usbNetworkTargetRoutes(cfg, up), "Tailscale not up")
}

func TestUSBNetworkNFTCommandsVPNMode(t *testing.T) {
	commands := usbNetworkNFTCommands("10.77.0.0/24", []string{"wt0"}, "usb0", "wt0", true)
	assert.Len(t, commands, 5+usbNetworkInputCommands)
	assert.Equal(t, []string{"nft", "add", "rule", "ip", "jetkvm_usb_network", "postrouting", "ip", "saddr", "10.77.0.0/24", "oifname", "wt0", "counter", "masquerade"}, commands[2])
	assert.Equal(t, []string{"nft", "add", "rule", "ip", "jetkvm_usb_network", "forward", "iifname", "usb0", "oifname", "!=", "wt0", "counter", "drop"}, commands[4])
	// NetBird down: no masquerade, the forward guard stays.
	assert.Len(t, usbNetworkNFTCommands("10.77.0.0/24", nil, "usb0", "wt0", true), 4+usbNetworkInputCommands)
	// NAT mode: masquerade only.
	assert.Len(t, usbNetworkNFTCommands("10.77.0.0/24", []string{"eth0"}, "usb0", "", true), 3+usbNetworkInputCommands)
}

// R7: without nft (or nft_ct) USB Ethernet still starts as before the input
// filter: Routed with no table, NAT without the input chain.
func TestUSBNetworkNATWithoutInputFilter(t *testing.T) {
	bin, modules := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin)
	savedRoot, savedInsmod := oemModulesRoot, insmodModule
	t.Cleanup(func() { oemModulesRoot, insmodModule = savedRoot, savedInsmod })
	oemModulesRoot = modules
	insmodModule = func(string) error { return nil }

	backend, err := ensureUSBNetworkNAT("10.77.0.0/24", nil, "usb0", "")
	assert.NoError(t, err, "Routed without nft")
	assert.Empty(t, backend)
	_, err = ensureUSBNetworkNAT("10.77.0.0/24", []string{"eth0"}, "usb0", "")
	assert.Error(t, err, "NAT without nft")

	log := filepath.Join(bin, "nft.log")
	assert.NoError(t, os.WriteFile(filepath.Join(bin, "nft"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(modules, "modules.dep"), []byte("nf_tables.ko:\nnft_chain_nat.ko: nf_tables.ko\nnft_masq.ko:\nnft_counter.ko:\n"), 0o644))
	backend, err = ensureUSBNetworkNAT("10.77.0.0/24", []string{"eth0"}, "usb0", "")
	assert.NoError(t, err, "NAT without nft_ct")
	assert.Equal(t, "nft", backend)
	data, _ := os.ReadFile(log)
	assert.Contains(t, string(data), "masquerade")
	assert.NotContains(t, string(data), "add table inet")
}

// The input table and chain plus seven rules.
const usbNetworkInputCommands = 9

// S4: in every mode the target reaches on the KVM only DHCP, DNS and ICMP.
func TestUSBNetworkNFTInputPolicy(t *testing.T) {
	modes := map[string][][]string{
		"nat":       usbNetworkNFTCommands("10.77.0.0/24", []string{"eth0"}, "usb0", "", true),
		"routed":    usbNetworkNFTCommands("10.77.0.0/24", nil, "usb0", "", true),
		"netbird":   usbNetworkNFTCommands("10.77.0.0/24", nil, "usb0", netbirdTargetInterface, true),
		"tailscale": usbNetworkNFTCommands("10.77.0.0/24", []string{"tailscale0"}, "usb0", "tailscale0", true),
	}
	for mode, commands := range modes {
		var rules []string
		for _, c := range commands {
			rules = append(rules, strings.Join(c, " "))
		}
		text := strings.Join(rules, "\n")
		for _, want := range []string{
			"nft add chain inet jetkvm_usb_network input { type filter hook input priority filter ; policy accept ; }",
			"input iifname usb0 ct state established,related accept",
			"input iifname usb0 udp dport 67 accept",
			"input iifname usb0 udp dport 53 accept",
			"input iifname usb0 tcp dport 53 accept",
			"input iifname usb0 meta l4proto icmp accept",
			"input iifname usb0 meta l4proto ipv6-icmp accept",
		} {
			assert.Contains(t, text, want, mode)
		}
		assert.True(t, strings.HasSuffix(text, "nft add rule inet jetkvm_usb_network input iifname usb0 counter drop"), "%s: drop must be the last input rule", mode)
	}
}

func TestNewUSBNetbirdPeer(t *testing.T) {
	peer := newUSBNetbirdPeer("100.111.29.244/16", "100.111.119.155/16")
	assert.True(t, peer.ready())
	assert.Equal(t, usbNetbirdPeer{Address: "100.111.29.244", Gateway: "100.111.119.155", PrefixLen: 16, Network: "100.111.0.0/16"}, peer)
	assert.Equal(t, "255.255.0.0", peer.netmask())
	assert.False(t, newUSBNetbirdPeer("100.111.29.244/16", "").ready())
	assert.False(t, newUSBNetbirdPeer("", "100.111.119.155/16").ready())
	assert.False(t, newUSBNetbirdPeer("100.111.29.244/16", "100.112.1.1/16").ready(), "different networks")
	assert.False(t, newUSBNetbirdPeer("100.111.29.244/16", "100.111.29.244/16").ready(), "same address")
	assert.False(t, newUSBNetbirdPeer("fd00::1/64", "fd00::2/64").ready(), "IPv6")
}

func TestUSBNetworkDHCPArgsNetbirdPeer(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	cfg.Mode = usbUplinkNetbird
	peer := newUSBNetbirdPeer("100.111.29.244/16", "100.111.119.155/16")
	args := usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", []string{"100.64.0.0/10"}, peer)
	assert.Contains(t, args, "--dhcp-range=100.111.29.244,100.111.29.244,255.255.0.0,2m")
	assert.Contains(t, args, "--port=0")
	assert.Contains(t, args, "--dhcp-option=option:router")
	assert.Contains(t, args, "--dhcp-option=option:dns-server")
	for _, arg := range args {
		assert.NotContains(t, arg, "10.77.0")
		assert.NotContains(t, arg, "--dhcp-option=121")
	}
	// Peer not ready: the plain USB subnet with a short lease.
	args = usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, usbNetbirdPeer{})
	assert.Contains(t, args, "--dhcp-range=10.77.0.10,10.77.0.200,255.255.255.0,2m")
	// Another mode ignores a peer.
	cfg.Mode = "nat"
	args = usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, peer)
	assert.Contains(t, args, "--dhcp-range=10.77.0.10,10.77.0.200,255.255.255.0,12h")
}

func TestUSBNetworkDHCPArgsNetbirdPeerDNS(t *testing.T) {
	cfg := defaultUSBNetworkConfig()
	cfg.Mode = usbUplinkNetbird
	peer := newUSBNetbirdPeer("100.111.29.244/16", "100.111.119.155/16")
	peer.DNSZone = "netbird.selfhosted"
	args := usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, peer)
	assert.NotContains(t, args, "--port=0")
	assert.Contains(t, args, "--dhcp-option=option:dns-server,100.111.119.155")
	assert.Contains(t, args, "--dhcp-option=option:domain-search,netbird.selfhosted")
	assert.Contains(t, args, "--server=/netbird.selfhosted/127.0.0.1#5054")
	assert.Contains(t, args, "--server=/111.100.in-addr.arpa/127.0.0.1#5054")
	assert.NotContains(t, args, "--no-resolv")

	peer.DNSDomains = usbPeerDNSDomains([]string{"corp.example", "lab.example", "corp.example"})
	assert.Equal(t, "corp.example,lab.example", peer.DNSDomains)
	args = usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, peer)
	assert.Contains(t, args, "--server=/corp.example/127.0.0.1#5054")
	assert.Contains(t, args, "--server=/lab.example/127.0.0.1#5054")
	assert.NotContains(t, args, "--no-resolv")

	// A nameserver group for all domains: NetBird answers everything.
	peer.DNSDomains = usbPeerDNSDomains([]string{".", "corp.example"})
	args = usbNetworkDHCPArgs(cfg, "255.255.255.0", "/tmp/l", "/tmp/p", nil, peer)
	assert.Contains(t, args, "--no-resolv")
	assert.Contains(t, args, "--server=127.0.0.1#5054")

	// DNS settings alone do not change the link.
	plain := newUSBNetbirdPeer("100.111.29.244/16", "100.111.119.155/16")
	assert.Equal(t, plain, peer.link())
	assert.NotEqual(t, plain, peer)
}

func TestReverseZone(t *testing.T) {
	assert.Equal(t, "111.100.in-addr.arpa", reverseZone("100.111.0.0/16"))
	assert.Equal(t, "100.in-addr.arpa", reverseZone("100.0.0.0/8"))
	assert.Equal(t, "", reverseZone("100.64.0.0/10"))
	assert.Equal(t, "", reverseZone("fd00::/64"))
}

func TestNetbirdTargetRuleHandles(t *testing.T) {
	want := netbirdTargetForwardRule("usb0", "100.111.29.244")
	assert.Equal(t, `"jetkvm-usb-target"`, want[len(want)-1])
	listing := `table ip netbird_target {
	chain netbird-acl-forward-filter { # handle 9
		type filter hook forward priority filter; policy accept;
		iifname "wt1" oifname "usb0" ip daddr 100.111.29.244 jump netbird-acl-input-rules comment "jetkvm-usb-target" # handle 31
		iifname "wt1" oifname "usb0" ip daddr 100.111.29.244 jump netbird-acl-input-rules comment "jetkvm-usb-target" # handle 32
		iifname "wt1" oifname "usb0" ip daddr 100.111.1.1 jump netbird-acl-input-rules comment "jetkvm-usb-target" # handle 33
		iifname "wt1" jump netbird-rt-fwd # handle 12
		iifname "wt1" drop # handle 13
	}
}`
	keep, stale := netbirdTargetRuleHandles(listing, want)
	assert.True(t, keep)
	assert.Equal(t, []string{"32", "33"}, stale)
	keep, stale = netbirdTargetRuleHandles("", want)
	assert.False(t, keep)
	assert.Empty(t, stale)
}

func TestNetbirdDaemonAddrOf(t *testing.T) {
	assert.Equal(t, "unix:///run/netbird.sock", netbirdDaemonAddrOf([]string{"netbird", "service", "run"}))
	assert.Equal(t, netbirdTargetDaemonAddr, netbirdDaemonAddrOf([]string{"netbird", "service", "run", "--daemon-addr", netbirdTargetDaemonAddr}))
	assert.Equal(t, "unix:///x", netbirdDaemonAddrOf([]string{"netbird", "service", "run", "--daemon-addr=unix:///x"}))
}

// E2: with no daemon wanted the manager loop sleeps until a nudge.
func TestNetbirdIdleWaitsForNudge(t *testing.T) {
	m := &netbirdManager{}
	assert.Zero(t, m.interval(), "idle")
	m.wantRunning = true
	assert.Equal(t, netbirdPollFast, m.interval(), "daemon wanted")
}

// E2: the forward rule is listed with nft once per address, not every poll;
// R2: and again on a reconnect or after netbirdTargetRecheck.
func TestNetbirdTargetForwardRuleCheckedOnce(t *testing.T) {
	usb := "usb0"
	if usbNetwork != nil {
		usb = usbNetwork.Config().InterfaceName
	}
	want := netbirdTargetForwardRule(usb, "100.111.29.244")
	listing := fmt.Sprintf(`iifname "%s" oifname "%s" ip daddr %s jump netbird-acl-input-rules comment "jetkvm-usb-target" # handle 31`, want[1], want[3], want[6])
	calls := 0
	saved := netbirdTargetListChain
	savedStatus := netbirdTargetMgr.status
	t.Cleanup(func() {
		netbirdTargetListChain = saved
		netbirdTargetLast.forwardRule = ""
		netbirdTargetMgr.status = savedStatus
	})
	netbirdTargetListChain = func() ([]byte, error) { calls++; return []byte(listing), nil }
	setStatus := func(st *netbird.Status) {
		netbirdTargetMgr.mu.Lock()
		netbirdTargetMgr.status = st
		netbirdTargetMgr.mu.Unlock()
	}
	setStatus(&netbird.Status{})
	assert.NoError(t, ensureNetbirdTargetForwardRule("100.111.29.244"))
	assert.NoError(t, ensureNetbirdTargetForwardRule("100.111.29.244"))
	assert.Equal(t, 1, calls)

	setStatus(&netbird.Status{ManagementConnected: true, SignalConnected: true})
	assert.NoError(t, ensureNetbirdTargetForwardRule("100.111.29.244"))
	assert.Equal(t, 2, calls, "reconnected")

	netbirdTargetLast.forwardChecked = time.Now().Add(-netbirdTargetRecheck)
	assert.NoError(t, ensureNetbirdTargetForwardRule("100.111.29.244"))
	assert.Equal(t, 3, calls, "recheck interval passed")
}
