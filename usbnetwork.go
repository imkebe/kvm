package kvm

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jetkvm/kvm/internal/tailscale"
)

type USBNetworkConfig struct {
	Enabled           bool   `json:"enabled"`
	Mode              string `json:"mode"`
	Function          string `json:"function"`
	InterfaceName     string `json:"interface_name"`
	UpstreamInterface string `json:"upstream_interface"`
	AddressCIDR       string `json:"address_cidr"`
	DHCPStart         string `json:"dhcp_start"`
	DHCPEnd           string `json:"dhcp_end"`
	DNS               string `json:"dns"`
	// The network adapter the target sees: a preset of usbNetworkAdapters
	// (its MAC prefix) or "custom" with HostMAC and DeviceMAC.
	Adapter   string `json:"adapter,omitempty"`
	HostMAC   string `json:"host_mac,omitempty"`
	DeviceMAC string `json:"device_mac,omitempty"`
}

// USB network modes. They are exclusive: in "nat" the target uses the KVM's
// network (masqueraded out of the upstream interface), in "routed" it is
// routed without NAT. In "netbird" the target is its own NetBird peer with its
// own address (netbird_target.go, usbnetwork_peer.go); in "tailscale" it
// reaches Tailscale through the KVM's Tailscale address (tailscale_share.go).
const (
	usbUplinkNetbird   = "netbird"
	usbUplinkTailscale = "tailscale"
)

// shares says whether the target is sent into the named VPN.
func (cfg USBNetworkConfig) shares(uplink string) bool {
	return cfg.vpnMode() && cfg.Mode == uplink
}

// vpnMode: the target reaches only a VPN through the KVM.
func (cfg USBNetworkConfig) vpnMode() bool {
	return cfg.Mode == usbUplinkNetbird ||
		cfg.Mode == usbUplinkTailscale
}

