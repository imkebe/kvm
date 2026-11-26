package kvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jetkvm/kvm/internal/netbird"
	"github.com/vishvananda/netlink"
)

// The USB Ethernet target as its own NetBird peer. In the USB Ethernet
// NetBird mode the KVM runs a second NetBird client for the target computer:
// its own identity, setup key and 100.x address, its own WireGuard interface
// (wt1), socket, state folder and firewall table. The KVM takes that address
// off wt1 and hands it to the target by DHCP, and routes the target's traffic
// into wt1 and back: the target is reached at, and talks from, its own NetBird
// address, without NAT. usbnetwork_peer.go sets up the USB side.

const (
	netbirdTargetDir        = netbirdDir + "/target"
	netbirdTargetConfigFile = netbirdTargetDir + "/default.json"
	netbirdTargetStateDir   = netbirdTargetDir + "/state"
	netbirdTargetSocket     = "/run/netbird-target.sock"
	netbirdTargetDaemonAddr = "unix://" + netbirdTargetSocket
	netbirdTargetKeyFile    = "/run/jetkvm-netbird-target-key"
	// netbirdTargetInterface and its WireGuard port, apart from the KVM's.
	netbirdTargetInterface = "wt1"
	netbirdTargetPort      = "51821"
	// netbirdTargetTable is the second client's nftables table.
	netbirdTargetTable = "netbird_target"
	// netbirdTargetRuleComment marks the forward rule the app keeps in it.
	netbirdTargetRuleComment = "jetkvm-usb-target"
)

// NetbirdTargetConfig is the saved setting of the target's peer.
type NetbirdTargetConfig struct {
	// Enabled: joined; the peer runs while USB Ethernet is in NetBird mode.
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
}

// NetbirdTargetState is the target's peer as the UI shows it.
type NetbirdTargetState struct {
	Enabled       bool            `json:"enabled"`
	Hostname      string          `json:"hostname,omitempty"`
	DaemonRunning bool            `json:"daemonRunning"`
	Joining       bool            `json:"joining"`
	Status        *netbird.Status `json:"status,omitempty"`
	Error         string          `json:"error,omitempty"`
	// Address is the target's NetBird address while the USB link hands it
	// out (both peers have their addresses and USB Ethernet runs).
	Address string `json:"address,omitempty"`
	// DNSZone is the NetBird zone the target resolves through the KVM, and
	// DNSServer the address it is given for that.
	DNSZone   string `json:"dnsZone,omitempty"`
	DNSServer string `json:"dnsServer,omitempty"`
}

var netbirdTargetMgr = &netbirdManager{
	name:       "target",
	daemonAddr: netbirdTargetDaemonAddr,
	socket:     netbirdTargetSocket,
	configFile: netbirdTargetConfigFile,
	env: []string{
		"NB_STATE_DIR=" + netbirdTargetStateDir,
		"NB_NFTABLES_TABLE=" + netbirdTargetTable,
		// Leave the KVM client's policy routing rules alone.
		"NB_DISABLE_CUSTOM_ROUTING=true",
	},
	prepare:      func() error { return os.MkdirAll(netbirdTargetStateDir, 0o700) },
	resolverAddr: netbirdTargetResolverAddr,
	wake:         make(chan struct{}, 1),
}

// netbirdTargetLast keeps the addresses last seen, so a daemon restart (no
// status for a moment) does not take the target's address away.
var netbirdTargetLast struct {
	sync.Mutex
	target  string // "100.111.29.244/16"
	gateway string // the KVM's own NetBird address
	// The target's NetBird DNS zone and nameserver group domains.
	dnsZone    string
	dnsDomains string
	// forwardRule: the forward rule was last checked, at forwardChecked, for
	// this interface, address, daemon process and connection state.
	forwardRule    string
	forwardChecked time.Time
}

// netbirdTargetRecheck: NetBird can rebuild its table on a reconnect inside
// the same daemon process, so the rule is listed again this often.
const netbirdTargetRecheck = time.Minute

