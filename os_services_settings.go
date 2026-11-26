package kvm

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jetkvm/kvm/internal/osservices"
)

// OSServicesConfig is the OS Services part of the KVM settings, in the same
// JSON shape as the JetKVM Mini's, which the browser reads and writes with
// getOsServicesSettings and setOsServicesSettings.
type OSServicesConfig struct {
	// Enabled: the KVM has the OS Services USB interface, so a sender on
	// the target computer can connect. While it is false, the USB device is
	// the device without OS Services.
	Enabled          bool   `json:"enabled"`
	TerminalEnabled  bool   `json:"terminal_enabled"`
	FilesEnabled     bool   `json:"files_enabled"`
	CaptureDisplay   string `json:"capture_display"`
	VideoEnabled     bool   `json:"video_enabled"`
	Resolution       string `json:"resolution"`
	DisplayMode      string `json:"display_mode"`
	Adaptation       string `json:"adaptation,omitempty"`
	ClipboardEnabled bool   `json:"clipboard_enabled"`

	// TerminalAPIEnabled lets programs open shells on the target computer
	// over /os-services/terminal/ws (os_services_terminal_ws.go). Off by
	// default.
	TerminalAPIEnabled bool `json:"terminal_api_enabled"`
	// TerminalShell is the syntax of the browser terminal's shell: "auto"
	// (or empty) works it out (chooseTerminalShell), or one of the kinds in
	// os_services_terminal_shell.go. TerminalShellCommand is typed first into
	// each new browser terminal, to start another shell (e.g. "pwsh").
	TerminalShell        string `json:"terminal_shell,omitempty"`
	TerminalShellCommand string `json:"terminal_shell_command,omitempty"`
	// A terminal multiplexer for the browser terminal: while
	// TerminalMuxEnabled is set, each new browser terminal gets
	// "restore %OR% start" typed once the shell is up
	// (terminalStartupLines): the restore command reattaches to the
	// multiplexer's session, and when that fails the start command creates
	// it. So ending or losing the browser terminal leaves the programs
	// running and the next one brings them back. Both may hold the macros of
	// expandTerminalMacros. Terminal API shells (MCP) never get it.
	TerminalMuxEnabled bool   `json:"terminal_mux_enabled"`
	TerminalMuxStart   string `json:"terminal_mux_start,omitempty"`
	TerminalMuxRestore string `json:"terminal_mux_restore,omitempty"`
	// HostInfoEnabled lets the program on the target computer report its
	// system information. Off until switched on.
	HostInfoEnabled bool `json:"host_info_enabled"`
	// LastSenderVersion and LastSenderAt (RFC 3339) are the sender that had
	// the last approved session. The KVM writes them; a settings request
	// cannot. They tell a KVM whose sender is away from a KVM that never
	// had one.
	LastSenderVersion string `json:"last_sender_version,omitempty"`
	LastSenderAt      string `json:"last_sender_at,omitempty"`
	// RepositoryURL is where the KVM and the browser fetch OS Services
	// releases: the offline installer disc, the update index and packages.
	// Empty is JetKVM's (osServicesBucketURL). See os_services_repository.go.
	RepositoryURL string `json:"repository_url,omitempty"`
}

type osServicesResolution struct {
	name          string
	width, height uint32
}

var osServicesResolutions = []osServicesResolution{
	{"1280x720", 1280, 720},
	{"1920x1080", 1920, 1080},
	{"2560x1440", 2560, 1440},
	{"3840x2160", 3840, 2160},
}

const (
	osServicesDefaultResolution = "2560x1440"
	// osServicesAdaptationMax bounds the adaptation text.
	osServicesAdaptationMax = 39
	// osServicesTerminalCommandMax bounds each typed command.
	osServicesTerminalCommandMax = 256
)