type USBNetworkState struct {
	Enabled           bool      `json:"enabled"`
	Running           bool      `json:"running"`
	Mode              string    `json:"mode"`
	Function          string    `json:"function"`
	InterfaceName     string    `json:"interface_name"`
	UpstreamInterface string    `json:"upstream_interface"`
	AddressCIDR       string    `json:"address_cidr"`
	DHCPStart         string    `json:"dhcp_start"`
	DHCPEnd           string    `json:"dhcp_end"`
	DNS               string    `json:"dns"`
	Adapter           string    `json:"adapter"`
	HostMAC           string    `json:"host_mac"`
	DeviceMAC         string    `json:"device_mac"`
	NetbirdUp         bool      `json:"netbird_up"`
	TailscaleUp       bool      `json:"tailscale_up"`
	SharedRoutes      []string  `json:"shared_routes,omitempty"` // VPN routes the target gets now
	LastStartedAt     time.Time `json:"last_started_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	RxBytes           uint64    `json:"rx_bytes"`
	TxBytes           uint64    `json:"tx_bytes"`
	RxPackets         uint64    `json:"rx_packets"`
	TxPackets         uint64    `json:"tx_packets"`
	LinkUp            bool      `json:"link_up"`
	StatsAvailable    bool      `json:"stats_available"`
	SampledAt         int64     `json:"sampled_at"`
}

type USBNetworkManager struct {
	mu                 sync.Mutex
	config             USBNetworkConfig
	state              USBNetworkState
	dhcpCmd            *exec.Cmd
	dhcpExited         chan struct{} // closed when dhcpCmd has exited
	natBackend         string
	previousForwarding string
	addressConfigured  bool
	// VPN uplinks as last reported by the Tailscale watcher, by usbUplink*
	// name.
	uplinks map[string]usbNetworkUplink
	// NetBird mode: the target's own peer as last reported (peer) and as set
	// up on the link now (peerLink); usbnetwork_peer.go.
	peer     usbNetbirdPeer
	peerLink usbNetbirdPeer
	// peerDNSOff: dnsmasq could not take port 53 for the peer's DNS.
	peerDNSOff bool
}

var usbNetwork *USBNetworkManager

func defaultUSBNetworkConfig() USBNetworkConfig {
	return USBNetworkConfig{
		Enabled:           false,
		Mode:              "nat",
		Function:          "ncm",
		InterfaceName:     "usb0",
		UpstreamInterface: "eth0",
		AddressCIDR:       "10.77.0.1/24",
		DHCPStart:         "10.77.0.10",
		DHCPEnd:           "10.77.0.200",
		DNS:               "1.1.1.1",
	}
}

func newUSBNetworkManager() *USBNetworkManager {
	cfg := defaultUSBNetworkConfig()
	return &USBNetworkManager{
		config: cfg,
		state: USBNetworkState{
			Enabled:           cfg.Enabled,
			Mode:              cfg.Mode,
			Function:          cfg.Function,
			InterfaceName:     cfg.InterfaceName,
			UpstreamInterface: cfg.UpstreamInterface,
			AddressCIDR:       cfg.AddressCIDR,
			DHCPStart:         cfg.DHCPStart,
			DHCPEnd:           cfg.DHCPEnd,
			DNS:               cfg.DNS,
		},
	}
}

func normalizeUSBNetworkConfig(cfg USBNetworkConfig) USBNetworkConfig {
	def := defaultUSBNetworkConfig()
	if cfg.Mode == "" {
		cfg.Mode = def.Mode
	}
	if cfg.Function == "" {
		cfg.Function = def.Function
	}
	if cfg.InterfaceName == "" {
		cfg.InterfaceName = def.InterfaceName
	}
	if cfg.UpstreamInterface == "" {
		cfg.UpstreamInterface = def.UpstreamInterface
	}
	if cfg.AddressCIDR == "" {
		cfg.AddressCIDR = def.AddressCIDR
	}
	if cfg.DHCPStart == "" {
		cfg.DHCPStart = def.DHCPStart
	}
	if cfg.DHCPEnd == "" {
		cfg.DHCPEnd = def.DHCPEnd
	}
	if cfg.DNS == "" {
		cfg.DNS = def.DNS
	}
	return normalizeUSBNetworkAdapter(cfg)
}

// Interface names go into nft rule text, ip/dnsmasq argv and /tmp paths.
var usbNetworkInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,14}$`)

