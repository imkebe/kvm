package kvm

import "testing"

func TestUSBNetworkAdapterPresets(t *testing.T) {
	for _, adapter := range usbNetworkAdapters {
		host, dev := usbNetworkMACs(adapter, "serial-1")
		cfg := USBNetworkConfig{Adapter: adapter.ID, HostMAC: host, DeviceMAC: dev}
		if err := validateUSBNetworkAdapter(cfg); err != nil {
			t.Fatalf("%s: %v", adapter.ID, err)
		}
		if host[:8] != adapter.OUI || dev[:8] != adapter.OUI {
			t.Fatalf("%s: %s / %s lack prefix %s", adapter.ID, host, dev, adapter.OUI)
		}
		again, _ := usbNetworkMACs(adapter, "serial-1")
		other, _ := usbNetworkMACs(adapter, "serial-2")
		if again != host {
			t.Fatalf("%s: not stable", adapter.ID)
		}
		if adapter.ID != usbNetworkAdapterDefault && other == host {
			t.Fatalf("%s: same address for two KVMs", adapter.ID)
		}
	}
}

func TestUSBNetworkCustomAdapter(t *testing.T) {
	cfg := normalizeUSBNetworkAdapter(USBNetworkConfig{Adapter: "custom", HostMAC: "BC-24-11-AA-BB-CC"})
	if cfg.HostMAC != "bc:24:11:aa:bb:cc" || cfg.DeviceMAC != "bc:24:11:aa:bb:cd" {
		t.Fatalf("got %s / %s", cfg.HostMAC, cfg.DeviceMAC)
	}
	if err := validateUSBNetworkAdapter(cfg); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []USBNetworkConfig{
		{Adapter: "custom", HostMAC: "01:00:5e:00:00:01", DeviceMAC: "02:00:00:00:00:01"},
		{Adapter: "custom", HostMAC: "02:00:00:00:00:01", DeviceMAC: "02:00:00:00:00:01"},
		{Adapter: "custom", HostMAC: "nope", DeviceMAC: "02:00:00:00:00:01"},
		{Adapter: "e1000", HostMAC: "02:00:00:00:00:02", DeviceMAC: "02:00:00:00:00:01"},
	} {
		if validateUSBNetworkAdapter(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
