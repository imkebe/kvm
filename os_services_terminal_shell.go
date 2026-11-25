package kvm

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/jetkvm/kvm/internal/osservices"
)

// The shell of the browser terminal on the target decides how the KVM
// writes the commands it types there (the custom shell command and the
// terminal multiplexer), through the macros below.
//
// The OS Services program starts the user's login shell ($SHELL -l) on
// macOS, Linux and BSD, and Windows PowerShell 5.1 (powershell.exe) on
// Windows, so the target's OS is enough to tell its default shell's syntax
// apart. A custom shell command names the shell it starts.

// Terminal shell kinds, as OSServicesConfig.TerminalShell stores them.
const (
	terminalShellAuto       = "auto"
	terminalShellPosix      = "posix"      // sh, bash, zsh, dash, ksh
	terminalShellFish       = "fish"       // fish
	terminalShellPwsh       = "pwsh"       // PowerShell 7 and later
	terminalShellPowerShell = "powershell" // Windows PowerShell 5.1
	terminalShellCmd        = "cmd"        // Command Prompt
)

func validTerminalShell(kind string) bool {
	switch kind {
	case "", terminalShellAuto, terminalShellPosix, terminalShellFish, terminalShellPwsh,
		terminalShellPowerShell, terminalShellCmd:
		return true
	}
	return false
}

// terminalShellOfCommand names the shell a command starts, from the base
// name of its first word; "" when it is not a known shell.
func terminalShellOfCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	first := fields[0]
	if first == "exec" && len(fields) > 1 {
		first = fields[1]
	}
	name := strings.ToLower(path.Base(strings.ReplaceAll(first, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "busybox":
		return terminalShellPosix
	case "fish":
		return terminalShellFish
	case "pwsh", "pwsh-preview":
		return terminalShellPwsh
	case "powershell":
		return terminalShellPowerShell
	case "cmd":
		return terminalShellCmd
	}
	return ""
}

// terminalShellOfOS is the syntax of the default shell on a target that
// reports this OS name; "" when the name is empty.
func terminalShellOfOS(osName string) string {
	switch {
	case osName == "":
		return ""
	case strings.Contains(strings.ToLower(osName), "windows"):
		return terminalShellPowerShell
	default:
		return terminalShellPosix
	}
}

// terminalShellChoice is the shell the KVM writes for and why: "setting"
// (chosen in the settings), "command" (named by the custom shell command),
// "os" (the target OS's default shell) or "unknown" (no OS reported yet;
// POSIX syntax).
type terminalShellChoice struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// OS is the target's OS name as host info reports it; empty without.
	OS string `json:"os,omitempty"`
}

func chooseTerminalShell(c OSServicesConfig, osName string) terminalShellChoice {
	choice := terminalShellChoice{OS: osName}
	if c.TerminalShell != "" && c.TerminalShell != terminalShellAuto {
		choice.Kind, choice.Source = c.TerminalShell, "setting"
		return choice
	}
	if kind := terminalShellOfCommand(c.TerminalShellCommand); kind != "" {
		choice.Kind, choice.Source = kind, "command"
		return choice
	}
	if kind := terminalShellOfOS(osName); kind != "" {
		choice.Kind, choice.Source = kind, "os"
		return choice
	}
	choice.Kind, choice.Source = terminalShellPosix, "unknown"
	return choice
}