func validateUSBNetworkConfig(cfg USBNetworkConfig) error {
	for _, name := range []string{cfg.InterfaceName, cfg.UpstreamInterface} {
		if name != "" && !usbNetworkInterfacePattern.MatchString(name) {
			return fmt.Errorf("invalid USB network interface name: %q", name)
		}
	}
	switch cfg.Mode {
	case "nat",
		"routed",
		usbUplinkNetbird,
		usbUplinkTailscale:
	default:
		return fmt.Errorf("invalid USB network mode: %s", cfg.Mode)
	}
	switch cfg.Function {
	case "ncm", "ecm", "rndis":
	default:
		return fmt.Errorf("invalid USB network function: %s", cfg.Function)
	}
	hostIP, subnet, err := net.ParseCIDR(cfg.AddressCIDR)
	if err != nil || hostIP.To4() == nil {
		return fmt.Errorf("USB network requires a valid IPv4 CIDR: %s", cfg.AddressCIDR)
	}
	start := net.ParseIP(cfg.DHCPStart).To4()
	if start == nil {
		return fmt.Errorf("invalid USB network DHCP start: %s", cfg.DHCPStart)
	}
	end := net.ParseIP(cfg.DHCPEnd).To4()
	if end == nil {
		return fmt.Errorf("invalid USB network DHCP end: %s", cfg.DHCPEnd)
	}
	if !subnet.Contains(hostIP) || !subnet.Contains(start) || !subnet.Contains(end) {
		return fmt.Errorf("USB network DHCP range must be inside %s", cfg.AddressCIDR)
	}
	if ipToUint32(start) > ipToUint32(end) {
		return fmt.Errorf("USB network DHCP start must be before end")
	}
	if ipToUint32(start) <= ipToUint32(hostIP) && ipToUint32(hostIP) <= ipToUint32(end) {
		return fmt.Errorf("USB network DHCP range must exclude the device address")
	}
	ones, _ := subnet.Mask.Size()
	broadcast := ipToUint32(subnet.IP) | ^ipToUint32(net.IP(subnet.Mask))
	if ones > 30 || ipToUint32(start) <= ipToUint32(subnet.IP) || ipToUint32(end) >= broadcast {
		return fmt.Errorf("USB network DHCP range requires usable host addresses")
	}
	if err := validateUSBNetworkAdapter(cfg); err != nil {
		return err
	}
	if net.ParseIP(cfg.DNS).To4() == nil {
		return fmt.Errorf("invalid USB network DNS server: %s", cfg.DNS)
	}
	return nil
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func usbNetworkSourceCIDR(addressCIDR string) (string, error) {
	_, subnet, err := net.ParseCIDR(addressCIDR)
	if err != nil {
		return "", err
	}
	return subnet.String(), nil
}

func usbNetworkNetmask(addressCIDR string) (string, error) {
	_, subnet, err := net.ParseCIDR(addressCIDR)
	if err != nil {
		return "", err
	}
	mask := net.IP(subnet.Mask).To4()
	if mask == nil {
		return "", fmt.Errorf("USB network only supports IPv4 CIDR")
	}
	return strings.Join([]string{
		strconv.Itoa(int(mask[0])),
		strconv.Itoa(int(mask[1])),
		strconv.Itoa(int(mask[2])),
		strconv.Itoa(int(mask[3])),
	}, "."), nil
}

func (m *USBNetworkManager) State() USBNetworkState {
	m.mu.Lock()
	state := m.state
	m.mu.Unlock()
	if iface, err := net.InterfaceByName(state.InterfaceName); err == nil {
		stats, err := readUSBNetworkStats("/sys/class/net", state.InterfaceName)
		if err != nil {
			// The vendor kernel can lose a USB netdev's sysfs statistics on
			// controller rebind while its netlink and proc counters remain live.
			if data, readErr := os.ReadFile("/proc/net/dev"); readErr == nil {
				stats, err = parseUSBNetworkProcStats(data, state.InterfaceName)
				stats.linkUp = iface.Flags&(net.FlagUp|net.FlagRunning) == net.FlagUp|net.FlagRunning
			}
		}
		if err == nil {
			state.RxBytes, state.TxBytes = stats.rxBytes, stats.txBytes
			state.RxPackets, state.TxPackets = stats.rxPackets, stats.txPackets
			state.LinkUp, state.StatsAvailable = stats.linkUp, true
		}
	}
	state.SampledAt = time.Now().UnixMilli()
	return state
}

type usbNetworkStats struct {
	rxBytes, txBytes, rxPackets, txPackets uint64
	linkUp                                 bool
}

func readUSBNetworkStats(root, name string) (usbNetworkStats, error) {
	var stats usbNetworkStats
	values := []*uint64{&stats.rxBytes, &stats.txBytes, &stats.rxPackets, &stats.txPackets}
	for i, counter := range []string{"rx_bytes", "tx_bytes", "rx_packets", "tx_packets"} {
		data, err := os.ReadFile(filepath.Join(root, name, "statistics", counter))
		if err != nil {
			return stats, err
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return stats, err
		}
		*values[i] = value
	}
	carrier, err := os.ReadFile(filepath.Join(root, name, "carrier"))
	stats.linkUp = err == nil && strings.TrimSpace(string(carrier)) == "1"
	return stats, nil
}

func parseUSBNetworkProcStats(data []byte, name string) (usbNetworkStats, error) {
	var stats usbNetworkStats
	for _, line := range strings.Split(string(data), "\n") {
		device, counters, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(device) != name {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) < 16 {
			return stats, fmt.Errorf("incomplete USB interface counters")
		}
		for i, output := range map[int]*uint64{0: &stats.rxBytes, 1: &stats.rxPackets, 8: &stats.txBytes, 9: &stats.txPackets} {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return stats, err
			}
			*output = value
		}
		return stats, nil
	}
	return stats, fmt.Errorf("USB interface %s counters unavailable", name)
}

