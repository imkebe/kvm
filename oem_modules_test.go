package kvm

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOEMModulesOrderAndSkip(t *testing.T) {
	// Excerpt of the image's /oem/usr/ko/modules.dep.
	dep := []byte(`chacha-neon.ko:
poly1305-arm.ko:
libchacha20poly1305.ko: chacha-neon.ko poly1305-arm.ko
wireguard.ko: libchacha20poly1305.ko chacha-neon.ko poly1305-arm.ko
tun.ko:
libcrc32c.ko:
nfnetlink.ko:
nf_tables.ko: nfnetlink.ko libcrc32c.ko
`)
	// chacha_neon is loaded already (listed with an underscore).
	loaded := []byte("chacha_neon 16384 0 - Live 0x0\n")

	var got []string
	saved := insmodModule
	defer func() { insmodModule = saved }()
	insmodModule = func(path string) error {
		got = append(got, filepath.Base(path))
		return nil
	}

	require.NoError(t, loadOEMModulesFrom(dep, loaded, []string{"tun", "wireguard", "nf_tables", "wireguard"}))
	assert.Equal(t, []string{
		"tun.ko", "poly1305-arm.ko", "libchacha20poly1305.ko", "wireguard.ko",
		"nfnetlink.ko", "libcrc32c.ko", "nf_tables.ko",
	}, got)
}

func TestLoadOEMModulesMissing(t *testing.T) {
	saved := insmodModule
	defer func() { insmodModule = saved }()
	insmodModule = func(string) error { return nil }

	err := loadOEMModulesFrom([]byte("tun.ko:\n"), nil, []string{"wireguard"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wireguard.ko")
}
