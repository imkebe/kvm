package usbgadget

var networkConfig = gadgetConfigItem{
	order:      6000,
	device:     "rndis.usb0",
	path:       []string{"functions", "rndis.usb0"},
	configPath: []string{"rndis.usb0"},
	attrs: gadgetAttributes{
		"dev_addr":  "02:1d:6b:00:00:01",
		"host_addr": "02:1d:6b:00:00:02",
	},
}