func (m *USBNetworkManager) Config() USBNetworkConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

func (m *USBNetworkManager) SetConfig(cfg USBNetworkConfig) (USBNetworkState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg = normalizeUSBNetworkConfig(cfg)
	if err := validateUSBNetworkConfig(cfg); err != nil {
		m.state.LastError = err.Error()
		return m.state, err
	}

	wasRunning := m.state.Running
	if wasRunning {
		_ = m.stopLocked()
	}

	m.config = cfg
	m.state.Enabled = cfg.Enabled
	m.state.Mode = cfg.Mode
	m.state.Function = cfg.Function
	m.state.InterfaceName = cfg.InterfaceName
	m.state.UpstreamInterface = cfg.UpstreamInterface
	m.state.AddressCIDR = cfg.AddressCIDR
	m.state.DHCPStart = cfg.DHCPStart
	m.state.DHCPEnd = cfg.DHCPEnd
	m.state.DNS = cfg.DNS
	m.state.Adapter = cfg.Adapter
	m.state.HostMAC = cfg.HostMAC
	m.state.DeviceMAC = cfg.DeviceMAC
	m.state.LastError = ""

	if wasRunning && cfg.Enabled {
		if err := m.startLocked(); err != nil {
			m.state.LastError = err.Error()
			return m.state, err
		}
	}
	return m.state, nil
}

func (m *USBNetworkManager) Start() (USBNetworkState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = true
	m.state.Enabled = true
	if err := validateUSBNetworkConfig(m.config); err != nil {
		m.state.LastError = err.Error()
		return m.state, err
	}
	if err := m.startLocked(); err != nil {
		m.config.Enabled = false
		m.state.Enabled = false
		m.state.LastError = err.Error()
		return m.state, err
	}
	m.state.LastError = ""
	return m.state, nil
}

func (m *USBNetworkManager) Stop() USBNetworkState {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = false
	m.state.Enabled = false
	if err := m.stopLocked(); err != nil {
		m.state.LastError = err.Error()
	} else {
		m.state.LastError = ""
	}
	return m.state
}

// SuspendForGadgetChange releases the old interface's DHCP, address and NAT
// state while preserving the user's intent to resume sharing after a rebind.
func (m *USBNetworkManager) SuspendForGadgetChange() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Running {
		return nil
	}
	return m.stopLocked()
}

func (m *USBNetworkManager) startLocked() error {
	if m.state.Running {
		return nil
	}
	cfg := m.config
	// Function directories survive class switches, so the selected function
	// may have been allocated usb1/usb2 rather than usb0.
	if name, err := os.ReadFile(filepath.Join("/sys/kernel/config/usb_gadget/jetkvm/functions", cfg.Function+".usb0", "ifname")); err == nil {
		cfg.InterfaceName = strings.TrimSpace(string(name))
		m.config.InterfaceName = cfg.InterfaceName
		m.state.InterfaceName = cfg.InterfaceName
	}
	started := false
	defer func() {
		if !started {
			_ = m.stopLocked()
		}
	}()

	sourceCIDR, err := usbNetworkSourceCIDR(cfg.AddressCIDR)
	if err != nil {
		return err
	}
	if _, err := usbNetworkDHCPBinary(); err != nil {
		return err
	}
	if _, err := net.InterfaceByName(cfg.InterfaceName); err != nil {
		return fmt.Errorf("enable the USB Network class in Hardware settings first: %w", err)
	}
	if err := runCommand([]string{"ip", "addr", "replace", cfg.AddressCIDR, "dev", cfg.InterfaceName}, false); err != nil {
		return err
	}
	m.addressConfigured = true
	if err := runCommand([]string{"ip", "link", "set", cfg.InterfaceName, "up"}, false); err != nil {
		return err
	}
	forwarding, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	m.previousForwarding = strings.TrimSpace(string(forwarding))
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil {
		return err
	}
	if err := m.applyNATLocked(sourceCIDR); err != nil {
		return err
	}
	if err := m.startPeerLinkLocked(cfg); err != nil {
		return err
	}
	if err := m.startDHCPLocked(); err != nil {
		return err
	}

	m.state.Running = true
	m.state.LastStartedAt = time.Now()
	started = true
	return nil
}

