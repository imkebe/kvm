package usbgadget

// The USB network function's addresses when none are configured: the KVM's
// end (dev_addr) and the target's end (host_addr), the address the target's
// network adapter shows.
const (
	DefaultNetworkDevAddr  = "02:1d:6b:00:00:01"
	DefaultNetworkHostAddr = "02:1d:6b:00:00:02"
)

var networkConfigKeys = []string{"network_ncm", "network_ecm", "network_rndis"}

func networkConfig(function string, order uint) gadgetConfigItem {
	name := function + ".usb0"
	return gadgetConfigItem{
		order: order, device: name,
		path: []string{"functions", name}, configPath: []string{name},
		attrs: gadgetAttributes{
			"dev_addr": DefaultNetworkDevAddr, "host_addr": DefaultNetworkHostAddr,
		},
	}
}

// syncNetworkAddresses writes the configured addresses into the network
// items. u_ether refuses a new address while the function is linked; the
// transaction unlinks a linked item before writing its attributes.
func (u *UsbGadget) syncNetworkAddresses() {
	dev := orDefault(u.customConfig.NetworkDevAddr, DefaultNetworkDevAddr)
	host := orDefault(u.customConfig.NetworkHostAddr, DefaultNetworkHostAddr)
	for _, key := range networkConfigKeys {
		if item, ok := u.configMap[key]; ok && item.attrs != nil {
			item.attrs["dev_addr"] = dev
			item.attrs["host_addr"] = host
		}
	}
}
