package nmlite

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jetkvm/kvm/internal/network/types"
	"github.com/jetkvm/kvm/pkg/nmlite/link"
)

// The static IPv4 fallback of DHCP mode: when DHCP gives no lease within
// the configured time after DHCP starts (at boot, on link up, on a settings
// change), the interface gets the fallback address. The DHCP client keeps
// trying, and its first lease replaces the fallback address.
//
// udhcpc's script sets the lease address with ifconfig, which replaces the
// primary address and the default routes; jetdhcpc's lease is reconciled,
// which removes any other address. Either way the lease callback also
// deletes the fallback address if it is still there.

const (
	defaultIPv4FallbackTimeout = 30 * time.Second
	minIPv4FallbackTimeout     = 5 * time.Second
	maxIPv4FallbackTimeout     = 10 * time.Minute
)

type ipv4Fallback struct {
	mu    sync.Mutex
	timer *time.Timer
	// Bumped on each reset, so a timer that fired just before a reset does
	// nothing.
	generation int
	// The applied fallback address, while it is on the interface.
	applied *types.IPAddress
}

func (im *InterfaceManager) ipv4FallbackTimeout() time.Duration {
	timeout := defaultIPv4FallbackTimeout
	if im.config.IPv4FallbackTimeout.Valid && im.config.IPv4FallbackTimeout.Int64 > 0 {
		timeout = time.Duration(im.config.IPv4FallbackTimeout.Int64) * time.Second
	}
	return min(max(timeout, minIPv4FallbackTimeout), maxIPv4FallbackTimeout)
}

func (im *InterfaceManager) ipv4FallbackWanted() bool {
	return im.config.IPv4Mode.String == "dhcp" &&
		im.config.IPv4FallbackEnabled.Bool &&
		im.config.IPv4Fallback != nil
}

// IPv4FallbackActive reports whether the fallback address is applied.
func (im *InterfaceManager) IPv4FallbackActive() bool {
	im.fallback.mu.Lock()
	defer im.fallback.mu.Unlock()
	return im.fallback.applied != nil
}

// resetIPv4Fallback runs each time the IPv4 configuration is applied: it
// stops a pending fallback, removes an applied one in DHCP mode (static
// and disabled mode replace all addresses themselves), and in DHCP mode
// with a fallback starts the wait for a lease again.
func (im *InterfaceManager) resetIPv4Fallback() {
	im.fallback.mu.Lock()
	if im.fallback.timer != nil {
		im.fallback.timer.Stop()
		im.fallback.timer = nil
	}
	im.fallback.generation++
	generation := im.fallback.generation
	applied := im.fallback.applied
	im.fallback.applied = nil
	wanted := im.ipv4FallbackWanted()
	if wanted {
		timeout := im.ipv4FallbackTimeout()
		// The timer goroutine gets the address now: SetConfig replaces
		// im.config without a lock.
		fallback := im.config.IPv4Fallback
		im.logger.Info().Dur("timeout", timeout).Msg("waiting for a DHCP lease before the static IPv4 fallback")
		im.fallback.timer = time.AfterFunc(timeout, func() { im.applyIPv4Fallback(generation, fallback) })
	}
	im.fallback.mu.Unlock()

	if applied == nil {
		return
	}
	if im.config.IPv4Mode.String == "dhcp" {
		im.removeIPv4FallbackAddress(applied, nil)
	}
	go im.refreshState()
}

// applyIPv4Fallback puts the fallback address on the interface, unless a
// lease arrived or the configuration changed since the wait started (each
// applied configuration bumps the generation).
func (im *InterfaceManager) applyIPv4Fallback(generation int, fallback *types.IPv4StaticConfig) {
	im.fallback.mu.Lock()
	defer im.fallback.mu.Unlock()
	if generation != im.fallback.generation {
		return
	}
	im.fallback.timer = nil

	// A settings change restarts the wait while a lease may already be in
	// place; an address on the interface means DHCP works.
	if im.hasGlobalIPv4Address() {
		im.logger.Debug().Msg("interface has an IPv4 address, static IPv4 fallback not needed")
		return
	}

	config, err := im.staticConfig.ToIPv4Static(fallback)
	if err != nil || len(config.Addresses) == 0 {
		im.logger.Error().Err(err).Msg("invalid static IPv4 fallback")
		return
	}
	im.logger.Warn().
		Str("address", config.Addresses[0].String()).
		Msg("no DHCP lease, applying the static IPv4 fallback; DHCP keeps trying")
	if err := im.ReconcileLinkAddrs(config.Addresses, link.AfInet, link.StaticProtocol); err != nil {
		im.logger.Error().Err(err).Msg("failed to apply the static IPv4 fallback")
		return
	}
	if im.onResolvConfChange != nil {
		if err := im.onResolvConfChange(link.AfInet, &types.InterfaceResolvConf{
			NameServers: config.Nameservers,
			Source:      "static",
		}); err != nil {
			im.logger.Warn().Err(err).Msg("failed to update resolv.conf for the IPv4 fallback")
		}
	}
	applied := config.Addresses[0]
	im.fallback.applied = &applied
	go im.refreshState()
}

// endIPv4Fallback runs on each IPv4 lease: the wait ends, and an applied
// fallback address goes, unless the lease has the same address.
func (im *InterfaceManager) endIPv4Fallback(lease *types.DHCPLease) {
	im.fallback.mu.Lock()
	if im.fallback.timer != nil {
		im.fallback.timer.Stop()
		im.fallback.timer = nil
	}
	im.fallback.generation++
	applied := im.fallback.applied
	im.fallback.applied = nil
	im.fallback.mu.Unlock()

	if applied == nil {
		return
	}
	im.logger.Info().Msg("DHCP lease received, removing the static IPv4 fallback")
	im.removeIPv4FallbackAddress(applied, lease.IPAddress)
	go im.refreshState()
}

func (im *InterfaceManager) removeIPv4FallbackAddress(applied *types.IPAddress, keep net.IP) {
	if keep != nil && applied.Address.IP.Equal(keep) {
		return
	}
	l, err := im.link()
	if err != nil || l == nil {
		return
	}
	nl := getNetlinkManager()
	addrs, err := nl.AddrList(l, link.AfInet)
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if !addr.IP.Equal(applied.Address.IP) {
			continue
		}
		if err := nl.AddrDel(l, &addr); err != nil {
			im.logger.Warn().Err(err).Msg("failed to remove the static IPv4 fallback address")
		}
	}
}

func (im *InterfaceManager) refreshState() {
	if err := im.updateInterfaceState(); err != nil {
		im.logger.Debug().Err(err).Msg("failed to update interface state")
	}
}

// validateIPv4Fallback checks what the field tags cannot: an enabled
// fallback needs its address.
func validateIPv4Fallback(config *types.NetworkConfig) error {
	if !config.IPv4FallbackEnabled.Bool {
		return nil
	}
	if config.IPv4Fallback == nil || !config.IPv4Fallback.Address.Valid || !config.IPv4Fallback.Netmask.Valid {
		return fmt.Errorf("invalid config: the IPv4 fallback needs an address and a netmask")
	}
	return nil
}

func (im *InterfaceManager) hasGlobalIPv4Address() bool {
	l, err := im.link()
	if err != nil || l == nil {
		return false
	}
	addrs, err := getNetlinkManager().AddrList(l, link.AfInet)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if addr.IP.IsGlobalUnicast() {
			return true
		}
	}
	return false
}