func (m *USBNetworkManager) stopLocked() error {
	cfg := m.config
	m.stopDHCPLocked()
	m.removeNATLocked()
	m.removePeerLinkLocked()
	m.state.SharedRoutes = nil
	if m.addressConfigured {
		_ = runCommand([]string{"ip", "addr", "del", cfg.AddressCIDR, "dev", cfg.InterfaceName}, true)
		m.addressConfigured = false
	}
	if m.previousForwarding != "" {
		if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte(m.previousForwarding), 0644); err != nil {
			return err
		}
		m.previousForwarding = ""
	}
	m.state.Running = false
	return nil
}

// usbNetworkNATUpstreams lists the interfaces the target's traffic is
// masqueraded out of: the upstream in NAT mode; in Tailscale mode the
// Tailscale interface while it is connected (peers know only the KVM's own
// address). In NetBird mode nothing is masqueraded: the target has its own
// peer address.
func usbNetworkNATUpstreams(cfg USBNetworkConfig, uplinks map[string]usbNetworkUplink) []string {
	switch {
	case cfg.Mode == "nat":
		return []string{cfg.UpstreamInterface}
	case cfg.Mode == usbUplinkTailscale:
		if uplink := uplinks[cfg.Mode]; uplink.Up && uplink.Interface != "" {
			return []string{uplink.Interface}
		}
	}
	return nil
}

// usbNetworkVPNInterface is the only interface the target's traffic may be
// forwarded to in a VPN mode, connected or not; "" in other modes.
func usbNetworkVPNInterface(cfg USBNetworkConfig) string {
	switch cfg.Mode {
	case usbUplinkNetbird:
		return netbirdTargetInterface
	case usbUplinkTailscale:
		return tailscale.InterfaceName
	}
	return ""
}

func (m *USBNetworkManager) applyNATLocked(sourceCIDR string) error {
	upstreams := usbNetworkNATUpstreams(m.config, m.uplinks)
	vpnOnly := usbNetworkVPNInterface(m.config)
	backend, err := ensureUSBNetworkNAT(sourceCIDR, upstreams, m.config.InterfaceName, vpnOnly)
	if err != nil {
		return err
	}
	m.natBackend = backend
	return nil
}

func (m *USBNetworkManager) removeNATLocked() {
	if m.natBackend == "" {
		return
	}
	deleteUSBNetworkNFTTables()
	m.natBackend = ""
}

// ensureUSBNetworkNAT masquerades the USB subnet out of the upstreams. With
// vpnOnly set (a VPN mode), traffic from the USB interface may be forwarded
// only to that interface. It needs nft: the KVM's images have no iptables.
func ensureUSBNetworkNAT(sourceCIDR string, upstreams []string, usbInterface, vpnOnly string) (string, error) {
	// Routed mode needs no table but the input filter; without nft it starts
	// unfiltered, as it did before the filter existed.
	needed := len(upstreams) > 0 || vpnOnly != ""
	if _, err := exec.LookPath("nft"); err != nil {
		if !needed {
			logger.Warn().Err(err).Msg(usbNetworkUnfiltered)
			return "", nil
		}
		return "", fmt.Errorf("nft is required for USB network NAT: %w", err)
	}
	if err := loadOEMModules(nftModules...); err != nil {
		if !needed {
			logger.Warn().Err(err).Msg(usbNetworkUnfiltered)
			return "", nil
		}
		return "", err
	}
	ctErr := loadOEMModules("nft_ct")
	if ctErr != nil {
		logger.Warn().Err(ctErr).Msg(usbNetworkUnfiltered)
	}
	inputFilter := ctErr == nil
	deleteUSBNetworkNFTTables()
	for _, args := range usbNetworkNFTCommands(sourceCIDR, upstreams, usbInterface, vpnOnly, inputFilter) {
		if err := runCommand(args, false); err != nil {
			deleteUSBNetworkNFTTables()
			return "", err
		}
	}
	return "nft", nil
}

