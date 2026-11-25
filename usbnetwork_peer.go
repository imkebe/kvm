package kvm

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// The USB side of the target's own NetBird peer (netbird_target.go). When
// both peers have addresses, the USB link carries the NetBird network itself:
//
//   - usb0 gets the KVM's own NetBird address with the network's prefix but no
//     prefix route (the KVM keeps reaching peers through wt0), so dnsmasq
//     serves that network and the target's gateway is the KVM's NetBird
//     address;
//   - the target gets its peer's address by DHCP, on-link for the whole
//     NetBird network; proxy ARP on usb0 answers for the other peers;
//   - a host route sends the target's address to usb0, and a rule sends
//     everything that comes in on usb0 to its own table, which routes the
//     NetBird network into wt1.

const (
	usbPeerRouteTable    = 73 // busybox ip accepts only small table numbers
	usbPeerRulePriority  = 90
	usbPeerFallbackLease = "2m"
)

// usbNetbirdPeer is the target's peer as the USB link uses it.
type usbNetbirdPeer struct {
	Address   string // the target's NetBird address
	Gateway   string // the KVM's own NetBird address
	PrefixLen int    // of the NetBird network
	Network   string // "100.111.0.0/16"
	// DNS: dnsmasq on usb0 answers the target's DNS at the gateway address.
	// DNSZone ("netbird.selfhosted") and DNSDomains (the nameserver groups'
	// domains, comma-separated; "." is all) go to the target's NetBird
	// resolver, everything else to the KVM's own DNS servers.
	DNSZone    string
	DNSDomains string
}

// link is the peer without its DNS settings: what the routes depend on.
func (p usbNetbirdPeer) link() usbNetbirdPeer {
	p.DNSZone, p.DNSDomains = "", ""
	return p
}

// dnsServer is the target's DNS server, "" when the KVM gives it none.
func (p usbNetbirdPeer) dnsServer() string {
	if !p.ready() || p.DNSZone == "" {
		return ""
	}
	return p.Gateway
}