// netbirdTargetListChain lists the second client's forward filter; replaced in tests.
var netbirdTargetListChain = func() ([]byte, error) {
	return exec.Command("nft", "-a", "list", "chain", "ip", netbirdTargetTable, "netbird-acl-forward-filter").Output()
}

func netbirdTargetSettings() NetbirdTargetConfig {
	settings := netbirdSettings()
	if settings.Target == nil {
		return NetbirdTargetConfig{}
	}
	return *settings.Target
}

func initNetbirdTarget() {
	netbirdTargetMgr.onChange = netbirdTargetChanged
	refreshNetbirdTarget()
	go netbirdTargetMgr.loop()
}

// refreshNetbirdTarget starts or stops the target's daemon: it runs while the
// peer is joined and USB Ethernet is in NetBird mode.
func refreshNetbirdTarget() {
	want := netbirdInstalled() && netbirdTargetSettings().Enabled && usbNetwork != nil &&
		usbNetwork.Config().Mode == usbUplinkNetbird
	m := netbirdTargetMgr
	m.mu.Lock()
	m.wantRunning = want
	m.mu.Unlock()
	m.nudge()
}

func stopNetbirdTarget() {
	m := netbirdTargetMgr
	m.cancelJoin()
	m.mu.Lock()
	m.wantRunning = false
	m.stopDaemonLocked()
	m.mu.Unlock()
	netbirdTargetChanged()
}

func (m *netbirdManager) targetState() NetbirdTargetState {
	settings := netbirdTargetSettings()
	var link usbNetbirdPeer
	if usbNetwork != nil {
		link = usbNetwork.PeerLink()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return NetbirdTargetState{
		Enabled:       settings.Enabled,
		Hostname:      settings.Hostname,
		DaemonRunning: m.daemon != nil,
		Joining:       m.joining,
		Status:        m.status,
		Error:         m.lastError,
		Address:       link.Address,
		DNSZone:       link.DNSZone,
		DNSServer:     link.dnsServer(),
	}
}

// netbirdTargetChanged runs after each poll of the target's daemon: it keeps
// the target's address off wt1, keeps the forward rule, passes the addresses
// to the USB link, and publishes the NetBird state.
func netbirdTargetChanged() {
	peer := netbirdTargetPeer()
	if peer.ready() {
		if err := removeNetbirdTargetAddress(peer); err != nil {
			netbirdLogger.Warn().Err(err).Msg("failed to take the target's address off wt1")
		}
		if err := ensureNetbirdTargetForwardRule(peer.Address); err != nil {
			netbirdLogger.Debug().Err(err).Msg("target forward rule")
		}
	}
	if usbNetwork != nil {
		if err := usbNetwork.SetNetbirdPeer(peer); err != nil {
			netbirdLogger.Warn().Err(err).Msg("failed to set up the USB link for the target's peer")
		}
	}
	netbirdMgr.publish()
}

// netbirdTargetPeer works out the target's address and gateway from the two
// daemons' status, falling back to the last ones seen while a daemon has no
// status but the peers stay joined.
func netbirdTargetPeer() usbNetbirdPeer {
	netbirdTargetLast.Lock()
	defer netbirdTargetLast.Unlock()
	if !netbirdTargetSettings().Enabled || !netbirdSettings().Enabled {
		netbirdTargetLast.target, netbirdTargetLast.gateway = "", ""
		return usbNetbirdPeer{}
	}
	if ipNet := netbirdStatusIPNet(netbirdTargetMgr); ipNet != "" {
		netbirdTargetLast.target = ipNet
	}
	if ipNet := netbirdStatusIPNet(netbirdMgr); ipNet != "" {
		netbirdTargetLast.gateway = ipNet
	}
	netbirdTargetMgr.mu.Lock()
	if status := netbirdTargetMgr.status; status != nil && status.FQDN != "" {
		netbirdTargetLast.dnsZone = netbird.DNSZone(status.FQDN)
		netbirdTargetLast.dnsDomains = usbPeerDNSDomains(status.NameserverDomains)
	}
	netbirdTargetMgr.mu.Unlock()
	peer := newUSBNetbirdPeer(netbirdTargetLast.target, netbirdTargetLast.gateway)
	if peer.ready() {
		peer.DNSZone = netbirdTargetLast.dnsZone
		peer.DNSDomains = netbirdTargetLast.dnsDomains
	}
	return peer
}

func netbirdStatusIPNet(m *netbirdManager) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == nil {
		return ""
	}
	return m.status.IPNet
}

