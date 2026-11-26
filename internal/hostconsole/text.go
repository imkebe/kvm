package hostconsole

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiPattern matches terminal control sequences: CSI, OSC, DCS/PM/APC
// strings, charset selection and other two-byte escapes.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]` + // CSI
	`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` + // OSC
	`|\x1b[PX^_][^\x1b]*(?:\x1b\\)?` + // DCS, SOS, PM, APC
	`|\x1b[()*+][0-9A-Za-z]` + // charset
	`|\x1b[ -/]*[0-~]`) // other escapes

// plainText turns terminal output into plain text: no control sequences,
// "\r\n" as "\n", a carriage return overwrites the line from its start, and
// a backspace removes the character before it.
func plainText(raw []byte) string {
	s := ansiPattern.ReplaceAllString(strings.ToValidUTF8(string(raw), "�"), "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if j := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); j >= 0 {
			line = line[j+1:]
		}
		line = strings.TrimRight(line, "\r")
		if strings.ContainsRune(line, '\b') {
			var b []rune
			for _, r := range line {
				if r == '\b' {
					if len(b) > 0 {
						b = b[:len(b)-1]
					}
					continue
				}
				b = append(b, r)
			}
			line = string(b)
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' || r >= 0x20 && r != 0x7f {
				return r
			}
			return -1
		}, line)
	}
	return strings.Join(lines, "\n")
}

// clip keeps text within max bytes: the first quarter and the last three
// quarters, with a line saying how much was left out.
func clip(text string, max int) (string, bool) {
	if max <= 0 || len(text) <= max {
		return text, false
	}
	head := max / 4
	tail := max - head
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	start := len(text) - tail
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	omitted := start - head
	return fmt.Sprintf("%s\n[… %d bytes left out …]\n%s", text[:head], omitted, text[start:]), true
}

// namedKeys are what console_send's keys accept.
var namedKeys = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"shift-tab": "\x1b[Z",
	"esc":       "\x1b",
	"escape":    "\x1b",
	"backspace": "\x7f",
	"delete":    "\x1b[3~",
	"space":     " ",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"pageup":    "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f10":       "\x1b[21~",
}

// keyBytes reads one key name: a named key or ctrl-<letter>.
func keyBytes(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if b, ok := namedKeys[n]; ok {
		return b, nil
	}
	if rest, ok := strings.CutPrefix(n, "ctrl-"); ok && len(rest) == 1 {
		c := rest[0]
		switch {
		case c >= 'a' && c <= 'z':
			return string(rune(c - 'a' + 1)), nil
		case c == '[':
			return "\x1b", nil
		case c == '\\':
			return "\x1c", nil
		case c == ']':
			return "\x1d", nil
		}
	}
	return "", fmt.Errorf("unknown key %q: use enter, tab, esc, backspace, delete, up, down, left, right, home, end, pageup, pagedown, f1-f5, f10, space or ctrl-<letter>", name)
}
