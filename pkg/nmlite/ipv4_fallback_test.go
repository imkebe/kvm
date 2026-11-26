package nmlite

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jetkvm/kvm/internal/network/types"
)

func TestValidateIPv4Fallback(t *testing.T) {
	off := &types.NetworkConfig{}
	if err := validateIPv4Fallback(off); err != nil {
		t.Errorf("disabled fallback: %v", err)
	}
	missing := &types.NetworkConfig{IPv4FallbackEnabled: null.BoolFrom(true)}
	if validateIPv4Fallback(missing) == nil {
		t.Error("enabled fallback without an address should fail")
	}
	ok := &types.NetworkConfig{
		IPv4FallbackEnabled: null.BoolFrom(true),
		IPv4Fallback: &types.IPv4StaticConfig{
			Address: null.StringFrom("192.168.1.250"),
			Netmask: null.StringFrom("255.255.255.0"),
		},
	}
	if err := validateIPv4Fallback(ok); err != nil {
		t.Errorf("complete fallback: %v", err)
	}
}

func TestIPv4FallbackTimeout(t *testing.T) {
	for _, c := range []struct {
		seconds null.Int
		want    time.Duration
	}{
		{null.Int{}, 30 * time.Second},
		{null.IntFrom(1), 5 * time.Second},
		{null.IntFrom(45), 45 * time.Second},
		{null.IntFrom(100000), 10 * time.Minute},
	} {
		im := &InterfaceManager{config: &types.NetworkConfig{IPv4FallbackTimeout: c.seconds}}
		if got := im.ipv4FallbackTimeout(); got != c.want {
			t.Errorf("%v: got %v, want %v", c.seconds, got, c.want)
		}
	}
}
