package kvm

// Network discovery from the KVM, for troubleshooting the network of the
// target computer and for checking an address before a static setup:
//
//   - getNetworkNeighbors: the kernel's neighbor table (ARP for IPv4, NDP
//     for IPv6) of the KVM's interface.
//   - scanNetwork: finds hosts.
//   - "arp" (IPv4, recommended on the local network): an ARP request to
//     each address of the subnet. Every IPv4 host on the same Ethernet
//     segment must answer ARP, whatever its firewall does with ping.
//   - "ping" (IPv4): an ICMP echo to each address. It also reaches subnets
//     behind a router, but hosts that drop ping stay invisible.
//   - "ipv6": an ICMPv6 echo to all-nodes (ff02::1) on the link, plus the
//     IPv6 neighbor table. A /64 has too many addresses to scan; the
//     multicast echo asks every node at once. Windows does not answer
//     multicast echo by default, so it shows only once it is in the
//     neighbor table.
//   - checkIpv4Address: whether an address is in use, by ARP probes as in
//     RFC 5227 (sender address 0.0.0.0), which work even when the address
//     is in another subnet than the KVM's, as long as it is on the same
//     segment.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// One scan or check at a time: they send many packets.
var discoveryMu sync.Mutex

const (
	maxScanAddresses = 1024
	scanSendGap      = 2 * time.Millisecond
	scanWaitAfter    = 1500 * time.Millisecond
)

type NeighborEntry struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac,omitempty"`
	State  string `json:"state"`
	Family string `json:"family"`
	Router bool   `json:"router,omitempty"`
}

type DiscoveredHost struct {
	IP       string  `json:"ip"`
	MAC      string  `json:"mac,omitempty"`
	RTTMs    float64 `json:"rtt_ms,omitempty"`
	Hostname string  `json:"hostname,omitempty"`
	// Self marks the KVM's own address.
	Self bool `json:"self,omitempty"`
}

type ScanResult struct {
	Method   string           `json:"method"`
	Target   string           `json:"target"`
	Scanned  int              `json:"scanned"`
	Hosts    []DiscoveredHost `json:"hosts"`
	Duration float64          `json:"duration_ms"`
}

type AddressCheck struct {
	Address string `json:"address"`
	InUse   bool   `json:"in_use"`
	MAC     string `json:"mac,omitempty"`
	// Own: the KVM itself has the address.
	Own bool `json:"own,omitempty"`
}

func discoveryInterface() (*net.Interface, error) {
	ifi, err := net.InterfaceByName(NetIfName)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", NetIfName, err)
	}
	return ifi, nil
}

// ownIPv4 returns the KVM's IPv4 addresses with their prefixes.
func ownIPv4(ifi *net.Interface) []netip.Prefix {
	var own []netip.Prefix
	addrs, _ := ifi.Addrs()
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}
		ip, _ := netip.AddrFromSlice(ipNet.IP.To4())
		ones, _ := ipNet.Mask.Size()
		own = append(own, netip.PrefixFrom(ip, ones))
	}
	return own
}

var nudStates = map[int]string{
	netlink.NUD_INCOMPLETE: "incomplete",
	netlink.NUD_REACHABLE:  "reachable",
	netlink.NUD_STALE:      "stale",
	netlink.NUD_DELAY:      "delay",
	netlink.NUD_PROBE:      "probe",
	netlink.NUD_FAILED:     "failed",
	netlink.NUD_NOARP:      "noarp",
	netlink.NUD_PERMANENT:  "permanent",
}

func neighbors(ifi *net.Interface, family int) ([]NeighborEntry, error) {
	list, err := netlink.NeighList(ifi.Index, family)
	if err != nil {
		return nil, err
	}
	entries := make([]NeighborEntry, 0, len(list))
	for _, n := range list {
		if n.IP == nil || n.IP.IsMulticast() {
			continue
		}
		state, ok := nudStates[n.State]
		if !ok {
			state = fmt.Sprintf("0x%x", n.State)
		}
		entry := NeighborEntry{IP: n.IP.String(), State: state, Family: "ipv6", Router: n.Flags&netlink.NTF_ROUTER != 0}
		if n.IP.To4() != nil {
			entry.Family = "ipv4"
		}
		if len(n.HardwareAddr) > 0 {
			entry.MAC = n.HardwareAddr.String()
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Family != entries[j].Family {
			return entries[i].Family == "ipv4"
		}
		a, _ := netip.ParseAddr(entries[i].IP)
		b, _ := netip.ParseAddr(entries[j].IP)
		return a.Less(b)
	})
	return entries, nil
}