// usbPeerDNSDomains joins nameserver group domains for usbNetbirdPeer.
func usbPeerDNSDomains(domains []string) string {
	sorted := slices.Clone(domains)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// usbPeerDNSArgs are dnsmasq's DNS settings for the peer: the NetBird zone,
// its reverse zone and the nameserver groups' domains go to the target's
// NetBird resolver; with a group for all domains, everything does.
func usbPeerDNSArgs(peer usbNetbirdPeer) []string {
	if peer.dnsServer() == "" {
		return []string{"--port=0", "--dhcp-option=option:dns-server"}
	}
	resolver := strings.Replace(netbirdTargetResolverAddr, ":", "#", 1)
	args := []string{
		"--no-hosts",
		"--dhcp-option=option:dns-server," + peer.Gateway,
		"--dhcp-option=option:domain-name," + peer.DNSZone,
		"--dhcp-option=option:domain-search," + peer.DNSZone,
		"--server=/" + peer.DNSZone + "/" + resolver,
	}
	if zone := reverseZone(peer.Network); zone != "" {
		args = append(args, "--server=/"+zone+"/"+resolver)
	}
	all := false
	for _, domain := range strings.Split(peer.DNSDomains, ",") {
		switch domain {
		case "":
		case ".":
			all = true
		default:
			args = append(args, "--server=/"+domain+"/"+resolver)
		}
	}
	if all {
		// No upstream from the KVM's resolv.conf: NetBird answers everything.
		args = append(args, "--no-resolv", "--server="+resolver)
	}
	return args
}

// reverseZone is the in-addr.arpa zone of an IPv4 network whose prefix is a
// whole number of octets ("111.100.in-addr.arpa" for 100.111.0.0/16).
func reverseZone(network string) string {
	_, ipNet, err := net.ParseCIDR(network)
	if err != nil || ipNet.IP.To4() == nil {
		return ""
	}
	ones, _ := ipNet.Mask.Size()
	if ones == 0 || ones%8 != 0 {
		return ""
	}
	ip := ipNet.IP.To4()
	labels := []string{}
	for i := ones/8 - 1; i >= 0; i-- {
		labels = append(labels, strconv.Itoa(int(ip[i])))
	}
	return strings.Join(labels, ".") + ".in-addr.arpa"
}

// usbPeerDNSPortFree says whether dnsmasq can take port 53 on the gateway
// address (a NetBird daemon that still has its resolver there holds it). It
// waits up to a second for the dnsmasq just killed to let go of it.
func usbPeerDNSPortFree(gateway string) bool {
	for range 10 {
		conn, err := net.ListenPacket("udp4", net.JoinHostPort(gateway, "53"))
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// newUSBNetbirdPeer builds the peer from both daemons' addresses
// ("100.111.29.244/16"); it is not ready unless both are in one network.
func newUSBNetbirdPeer(target, gateway string) usbNetbirdPeer {
	targetIP, targetNet, err := net.ParseCIDR(target)
	if err != nil || targetIP.To4() == nil {
		return usbNetbirdPeer{}
	}
	gatewayIP, gatewayNet, err := net.ParseCIDR(gateway)
	if err != nil || gatewayIP.To4() == nil || gatewayNet.String() != targetNet.String() || gatewayIP.Equal(targetIP) {
		return usbNetbirdPeer{}
	}
	ones, _ := targetNet.Mask.Size()
	return usbNetbirdPeer{
		Address:   targetIP.String(),
		Gateway:   gatewayIP.String(),
		PrefixLen: ones,
		Network:   targetNet.String(),
	}
}

func (p usbNetbirdPeer) ready() bool {
	return p.Address != "" && p.Gateway != "" && p.PrefixLen > 0
}

func (p usbNetbirdPeer) netmask() string {
	return net.IP(net.CIDRMask(p.PrefixLen, 32)).String()
}

// PeerLink is the peer as the USB link serves it now; DNS fields are empty
// while dnsmasq does not answer DNS.
func (m *USBNetworkManager) PeerLink() usbNetbirdPeer {
	m.mu.Lock()
	defer m.mu.Unlock()
	link := m.peerLink
	if m.peerDNSOff {
		link.DNSZone, link.DNSDomains = "", ""
	}
	return link
}

// SetNetbirdPeer is called after every poll of the target's daemon. It puts
// the link into the peer's network, or back to the plain USB subnet, when the
// peer changes, and makes sure the routes are in place otherwise (wt1 and its
// routes are recreated when the daemon restarts).
func (m *USBNetworkManager) SetNetbirdPeer(peer usbNetbirdPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peer = peer
	if !m.state.Running {
		return nil
	}
	return m.applyPeerLinkLocked()
}

// applyPeerLinkLocked brings the link to the wanted peer state and restarts
// DHCP when that state changed.
func (m *USBNetworkManager) applyPeerLinkLocked() error {
	want := usbNetbirdPeer{}
	if m.config.Mode == usbUplinkNetbird && m.peer.ready() {
		want = m.peer
	}
	// The routes follow the addresses; DNS settings only restart DHCP.
	changed := want.link() != m.peerLink.link()
	dnsChanged := want != m.peerLink
	if changed && m.peerLink.ready() {
		m.removePeerLinkLocked()
	}
	if changed {
		// The target holds a lease for the old addresses (12 h in NAT or
		// Routed mode); a short carrier loss makes it ask again now.
		resetUSBLink(m.config.InterfaceName)
	}
	if want.ready() {
		if err := configureUSBPeerLink(m.config, want); err != nil {
			m.state.LastError = err.Error()
			return err
		}
	} else if changed {
		// Back on the plain USB subnet.
		if err := runCommand([]string{"ip", "addr", "replace", m.config.AddressCIDR, "dev", m.config.InterfaceName}, false); err != nil {
			return err
		}
	}
	m.peerLink = want
	if changed && want.ready() {
		netbirdLogger.Info().Str("address", want.Address).Str("gateway", want.Gateway).Msg("USB link carries the target's NetBird address")
	}
	if changed || dnsChanged {
		if err := m.startDHCPLocked(); err != nil {
			m.state.LastError = err.Error()
			return err
		}
	}
	return nil
}

// configureUSBPeerLink is idempotent; it runs after every poll.
func configureUSBPeerLink(cfg USBNetworkConfig, peer usbNetbirdPeer) error {
	usb, err := netlink.LinkByName(cfg.InterfaceName)
	if err != nil {
		return err
	}
	// The plain USB subnet goes, so DHCP hands out only the peer's address.
	if plain, err := netlink.ParseAddr(cfg.AddressCIDR); err == nil {
		_ = netlink.AddrDel(usb, plain)
	}
	gateway := &netlink.Addr{
		IPNet: &net.IPNet{IP: net.ParseIP(peer.Gateway).To4(), Mask: net.CIDRMask(peer.PrefixLen, 32)},
		Flags: unix.IFA_F_NOPREFIXROUTE,
	}
	if err := netlink.AddrReplace(usb, gateway); err != nil {
		return fmt.Errorf("gateway address on %s: %w", cfg.InterfaceName, err)
	}
	if err := netlink.RouteReplace(&netlink.Route{
		LinkIndex: usb.Attrs().Index,
		Dst:       &net.IPNet{IP: net.ParseIP(peer.Address).To4(), Mask: net.CIDRMask(32, 32)},
		Scope:     netlink.SCOPE_LINK,
	}); err != nil {
		return fmt.Errorf("route to the target: %w", err)
	}
	if err := os.WriteFile(filepath.Join("/proc/sys/net/ipv4/conf", cfg.InterfaceName, "proxy_arp"), []byte("1"), 0o644); err != nil {
		return err
	}
	if err := ensureUSBPeerRule(cfg.InterfaceName); err != nil {
		return err
	}
	// Into wt1: only while it exists (the daemon may be restarting).
	if wt, err := netlink.LinkByName(netbirdTargetInterface); err == nil {
		_, network, _ := net.ParseCIDR(peer.Network)
		if err := netlink.RouteReplace(&netlink.Route{
			LinkIndex: wt.Attrs().Index,
			Dst:       network,
			Scope:     netlink.SCOPE_LINK,
			Table:     usbPeerRouteTable,
		}); err != nil {
			return fmt.Errorf("route into %s: %w", netbirdTargetInterface, err)
		}
	}
	return nil
}

func usbPeerRule(usbInterface string) *netlink.Rule {
	rule := netlink.NewRule()
	rule.IifName = usbInterface
	rule.Table = usbPeerRouteTable
	rule.Priority = usbPeerRulePriority
	rule.Family = netlink.FAMILY_V4
	return rule
}

func ensureUSBPeerRule(usbInterface string) error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Priority == usbPeerRulePriority && rule.Table == usbPeerRouteTable && rule.IifName == usbInterface {
			return nil
		}
	}
	return netlink.RuleAdd(usbPeerRule(usbInterface))
}

// removePeerLinkLocked undoes configureUSBPeerLink for the applied peer.
func (m *USBNetworkManager) removePeerLinkLocked() {
	peer := m.peerLink
	if !peer.ready() {
		return
	}
	name := m.config.InterfaceName
	_ = netlink.RuleDel(usbPeerRule(name))
	if routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: usbPeerRouteTable}, netlink.RT_FILTER_TABLE); err == nil {
		for _, route := range routes {
			_ = netlink.RouteDel(&route)
		}
	}
	if usb, err := netlink.LinkByName(name); err == nil {
		_ = netlink.RouteDel(&netlink.Route{
			LinkIndex: usb.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP(peer.Address).To4(), Mask: net.CIDRMask(32, 32)},
		})
		_ = netlink.AddrDel(usb, &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(peer.Gateway).To4(), Mask: net.CIDRMask(peer.PrefixLen, 32)}})
	}
	_ = os.WriteFile(filepath.Join("/proc/sys/net/ipv4/conf", name, "proxy_arp"), []byte("0"), 0o644)
	m.peerLink = usbNetbirdPeer{}
}

