package netbird

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Output of `netbird status --json` on KVM .6 before sign-in (2026-11-23).
const needsLoginJSON = `{"peers":{"total":0,"connected":0,"details":null},"cliVersion":"0.83.2","daemonVersion":"0.83.2","daemonStatus":"NeedsLogin","management":{"url":"https://api.netbird.io:443","connected":false,"error":""},"signal":{"url":"","connected":false,"error":""},"relays":{"total":0,"available":0,"details":null},"netbirdIp":"","publicKey":"","usesKernelInterface":false,"wireguardPort":0,"fqdn":"","quantumResistance":false,"quantumResistancePermissive":false,"networks":null,"dnsServers":[],"events":[],"lazyConnectionEnabled":false,"profileName":"default","sshServer":{"enabled":false,"sessions":[]}}`

const connectedJSON = `{"peers":{"total":3,"connected":2,"details":[{"fqdn":"peer.netbird.selfhosted"}]},"daemonVersion":"0.83.2","daemonStatus":"Connected","management":{"url":"http://192.168.2.2:33073","connected":true,"error":""},"signal":{"url":"http://192.168.2.2:10000","connected":true,"error":""},"relays":{"total":2,"available":1},"netbirdIp":"100.92.4.7/16","usesKernelInterface":true,"fqdn":"jetkvm-6.netbird.selfhosted.","networks":["10.20.0.0/24"]}`

func TestParseStatus(t *testing.T) {
	status, err := ParseStatus([]byte(needsLoginJSON))
	require.NoError(t, err)
	assert.Equal(t, StateNeedsLogin, status.DaemonStatus)
	assert.Equal(t, "0.83.2", status.Version)
	assert.Equal(t, DefaultManagementURL, status.ManagementURL)
	assert.False(t, status.ManagementConnected)
	assert.Empty(t, status.IP)
	assert.Nil(t, status.Networks)

	status, err = ParseStatus([]byte(connectedJSON))
	require.NoError(t, err)
	assert.Equal(t, StateConnected, status.DaemonStatus)
	assert.Equal(t, "100.92.4.7", status.IP)
	assert.Equal(t, "jetkvm-6.netbird.selfhosted", status.FQDN)
	assert.True(t, status.KernelInterface)
	assert.True(t, status.SignalConnected)
	assert.Equal(t, 3, status.PeersTotal)
	assert.Equal(t, 2, status.PeersConnected)
	assert.Equal(t, 1, status.RelaysAvailable)
	assert.Equal(t, []string{"10.20.0.0/24"}, status.Networks)

	// Networks in an unexpected shape do not break the status.
	status, err = ParseStatus([]byte(`{"daemonStatus":"Connected","networks":[{"id":"x"}]}`))
	require.NoError(t, err)
	assert.Nil(t, status.Networks)

	_, err = ParseStatus([]byte(`{bad`))
	assert.Error(t, err)
}

func TestUpArgs(t *testing.T) {
	assert.Equal(t, []string{"up", "--daemon-addr", DaemonAddr, "--disable-dns"}, UpOptions{}.args())
	assert.Equal(t, []string{
		"up", "--daemon-addr", DaemonAddr, "--disable-dns",
		"--management-url", "http://10.0.0.1:33073", "--setup-key-file", "/run/k", "--hostname", "jetkvm-6",
	}, UpOptions{ManagementURL: "http://10.0.0.1:33073", SetupKeyFile: "/run/k", Hostname: "jetkvm-6"}.args())
}

func TestFindSignInURL(t *testing.T) {
	assert.Equal(t, "", FindSignInURL("Please do the SSO login in your browser."))
	assert.Equal(t, "https://login.netbird.io/activate?user_code=ABCD-EFGH",
		FindSignInURL("https://login.netbird.io/activate?user_code=ABCD-EFGH"))
	assert.Equal(t, "", FindSignInURL("More info: https://docs.netbird.io/how-to/register-machines-using-setup-keys"))
	assert.Equal(t, "http://192.168.2.2/device?code=1", FindSignInURL("  open http://192.168.2.2/device?code=1 now"))
}

