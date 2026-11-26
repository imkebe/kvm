package kvm

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/jetkvm/kvm/internal/tailscale"
)

// The Tailscale mode of the USB Ethernet target. The app does not run
// Tailscale; when tailscaled is installed on the KVM and USB Ethernet is in
// Tailscale mode, this watcher reports its state to the USB network like the
// NetBird manager does.

const tailscaleSharePoll = 15 * time.Second

var tailscaleShareKick = make(chan struct{}, 1)

// kickTailscaleShare makes the watcher check now, for example right after
// the mode is switched to Tailscale.
func kickTailscaleShare() {
	select {
	case tailscaleShareKick <- struct{}{}:
	default:
	}
}

func initTailscaleShare() {
	go func() {
		last := ""
		timer := time.NewTimer(0)
		for {
			select {
			case <-timer.C:
			case <-tailscaleShareKick:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			last = checkTailscaleShare(last)
			timer.Reset(tailscaleSharePoll)
		}
	}()
}

// checkTailscaleShare passes the Tailscale uplink to the USB network when it
// changed, and returns the new value of last. Tailscale is queried only while
// USB Ethernet runs in Tailscale mode.
func checkTailscaleShare(last string) string {
	if usbNetwork == nil {
		return last
	}
	uplink := usbNetworkUplink{Interface: tailscale.InterfaceName}
	cfg := usbNetwork.Config()
	if cfg.Mode == usbUplinkTailscale && usbNetwork.State().Running {
		logErr := func(err error) { tailscaleLogger.Debug().Err(err).Msg("tailscale status for USB sharing") }
		status, err := tailscale.GetStatus("", logErr)
		if err != nil {
			logErr(err)
		}
		uplink.Up, uplink.Routes = tailscaleUplink(status, interfaceExists)
	}
	key := fmt.Sprint(uplink.Up, uplink.Routes)
	if key == last {
		return last
	}
	if err := usbNetwork.SetUplink(usbUplinkTailscale, uplink); err != nil {
		tailscaleLogger.Warn().Err(err).Msg("failed to update USB network sharing")
		return last
	}
	return key
}

func interfaceExists(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

// tailscaleUplink says whether the target can be sent into Tailscale, and
// with which routes: Tailscale must be connected with a kernel interface.
func tailscaleUplink(status *tailscale.Status, exists func(string) bool) (bool, []string) {
	if status == nil || !status.Running || !status.TUN || !exists(tailscale.InterfaceName) {
		return false, nil
	}
	routes := slices.Clone(status.PeerRoutes)
	slices.Sort(routes) // peers come from a map
	return true, usbNetworkVPNRoutes(routes)
}

// rpcSetUsbNetworkMode sets only the USB network mode (nat, routed, netbird,
// tailscale), saves it, and returns the USB network state.
func rpcSetUsbNetworkMode(mode string) (USBNetworkState, error) {
	if usbNetwork == nil {
		return USBNetworkState{}, fmt.Errorf("USB network is not available")
	}
	cfg := usbNetwork.Config()
	changed := cfg.Mode != mode
	cfg.Mode = mode
	if _, err := usbNetwork.SetConfig(cfg); err != nil {
		return usbNetwork.State(), err
	}
	saved := usbNetwork.Config()
	config.USBNetworkConfig = &saved
	if err := SaveConfig(); err != nil {
		return usbNetwork.State(), err
	}
	refreshNetbirdTarget()
	netbirdTargetChanged()
	kickTailscaleShare()
	if changed {
		// The target holds a lease from the old mode (12 h in NAT or
		// Routed); make it ask again now.
		usbNetwork.ResetLink()
	}
	return usbNetwork.State(), nil
}

// usbNetworkUplink is a VPN on the KVM that the target can reach through it.
type usbNetworkUplink struct {
	Interface string
	Up        bool
	Routes    []string
}

// usbNetworkTargetRoutes are the VPN routes the target gets by DHCP in a VPN
// mode while the VPN is connected. Routes that overlap the USB subnet are
// left out: when the KVM is the routing peer for that subnet, the VPN lists
// it among its own networks, and the target already reaches it directly.
func usbNetworkTargetRoutes(cfg USBNetworkConfig, uplinks map[string]usbNetworkUplink) []string {
	uplink := uplinks[cfg.Mode]
	if !cfg.vpnMode() || !uplink.Up {
		return nil
	}
	_, usbSubnet, subnetErr := net.ParseCIDR(cfg.AddressCIDR)
	var kept []string
	for _, route := range uplink.Routes {
		_, network, err := net.ParseCIDR(route)
		if err != nil {
			continue
		}
		if subnetErr == nil && (network.Contains(usbSubnet.IP) || usbSubnet.Contains(network.IP)) {
			continue
		}
		if !slices.Contains(kept, network.String()) {
			kept = append(kept, network.String())
		}
	}
	return kept
}

// usbNetworkVPNRoutes turns the networks a VPN reports into routes for the
// target: the shared CGNAT range both NetBird and Tailscale use for peer
// addresses, then the IPv4 networks other than the default route.
func usbNetworkVPNRoutes(networks []string) []string {
	routes := []string{"100.64.0.0/10"}
	for _, network := range networks {
		_, cidr, err := net.ParseCIDR(strings.TrimSpace(network))
		if err != nil || cidr.IP.To4() == nil {
			continue
		}
		if ones, _ := cidr.Mask.Size(); ones == 0 {
			continue
		}
		if !slices.Contains(routes, cidr.String()) {
			routes = append(routes, cidr.String())
		}
	}
	return routes
}

// SetUplink is called by the NetBird manager and the Tailscale watcher when
// their VPN connects, disconnects or its routes change. While sharing runs, it
// rebuilds the NAT and restarts DHCP when the target's routes change (the
// target picks them up when it renews its lease or USB reconnects).
func (m *USBNetworkManager) SetUplink(name string, uplink usbNetworkUplink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldRoutes := usbNetworkTargetRoutes(m.config, m.uplinks)
	if m.uplinks == nil {
		m.uplinks = map[string]usbNetworkUplink{}
	}
	uplink.Routes = slices.Clone(uplink.Routes)
	m.uplinks[name] = uplink
	m.state.NetbirdUp = m.uplinks[usbUplinkNetbird].Up
	m.state.TailscaleUp = m.uplinks[usbUplinkTailscale].Up
	if !m.state.Running || !m.config.shares(name) {
		return nil
	}
	sourceCIDR, err := usbNetworkSourceCIDR(m.config.AddressCIDR)
	if err != nil {
		return err
	}
	if err := m.applyNATLocked(sourceCIDR); err != nil {
		m.state.LastError = err.Error()
		return err
	}
	if !slices.Equal(oldRoutes, usbNetworkTargetRoutes(m.config, m.uplinks)) {
		if err := m.startDHCPLocked(); err != nil {
			m.state.LastError = err.Error()
			return err
		}
	}
	return nil
}
