package netbird

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"
)

// ConfiguredManagementURL reads the management server from the client's
// config file (default.json), or "" when the file has none.
func ConfiguredManagementURL(data []byte) string {
	var cfg struct {
		ManagementURL *struct {
			Scheme string `json:"Scheme"`
			Host   string `json:"Host"`
		} `json:"ManagementURL"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.ManagementURL == nil {
		return ""
	}
	if cfg.ManagementURL.Scheme == "" || cfg.ManagementURL.Host == "" {
		return ""
	}
	return cfg.ManagementURL.Scheme + "://" + cfg.ManagementURL.Host
}

// SameServer says whether two management URLs name the same server; a
// missing port is the scheme's default port.
func SameServer(a, b string) bool {
	return serverKey(a) != "" && serverKey(a) == serverKey(b)
}

func serverKey(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(parsed.Hostname()), port)
}

// CustomDNSAddress reads the address of the client's local DNS resolver from
// its config file; "" lets the client choose (port 53 of its NetBird address).
func CustomDNSAddress(data []byte) string {
	var cfg struct {
		CustomDNSAddress string `json:"CustomDNSAddress"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.CustomDNSAddress
}

// WithCustomDNSAddress returns the config file with the local resolver's
// address set; every other setting is kept.
func WithCustomDNSAddress(data []byte, address string) ([]byte, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	value, err := json.Marshal(address)
	if err != nil {
		return nil, err
	}
	cfg["CustomDNSAddress"] = value
	return json.MarshalIndent(cfg, "", "    ")
}

// DNSZone is the NetBird DNS zone of a peer's name: "netbird.selfhosted" for
// "kvm6-usb.netbird.selfhosted"; "" for a name without one.
func DNSZone(fqdn string) string {
	_, zone, found := strings.Cut(strings.TrimSuffix(fqdn, "."), ".")
	if !found {
		return ""
	}
	return zone
}