func TestUpInteractiveReportsURL(t *testing.T) {
	saved := StartCommand
	defer func() { StartCommand = saved }()
	StartCommand = func(_ context.Context, args ...string) (io.Reader, func() error, error) {
		assert.Equal(t, "up", args[0])
		out := "Please do the SSO login in your browser. \nuse this URL to log in:\n\nhttps://idp.example/device?user_code=XY\n\nConnected\n"
		return strings.NewReader(out), func() error { return nil }, nil
	}
	var got []string
	require.NoError(t, UpInteractive(context.Background(), UpOptions{}, func(u string) { got = append(got, u) }))
	assert.Equal(t, []string{"https://idp.example/device?user_code=XY"}, got)
}

func TestUpInteractiveError(t *testing.T) {
	saved := StartCommand
	defer func() { StartCommand = saved }()
	StartCommand = func(context.Context, ...string) (io.Reader, func() error, error) {
		return strings.NewReader("login failed: denied\n"), func() error { return errors.New("exit status 1") }, nil
	}
	err := UpInteractive(context.Background(), UpOptions{}, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "login failed: denied")
}

func TestNormalizeManagementURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		" https://nb.example.com/ ":  "https://nb.example.com",
		"http://192.168.2.2:33073":   "http://192.168.2.2:33073",
		"http://10.0.0.5":            "http://10.0.0.5",
		"http://127.0.0.1:8080":      "http://127.0.0.1:8080",
		"http://[fd00::1]:33073":     "http://[fd00::1]:33073",
		"https://api.netbird.io:443": "https://api.netbird.io:443",
	} {
		got, err := NormalizeManagementURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"ftp://x", "https://", "https://u:p@x", "https://x/path", "https://x?q=1",
		"http://nb.example.com", "http://203.0.113.5:33073", "http://100.64.0.1"} {
		_, err := NormalizeManagementURL(bad)
		assert.Error(t, err, bad)
	}
}