func rpcGetNetworkNeighbors() ([]NeighborEntry, error) {
	ifi, err := discoveryInterface()
	if err != nil {
		return nil, err
	}
	return neighbors(ifi, netlink.FAMILY_ALL)
}

// The KVM's current IPv4 subnet, as the scan's default target.
func rpcGetNetworkScanDefaults() (map[string]string, error) {
	ifi, err := discoveryInterface()
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	if own := ownIPv4(ifi); len(own) > 0 {
		result["ipv4"] = own[0].Masked().String()
	}
	return result, nil
}

func rpcScanNetwork(method string, target string) (*ScanResult, error) {
	if !discoveryMu.TryLock() {
		return nil, errors.New("a scan is already running")
	}
	defer discoveryMu.Unlock()

	ifi, err := discoveryInterface()
	if err != nil {
		return nil, err
	}
	started := time.Now()
	result := &ScanResult{Method: method, Target: target}
	switch method {
	case "arp", "ping":
		prefix, err := netip.ParsePrefix(target)
		if err != nil {
			addr, addrErr := netip.ParseAddr(target)
			if addrErr != nil {
				return nil, fmt.Errorf("enter a subnet such as 192.168.1.0/24 or an address")
			}
			prefix = netip.PrefixFrom(addr, 32)
		}
		if !prefix.Addr().Is4() {
			return nil, errors.New("ARP and ping scan IPv4 subnets; use the IPv6 scan for IPv6")
		}
		if !scanTargetAllowed(prefix.Masked(), allOwnIPv4()) {
			return nil, errors.New("scan only the KVM's own networks or private address ranges")
		}
		addrs, err := scanAddresses(prefix.Masked())
		if err != nil {
			return nil, err
		}
		result.Target = prefix.Masked().String()
		result.Scanned = len(addrs)
		if method == "arp" {
			result.Hosts, err = arpScan(ifi, addrs)
		} else {
			result.Hosts, err = pingScan(addrs)
		}
		if err != nil {
			return nil, err
		}
		markSelf(ifi, result.Hosts)
		resolveNames(result.Hosts)
	case "ipv6":
		result.Target = "ff02::1%" + ifi.Name
		result.Hosts, err = ipv6Scan(ifi)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown scan method %q", method)
	}
	sortHosts(result.Hosts)
	result.Duration = float64(time.Since(started).Milliseconds())
	return result, nil
}

var privateIPv4 = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// scanTargetAllowed: the device API reaches scanNetwork, so it must not
// become a scanner of public networks.
func scanTargetAllowed(target netip.Prefix, own []netip.Prefix) bool {
	for _, allowed := range append(own, privateIPv4...) {
		if allowed.Bits() <= target.Bits() && allowed.Contains(target.Addr()) {
			return true
		}
	}
	return false
}

// allOwnIPv4 is the IPv4 prefixes of all the KVM's interfaces (VPNs and USB
// Ethernet included).
func allOwnIPv4() []netip.Prefix {
	var own []netip.Prefix
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		own = append(own, ownIPv4(&ifaces[i])...)
	}
	return own
}

// The addresses of a subnet without its network and broadcast address
// (both kept for /31 and /32).
func scanAddresses(prefix netip.Prefix) ([]netip.Addr, error) {
	hostBits := 32 - prefix.Bits()
	if hostBits > 10 {
		return nil, fmt.Errorf("the subnet has %d addresses; scan at most %d (a /22 or smaller)", 1<<hostBits, maxScanAddresses)
	}
	var addrs []netip.Addr
	for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
		addrs = append(addrs, addr)
		if !addr.Next().IsValid() {
			break
		}
	}
	if len(addrs) > 2 {
		addrs = addrs[1 : len(addrs)-1]
	}
	return addrs, nil
}

func markSelf(ifi *net.Interface, hosts []DiscoveredHost) {
	for _, own := range ownIPv4(ifi) {
		for i := range hosts {
			if hosts[i].IP == own.Addr().String() {
				hosts[i].Self = true
			}
		}
	}
}

