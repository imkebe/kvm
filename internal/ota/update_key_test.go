package ota

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Firmware updates and OS Services packages are signed by different parties.
func TestFirmwareUpdatesUseTheirOwnKey(t *testing.T) {
	logger := zerolog.Nop()
	if got := newUpdateVerifier(&logger, nil).GetRootKeyFingerprint(); got != updateKeyFingerprint || got == rootKeyFingerprint {
		t.Errorf("firmware update key = %q", got)
	}
	// A nil HTTP client: asking a key server would panic.
	v := newUpdateVerifier(&logger, nil)
	v.cachedKeyTime = v.cachedKeyTime.Add(-48 * time.Hour) // two days of uptime
	keyring, err := v.getKeyring(context.Background())
	if err != nil || len(keyring) != 1 {
		t.Fatalf("built-in key: %v, %d keys", err, len(keyring))
	}
	if got := strings.ToUpper(hex.EncodeToString(keyring[0].PrimaryKey.Fingerprint)); got != updateKeyFingerprint {
		t.Errorf("built-in key has fingerprint %s", got)
	}
	if got := NewGPGVerifier(&logger, nil).GetRootKeyFingerprint(); got != rootKeyFingerprint {
		t.Errorf("OS Services key = %q, want the JetKVM release key", got)
	}
}
