package hostconsole

import "strings"

// dialect is a shell language: how a command is wrapped in markers, and the
// setup an MCP session's shell gets.
//
// The markers are printed by the command line itself and never appear in
// its echo: the echo shows "__MCPS <nonce>" or "'__MCPS'+'_<nonce>'", the
// output "__MCPS_<nonce>". The command goes inside a block that the shell
// reads whole before it runs anything, so a command reading its standard
// input cannot take the end marker's line, and a comment in it ends at its
// own line. Every line starts with a space, which keeps it out of the
// history of shells that ignore such lines.
type dialect interface {
	Name() string
	wrap(command, nonce string) string
	setup() string
}

// enterKeys turns every line break into the Enter key.
func enterKeys(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r")
}

type posixShell struct{}

func (posixShell) Name() string { return "posix" }

func (posixShell) wrap(command, nonce string) string {
	return enterKeys(" printf '%s_%s\\n' __MCPS " + nonce + "; { " + command +
		"\n}; printf '%s_%s_%d\\n' __MCPE " + nonce + " $?\n")
}

func (posixShell) setup() string {
	// zsh takes "#" literally in interactive shells unless told otherwise.
	return "unset HISTFILE; export PAGER=cat GIT_PAGER=cat MANPAGER=cat SYSTEMD_PAGER=cat NO_COLOR=1; " +
		"[ -n \"$ZSH_VERSION\" ] && setopt interactive_comments; true"
}

type fishShell struct{}

func (fishShell) Name() string { return "fish" }

func (fishShell) wrap(command, nonce string) string {
	return enterKeys(" printf '%s_%s\\n' __MCPS " + nonce + "; begin; " + command +
		"\nend; printf '%s_%s_%d\\n' __MCPE " + nonce + " $status\n")
}

func (fishShell) setup() string {
	return "set -g fish_history ''; set -gx PAGER cat; set -gx GIT_PAGER cat; set -gx MANPAGER cat; set -gx NO_COLOR 1"
}

type powerShell struct{}

func (powerShell) Name() string { return "powershell" }

// The exit code: $LASTEXITCODE of a native program, else 1 for a failed
// command and 0 for success. $? is taken inside the block, right after the
// command: after the block itself it stays true when a cmdlet in it had a
// non-terminating error (Get-Item of a missing path), in Windows PowerShell
// 5.1 as in 7. The block is dot-sourced, so $__mcpOk is the caller's.
func (powerShell) wrap(command, nonce string) string {
	return enterKeys(" Write-Output ('__MCPS'+'_" + nonce + "'); $global:LASTEXITCODE=0; $__mcpOk=$true; . {\n" + command +
		"\n$__mcpOk=$?\n}; Write-Output ('__MCPE'+'_" + nonce + "_'+$(if($__mcpOk -and $global:LASTEXITCODE -eq 0){0}elseif($global:LASTEXITCODE){$global:LASTEXITCODE}else{1}))\n")
}

func (powerShell) setup() string {
	return "$ProgressPreference='SilentlyContinue'; try { Set-PSReadLineOption -HistorySaveStyle SaveNothing -ErrorAction Stop } catch {}"
}
