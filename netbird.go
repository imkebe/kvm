package kvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gwatts/rootcerts"
	"github.com/jetkvm/kvm/internal/imagedownload"
	"github.com/jetkvm/kvm/internal/netbird"
)

// NetBird thin client: the app installs the official client into
// /userdata/netbird, runs `netbird service run` as a supervised child, and
// drives it through the CLI. Nothing of it is in the system image.

const (
	netbirdDir           = "/userdata/netbird"
	netbirdConfigFile    = netbirdDir + "/default.json"
	netbirdCABundle      = netbirdDir + "/ca-certificates.crt"
	netbirdStateDir      = netbirdDir + "/state"
	netbirdVarLib        = "/var/lib/netbird"
	netbirdSetupKeyFile  = "/run/jetkvm-netbird-setup-key"
	netbirdSocket        = "/run/netbird.sock"
	netbirdSignInTimeout = 10 * time.Minute
	netbirdLinkTimeout   = 60 * time.Second

	netbirdPollRunning = 15 * time.Second
	netbirdPollFast    = 2 * time.Second
	netbirdFastWindow  = 30 * time.Second
	netbirdBackoffMin  = 2 * time.Second
	netbirdBackoffMax  = 60 * time.Second
)

// netbirdModules are loaded from /oem/usr/ko before the daemon starts:
// TUN and kernel WireGuard, and the nftables parts NetBird's firewall uses.
var netbirdModules = append([]string{"tun", "wireguard", "nft_nat", "nft_reject_inet", "nft_fib_ipv4", "nft_ct"}, nftModules...)

// NetbirdConfig is the saved NetBird setting. The setup key is never saved.
type NetbirdConfig struct {
	// Enabled: the daemon runs and the peer stays connected across reboots.
	Enabled bool `json:"enabled"`
	// ManagementURL is "" for NetBird Cloud.
	ManagementURL string `json:"management_url,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	// Target is the USB Ethernet target's own peer (netbird_target.go).
	Target *NetbirdTargetConfig `json:"target,omitempty"`
}

type NetbirdInstallState struct {
	Active   bool   `json:"active"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Error    string `json:"error,omitempty"`
}

// NetbirdState is what the UI shows (getNetbirdState, netbirdState event).
type NetbirdState struct {
	Installed     bool                `json:"installed"`
	Version       string              `json:"version,omitempty"`
	PinnedVersion string              `json:"pinnedVersion"`
	Install       NetbirdInstallState `json:"install"`
	Enabled       bool                `json:"enabled"`
	DaemonRunning bool                `json:"daemonRunning"`
	ManagementURL string              `json:"managementURL"`
	Joining       bool                `json:"joining"`
	AuthURL       string              `json:"authURL,omitempty"`
	Status        *netbird.Status     `json:"status,omitempty"`
	Error         string              `json:"error,omitempty"`
	UsbShare      bool                `json:"usbShare"`
	UsbNetworkOn  bool                `json:"usbNetworkOn"`
	Target        NetbirdTargetState  `json:"target"`
}

type netbirdDaemon struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// netbirdManager runs one NetBird daemon and polls it. There are two: the
// KVM's own peer (netbirdMgr) and, in the USB Ethernet NetBird mode, the
// target computer's peer (netbirdTargetMgr, netbird_target.go).
type netbirdManager struct {
	// Instance settings.
	name       string
	daemonAddr string
	socket     string
	configFile string
	env        []string     // added to the daemon's environment
	prepare    func() error // before each daemon start
	// resolverAddr is where the daemon's local DNS resolver listens, off
	// port 53 of its NetBird address, which usb0 also carries in the USB
	// Ethernet NetBird mode (dnsmasq answers DNS there).
	resolverAddr string
	// resolverChecked: the config file was checked since the daemon started.
	resolverChecked bool
	onChange        func() // after each poll; nil: publish
	mu              sync.Mutex
	wake            chan struct{}
	daemon          *netbirdDaemon
	wantRunning     bool
	nextStart       time.Time
	backoff         time.Duration

	version   string
	status    *netbird.Status
	lastError string
	lastLines []string

	joining    bool
	joinID     int
	joinCancel context.CancelFunc
	authURL    string

	install NetbirdInstallState

	fastUntil  time.Time
	lastPushed string
	lastUplink string
}

