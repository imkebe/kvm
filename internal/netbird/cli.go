// Package netbird drives the NetBird client on the KVM through its CLI: the
// daemon (`netbird service run`) is started by the app, and every other
// action (status, up, down) is a CLI call against the daemon's socket.
package netbird

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// DaemonAddr is the control socket of the app-started daemon.
	DaemonAddr = "unix:///run/netbird.sock"
	// DefaultManagementURL is NetBird Cloud.
	DefaultManagementURL = "https://api.netbird.io:443"
	// InterfaceName is the WireGuard interface the daemon creates.
	InterfaceName = "wt0"

	commandTimeout = 30 * time.Second
)

// Daemon states reported by `netbird status --json` (daemonStatus).
const (
	StateIdle           = "Idle"
	StateConnecting     = "Connecting"
	StateConnected      = "Connected"
	StateNeedsLogin     = "NeedsLogin"
	StateLoginFailed    = "LoginFailed"
	StateSessionExpired = "SessionExpired"
)

// Binary is the installed client; tests and other images may change it.
var Binary = "/userdata/netbird/netbird"

// ExecCommand runs the client with args and returns its combined output.
// Package-level so tests can replace it.
var ExecCommand = func(ctx context.Context, args ...string) ([]byte, error) {
	output, err := command(ctx, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("netbird %s: %w: %s", firstArg(args), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// StartCommand starts the client with args and returns a reader over its
// combined output; wait reports how it ended. Replaced in tests.
var StartCommand = func(ctx context.Context, args ...string) (io.Reader, func() error, error) {
	cmd := command(ctx, args...)
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	// Close the pipe when the process ends, so the reader sees EOF.
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = writer.Close()
		done <- err
	}()
	wait := func() error { return <-done }
	return reader, wait, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// Status is the part of `netbird status --json` the KVM shows.
type Status struct {
	DaemonStatus        string `json:"daemonStatus"`
	Version             string `json:"version"`
	ManagementURL       string `json:"managementURL"`
	ManagementConnected bool   `json:"managementConnected"`
	ManagementError     string `json:"managementError,omitempty"`
	SignalConnected     bool   `json:"signalConnected"`
	RelaysTotal         int    `json:"relaysTotal"`
	RelaysAvailable     int    `json:"relaysAvailable"`
	PeersTotal          int    `json:"peersTotal"`
	PeersConnected      int    `json:"peersConnected"`
	IP                  string `json:"ip,omitempty"`
	// IPNet is the address with the NetBird network's prefix length, as
	// "100.111.29.244/16".
	IPNet           string   `json:"ipNet,omitempty"`
	FQDN            string   `json:"fqdn,omitempty"`
	KernelInterface bool     `json:"kernelInterface"`
	Networks        []string `json:"networks,omitempty"`
	// NameserverDomains are the domains the dashboard's nameserver groups
	// answer for this peer; "." is a group for all domains.
	NameserverDomains []string `json:"nameserverDomains,omitempty"`
}

type rawEndpoint struct {
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	Error     string `json:"error"`
}

type rawStatus struct {
	DaemonStatus  string      `json:"daemonStatus"`
	DaemonVersion string      `json:"daemonVersion"`
	Management    rawEndpoint `json:"management"`
	Signal        rawEndpoint `json:"signal"`
	Relays        struct {
		Total     int `json:"total"`
		Available int `json:"available"`
	} `json:"relays"`
	Peers struct {
		Total     int `json:"total"`
		Connected int `json:"connected"`
	} `json:"peers"`
	NetbirdIP           string          `json:"netbirdIp"`
	FQDN                string          `json:"fqdn"`
	UsesKernelInterface bool            `json:"usesKernelInterface"`
	Networks            json.RawMessage `json:"networks"`
	DNSServers          []struct {
		Domains []string `json:"domains"`
	} `json:"dnsServers"`
}

// ParseStatus reads `netbird status --json` output.
func ParseStatus(data []byte) (*Status, error) {
	var raw rawStatus
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse netbird status: %w", err)
	}
	status := &Status{
		DaemonStatus:        raw.DaemonStatus,
		Version:             raw.DaemonVersion,
		ManagementURL:       raw.Management.URL,
		ManagementConnected: raw.Management.Connected,
		ManagementError:     raw.Management.Error,
		SignalConnected:     raw.Signal.Connected,
		RelaysTotal:         raw.Relays.Total,
		RelaysAvailable:     raw.Relays.Available,
		PeersTotal:          raw.Peers.Total,
		PeersConnected:      raw.Peers.Connected,
		IP:                  strings.Split(raw.NetbirdIP, "/")[0],
		IPNet:               raw.NetbirdIP,
		FQDN:                strings.TrimSuffix(raw.FQDN, "."),
		KernelInterface:     raw.UsesKernelInterface,
	}
	// The networks list has been plain strings; ignore any other shape
	// rather than failing the whole status.
	var networks []string
	if len(raw.Networks) > 0 && json.Unmarshal(raw.Networks, &networks) == nil {
		status.Networks = networks
	}
	for _, group := range raw.DNSServers {
		domains := group.Domains
		if len(domains) == 0 {
			domains = []string{"."}
		}
		for _, domain := range domains {
			domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
			if domain == "" {
				domain = "."
			}
			if !slices.Contains(status.NameserverDomains, domain) {
				status.NameserverDomains = append(status.NameserverDomains, domain)
			}
		}
	}
	return status, nil
}

// GetStatus asks the daemon for its status.
func GetStatus(ctx context.Context) (*Status, error) {
	return GetStatusAt(ctx, DaemonAddr)
}

// GetStatusAt asks the daemon at daemonAddr for its status.
func GetStatusAt(ctx context.Context, daemonAddr string) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := ExecCommand(ctx, "status", "--json", "--daemon-addr", daemonAddr)
	if err != nil {
		return nil, err
	}
	return ParseStatus(output)
}

// UpOptions are the settings `netbird up` is given.
type UpOptions struct {
	// DaemonAddr is the daemon to use; "" is DaemonAddr.
	DaemonAddr    string
	ManagementURL string
	SetupKeyFile  string
	Hostname      string
	// ResolverAddress moves the client's local DNS resolver off port 53 of
	// its NetBird address ("127.0.0.1:5053"); "" leaves it there.
	ResolverAddress string
	// ExtraArgs are added as they are (the second client's interface, port
	// and route flags).
	ExtraArgs []string
}

func (o UpOptions) args() []string {
	daemonAddr := o.DaemonAddr
	if daemonAddr == "" {
		daemonAddr = DaemonAddr
	}
	// DNS stays off: the KVM's own DHCP client owns /etc/resolv.conf.
	args := []string{"up", "--daemon-addr", daemonAddr, "--disable-dns"}
	if o.ManagementURL != "" {
		args = append(args, "--management-url", o.ManagementURL)
	}
	if o.SetupKeyFile != "" {
		args = append(args, "--setup-key-file", o.SetupKeyFile)
	}
	if o.Hostname != "" {
		args = append(args, "--hostname", o.Hostname)
	}
	if o.ResolverAddress != "" {
		args = append(args, "--dns-resolver-address", o.ResolverAddress)
	}
	return append(args, o.ExtraArgs...)
}

// Up connects a peer that can sign in without a browser: with a setup key, or
// one that is already registered.
func Up(ctx context.Context, opts UpOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 2*commandTimeout)
	defer cancel()
	_, err := ExecCommand(ctx, opts.args()...)
	return err
}