// resolveNames adds reverse DNS names, within a short overall time.
func resolveNames(hosts []DiscoveredHost) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	jobs := make(chan *DiscoveredHost)
	for range min(16, len(hosts)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range jobs {
				if ctx.Err() != nil {
					continue
				}
				names, err := net.DefaultResolver.LookupAddr(ctx, host.IP)
				if err == nil && len(names) > 0 {
					host.Hostname = trimDot(names[0])
				}
			}
		}()
	}
	for i := range hosts {
		jobs <- &hosts[i]
	}
	close(jobs)
	wg.Wait()
}

func trimDot(name string) string {
	if len(name) > 0 && name[len(name)-1] == '.' {
		return name[:len(name)-1]
	}
	return name
}

func sortHosts(hosts []DiscoveredHost) {
	sort.Slice(hosts, func(i, j int) bool {
		a, _ := netip.ParseAddr(hosts[i].IP)
		b, _ := netip.ParseAddr(hosts[j].IP)
		return a.Less(b)
	})
}

// ARP over a raw packet socket.

func htons(v uint16) uint16 { return v<<8 | v>>8 }

type arpSocket struct {
	fd  int
	ifi *net.Interface
}

func openARP(ifi *net.Interface) (*arpSocket, error) {
	if len(ifi.HardwareAddr) != 6 {
		return nil, fmt.Errorf("interface %s has no Ethernet address", ifi.Name)
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(unix.ETH_P_ARP)))
	if err != nil {
		return nil, fmt.Errorf("ARP socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ARP), Ifindex: ifi.Index}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ARP socket: %w", err)
	}
	timeout := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ARP socket: %w", err)
	}
	return &arpSocket{fd: fd, ifi: ifi}, nil
}

func (s *arpSocket) Close() { unix.Close(s.fd) }

// request sends an ARP request for target from sender (0.0.0.0 for a
// probe).
func (s *arpSocket) request(sender, target netip.Addr) error {
	frame := make([]byte, 42)
	broadcast := net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	copy(frame[0:6], broadcast)
	copy(frame[6:12], s.ifi.HardwareAddr)
	binary.BigEndian.PutUint16(frame[12:14], unix.ETH_P_ARP)
	binary.BigEndian.PutUint16(frame[14:16], 1)             // Ethernet
	binary.BigEndian.PutUint16(frame[16:18], unix.ETH_P_IP) // IPv4
	frame[18], frame[19] = 6, 4
	binary.BigEndian.PutUint16(frame[20:22], 1) // request
	copy(frame[22:28], s.ifi.HardwareAddr)
	sender4, target4 := sender.As4(), target.As4()
	copy(frame[28:32], sender4[:])
	// Target hardware address stays zero.
	copy(frame[38:42], target4[:])
	to := &unix.SockaddrLinklayer{Ifindex: s.ifi.Index, Halen: 6, Protocol: htons(unix.ETH_P_ARP)}
	copy(to.Addr[:], broadcast)
	return unix.Sendto(s.fd, frame, 0, to)
}

// replies reads ARP replies until the deadline and records each sender
// that wanted is true for.
func (s *arpSocket) replies(deadline time.Time, wanted func(netip.Addr) bool, found func(netip.Addr, net.HardwareAddr)) {
	buf := make([]byte, 1500)
	for time.Now().Before(deadline) {
		n, _, err := unix.Recvfrom(s.fd, buf, 0)
		if err != nil || n < 42 {
			continue
		}
		if binary.BigEndian.Uint16(buf[12:14]) != unix.ETH_P_ARP || binary.BigEndian.Uint16(buf[20:22]) != 2 {
			continue
		}
		sender, _ := netip.AddrFromSlice(buf[28:32])
		if !wanted(sender) {
			continue
		}
		found(sender, net.HardwareAddr(append([]byte(nil), buf[22:28]...)))
	}
}

// senderFor is the KVM's address in target's subnet, else 0.0.0.0.
func senderFor(ifi *net.Interface, target netip.Addr) netip.Addr {
	for _, own := range ownIPv4(ifi) {
		if own.Contains(target) {
			return own.Addr()
		}
	}
	return netip.IPv4Unspecified()
}

// arpConn is the part of arpSocket a scan uses (a seam for tests).
type arpConn interface {
	request(sender, target netip.Addr) error
	replies(deadline time.Time, wanted func(netip.Addr) bool, found func(netip.Addr, net.HardwareAddr))
}