var netbirdMgr = &netbirdManager{
	name:         "kvm",
	daemonAddr:   netbird.DaemonAddr,
	socket:       netbirdSocket,
	configFile:   netbirdConfigFile,
	prepare:      prepareNetbirdStateDir,
	resolverAddr: netbirdResolverAddr,
	wake:         make(chan struct{}, 1),
}

// The daemons' local DNS resolvers. The target's is where dnsmasq on usb0
// sends the target's NetBird names.
const (
	netbirdResolverAddr       = "127.0.0.1:5053"
	netbirdTargetResolverAddr = "127.0.0.1:5054"
)

// ensureResolverConfig puts resolverAddr into the config file; it reports
// whether the file changed (a running daemon then needs a restart). A missing
// file is left alone: the daemon writes it, and `up` is given the address.
func (m *netbirdManager) ensureResolverConfig() (bool, error) {
	if m.resolverAddr == "" {
		return false, nil
	}
	data, err := os.ReadFile(m.configFile)
	if os.IsNotExist(err) || len(data) == 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if netbird.CustomDNSAddress(data) == m.resolverAddr {
		return false, nil
	}
	updated, err := netbird.WithCustomDNSAddress(data, m.resolverAddr)
	if err != nil {
		return false, err
	}
	tmp := m.configFile + ".tmp"
	if err := os.WriteFile(tmp, updated, 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, m.configFile); err != nil {
		return false, err
	}
	netbirdLogger.Info().Str("peer", m.name).Str("address", m.resolverAddr).Msg("moved the NetBird DNS resolver")
	return true, nil
}

func netbirdSettings() NetbirdConfig {
	if config == nil || config.Netbird == nil {
		return NetbirdConfig{}
	}
	return *config.Netbird
}

func netbirdInstalled() bool {
	info, err := os.Stat(netbird.Binary)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func initNetbird() {
	netbird.CAFile = netbirdCABundle
	m := netbirdMgr
	installed := netbirdInstalled()
	m.mu.Lock()
	// A registered peer reconnects on its own when the daemon starts
	// (NetBird's auto-connect), so starting the daemon is all boot needs.
	if installed && netbirdSettings().Enabled {
		m.wantRunning = true
	}
	m.mu.Unlock()
	go func() {
		// Off the boot path: running the 40 MB client takes a moment.
		if installed {
			if version, err := netbird.Version(context.Background()); err == nil {
				m.mu.Lock()
				m.version = version
				m.mu.Unlock()
			}
		}
		m.loop()
	}()
	initNetbirdTarget()
}

func (m *netbirdManager) nudge() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *netbirdManager) loop() {
	for {
		m.tick()
		wait := m.interval()
		if wait == 0 {
			<-m.wake
			continue
		}
		select {
		case <-m.wake:
		case <-time.After(wait):
		}
	}
}

func (m *netbirdManager) interval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.daemon != nil && time.Now().Before(m.fastUntil):
		return netbirdPollFast
	case m.daemon != nil:
		return netbirdPollRunning
	case m.wantRunning:
		if wait := time.Until(m.nextStart); wait > 0 {
			return wait
		}
		return netbirdPollFast
	default:
		return 0 // no daemon wanted: sleep until a nudge
	}
}