// removeNetbirdTargetAddress takes the target's address off wt1, so the KVM
// forwards traffic for it to the target instead of taking it itself. NetBird
// sets it when the interface comes up; this runs after every poll.
func removeNetbirdTargetAddress(peer usbNetbirdPeer) error {
	link, err := netlink.LinkByName(netbirdTargetInterface)
	if err != nil {
		return nil // not up yet
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if addr.IP.Equal(net.ParseIP(peer.Address)) {
			if err := netlink.AddrDel(link, &addr); err != nil {
				return err
			}
			netbirdLogger.Info().Str("address", peer.Address).Msg("moved the target's NetBird address to the USB link")
		}
	}
	return nil
}

// ensureNetbirdTargetForwardRule keeps one rule at the top of the second
// client's forward filter: traffic from wt1 to the target's address on the USB
// link goes through NetBird's own access rules for that peer. Without it the
// filter drops new connections to the target (it only passes routed networks).
// It forks nft only when the interface, the address, the daemon (which
// rebuilds its table at start) or its connection state changed since the last
// successful check, or netbirdTargetRecheck passed.
func ensureNetbirdTargetForwardRule(address string) error {
	usb := "usb0"
	if usbNetwork != nil {
		usb = usbNetwork.Config().InterfaceName
	}
	connected := false
	netbirdTargetMgr.mu.Lock()
	if st := netbirdTargetMgr.status; st != nil {
		connected = st.ManagementConnected && st.SignalConnected
	}
	netbirdTargetMgr.mu.Unlock()
	key := fmt.Sprint(usb, " ", address, " ", netbirdTargetMgr.daemonPID(), " ", connected)
	netbirdTargetLast.Lock()
	defer netbirdTargetLast.Unlock()
	if key == netbirdTargetLast.forwardRule && time.Since(netbirdTargetLast.forwardChecked) < netbirdTargetRecheck {
		return nil
	}
	listing, err := netbirdTargetListChain()
	if err != nil {
		return fmt.Errorf("the target's firewall table is not there yet: %w", err)
	}
	want := netbirdTargetForwardRule(usb, address)
	keep, stale := netbirdTargetRuleHandles(string(listing), want)
	for _, handle := range stale {
		_ = runCommand([]string{"nft", "delete", "rule", "ip", netbirdTargetTable, "netbird-acl-forward-filter", "handle", handle}, true)
	}
	if !keep {
		args := append([]string{"nft", "insert", "rule", "ip", netbirdTargetTable, "netbird-acl-forward-filter"}, want...)
		if err := runCommand(args, false); err != nil {
			return err
		}
	}
	netbirdTargetLast.forwardRule = key
	netbirdTargetLast.forwardChecked = time.Now()
	return nil
}

func netbirdTargetForwardRule(usbInterface, address string) []string {
	return []string{
		"iifname", netbirdTargetInterface, "oifname", usbInterface, "ip", "daddr", address,
		"jump", "netbird-acl-input-rules", "comment", `"` + netbirdTargetRuleComment + `"`,
	}
}