func arpScan(ifi *net.Interface, addrs []netip.Addr) ([]DiscoveredHost, error) {
	sock, err := openARP(ifi)
	if err != nil {
		return nil, err
	}
	defer sock.Close()
	return arpScanOn(sock, ifi, addrs)
}

func arpScanOn(sock arpConn, ifi *net.Interface, addrs []netip.Addr) ([]DiscoveredHost, error) {
	wanted := make(map[netip.Addr]time.Time, len(addrs))
	var mu sync.Mutex
	hosts := map[netip.Addr]*DiscoveredHost{}
	done := make(chan struct{})
	stopAt := make(chan time.Time, 1)
	go func() {
		defer close(done)
		deadline := time.Now().Add(time.Hour)
		for {
			select {
			case deadline = <-stopAt:
			default:
			}
			if !time.Now().Before(deadline) {
				return
			}
			sock.replies(time.Now().Add(200*time.Millisecond), func(a netip.Addr) bool {
				mu.Lock()
				defer mu.Unlock()
				_, ok := wanted[a]
				return ok
			}, func(a netip.Addr, mac net.HardwareAddr) {
				mu.Lock()
				defer mu.Unlock()
				if _, seen := hosts[a]; seen {
					return
				}
				rtt := time.Since(wanted[a])
				hosts[a] = &DiscoveredHost{IP: a.String(), MAC: mac.String(), RTTMs: float64(rtt.Microseconds()) / 1000}
			})
		}
	}()

	// Each address gets two requests, the second after the whole first
	// round, for a host whose first reply was lost.
	var sendErr error
send:
	for round := 0; round < 2; round++ {
		for _, addr := range addrs {
			mu.Lock()
			found := hosts[addr] != nil
			if round == 0 {
				wanted[addr] = time.Now()
			}
			mu.Unlock()
			if found {
				continue
			}
			if err := sock.request(senderFor(ifi, addr), addr); err != nil {
				sendErr = fmt.Errorf("send ARP: %w", err)
				break send
			}
			time.Sleep(scanSendGap)
		}
	}
	// The reader must end before the caller closes the socket, on every path.
	if sendErr != nil {
		stopAt <- time.Now()
		<-done
		return nil, sendErr
	}
	stopAt <- time.Now().Add(scanWaitAfter)
	<-done

	result := make([]DiscoveredHost, 0, len(hosts))
	for _, host := range hosts {
		result = append(result, *host)
	}
	return result, nil
}

func pingScan(addrs []netip.Addr) ([]DiscoveredHost, error) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("ICMP socket: %w", err)
	}
	defer conn.Close()

	id := os.Getpid() & 0xffff
	sent := make(map[netip.Addr]time.Time, len(addrs))
	var mu sync.Mutex
	hosts := map[netip.Addr]*DiscoveredHost{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				continue
			}
			msg, err := icmp.ParseMessage(1, buf[:n])
			if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
				continue
			}
			echo, ok := msg.Body.(*icmp.Echo)
			if !ok || echo.ID != id {
				continue
			}
			ipAddr, ok := peer.(*net.IPAddr)
			if !ok {
				continue
			}
			addr, _ := netip.AddrFromSlice(ipAddr.IP.To4())
			mu.Lock()
			if at, ok := sent[addr]; ok && hosts[addr] == nil {
				hosts[addr] = &DiscoveredHost{IP: addr.String(), RTTMs: float64(time.Since(at).Microseconds()) / 1000}
			}
			mu.Unlock()
		}
	}()

	for seq, addr := range addrs {
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("jetkvm-scan")}}
		data, err := msg.Marshal(nil)
		if err != nil {
			close(stop)
			<-done
			return nil, err
		}
		mu.Lock()
		sent[addr] = time.Now()
		mu.Unlock()
		// A host without a route or ARP answer gives an error here;
		// it simply does not reply.
		_, _ = conn.WriteTo(data, &net.IPAddr{IP: net.IP(addr.AsSlice())})
		time.Sleep(scanSendGap)
	}
	time.Sleep(scanWaitAfter)
	close(stop)
	<-done

	// The kernel resolved each answering host on the local subnet; its
	// neighbor entry has the MAC address.
	macs := map[string]string{}
	if ifi, err := discoveryInterface(); err == nil {
		if entries, err := neighbors(ifi, netlink.FAMILY_V4); err == nil {
			for _, e := range entries {
				macs[e.IP] = e.MAC
			}
		}
	}
	result := make([]DiscoveredHost, 0, len(hosts))
	for _, host := range hosts {
		host.MAC = macs[host.IP]
		result = append(result, *host)
	}
	return result, nil
}

