package kvm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestValidateUSBNetworkConfig(t *testing.T) {
	cfg := normalizeUSBNetworkConfig(USBNetworkConfig{})
	if err := validateUSBNetworkConfig(cfg); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestValidateUSBNetworkConfigRejectsBadMode(t *testing.T) {
	cfg := normalizeUSBNetworkConfig(USBNetworkConfig{Mode: "bridge"})
	if err := validateUSBNetworkConfig(cfg); err == nil {
		t.Fatalf("expected invalid mode error")
	}
}

func TestValidateUSBNetworkConfigRejectsBadCIDR(t *testing.T) {
	cfg := normalizeUSBNetworkConfig(USBNetworkConfig{AddressCIDR: "10.77.0.1"})
	if err := validateUSBNetworkConfig(cfg); err == nil {
		t.Fatalf("expected invalid CIDR error")
	}
}

// S10: interface names reach nft rule text, ip/dnsmasq argv and /tmp paths.
func TestValidateUSBNetworkConfigRejectsBadInterface(t *testing.T) {
	for _, name := range []string{"eth0 accept", "../x", "-x", "eth0;drop", "a\nb", "abcdefghijklmnop", ".", ".."} {
		if validateUSBNetworkConfig(normalizeUSBNetworkConfig(USBNetworkConfig{InterfaceName: name})) == nil {
			t.Errorf("interface %q accepted", name)
		}
		if validateUSBNetworkConfig(normalizeUSBNetworkConfig(USBNetworkConfig{UpstreamInterface: name})) == nil {
			t.Errorf("upstream %q accepted", name)
		}
	}
	for _, name := range []string{"eth0", "wlan0", "usb0", "wt0", "tailscale0", "br-lan.10"} {
		if err := validateUSBNetworkConfig(normalizeUSBNetworkConfig(USBNetworkConfig{InterfaceName: name, UpstreamInterface: name})); err != nil {
			t.Errorf("%q refused: %v", name, err)
		}
	}
}

func TestUSBNetworkHostRanges(t *testing.T) {
	for _, cfg := range []USBNetworkConfig{
		{AddressCIDR: "::1/64"}, {DHCPStart: "10.77.0.0"}, {DHCPEnd: "10.77.0.255"}, {DHCPStart: "10.77.0.1"},
	} {
		if err := validateUSBNetworkConfig(normalizeUSBNetworkConfig(cfg)); err == nil {
			t.Fatalf("accepted invalid range: %+v", cfg)
		}
	}
	for _, function := range []string{"ncm", "ecm", "rndis"} {
		if err := validateUSBNetworkConfig(normalizeUSBNetworkConfig(USBNetworkConfig{Function: function})); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUSBNetworkStats(t *testing.T) {
	root := t.TempDir()
	statsPath := filepath.Join(root, "usb0", "statistics")
	if err := os.MkdirAll(statsPath, 0755); err != nil {
		t.Fatal(err)
	}
	counters := map[string]uint64{"rx_bytes": 1<<40 + 17, "tx_bytes": 1<<41 + 9, "rx_packets": 1234, "tx_packets": 987}
	for name, value := range counters {
		if err := os.WriteFile(filepath.Join(statsPath, name), []byte(strconv.FormatUint(value, 10)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	carrier := filepath.Join(root, "usb0", "carrier")
	if err := os.WriteFile(carrier, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readUSBNetworkStats(root, "usb0")
	if err != nil {
		t.Fatal(err)
	}
	if got.rxBytes != counters["rx_bytes"] || got.txBytes != counters["tx_bytes"] || got.rxPackets != counters["rx_packets"] || got.txPackets != counters["tx_packets"] || !got.linkUp {
		t.Fatalf("bad stats: %+v", got)
	}
	if err := os.WriteFile(carrier, []byte("0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = readUSBNetworkStats(root, "usb0")
	if err != nil || got.linkUp {
		t.Fatalf("link did not go down: %+v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(statsPath, "rx_bytes"), []byte("invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readUSBNetworkStats(root, "usb0"); err == nil {
		t.Fatal("accepted malformed counter")
	}
	if _, err := readUSBNetworkStats(root, "missing"); err == nil {
		t.Fatal("accepted missing counters")
	}
}

func TestUSBNetworkProcCounterFallback(t *testing.T) {
	data := []byte("Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n eth0: 99 8 0 0 0 0 0 0 88 7 0 0 0 0 0 0\n usb0: 1099511627793 1234 0 0 0 0 0 0 2199023255561 987 0 0 0 0 0 0\n")
	got, err := parseUSBNetworkProcStats(data, "usb0")
	if err != nil {
		t.Fatal(err)
	}
	if got.rxBytes != 1<<40+17 || got.txBytes != 1<<41+9 || got.rxPackets != 1234 || got.txPackets != 987 {
		t.Fatalf("bad proc fallback: %+v", got)
	}
	if _, err := parseUSBNetworkProcStats(data, "usb1"); err == nil {
		t.Fatal("accepted missing interface")
	}
	for _, data := range []string{"usb0: 5 4", "usb0: x 1 0 0 0 0 0 0 2 3 0 0 0 0 0 0"} {
		if _, err := parseUSBNetworkProcStats([]byte(data), "usb0"); err == nil {
			t.Fatal("accepted malformed proc counters")
		}
	}
}