// validTerminalCommand: one line of printable text, typed into a shell.
func validTerminalCommand(text string) bool {
	if len(text) > osServicesTerminalCommandMax {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func defaultOSServicesConfig() *OSServicesConfig {
	return &OSServicesConfig{
		TerminalEnabled:  true,
		FilesEnabled:     true,
		VideoEnabled:     true,
		ClipboardEnabled: true,
		Resolution:       osServicesDefaultResolution,
		DisplayMode:      "extend",
	}
}

// parseOSServicesAdaptation reads "", "off", "auto" (no fixed rate) or
// "fixed:<kbps>:<fps>".
func parseOSServicesAdaptation(text string) (kbps, fps uint32, ok bool) {
	if text == "" || text == "off" || text == "auto" {
		return 0, 0, true
	}
	rest, found := strings.CutPrefix(text, "fixed:")
	if !found {
		return 0, 0, false
	}
	k, f, found := strings.Cut(rest, ":")
	if !found {
		return 0, 0, false
	}
	parse := func(s string) (uint32, bool) {
		if s == "" || len(s) > 10 || strings.TrimLeft(s, "0123456789") != "" {
			return 0, false
		}
		v, err := strconv.ParseUint(s, 10, 32)
		return uint32(v), err == nil
	}
	kv, ok1 := parse(k)
	fv, ok2 := parse(f)
	return kv, fv, ok1 && ok2
}

func validCaptureDisplay(text string) bool {
	return len(text) <= osservices.CaptureDisplayMax && !strings.ContainsAny(text, "\x00\r\n")
}

// mergeOSServicesConfig applies a setOsServicesSettings request: a member
// that is left out or null keeps its value, and any invalid member rejects
// the whole request.
func mergeOSServicesConfig(base OSServicesConfig, request map[string]any) (OSServicesConfig, error) {
	merged := base
	field := func(name string) (any, bool) {
		value, ok := request[name]
		return value, ok && value != nil
	}
	bools := []struct {
		name  string
		value *bool
	}{
		{"enabled", &merged.Enabled},
		{"terminal_enabled", &merged.TerminalEnabled},
		{"terminal_api_enabled", &merged.TerminalAPIEnabled},
		{"terminal_mux_enabled", &merged.TerminalMuxEnabled},
		{"files_enabled", &merged.FilesEnabled},
		{"video_enabled", &merged.VideoEnabled},
		{"clipboard_enabled", &merged.ClipboardEnabled},
		{"host_info_enabled", &merged.HostInfoEnabled},
	}
	for _, b := range bools {
		if value, ok := field(b.name); ok {
			v, isBool := value.(bool)
			if !isBool {
				return base, errors.New(b.name + " must be a boolean")
			}
			*b.value = v
		}
	}
	if value, ok := field("capture_display"); ok {
		v, isString := value.(string)
		if !isString || !validCaptureDisplay(v) {
			return base, errors.New("invalid capture display name")
		}
		merged.CaptureDisplay = v
	}
	commands := []struct {
		name  string
		value *string
	}{
		{"terminal_mux_start", &merged.TerminalMuxStart},
		{"terminal_mux_restore", &merged.TerminalMuxRestore},
		{"terminal_shell_command", &merged.TerminalShellCommand},
	}
	for _, c := range commands {
		if value, ok := field(c.name); ok {
			v, isString := value.(string)
			if !isString {
				return base, errors.New(c.name + " must be a string")
			}
			v = strings.TrimSpace(v)
			if !validTerminalCommand(v) {
				return base, errors.New("a terminal command must be one line of at most 256 characters")
			}
			*c.value = v
		}
	}
	if value, ok := field("terminal_shell"); ok {
		v, isString := value.(string)
		if !isString || !validTerminalShell(v) {
			return base, errors.New("unsupported terminal shell")
		}
		merged.TerminalShell = v
	}
	if value, ok := field("resolution"); ok {
		v, _ := value.(string)
		if _, found := osServicesResolutionSize(v); !found {
			return base, errors.New("unsupported OS Services resolution")
		}
		merged.Resolution = v
	}
	if value, ok := field("display_mode"); ok {
		v, _ := value.(string)
		if v != "mirror" && v != "extend" {
			return base, errors.New("unsupported OS Services display mode")
		}
		merged.DisplayMode = v
	}
	if value, ok := request["repository_url"]; ok {
		v, isString := value.(string)
		if value != nil && !isString {
			return base, errors.New("repository_url must be a string")
		}
		// Only a changed URL is checked: the page sends the saved one back with
		// every other setting, and it may predate the current rule.
		if v != base.RepositoryURL {
			repository, err := normalizeOSServicesRepository(v)
			if err != nil {
				return base, err
			}
			merged.RepositoryURL = repository
		}
	}
	if value, ok := field("adaptation"); ok {
		v, isString := value.(string)
		if !isString || len(v) > osServicesAdaptationMax {
			return base, errors.New("invalid adaptation value")
		}
		if _, _, valid := parseOSServicesAdaptation(v); !valid {
			return base, errors.New("invalid adaptation value")
		}
		merged.Adaptation = v
	}
	return merged, nil
}

func osServicesResolutionSize(name string) (osServicesResolution, bool) {
	for _, r := range osServicesResolutions {
		if r.name == name {
			return r, true
		}
	}
	return osServicesResolution{}, false
}

// sessionSettings maps the KVM settings onto what the session acts on.
func (c OSServicesConfig) sessionSettings(codec osservices.Codec) osservices.Settings {
	r, ok := osServicesResolutionSize(c.Resolution)
	if !ok {
		r, _ = osServicesResolutionSize(osServicesDefaultResolution)
	}
	s := osservices.Settings{
		ClipboardEnabled: c.ClipboardEnabled,
		TerminalEnabled:  c.TerminalEnabled,
		FilesEnabled:     c.FilesEnabled,
		VideoEnabled:     c.VideoEnabled,
		HostInfoEnabled:  c.HostInfoEnabled,
		CaptureDisplay:   c.CaptureDisplay,
		Width:            r.width,
		Height:           r.height,
		Layout:           osservices.LayoutExtend,
		Codec:            codec,
	}
	if c.DisplayMode == "mirror" {
		s.Layout = osservices.LayoutMirror
	}
	if kbps, fps, ok := parseOSServicesAdaptation(c.Adaptation); ok {
		s.TargetKbps = kbps
		s.MaximumFPS = fps
	}
	return s
}