func ipv6Scan(ifi *net.Interface) ([]DiscoveredHost, error) {
	conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		return nil, fmt.Errorf("ICMPv6 socket: %w", err)
	}
	defer conn.Close()
	if err := conn.IPv6PacketConn().SetMulticastInterface(ifi); err != nil {
		return nil, fmt.Errorf("ICMPv6 multicast: %w", err)
	}
	_ = conn.IPv6PacketConn().SetMulticastHopLimit(1)

	id := os.Getpid() & 0xffff
	hosts := map[string]*DiscoveredHost{}
	var sentAt time.Time
	for seq := 0; seq < 2; seq++ {
		msg := icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("jetkvm-scan")}}
		data, err := msg.Marshal(nil)
		if err != nil {
			return nil, err
		}
		if seq == 0 {
			sentAt = time.Now()
		}
		if _, err := conn.WriteTo(data, &net.IPAddr{IP: net.ParseIP("ff02::1"), Zone: ifi.Name}); err != nil {
			return nil, fmt.Errorf("send ICMPv6: %w", err)
		}
		deadline := time.Now().Add(1 * time.Second)
		buf := make([]byte, 1500)
		for time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(deadline)
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				break
			}
			msg, err := icmp.ParseMessage(58, buf[:n])
			if err != nil || msg.Type != ipv6.ICMPTypeEchoReply {
				continue
			}
			echo, ok := msg.Body.(*icmp.Echo)
			if !ok || echo.ID != id {
				continue
			}
			ipAddr, ok := peer.(*net.IPAddr)
			if !ok {
				continue
			}
			key := ipAddr.IP.String()
			if hosts[key] == nil {
				hosts[key] = &DiscoveredHost{IP: key, RTTMs: float64(time.Since(sentAt).Microseconds()) / 1000}
			}
		}
	}

	ownAddrs := map[string]bool{}
	if addrs, err := ifi.Addrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				ownAddrs[ipNet.IP.String()] = true
			}
		}
	}
	// The neighbor table adds MAC addresses and hosts that did not answer
	// the multicast echo.
	if entries, err := neighbors(ifi, netlink.FAMILY_V6); err == nil {
		for _, e := range entries {
			if e.State == "failed" || e.State == "incomplete" {
				continue
			}
			if host := hosts[e.IP]; host != nil {
				host.MAC = e.MAC
			} else {
				hosts[e.IP] = &DiscoveredHost{IP: e.IP, MAC: e.MAC}
			}
		}
	}
	result := make([]DiscoveredHost, 0, len(hosts))
	for _, host := range hosts {
		host.Self = ownAddrs[host.IP]
		result = append(result, *host)
	}
	return result, nil
}

// rpcCheckIpv4Address tells whether another device answers ARP for an
// address, as the static IPv4 setup checks before it saves.
func rpcCheckIpv4Address(address string) (*AddressCheck, error) {
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() {
		return nil, fmt.Errorf("%q is not an IPv4 address", address)
	}
	ifi, err := discoveryInterface()
	if err != nil {
		return nil, err
	}
	result := &AddressCheck{Address: addr.String()}
	for _, own := range ownIPv4(ifi) {
		if own.Addr() == addr {
			result.Own = true
			return result, nil
		}
	}

	discoveryMu.Lock()
	defer discoveryMu.Unlock()
	sock, err := openARP(ifi)
	if err != nil {
		return nil, err
	}
	defer sock.Close()
	// RFC 5227: probes with sender 0.0.0.0, so no host caches a mapping
	// for an address that may become the KVM's.
	for probe := 0; probe < 3; probe++ {
		if err := sock.request(netip.IPv4Unspecified(), addr); err != nil {
			return nil, fmt.Errorf("send ARP: %w", err)
		}
		sock.replies(time.Now().Add(400*time.Millisecond), func(a netip.Addr) bool { return a == addr },
			func(_ netip.Addr, mac net.HardwareAddr) {
				result.InUse = true
				result.MAC = mac.String()
			})
		if result.InUse {
			break
		}
	}
	return result, nil
}