const usbNetworkUnfiltered = "USB Ethernet: nft is not usable; the target is not filtered from the KVM's services"

func usbNetworkNFTCommands(sourceCIDR string, upstreams []string, usbInterface, vpnOnly string, inputFilter bool) [][]string {
	commands := [][]string{
		{"nft", "add", "table", "ip", "jetkvm_usb_network"},
		{"nft", "add", "chain", "ip", "jetkvm_usb_network", "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "policy", "accept", ";", "}"},
	}
	for _, upstream := range upstreams {
		commands = append(commands, []string{"nft", "add", "rule", "ip", "jetkvm_usb_network", "postrouting", "ip", "saddr", sourceCIDR, "oifname", upstream, "counter", "masquerade"})
	}
	if vpnOnly != "" {
		commands = append(commands,
			[]string{"nft", "add", "chain", "ip", "jetkvm_usb_network", "forward", "{", "type", "filter", "hook", "forward", "priority", "filter", ";", "policy", "accept", ";", "}"},
			[]string{"nft", "add", "rule", "ip", "jetkvm_usb_network", "forward", "iifname", usbInterface, "oifname", "!=", vpnOnly, "counter", "drop"})
	}
	if !inputFilter {
		return commands
	}
	// The target may reach on the KVM itself only DHCP and DNS (dnsmasq on the
	// USB interface) and ICMP, in every mode. inet, so IPv6 link-local is covered.
	input := []string{"nft", "add", "rule", "inet", "jetkvm_usb_network", "input", "iifname", usbInterface}
	commands = append(commands,
		[]string{"nft", "add", "table", "inet", "jetkvm_usb_network"},
		[]string{"nft", "add", "chain", "inet", "jetkvm_usb_network", "input", "{", "type", "filter", "hook", "input", "priority", "filter", ";", "policy", "accept", ";", "}"},
		append(slices.Clone(input), "ct", "state", "established,related", "accept"),
		append(slices.Clone(input), "udp", "dport", "67", "accept"),
		append(slices.Clone(input), "udp", "dport", "53", "accept"),
		append(slices.Clone(input), "tcp", "dport", "53", "accept"),
		append(slices.Clone(input), "meta", "l4proto", "icmp", "accept"),
		append(slices.Clone(input), "meta", "l4proto", "ipv6-icmp", "accept"),
		append(slices.Clone(input), "counter", "drop"))
	return commands
}

func deleteUSBNetworkNFTTables() {
	_ = runCommand([]string{"nft", "delete", "table", "ip", "jetkvm_usb_network"}, true)
	_ = runCommand([]string{"nft", "delete", "table", "inet", "jetkvm_usb_network"}, true)
}

func (m *USBNetworkManager) startDHCPLocked() error {
	m.stopDHCPLocked()

	cfg := m.config
	binary, err := usbNetworkDHCPBinary()
	if err != nil {
		return err
	}
	netmask, err := usbNetworkNetmask(cfg.AddressCIDR)
	if err != nil {
		return err
	}
	leaseFile := fmt.Sprintf("/tmp/jetkvm-%s-dnsmasq.leases", cfg.InterfaceName)
	pidFile := fmt.Sprintf("/tmp/jetkvm-%s-dnsmasq.pid", cfg.InterfaceName)
	_ = os.Remove(pidFile)

	routes := usbNetworkTargetRoutes(cfg, m.uplinks)
	peer := m.dhcpPeerLocked()
	args := usbNetworkDHCPArgs(cfg, netmask, leaseFile, pidFile, routes, peer)
	m.state.SharedRoutes = slices.Clone(routes)
	cmd := exec.Command(binary, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dnsmasq failed: %w", err)
	}
	m.dhcpCmd = cmd
	exited := make(chan struct{})
	m.dhcpExited = exited
	// Catch startup failures (for example a port conflict) before reporting success.
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		close(exited)
		done <- err
	}()
	select {
	case err := <-done:
		m.dhcpCmd = nil
		return fmt.Errorf("dnsmasq exited during startup: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	go func() {
		err := <-done
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.dhcpCmd != cmd {
			return // An intentional stop or replacement already cleaned up.
		}
		m.dhcpCmd = nil
		_ = m.stopLocked()
		m.config.Enabled = false
		m.state.Enabled = false
		m.state.LastError = fmt.Sprintf("dnsmasq stopped unexpectedly: %v", err)
	}()
	return nil
}

