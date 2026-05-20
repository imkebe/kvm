package kvm

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
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
	LastStartedAt     time.Time `json:"last_started_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
}

type USBNetworkManager struct {
	mu         sync.Mutex
	config     USBNetworkConfig
	state      USBNetworkState
	dhcpCmd    *exec.Cmd
	natBackend string
	natSource  string
}

var usbNetwork *USBNetworkManager

func defaultUSBNetworkConfig() USBNetworkConfig {
	return USBNetworkConfig{
		Enabled:           false,
		Mode:              "nat",
		Function:          "rndis",
		InterfaceName:     "usb0",
		UpstreamInterface: "eth0",
		AddressCIDR:       "10.77.0.1/24",
		DHCPStart:         "10.77.0.10",
		DHCPEnd:           "10.77.0.200",
		DNS:               "1.1.1.1",
	}
}

func initUSBNetwork() {
	usbNetwork = newUSBNetworkManager()
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
	return cfg
}

func validateUSBNetworkConfig(cfg USBNetworkConfig) error {
	switch cfg.Mode {
	case "nat", "routed":
	default:
		return fmt.Errorf("invalid USB network mode: %s", cfg.Mode)
	}
	switch cfg.Function {
	case "rndis":
	default:
		return fmt.Errorf("invalid USB network function: %s", cfg.Function)
	}
	hostIP, subnet, err := net.ParseCIDR(cfg.AddressCIDR)
	if err != nil {
		return fmt.Errorf("invalid USB network CIDR: %w", err)
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
	defer m.mu.Unlock()
	return m.state
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

func (m *USBNetworkManager) startLocked() error {
	cfg := m.config
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
	commands := [][]string{
		{"ip", "addr", "replace", cfg.AddressCIDR, "dev", cfg.InterfaceName},
		{"ip", "link", "set", cfg.InterfaceName, "up"},
		{"sysctl", "-w", "net.ipv4.ip_forward=1"},
	}
	for _, args := range commands {
		if err := runUSBNetworkCommand(args, false); err != nil {
			return err
		}
	}
	if cfg.Mode == "nat" {
		backend, err := ensureUSBNetworkNAT(sourceCIDR, cfg.UpstreamInterface)
		if err != nil {
			return err
		}
		m.natBackend = backend
		m.natSource = sourceCIDR
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
	if cfg.Mode == "nat" {
		sourceCIDR := m.natSource
		if sourceCIDR == "" {
			sourceCIDR, _ = usbNetworkSourceCIDR(cfg.AddressCIDR)
		}
		removeUSBNetworkNAT(m.natBackend, sourceCIDR, cfg.UpstreamInterface)
		m.natBackend = ""
		m.natSource = ""
	}
	_ = runUSBNetworkCommand([]string{"ip", "addr", "flush", "dev", cfg.InterfaceName}, true)
	m.state.Running = false
	return nil
}

func ensureUSBNetworkNAT(sourceCIDR string, upstreamInterface string) (string, error) {
	if _, err := exec.LookPath("iptables"); err == nil {
		checkRule := []string{"iptables", "-t", "nat", "-C", "POSTROUTING", "-s", sourceCIDR, "-o", upstreamInterface, "-j", "MASQUERADE"}
		addRule := []string{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", sourceCIDR, "-o", upstreamInterface, "-j", "MASQUERADE"}
		if err := runUSBNetworkCommand(checkRule, true); err != nil {
			if err := runUSBNetworkCommand(addRule, false); err != nil {
				return "", err
			}
		}
		return "iptables", nil
	}

	if _, err := exec.LookPath("nft"); err != nil {
		return "", fmt.Errorf("iptables or nft is required for USB network NAT: %w", err)
	}
	_ = runUSBNetworkCommand([]string{"nft", "delete", "table", "ip", "jetkvm_usb_network"}, true)
	for _, args := range [][]string{
		{"nft", "add", "table", "ip", "jetkvm_usb_network"},
		{"nft", "add", "chain", "ip", "jetkvm_usb_network", "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "policy", "accept", ";", "}"},
		{"nft", "add", "rule", "ip", "jetkvm_usb_network", "postrouting", "ip", "saddr", sourceCIDR, "oifname", upstreamInterface, "masquerade"},
	} {
		if err := runUSBNetworkCommand(args, false); err != nil {
			_ = runUSBNetworkCommand([]string{"nft", "delete", "table", "ip", "jetkvm_usb_network"}, true)
			return "", err
		}
	}
	return "nft", nil
}

func removeUSBNetworkNAT(backend string, sourceCIDR string, upstreamInterface string) {
	switch backend {
	case "nft":
		_ = runUSBNetworkCommand([]string{"nft", "delete", "table", "ip", "jetkvm_usb_network"}, true)
	default:
		_ = runUSBNetworkCommand([]string{"iptables", "-t", "nat", "-D", "POSTROUTING", "-s", sourceCIDR, "-o", upstreamInterface, "-j", "MASQUERADE"}, true)
	}
}

func (m *USBNetworkManager) startDHCPLocked() error {
	m.stopDHCPLocked()

	cfg := m.config
	if _, err := exec.LookPath("dnsmasq"); err != nil {
		return fmt.Errorf("dnsmasq is required for USB network DHCP: %w", err)
	}
	netmask, err := usbNetworkNetmask(cfg.AddressCIDR)
	if err != nil {
		return err
	}
	leaseFile := fmt.Sprintf("/tmp/jetkvm-%s-dnsmasq.leases", cfg.InterfaceName)
	pidFile := fmt.Sprintf("/tmp/jetkvm-%s-dnsmasq.pid", cfg.InterfaceName)
	_ = os.Remove(pidFile)

	args := []string{
		"--keep-in-foreground",
		"--bind-interfaces",
		"--except-interface=lo",
		"--interface=" + cfg.InterfaceName,
		"--dhcp-authoritative",
		"--dhcp-range=" + cfg.DHCPStart + "," + cfg.DHCPEnd + "," + netmask + ",12h",
		"--dhcp-option=option:router," + strings.Split(cfg.AddressCIDR, "/")[0],
		"--dhcp-option=option:dns-server," + cfg.DNS,
		"--dhcp-leasefile=" + leaseFile,
		"--pid-file=" + pidFile,
	}
	cmd := exec.Command("dnsmasq", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dnsmasq failed: %w", err)
	}
	m.dhcpCmd = cmd
	return nil
}

func (m *USBNetworkManager) stopDHCPLocked() {
	if m.dhcpCmd == nil || m.dhcpCmd.Process == nil {
		m.dhcpCmd = nil
		return
	}
	_ = m.dhcpCmd.Process.Kill()
	_, _ = m.dhcpCmd.Process.Wait()
	m.dhcpCmd = nil
}

func runUSBNetworkCommand(args []string, ignoreError bool) error {
	if len(args) == 0 {
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ignoreError {
			return err
		}
		return fmt.Errorf("%s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func rpcGetUSBNetworkState() USBNetworkState {
	return usbNetwork.State()
}

func rpcSetUSBNetworkConfig(cfg USBNetworkConfig) (USBNetworkState, error) {
	return usbNetwork.SetConfig(cfg)
}

func rpcStartUSBNetwork() (USBNetworkState, error) {
	return usbNetwork.Start()
}

func rpcStopUSBNetwork() USBNetworkState {
	return usbNetwork.Stop()
}