// terminalMacros are what each macro becomes in each shell. Windows
// PowerShell 5.1 has no || or &&, so there %OR% and %AND% open a block that
// expandTerminalMacros closes at the end of the line.
var terminalMacros = map[string]map[string]string{
	"OR": {
		terminalShellPosix: "||", terminalShellFish: "; or", terminalShellPwsh: "||",
		terminalShellPowerShell: "; if (-not $?) {", terminalShellCmd: "||",
	},
	"AND": {
		terminalShellPosix: "&&", terminalShellFish: "; and", terminalShellPwsh: "&&",
		terminalShellPowerShell: "; if ($?) {", terminalShellCmd: "&&",
	},
	"SEP": {
		terminalShellPosix: ";", terminalShellFish: ";", terminalShellPwsh: ";",
		terminalShellPowerShell: ";", terminalShellCmd: "&",
	},
	"NULL": {
		terminalShellPosix: "/dev/null", terminalShellFish: "/dev/null", terminalShellPwsh: "$null",
		terminalShellPowerShell: "$null", terminalShellCmd: "NUL",
	},
	"QUIET": {
		terminalShellPosix: "2>/dev/null", terminalShellFish: "2>/dev/null", terminalShellPwsh: "2>$null",
		terminalShellPowerShell: "2>$null", terminalShellCmd: "2>NUL",
	},
	"HOME": {
		terminalShellPosix: "$HOME", terminalShellFish: "$HOME", terminalShellPwsh: "$HOME",
		terminalShellPowerShell: "$HOME", terminalShellCmd: "%USERPROFILE%",
	},
}

var terminalMacroPattern = regexp.MustCompile(`%([A-Z]+)%`)

// expandTerminalMacros writes text for the shell: each known %NAME% becomes
// the shell's own form, an unknown one stays as it is.
func expandTerminalMacros(text, kind string) string {
	blocks := 0
	out := terminalMacroPattern.ReplaceAllStringFunc(text, func(token string) string {
		forms, ok := terminalMacros[strings.Trim(token, "%")]
		if !ok {
			return token
		}
		form := forms[kind]
		if form == "" {
			form = forms[terminalShellPosix]
		}
		if strings.HasSuffix(form, "{") {
			blocks++
		}
		return form
	})
	return out + strings.Repeat(" }", blocks)
}

// terminalStartupLines are what a new browser terminal gets typed, one line
// after another: the custom shell command, written for the target's
// default shell, then the multiplexer ("restore %OR% start"), written for
// the shell that will read it.
func terminalStartupLines(c OSServicesConfig, osName string) []string {
	var lines []string
	if c.TerminalShellCommand != "" {
		base := terminalShellOfOS(osName)
		if base == "" {
			base = terminalShellPosix
		}
		lines = append(lines, expandTerminalMacros(c.TerminalShellCommand, base))
	}
	if c.TerminalMuxEnabled {
		var mux string
		switch {
		case c.TerminalMuxRestore != "" && c.TerminalMuxStart != "":
			mux = c.TerminalMuxRestore + " %OR% " + c.TerminalMuxStart
		case c.TerminalMuxRestore != "":
			mux = c.TerminalMuxRestore
		default:
			mux = c.TerminalMuxStart
		}
		if mux != "" {
			lines = append(lines, expandTerminalMacros(mux, chooseTerminalShell(c, osName).Kind))
		}
	}
	return lines
}

// targetOSName is the target's OS name from host info; empty without.
func (s *osServicesService) targetOSName() string {
	var inventory json.RawMessage
	s.do(func(session *osservices.Session) { inventory = session.State().HostInfoInventory })
	if len(inventory) > 0 {
		var parsed hostInventory
		if json.Unmarshal(inventory, &parsed) == nil && parsed.OS.Name != "" {
			s.lastTargetOS.Store(&parsed.OS.Name)
			return parsed.OS.Name
		}
	}
	// Host info comes a moment after the session starts, and can be off:
	// the last name seen stands in.
	if name := s.lastTargetOS.Load(); name != nil {
		return *name
	}
	return ""
}

// rpcGetOsServicesTerminalShell tells the settings page which shell the KVM
// writes the typed commands for, and why.
func rpcGetOsServicesTerminalShell() (terminalShellChoice, error) {
	s, err := requireOSServices()
	if err != nil {
		return terminalShellChoice{}, err
	}
	return chooseTerminalShell(s.config(), s.targetOSName()), nil
}