func usbNetworkDHCPArgs(cfg USBNetworkConfig, netmask, leaseFile, pidFile string, routes []string, peer usbNetbirdPeer) []string {
	gateway := strings.Split(cfg.AddressCIDR, "/")[0]
	if cfg.Mode == usbUplinkNetbird && peer.ready() {
		return usbPeerDHCPArgs(cfg, leaseFile, pidFile, peer)
	}
	lease := "12h"
	if cfg.vpnMode() {
		// Shorter, so a target picks up changed VPN routes sooner. While the
		// VPN is not up yet (after a KVM restart the target asks for an
		// address before the VPN connects), very short, so the target gets
		// the routes within a minute or so of the VPN connecting.
		lease = "1h"
		if len(routes) == 0 {
			lease = usbPeerFallbackLease
		}
	}
	args := []string{
		"--conf-file=/dev/null",
		"--keep-in-foreground",
		"--bind-dynamic",
		"--except-interface=lo",
		"--interface=" + cfg.InterfaceName,
		"--dhcp-authoritative",
		"--dhcp-range=" + cfg.DHCPStart + "," + cfg.DHCPEnd + "," + netmask + "," + lease,
		"--dhcp-leasefile=" + leaseFile,
		"--pid-file=" + pidFile,
	}
	if cfg.vpnMode() {
		// Only the VPN's networks go through the KVM: no default route and no
		// DNS server, so the target keeps its own (given empty, dnsmasq does
		// not fill in its own address).
		args = append(args, "--dhcp-option=option:router", "--dhcp-option=option:dns-server")
	} else {
		args = append(args,
			"--dhcp-option=option:router,"+gateway,
			"--dhcp-option=option:dns-server,"+cfg.DNS)
	}
	if len(routes) > 0 {
		// Classless static routes (RFC 3442); option 249 is the same list for
		// older Windows.
		pairs := []string{}
		for _, route := range routes {
			pairs = append(pairs, route, gateway)
		}
		list := strings.Join(pairs, ",")
		args = append(args, "--dhcp-option=121,"+list, "--dhcp-option=249,"+list)
	}
	return args
}

func (m *USBNetworkManager) stopDHCPLocked() {
	if m.dhcpCmd == nil || m.dhcpCmd.Process == nil {
		m.dhcpCmd = nil
		return
	}
	_ = m.dhcpCmd.Process.Kill()
	// Wait until it is gone: a new dnsmasq started right away finds its
	// ports (DNS on usb0's addresses) still taken and exits.
	if m.dhcpExited != nil {
		select {
		case <-m.dhcpExited:
		case <-time.After(3 * time.Second):
			netbirdLogger.Warn().Msg("dnsmasq did not exit within 3 s")
		}
	}
	m.dhcpCmd = nil
	m.dhcpExited = nil
}

func usbNetworkDHCPBinary() (string, error) {
	for _, candidate := range []string{"dnsmasq", "/userdata/jetkvm/bin/dnsmasq"} {
		if binary, err := exec.LookPath(candidate); err == nil {
			return binary, nil
		}
	}
	return "", fmt.Errorf("dnsmasq is required for USB network DHCP; install the firmware's USB network helper")
}