func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestInstall(t *testing.T) {
	tarball := makeTarball(t, map[string]string{"LICENSE": "license", "README.md": "readme", "netbird": "#!/bin/sh\necho 0.83.2\n"})
	server := serve(t, tarball)
	dir := t.TempDir()
	var last int64
	installer := &Installer{Dir: dir, Client: server.Client(), Progress: func(received, _ int64) { last = received }}

	require.NoError(t, installer.Install(context.Background(), Release{URL: server.URL, SHA256: sum(tarball), Size: int64(len(tarball))}))
	data, err := os.ReadFile(filepath.Join(dir, "netbird"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho 0.83.2\n", string(data))
	info, err := os.Stat(filepath.Join(dir, "netbird"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	assert.Equal(t, int64(len(tarball)), last)
	entries, _ := os.ReadDir(dir)
	assert.Len(t, entries, 1, "only the binary is left")
}

func TestInstallRejects(t *testing.T) {
	good := makeTarball(t, map[string]string{"netbird": "binary"})
	noBinary := makeTarball(t, map[string]string{"other": "x"})

	cases := map[string]struct {
		body    []byte
		release func(url string) Release
		want    error
	}{
		"bad checksum": {good, func(u string) Release {
			return Release{URL: u, SHA256: strings.Repeat("0", 64), Size: int64(len(good))}
		}, ErrChecksum},
		"too large": {good, func(u string) Release { return Release{URL: u, SHA256: sum(good), Size: 10} }, ErrTooLarge},
		"no binary": {noBinary, func(u string) Release { return Release{URL: u, SHA256: sum(noBinary), Size: int64(len(noBinary))} }, ErrNoBinary},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := serve(t, tc.body)
			dir := t.TempDir()
			installer := &Installer{Dir: dir, Client: server.Client()}
			err := installer.Install(context.Background(), tc.release(server.URL))
			assert.ErrorIs(t, err, tc.want)
			_, statErr := os.Stat(filepath.Join(dir, "netbird"))
			assert.True(t, os.IsNotExist(statErr), "nothing installed")
			entries, _ := os.ReadDir(dir)
			assert.Empty(t, entries, "temporary files removed")
		})
	}
}

func TestInstallNoSpace(t *testing.T) {
	installer := &Installer{Dir: t.TempDir(), FreeSpace: func() (int64, error) { return 10 << 20, nil }}
	assert.ErrorIs(t, installer.Install(context.Background(), PinnedRelease), ErrNoSpace)
}

func TestConfiguredManagementURL(t *testing.T) {
	data := []byte(`{"PrivateKey":"x","ManagementURL":{"Scheme":"http","Opaque":"","User":null,"Host":"192.168.2.2:8780","Path":"","RawPath":"","OmitHost":false}}`)
	if got := ConfiguredManagementURL(data); got != "http://192.168.2.2:8780" {
		t.Fatalf("got %q", got)
	}
	if got := ConfiguredManagementURL([]byte(`{}`)); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := ConfiguredManagementURL([]byte(`not json`)); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSameServer(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"https://api.netbird.io:443", "https://api.netbird.io", true},
		{"https://API.netbird.io", "https://api.netbird.io:443/", true},
		{"http://192.168.2.2:8780", "https://api.netbird.io:443", false},
		{"http://example.com", "https://example.com", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := SameServer(c.a, c.b); got != c.same {
			t.Errorf("SameServer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestNameserverDomains(t *testing.T) {
	status, err := ParseStatus([]byte(`{"daemonStatus":"Connected","dnsServers":[
		{"servers":["10.0.0.53:53"],"domains":["corp.example."],"enabled":true},
		{"servers":["1.1.1.1:53"],"domains":[],"enabled":true},
		{"servers":["10.0.0.54:53"],"domains":["corp.example","lab.example"],"enabled":true}]}`))
	assert.NoError(t, err)
	assert.Equal(t, []string{"corp.example", ".", "lab.example"}, status.NameserverDomains)
	status, err = ParseStatus([]byte(`{"daemonStatus":"Connected","dnsServers":[]}`))
	assert.NoError(t, err)
	assert.Empty(t, status.NameserverDomains)
}

func TestCustomDNSAddress(t *testing.T) {
	data := []byte(`{"PrivateKey":"k","DisableDNS":true,"CustomDNSAddress":"","ManagementURL":{"Scheme":"http","Host":"10.0.0.1:80"}}`)
	assert.Equal(t, "", CustomDNSAddress(data))
	updated, err := WithCustomDNSAddress(data, "127.0.0.1:5054")
	assert.NoError(t, err)
	assert.Equal(t, "127.0.0.1:5054", CustomDNSAddress(updated))
	assert.Equal(t, "http://10.0.0.1:80", ConfiguredManagementURL(updated))
	assert.Contains(t, string(updated), `"PrivateKey": "k"`)
	assert.Contains(t, string(updated), `"DisableDNS": true`)
	_, err = WithCustomDNSAddress([]byte(`{bad`), "x")
	assert.Error(t, err)
}

func TestDNSZone(t *testing.T) {
	assert.Equal(t, "netbird.selfhosted", DNSZone("kvm6-usb.netbird.selfhosted"))
	assert.Equal(t, "netbird.cloud", DNSZone("pc.netbird.cloud."))
	assert.Equal(t, "", DNSZone("pc"))
}

func TestUpResolverAddress(t *testing.T) {
	args := UpOptions{ResolverAddress: "127.0.0.1:5053"}.args()
	assert.Equal(t, []string{"up", "--daemon-addr", DaemonAddr, "--disable-dns", "--dns-resolver-address", "127.0.0.1:5053"}, args)
}
