package hostconsole

import (
	"fmt"
	"strings"
)

// USB HID keyboard usages and modifier bits, and a US layout for typing
// text. The target computer reads key positions, so typed text comes out
// right only where the target uses a US layout.

const (
	modCtrl  = 0x01
	modShift = 0x02
	modAlt   = 0x04
	modMeta  = 0x08
	modRCtrl = 0x10
	modRAlt  = 0x40
)

var hidModifiers = map[string]byte{
	"ctrl": modCtrl, "control": modCtrl, "shift": modShift, "alt": modAlt, "option": modAlt,
	"meta": modMeta, "win": modMeta, "super": modMeta, "cmd": modMeta, "command": modMeta,
	"rctrl": modRCtrl, "ralt": modRAlt, "altgr": modRAlt,
}

var hidKeys = map[string]byte{
	"enter": 0x28, "return": 0x28, "esc": 0x29, "escape": 0x29, "backspace": 0x2a, "tab": 0x2b,
	"space": 0x2c, "capslock": 0x39, "printscreen": 0x46, "sysrq": 0x46, "scrolllock": 0x47,
	"pause": 0x48, "break": 0x48, "insert": 0x49, "home": 0x4a, "pageup": 0x4b, "delete": 0x4c,
	"del": 0x4c, "end": 0x4d, "pagedown": 0x4e, "right": 0x4f, "left": 0x50, "down": 0x51, "up": 0x52,
	"numlock": 0x53, "menu": 0x65, "application": 0x65,
	"f1": 0x3a, "f2": 0x3b, "f3": 0x3c, "f4": 0x3d, "f5": 0x3e, "f6": 0x3f, "f7": 0x40, "f8": 0x41,
	"f9": 0x42, "f10": 0x43, "f11": 0x44, "f12": 0x45,
	"minus": 0x2d, "equal": 0x2e, "comma": 0x36, "period": 0x37, "slash": 0x38,
}

// usKey is a character's key and whether it needs Shift.
type usKey struct {
	code  byte
	shift bool
}

var usLayout = func() map[rune]usKey {
	m := map[rune]usKey{'\n': {0x28, false}, '\t': {0x2b, false}, ' ': {0x2c, false}}
	for c := 'a'; c <= 'z'; c++ {
		m[c] = usKey{byte(0x04 + c - 'a'), false}
		m[c-'a'+'A'] = usKey{byte(0x04 + c - 'a'), true}
	}
	for i, c := range "1234567890" {
		m[c] = usKey{byte(0x1e + i), false}
	}
	for i, c := range "!@#$%^&*()" {
		m[c] = usKey{byte(0x1e + i), true}
	}
	plain := map[rune]byte{'-': 0x2d, '=': 0x2e, '[': 0x2f, ']': 0x30, '\\': 0x31, ';': 0x33, '\'': 0x34, '`': 0x35, ',': 0x36, '.': 0x37, '/': 0x38}
	shifted := map[rune]byte{'_': 0x2d, '+': 0x2e, '{': 0x2f, '}': 0x30, '|': 0x31, ':': 0x33, '"': 0x34, '~': 0x35, '<': 0x36, '>': 0x37, '?': 0x38}
	for c, k := range plain {
		m[c] = usKey{k, false}
	}
	for c, k := range shifted {
		m[c] = usKey{k, true}
	}
	return m
}()

type macroStep struct {
	Modifier byte
	Keys     []byte
	Delay    int
}

// toJSON is the step as executeKeyboardMacro takes it, keys as numbers.
func (s macroStep) toJSON() map[string]any {
	keys := make([]int, len(s.Keys))
	for i, k := range s.Keys {
		keys[i] = int(k)
	}
	return map[string]any{"modifier": int(s.Modifier), "keys": keys, "delay": s.Delay}
}

// textSteps types text: each character pressed and released.
func textSteps(text string, delay int) ([]macroStep, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var steps []macroStep
	var bad []string
	for _, c := range text {
		k, ok := usLayout[c]
		if !ok {
			bad = append(bad, fmt.Sprintf("%q", c))
			continue
		}
		var mod byte
		if k.shift {
			mod = modShift
		}
		steps = append(steps, macroStep{Modifier: mod, Keys: []byte{k.code}, Delay: delay}, macroStep{Delay: delay})
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("these characters have no key on a US keyboard: %s; type them another way", strings.Join(uniq(bad), " "))
	}
	return steps, nil
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// comboSteps presses one combination such as "ctrl+alt+delete", "enter",
// "shift+f10" or "a", holds it for hold ms and releases it.
func comboSteps(combo string, hold int) ([]macroStep, error) {
	var mod byte
	var keys []byte
	for _, part := range strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+") {
		part = strings.TrimSpace(part)
		if m, ok := hidModifiers[part]; ok {
			mod |= m
			continue
		}
		if k, ok := hidKeys[part]; ok {
			keys = append(keys, k)
			continue
		}
		if r := []rune(part); len(r) == 1 {
			if k, ok := usLayout[r[0]]; ok {
				keys = append(keys, k.code)
				if k.shift {
					mod |= modShift
				}
				continue
			}
		}
		return nil, fmt.Errorf("unknown key %q in %q: use names such as ctrl, alt, shift, meta, enter, esc, tab, f1-f12, delete, up, or a single character", part, combo)
	}
	if len(keys) > 6 {
		return nil, fmt.Errorf("%q presses more than 6 keys", combo)
	}
	if mod == 0 && len(keys) == 0 {
		return nil, fmt.Errorf("empty key combination")
	}
	return []macroStep{{Modifier: mod, Keys: keys, Delay: hold}, {Delay: hold}}, nil
}
