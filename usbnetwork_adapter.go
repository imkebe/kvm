package kvm

import (
	"crypto/sha256"
	"fmt"
	"net"
	"strings"

	"github.com/jetkvm/kvm/internal/usbgadget"
)

// The USB Ethernet adapter the target sees. A composite gadget has one
// vendor and product ID for all its functions, and the kernel fixes the
// network interface strings, so what can look like a given card is its MAC
// address: the vendor prefix (OUI) is what DHCP servers, routers and network
// tools show as the adapter's maker. The presets are the prefixes of common
// virtual and physical network cards, as a hypervisor assigns them.
type usbNetworkAdapter struct {
	ID  string `json:"id"`
	OUI string `json:"oui"`
}

const (
	usbNetworkAdapterDefault = "jetkvm"
	usbNetworkAdapterCustom  = "custom"
)

var usbNetworkAdapters = []usbNetworkAdapter{
	{ID: usbNetworkAdapterDefault, OUI: "02:1d:6b"},
	{ID: "proxmox", OUI: "bc:24:11"},
	{ID: "qemu", OUI: "52:54:00"},
	{ID: "vmware", OUI: "00:50:56"},
	{ID: "hyperv", OUI: "00:15:5d"},
	{ID: "virtualbox", OUI: "08:00:27"},
	{ID: "xen", OUI: "00:16:3e"},
	{ID: "parallels", OUI: "00:1c:42"},
	{ID: "realtek", OUI: "00:e0:4c"},
	{ID: "intel", OUI: "00:1b:21"},
	{ID: "asix", OUI: "00:0e:c6"},
}

func findUSBNetworkAdapter(id string) (usbNetworkAdapter, bool) {
	for _, adapter := range usbNetworkAdapters {
		if adapter.ID == id {
			return adapter, true
		}
	}
	return usbNetworkAdapter{}, false
}

// usbNetworkMACs gives a preset's two addresses: the vendor prefix and three
// bytes that are stable for this KVM and this preset, the target's end
// first; the KVM's end differs in the last bit. The default preset keeps the
// fixed addresses earlier builds used.
func usbNetworkMACs(adapter usbNetworkAdapter, deviceID string) (host, dev string) {
	if adapter.ID == usbNetworkAdapterDefault {
		return usbgadget.DefaultNetworkHostAddr, usbgadget.DefaultNetworkDevAddr
	}
	sum := sha256.Sum256([]byte(deviceID + "/usb-network/" + adapter.ID))
	suffix := func(last byte) string {
		return fmt.Sprintf("%s:%02x:%02x:%02x", adapter.OUI, sum[0], sum[1], last)
	}
	return suffix(sum[2] &^ 1), suffix(sum[2] | 1)
}

func normalizeMAC(mac string) string {
	parsed, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil || len(parsed) != 6 {
		return strings.TrimSpace(mac)
	}
	return parsed.String()
}

// normalizeUSBNetworkAdapter fills in the preset and its addresses. A custom
// adapter keeps the given addresses; without a KVM address it takes the
// target's address with the last bit flipped.
func normalizeUSBNetworkAdapter(cfg USBNetworkConfig) USBNetworkConfig {
	if cfg.Adapter == "" {
		cfg.Adapter = usbNetworkAdapterDefault
	}
	if cfg.Adapter == usbNetworkAdapterCustom {
		cfg.HostMAC = normalizeMAC(cfg.HostMAC)
		cfg.DeviceMAC = normalizeMAC(cfg.DeviceMAC)
		if cfg.DeviceMAC == "" {
			if host, err := net.ParseMAC(cfg.HostMAC); err == nil && len(host) == 6 {
				host[5] ^= 1
				cfg.DeviceMAC = host.String()
			}
		}
		return cfg
	}
	if adapter, ok := findUSBNetworkAdapter(cfg.Adapter); ok {
		cfg.HostMAC, cfg.DeviceMAC = usbNetworkMACs(adapter, GetDeviceID())
	}
	return cfg
}

func validateUnicastMAC(label, mac string) error {
	parsed, err := net.ParseMAC(mac)
	if err != nil || len(parsed) != 6 {
		return fmt.Errorf("invalid USB network %s MAC address: %q", label, mac)
	}
	if parsed[0]&1 == 1 {
		return fmt.Errorf("USB network %s MAC address %s is a multicast address", label, mac)
	}
	if parsed.String() == "00:00:00:00:00:00" {
		return fmt.Errorf("USB network %s MAC address cannot be all zeros", label)
	}
	return nil
}

func validateUSBNetworkAdapter(cfg USBNetworkConfig) error {
	if cfg.Adapter != usbNetworkAdapterCustom {
		if _, ok := findUSBNetworkAdapter(cfg.Adapter); !ok {
			return fmt.Errorf("unknown USB network adapter: %s", cfg.Adapter)
		}
	}
	if err := validateUnicastMAC("target", cfg.HostMAC); err != nil {
		return err
	}
	if err := validateUnicastMAC("KVM", cfg.DeviceMAC); err != nil {
		return err
	}
	if cfg.HostMAC == cfg.DeviceMAC {
		return fmt.Errorf("the target's and the KVM's USB network MAC addresses must differ")
	}
	return nil
}

// gadgetNetworkAddresses gives the addresses the gadget writes, the
// target's end first.
func gadgetNetworkAddresses(cfg USBNetworkConfig) (host, dev string) {
	return cfg.HostMAC, cfg.DeviceMAC
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// rpcGetUSBNetworkAdapters lists the adapter presets with the addresses
// each would give this KVM.
func rpcGetUSBNetworkAdapters() []map[string]string {
	list := make([]map[string]string, 0, len(usbNetworkAdapters))
	for _, adapter := range usbNetworkAdapters {
		host, dev := usbNetworkMACs(adapter, GetDeviceID())
		list = append(list, map[string]string{
			"id": adapter.ID, "oui": adapter.OUI, "host_mac": host, "device_mac": dev,
		})
	}
	return list
}
