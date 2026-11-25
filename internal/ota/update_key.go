package ota

import (
	_ "embed"
	"time"

	"github.com/rs/zerolog"
)

// updateKeyFingerprint is the key that signs this firmware's own releases. OS Services
// packages still come from JetKVM and keep rootKeyFingerprint.
const updateKeyFingerprint = "4835C154F7B285B9D206E0AAFFEE7E838E6BBB6B"

// updateKey is that key's public part, so a firmware update asks no key server.
//
//go:embed update_key.asc
var updateKey []byte

func newUpdateVerifier(logger *zerolog.Logger, httpClient func() HttpClient) *GPGVerifier {
	v := NewGPGVerifier(logger, httpClient)
	v.rootKeyFP = updateKeyFingerprint
	if keyring, err := v.parseAndValidateKeyring(updateKey); err == nil {
		v.updateMemoryCache(updateKey, keyring)
		// FetchPublicKey drops a cached key after 24 hours; this one must stay.
		v.cachedKeyTime = time.Now().AddDate(100, 0, 0)
	}
	return v
}