// netbirdTargetRuleHandles reads `nft -a list chain` output: whether the
// wanted rule is there, and the handles of the app's other (stale) rules.
func netbirdTargetRuleHandles(listing string, want []string) (bool, []string) {
	wantText := fmt.Sprintf(`iifname "%s" oifname "%s" ip daddr %s jump netbird-acl-input-rules`, want[1], want[3], want[6])
	keep := false
	var stale []string
	for _, line := range strings.Split(listing, "\n") {
		if !strings.Contains(line, netbirdTargetRuleComment) {
			continue
		}
		_, handle, found := strings.Cut(line, "# handle ")
		if !found {
			continue
		}
		if strings.Contains(line, wantText) && !keep {
			keep = true
			continue
		}
		stale = append(stale, strings.TrimSpace(handle))
	}
	return keep, stale
}

func rpcNetbirdTargetJoin(setupKey string, hostname string) (NetbirdState, error) {
	t := netbirdTargetMgr
	if !netbirdInstalled() {
		return netbirdMgr.State(), errors.New("NetBird is not installed")
	}
	settings := netbirdSettings()
	if !settings.Enabled {
		return netbirdMgr.State(), errors.New("join this KVM to NetBird first (Access page)")
	}
	if usbNetwork == nil || usbNetwork.Config().Mode != usbUplinkNetbird {
		return netbirdMgr.State(), errors.New("choose NetBird as the USB Ethernet mode first")
	}
	setupKey = strings.TrimSpace(setupKey)
	if setupKey == "" {
		return netbirdMgr.State(), errors.New("a setup key is required")
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = netbirdDefaultTargetHostname()
	}
	if err := saveNetbirdSettings(func(c *NetbirdConfig) {
		c.Target = &NetbirdTargetConfig{Enabled: true, Hostname: hostname}
	}); err != nil {
		return netbirdMgr.State(), err
	}
	managementURL := settings.ManagementURL
	if managementURL == "" {
		managementURL = netbird.DefaultManagementURL
	}
	if err := t.switchServerIfNeeded(managementURL); err != nil {
		return netbirdMgr.State(), err
	}
	if err := t.waitForDaemon(context.Background()); err != nil {
		return netbirdMgr.State(), err
	}
	t.cancelJoin()
	t.mu.Lock()
	t.lastError = ""
	t.mu.Unlock()

	err := t.joinWithKey(netbirdTargetKeyFile, setupKey, netbird.UpOptions{
		DaemonAddr:    netbirdTargetDaemonAddr,
		ManagementURL: settings.ManagementURL,
		Hostname:      hostname,
		// dnsmasq on usb0 sends the target's NetBird names here.
		ResolverAddress: netbirdTargetResolverAddr,
		ExtraArgs: []string{
			"--interface-name", netbirdTargetInterface,
			"--wireguard-port", netbirdTargetPort,
			// The target uses the networks it is given by the KVM, and
			// routes nothing for others.
			"--disable-client-routes", "--disable-server-routes",
		},
	})
	if err != nil {
		return netbirdMgr.State(), err
	}
	return netbirdMgr.State(), nil
}

func netbirdDefaultTargetHostname() string {
	name := "jetkvm"
	if networkManager != nil && networkManager.Hostname() != "" {
		name = networkManager.Hostname()
	}
	return name + "-usb"
}

// rpcNetbirdTargetLeave disconnects the target's peer and stops its daemon;
// its identity stays, so joining again with the same server reuses it.
func rpcNetbirdTargetLeave() (NetbirdState, error) {
	t := netbirdTargetMgr
	t.cancelJoin()
	t.mu.Lock()
	running := t.daemon != nil
	t.mu.Unlock()
	if running {
		if err := netbird.DownAt(context.Background(), netbirdTargetDaemonAddr); err != nil {
			netbirdLogger.Warn().Err(err).Msg("netbird down (target) failed")
		}
	}
	if err := saveNetbirdSettings(func(c *NetbirdConfig) {
		if c.Target != nil {
			c.Target.Enabled = false
		}
	}); err != nil {
		return netbirdMgr.State(), err
	}
	t.mu.Lock()
	t.lastError = ""
	t.mu.Unlock()
	stopNetbirdTarget()
	return netbirdMgr.State(), nil
}