func (m *netbirdManager) tick() {
	m.mu.Lock()
	if m.wantRunning && m.daemon == nil && !time.Now().Before(m.nextStart) {
		if err := m.startDaemonLocked(); err != nil {
			m.lastError = err.Error()
			m.scheduleRestartLocked()
			netbirdLogger.Warn().Err(err).Str("peer", m.name).Msg("failed to start the NetBird daemon")
		}
	}
	if !m.wantRunning && m.daemon != nil {
		m.stopDaemonLocked()
	}
	running := m.daemon != nil
	m.mu.Unlock()

	var status *netbird.Status
	var statusErr error
	if running {
		status, statusErr = netbird.GetStatusAt(context.Background(), m.daemonAddr)
	}

	m.mu.Lock()
	if running && m.daemon != nil {
		if statusErr == nil {
			m.status = status
			m.backoff = 0
		}
		// A peer joined after the daemon started has a config file only now;
		// the daemon reads the resolver address at start.
		if statusErr == nil && status.DaemonStatus == netbird.StateConnected && !m.resolverChecked {
			changed, err := m.ensureResolverConfig()
			if err != nil {
				netbirdLogger.Warn().Err(err).Str("peer", m.name).Msg("failed to set the NetBird DNS resolver address")
			}
			m.resolverChecked = err == nil
			if changed {
				m.stopDaemonLocked()
				m.status = nil
				m.nextStart = time.Time{}
				m.nudge()
			}
		}
	} else {
		m.status = nil
	}
	m.mu.Unlock()

	if m.onChange != nil {
		m.onChange()
	} else {
		m.publish()
	}
}

// daemonPID is 0 while no daemon runs.
func (m *netbirdManager) daemonPID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.daemon == nil {
		return 0
	}
	return m.daemon.cmd.Process.Pid
}

func (m *netbirdManager) pollFast() {
	m.mu.Lock()
	m.fastUntil = time.Now().Add(netbirdFastWindow)
	m.mu.Unlock()
	m.nudge()
}

func (m *netbirdManager) scheduleRestartLocked() {
	if m.backoff == 0 {
		m.backoff = netbirdBackoffMin
	} else {
		m.backoff = min(m.backoff*2, netbirdBackoffMax)
	}
	m.nextStart = time.Now().Add(m.backoff)
}

// prepareNetbirdStateDir points /var/lib/netbird, where the daemon keeps its
// profile list, at /userdata, so nothing is written to the system partition
// and it survives a system update (which recreates /var/lib).
func prepareNetbirdStateDir() error {
	if err := os.MkdirAll(netbirdStateDir, 0o700); err != nil {
		return err
	}
	if target, err := os.Readlink(netbirdVarLib); err == nil && target == netbirdStateDir {
		return nil
	}
	if err := os.RemoveAll(netbirdVarLib); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(netbirdVarLib), 0o755); err != nil {
		return err
	}
	return os.Symlink(netbirdStateDir, netbirdVarLib)
}

