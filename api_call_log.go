package kvm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)


const (
	apiCallArgsMax  = 400
)

// apiCallPrivate: argument names whose values are shown only by length.
var apiCallPrivate = map[string]bool{
	"text": true, "password": true, "token": true, "secret": true, "data": true,
	"content": true, "clipboard": true, "input": true, "keys": true, "steps": true,
	// A URL may carry credentials; a NetBird setup key enrols peers.
	"url": true, "setupkey": true,
}

// summarizeAPIArgs shortens arguments for the log.
func summarizeAPIArgs(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return shorten(string(raw), apiCallArgsMax)
	}
	return summarizeAPIArgMap(args)
}

// summarizeAPIArgMap is summarizeAPIArgs for parsed arguments; args stays unchanged.
func summarizeAPIArgMap(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(redactAPIArgs(args)); err != nil {
		return ""
	}
	return shorten(strings.TrimSpace(out.String()), apiCallArgsMax)
}

// redactAPIArgs copies args with the private values hidden, at any depth.
func redactAPIArgs(args map[string]any) map[string]any {
	shown := make(map[string]any, len(args))
	for key, value := range args {
		shown[key] = redactAPIArg(apiCallPrivate[strings.ToLower(key)], value)
	}
	return shown
}

func redactAPIArg(private bool, value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if private {
			return fmt.Sprintf("<%d characters>", utf8.RuneCountInString(v))
		}
	case []any:
		if private {
			return fmt.Sprintf("<%d items>", len(v))
		}
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = redactAPIArg(false, item)
		}
		return items
	case map[string]any:
		if !private {
			return redactAPIArgs(v)
		}
	}
	if private {
		return "<hidden>"
	}
	return value
}

func shorten(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}