// resetUSBLink takes the USB link down and up: the target sees its adapter
// lose carrier and asks DHCP again. Routes on the link go with it;
// configureUSBPeerLink puts them back.
func resetUSBLink(name string) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return
	}
	if err := netlink.LinkSetDown(link); err != nil {
		netbirdLogger.Warn().Err(err).Str("interface", name).Msg("failed to reset the USB link")
		return
	}
	time.Sleep(time.Second)
	if err := netlink.LinkSetUp(link); err != nil {
		netbirdLogger.Warn().Err(err).Str("interface", name).Msg("failed to bring the USB link back up")
	}
}

// ResetLink resets the USB link while USB Ethernet runs (resetUSBLink) and
// puts the peer routes back at once.
func (m *USBNetworkManager) ResetLink() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Running {
		return
	}
	resetUSBLink(m.config.InterfaceName)
	if m.peerLink.ready() {
		if err := configureUSBPeerLink(m.config, m.peerLink); err != nil {
			m.state.LastError = err.Error()
		}
	}
}

// startPeerLinkLocked puts the link into the peer's network when USB
// Ethernet starts in NetBird mode with the peer ready.
func (m *USBNetworkManager) startPeerLinkLocked(cfg USBNetworkConfig) error {
	if cfg.Mode == usbUplinkNetbird && m.peer.ready() {
		if err := configureUSBPeerLink(cfg, m.peer); err != nil {
			return err
		}
		m.peerLink = m.peer
	}
	return nil
}