// killStrayNetbirdDaemons stops `netbird service run` processes on daemonAddr
// that the app does not own: left by a previous app process, or started by
// hand. A daemon without --daemon-addr counts as the default one.
func killStrayNetbirdDaemons(daemonAddr string) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		var pid int
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil || pid == self {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if len(args) >= 3 && filepath.Base(args[0]) == "netbird" && args[1] == "service" && args[2] == "run" &&
			netbirdDaemonAddrOf(args) == daemonAddr {
			pids = append(pids, pid)
		}
	}
	for _, pid := range pids {
		netbirdLogger.Info().Int("pid", pid).Msg("stopping a NetBird daemon the app did not start")
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range pids {
		for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// netbirdDaemonAddrOf reads --daemon-addr from a daemon's command line.
func netbirdDaemonAddrOf(args []string) string {
	for i, arg := range args {
		if arg == "--daemon-addr" && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--daemon-addr="); ok {
			return value
		}
	}
	return netbird.DaemonAddr
}

// writeNetbirdCABundle writes the app's built-in root certificates (Mozilla's
// list, the same the app trusts) as a PEM file for the NetBird binary. The KVM
// has no system CA bundle, so without it NetBird cannot verify an HTTPS
// management server: NetBird Cloud failed with "create connection: context
// deadline exceeded". The file is rewritten only when it differs.
func writeNetbirdCABundle() error {
	var bundle bytes.Buffer
	for _, cert := range rootcerts.CertsByTrust(rootcerts.ServerTrustedDelegator) {
		if err := pem.Encode(&bundle, &pem.Block{Type: "CERTIFICATE", Bytes: cert.DER}); err != nil {
			return err
		}
	}
	if current, err := os.ReadFile(netbirdCABundle); err == nil && bytes.Equal(current, bundle.Bytes()) {
		return nil
	}
	tmp := netbirdCABundle + ".new"
	if err := os.WriteFile(tmp, bundle.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, netbirdCABundle)
}

func (m *netbirdManager) startDaemonLocked() error {
	if !netbirdInstalled() {
		m.wantRunning = false
		return errors.New("NetBird is not installed")
	}
	if err := loadOEMModules(netbirdModules...); err != nil {
		return fmt.Errorf("kernel modules: %w", err)
	}
	if m.prepare != nil {
		if err := m.prepare(); err != nil {
			return fmt.Errorf("state directory: %w", err)
		}
	}
	if _, err := m.ensureResolverConfig(); err != nil {
		netbirdLogger.Warn().Err(err).Str("peer", m.name).Msg("failed to set the NetBird DNS resolver address")
	}
	m.resolverChecked = false
	if err := writeNetbirdCABundle(); err != nil {
		return fmt.Errorf("CA certificates: %w", err)
	}
	killStrayNetbirdDaemons(m.daemonAddr)
	_ = os.Remove(m.socket)

	cmd := exec.Command(netbird.Binary, "service", "run",
		"--config", m.configFile,
		"--daemon-addr", m.daemonAddr,
		"--log-file", "console",
		"--log-level", "info")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(append(os.Environ(), "SSL_CERT_FILE="+netbirdCABundle), m.env...)
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		return err
	}
	daemon := &netbirdDaemon{cmd: cmd, done: make(chan struct{})}
	m.daemon = daemon
	m.lastLines = nil
	m.lastError = ""
	m.fastUntil = time.Now().Add(netbirdFastWindow)
	netbirdLogger.Info().Int("pid", cmd.Process.Pid).Str("peer", m.name).Msg("NetBird daemon started")

	go m.readDaemonOutput(reader)
	go func() {
		err := cmd.Wait()
		_ = writer.Close()
		daemon.err = err
		close(daemon.done)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.daemon != daemon {
			return // stopped on purpose
		}
		m.daemon = nil
		m.status = nil
		m.lastError = "the NetBird daemon stopped"
		if err != nil {
			m.lastError += ": " + err.Error()
		}
		if len(m.lastLines) > 0 {
			m.lastError += " (" + m.lastLines[len(m.lastLines)-1] + ")"
		}
		netbirdLogger.Warn().Err(err).Str("peer", m.name).Msg("NetBird daemon exited")
		m.scheduleRestartLocked()
		m.nudge()
	}()
	return nil
}

func (m *netbirdManager) readDaemonOutput(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, " WARN ") || strings.Contains(line, " ERRO") {
			netbirdLogger.Info().Str("peer", m.name).Msg(line)
		} else {
			netbirdLogger.Debug().Str("peer", m.name).Msg(line)
		}
		m.mu.Lock()
		m.lastLines = append(m.lastLines, line)
		if len(m.lastLines) > 20 {
			m.lastLines = m.lastLines[len(m.lastLines)-20:]
		}
		m.mu.Unlock()
	}
	_, _ = io.Copy(io.Discard, reader)
}