// UpInteractive runs `netbird up` for a browser sign-in. It calls onURL with
// the sign-in link as soon as the client prints it, then waits until the
// sign-in completes, fails or ctx ends.
func UpInteractive(ctx context.Context, opts UpOptions, onURL func(string)) error {
	reader, wait, err := StartCommand(ctx, opts.args()...)
	if err != nil {
		return err
	}
	var output strings.Builder
	scanner := bufio.NewScanner(reader)
	reported := false
	for scanner.Scan() {
		line := scanner.Text()
		if output.Len() < 4096 {
			output.WriteString(line + "\n")
		}
		if !reported {
			if link := FindSignInURL(line); link != "" {
				reported = true
				onURL(link)
			}
		}
	}
	if err := wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("netbird up: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// FindSignInURL returns the sign-in link in a line of `netbird up` output, or
// "" for lines without one (documentation links are skipped).
func FindSignInURL(line string) string {
	for _, match := range urlPattern.FindAllString(line, -1) {
		parsed, err := url.Parse(match)
		if err != nil || parsed.Host == "" {
			continue
		}
		if strings.HasSuffix(parsed.Host, "docs.netbird.io") {
			continue
		}
		return match
	}
	return ""
}

// Down disconnects the peer; the daemon keeps running.
func Down(ctx context.Context) error {
	return DownAt(ctx, DaemonAddr)
}

// DownAt disconnects the peer of the daemon at daemonAddr.
func DownAt(ctx context.Context, daemonAddr string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	_, err := ExecCommand(ctx, "down", "--daemon-addr", daemonAddr)
	return err
}

// Version returns the installed client's version.
func Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := ExecCommand(ctx, "version")
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", errors.New("netbird version printed nothing")
	}
	return version, nil
}

// NormalizeManagementURL checks a management URL and returns it in the form
// the client accepts; "" means NetBird Cloud.
func NormalizeManagementURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid management URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("management URL must start with http:// or https://")
	}
	if parsed.Host == "" {
		return "", errors.New("management URL must include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("management URL must not include user info, query or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("management URL path is not supported")
	}
	// The setup key and sign-in travel in clear over http.
	if parsed.Scheme == "http" && !privateHost(parsed.Hostname()) {
		return "", errors.New("http:// is allowed only for a private or loopback IP address; use https://")
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func privateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && (addr.IsPrivate() || addr.IsLoopback())
}

// CAFile, when set, is the CA bundle the NetBird CLI is given as
// SSL_CERT_FILE (the KVM has no system bundle).
var CAFile string

func command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, Binary, args...)
	if CAFile != "" {
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+CAFile)
	}
	return cmd
}
