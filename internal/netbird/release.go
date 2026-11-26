package netbird

// Release is a NetBird client build the KVM installs: the official linux
// armv6 tarball (it runs on the RV1106's armv7 core), pinned by sha256 so a
// swapped file is refused even when the download source is compromised.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	// Size of the tarball in bytes; a download larger than this is refused.
	Size int64 `json:"size"`
}

// PinnedRelease is the build the Install button fetches. The sha256 is the
// "digest" GitHub publishes for the release asset; it matched the file
// downloaded on a KVM on 2026-11-23.
var PinnedRelease = Release{
	Version: "0.83.2",
	URL:     "https://github.com/netbirdio/netbird/releases/download/v0.83.2/netbird_0.83.2_linux_armv6.tar.gz",
	SHA256:  "411d09a70319ecb100d924bdde207144e9a0c186280dd49499871064195d171f",
	Size:    15322310,
}