func (m *netbirdManager) stopDaemonLocked() {
	daemon := m.daemon
	if daemon == nil {
		return
	}
	m.daemon = nil
	m.status = nil
	pid := daemon.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	m.mu.Unlock()
	select {
	case <-daemon.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-daemon.done
	}
	m.mu.Lock()
	netbirdLogger.Info().Int("pid", pid).Str("peer", m.name).Msg("NetBird daemon stopped")
}

// waitForDaemon starts the daemon if needed and waits until it answers.
func (m *netbirdManager) waitForDaemon(ctx context.Context) error {
	m.mu.Lock()
	m.wantRunning = true
	m.nextStart = time.Time{}
	m.mu.Unlock()
	m.nudge()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		m.mu.Lock()
		running := m.daemon != nil
		startErr := m.lastError
		m.mu.Unlock()
		if running {
			if _, err := netbird.GetStatusAt(ctx, m.daemonAddr); err == nil {
				return nil
			} else {
				lastErr = err
			}
		} else if startErr != "" {
			lastErr = errors.New(startErr)
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("the NetBird daemon did not start: %w", lastErr)
			}
			return errors.New("the NetBird daemon did not start")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (m *netbirdManager) State() NetbirdState {
	settings := netbirdSettings()
	var usbCfg USBNetworkConfig
	var usbState USBNetworkState
	if usbNetwork != nil {
		usbCfg = usbNetwork.Config()
		usbState = usbNetwork.State()
	}
	target := netbirdTargetMgr.targetState()

	m.mu.Lock()
	defer m.mu.Unlock()
	state := NetbirdState{
		Installed:     netbirdInstalled(),
		Version:       m.version,
		PinnedVersion: netbird.PinnedRelease.Version,
		Install:       m.install,
		Enabled:       settings.Enabled,
		DaemonRunning: m.daemon != nil,
		ManagementURL: settings.ManagementURL,
		Joining:       m.joining,
		AuthURL:       m.authURL,
		Status:        m.status,
		Error:         m.lastError,
		UsbShare:      usbCfg.Mode == usbUplinkNetbird,
		UsbNetworkOn:  usbState.Running,
		Target:        target,
	}
	if state.ManagementURL == "" {
		state.ManagementURL = netbird.DefaultManagementURL
	}
	if m.status != nil && m.status.Version != "" {
		state.Version = m.status.Version
	}
	return state
}

// publish sends the state to the browser when it changed. When the KVM's
// NetBird address changes, the target peer's link is checked again (the
// address is the target's gateway).
func (m *netbirdManager) publish() {
	m.mu.Lock()
	ip := ""
	if m.status != nil {
		ip = m.status.IPNet
	}
	ipChanged := ip != m.lastUplink
	m.lastUplink = ip
	m.mu.Unlock()
	if ipChanged {
		netbirdTargetMgr.nudge()
	}

	state := netbirdMgr.State()
	data, _ := json.Marshal(state)
	m.mu.Lock()
	changed := string(data) != m.lastPushed
	m.lastPushed = string(data)
	m.mu.Unlock()
	if changed {
		if session := currentSession; session != nil {
			go writeJSONRPCEvent("netbirdState", state, session)
		}
	}
}

func saveNetbirdSettings(update func(*NetbirdConfig)) error {
	ensureConfigLoaded()
	previous := config.Netbird
	next := netbirdSettings()
	update(&next)
	config.Netbird = &next
	if err := SaveConfig(); err != nil {
		config.Netbird = previous
		return fmt.Errorf("failed to save the NetBird setting: %w", err)
	}
	return nil
}

func rpcGetNetbirdState() NetbirdState {
	return netbirdMgr.State()
}

func netbirdHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = config.NetworkConfig.GetTransportProxyFunc()
	return imagedownload.NewClient(transport)
}

func freeSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func rpcInstallNetbird() (NetbirdState, error) {
	m := netbirdMgr
	m.mu.Lock()
	if m.install.Active {
		m.mu.Unlock()
		return m.State(), errors.New("NetBird is being installed")
	}
	m.install = NetbirdInstallState{Active: true, Total: netbird.PinnedRelease.Size}
	m.mu.Unlock()

	go func() {
		var lastPush time.Time
		installer := &netbird.Installer{
			Dir:       netbirdDir,
			Client:    netbirdHTTPClient(),
			FreeSpace: func() (int64, error) { return freeSpace("/userdata") },
			Progress: func(received, total int64) {
				m.mu.Lock()
				m.install.Received, m.install.Total = received, total
				m.mu.Unlock()
				if time.Since(lastPush) > 500*time.Millisecond {
					lastPush = time.Now()
					m.publish()
				}
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := installer.Install(ctx, netbird.PinnedRelease)
		version := ""
		if err == nil {
			version, err = netbird.Version(ctx)
		}
		m.mu.Lock()
		m.install.Active = false
		if err != nil {
			m.install.Error = err.Error()
			netbirdLogger.Warn().Err(err).Msg("NetBird install failed")
		} else {
			m.install.Error = ""
			m.version = version
			netbirdLogger.Info().Str("version", version).Msg("NetBird installed")
			// A running daemon keeps the old program; restart it.
			if m.daemon != nil {
				m.stopDaemonLocked()
			}
		}
		m.mu.Unlock()
		m.nudge()
		m.publish()
	}()
	return m.State(), nil
}

func rpcNetbirdJoin(managementURL string, setupKey string) (NetbirdState, error) {
	m := netbirdMgr
	if !netbirdInstalled() {
		return m.State(), errors.New("NetBird is not installed")
	}
	normalized, err := netbird.NormalizeManagementURL(managementURL)
	if err != nil {
		return m.State(), err
	}
	if err := saveNetbirdSettings(func(c *NetbirdConfig) {
		c.Enabled = true
		c.ManagementURL = normalized
	}); err != nil {
		return m.State(), err
	}
	if err := m.switchServerIfNeeded(normalized); err != nil {
		return m.State(), err
	}
	if err := m.waitForDaemon(context.Background()); err != nil {
		return m.State(), err
	}

	opts := netbird.UpOptions{ManagementURL: normalized, Hostname: netbirdSettings().Hostname, ResolverAddress: netbirdResolverAddr}
	if opts.Hostname == "" && networkManager != nil {
		opts.Hostname = networkManager.Hostname()
	}

	m.cancelJoin()
	m.mu.Lock()
	m.lastError = ""
	m.mu.Unlock()

	setupKey = strings.TrimSpace(setupKey)
	if setupKey != "" {
		if err := m.joinWithKey(netbirdSetupKeyFile, setupKey, opts); err != nil {
			return m.State(), err
		}
		return m.State(), nil
	}

	// No key: browser sign-in. The link arrives in a netbirdState event.
	ctx, cancel := context.WithTimeout(context.Background(), netbirdSignInTimeout)
	m.mu.Lock()
	m.joinID++
	id := m.joinID
	m.joining = true
	m.joinCancel = cancel
	m.mu.Unlock()
	go func() {
		defer cancel()
		// `netbird up` retries an unreachable server without end; give up
		// when no sign-in link has come within netbirdLinkTimeout.
		var noLink atomic.Bool
		linkTimer := time.AfterFunc(netbirdLinkTimeout, func() {
			noLink.Store(true)
			cancel()
		})
		err := netbird.UpInteractive(ctx, opts, func(link string) {
			linkTimer.Stop()
			m.mu.Lock()
			if m.joinID == id {
				m.authURL = link
			}
			m.mu.Unlock()
			m.publish()
		})
		linkTimer.Stop()
		if noLink.Load() {
			err = fmt.Errorf("no sign-in link from %s within %s; check the management URL", netbirdManagementLabel(normalized), netbirdLinkTimeout)
		}
		m.mu.Lock()
		// A newer join or a leave owns the state now.
		if m.joinID == id && m.joining {
			m.joining = false
			m.authURL = ""
			m.joinCancel = nil
			if err != nil {
				m.lastError = err.Error()
			}
		}
		m.mu.Unlock()
		m.pollFast()
	}()
	m.pollFast()
	return m.State(), nil
}

// joinWithKey runs `netbird up` with the setup key in a 0600 file that is
// removed right after, so the key is never saved.
func (m *netbirdManager) joinWithKey(keyFile, setupKey string, opts netbird.UpOptions) error {
	if err := os.WriteFile(keyFile, []byte(setupKey), 0o600); err != nil {
		return err
	}
	opts.SetupKeyFile = keyFile
	err := netbird.Up(context.Background(), opts)
	_ = os.Remove(keyFile)
	m.pollFast()
	if err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
	}
	return err
}

// switchServerIfNeeded drops the client's identity when it is registered
// with another management server than the one asked for. The client keeps a
// registered peer on its saved server: `up --management-url` does not move it,
// and a join with NetBird Cloud chosen quietly reconnected to the old
// self-hosted server. A new identity on the new server is what the owner asked
// for; the old peer stays in the old server's dashboard until removed there.
func (m *netbirdManager) switchServerIfNeeded(managementURL string) error {
	data, err := os.ReadFile(m.configFile)
	if err != nil {
		return nil // no identity yet
	}
	current := netbird.ConfiguredManagementURL(data)
	if current == "" || netbird.SameServer(current, managementURL) {
		return nil
	}
	netbirdLogger.Info().Str("peer", m.name).Str("from", current).Str("to", managementURL).
		Msg("joining another management server; starting with a new NetBird identity")
	m.cancelJoin()
	m.mu.Lock()
	m.wantRunning = false
	m.stopDaemonLocked()
	m.mu.Unlock()
	if err := os.Remove(m.configFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to reset the NetBird identity: %w", err)
	}
	return nil
}

func netbirdManagementLabel(url string) string {
	if url == "" {
		return "NetBird Cloud"
	}
	return url
}

func (m *netbirdManager) cancelJoin() {
	m.mu.Lock()
	if m.joinCancel != nil {
		m.joinCancel()
		m.joinCancel = nil
	}
	m.joinID++
	m.joining = false
	m.authURL = ""
	m.mu.Unlock()
}

func rpcNetbirdLeave() (NetbirdState, error) {
	m := netbirdMgr
	m.cancelJoin()
	m.mu.Lock()
	running := m.daemon != nil
	m.mu.Unlock()
	if running {
		if err := netbird.Down(context.Background()); err != nil {
			netbirdLogger.Warn().Err(err).Msg("netbird down failed")
		}
	}
	if err := saveNetbirdSettings(func(c *NetbirdConfig) { c.Enabled = false }); err != nil {
		return m.State(), err
	}
	m.mu.Lock()
	m.wantRunning = false
	m.lastError = ""
	m.stopDaemonLocked()
	m.mu.Unlock()
	m.publish()
	return m.State(), nil
}

func rpcRemoveNetbird() (NetbirdState, error) {
	m := netbirdMgr
	m.mu.Lock()
	installing := m.install.Active
	m.mu.Unlock()
	if installing {
		return m.State(), errors.New("NetBird is being installed")
	}
	if _, err := rpcNetbirdLeave(); err != nil {
		return m.State(), err
	}
	stopNetbirdTarget()
	if err := os.RemoveAll(netbirdDir); err != nil {
		return m.State(), err
	}
	if target, err := os.Readlink(netbirdVarLib); err == nil && target == netbirdStateDir {
		_ = os.Remove(netbirdVarLib)
	}
	if err := saveNetbirdSettings(func(c *NetbirdConfig) { *c = NetbirdConfig{} }); err != nil {
		return m.State(), err
	}
	m.mu.Lock()
	m.version = ""
	m.install = NetbirdInstallState{}
	m.mu.Unlock()
	m.publish()
	return m.State(), nil
}