// dhcpPeerLocked is the peer DHCP serves now: without its DNS while port 53
// is taken.
func (m *USBNetworkManager) dhcpPeerLocked() usbNetbirdPeer {
	peer := m.peerLink
	m.peerDNSOff = false
	if peer.dnsServer() != "" && !usbPeerDNSPortFree(peer.Gateway) {
		// DHCP without DNS rather than no DHCP at all.
		netbirdLogger.Warn().Str("address", peer.Gateway).Msg("port 53 is taken; the target gets no NetBird DNS")
		peer.DNSZone, peer.DNSDomains = "", ""
		m.peerDNSOff = true
	}
	return peer
}

// usbPeerDHCPArgs are dnsmasq's arguments while the link carries the peer.
func usbPeerDHCPArgs(cfg USBNetworkConfig, leaseFile, pidFile string, peer usbNetbirdPeer) []string {
	// The target's own NetBird address, on-link for the whole NetBird
	// network (the KVM answers ARP for the other peers); no router, so
	// the target keeps its own default route. DNS: usbPeerDNSArgs.
	args := []string{
		"--conf-file=/dev/null",
		"--keep-in-foreground",
		// bind-dynamic: usb0's IPv6 link-local address comes back
		// only after a link reset (resetUSBLink), and dnsmasq must not
		// fail on it.
		"--bind-dynamic",
		"--except-interface=lo",
		"--interface=" + cfg.InterfaceName,
		"--dhcp-authoritative",
		// Short: the target renews every minute, so a new peer address
		// (Leave and Join, another key) or new DNS settings reach it
		// within about two minutes; dnsmasq refuses the old address.
		"--dhcp-range=" + peer.Address + "," + peer.Address + "," + peer.netmask() + "," + usbPeerFallbackLease,
		"--dhcp-option=option:router",
		"--dhcp-leasefile=" + leaseFile,
		"--pid-file=" + pidFile,
	}
	return append(args, usbPeerDNSArgs(peer)...)
}
