package kvm

import "testing"

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
