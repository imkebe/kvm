package kvm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The live record of what programs do with the KVM's APIs: MCP tool calls,
// device API calls and terminal API shells. The browser gets each call as
// an "apiCall" event, for the MCP card's activity light and the call log;
// getApiCallLog gives the recent calls and the counts since the app
// started. Nothing is saved.

const (
	apiCallSourceMCP      = "mcp"
	apiCallSourceAPI      = "api"
	apiCallSourceTerminal = "terminal"

	apiCallLogSize  = 300
	apiCallArgsMax  = 400
	apiCallErrorMax = 300

	// The gin context key set on calls that come from MCP tools.
	viaMCPKey = "viaMCP"
)

type APICallEntry struct {
	ID     uint64 `json:"id"`
	At     int64  `json:"at"` // Unix ms, when the call started
	Source string `json:"source"`
	// The tool, the RPC method, or "shell".
	Name       string  `json:"name"`
	DurationMs float64 `json:"duration_ms"`
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	// The arguments, shortened; text that may be private (typed text,
	// clipboard, passwords) shows only its length.
	Args   string `json:"args,omitempty"`
	Token  string `json:"token,omitempty"`
	Remote string `json:"remote,omitempty"`
	// A device API call made by an MCP tool.
	ViaMCP bool `json:"via_mcp,omitempty"`
}

type APICallCount struct {
	Source  string  `json:"source"`
	Name    string  `json:"name"`
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	TotalMs float64 `json:"total_ms"`
	LastAt  int64   `json:"last_at"`
}

type APICallLog struct {
	Started int64          `json:"started"` // Unix ms, the app's start
	Entries []APICallEntry `json:"entries"` // oldest first
	Counts  []APICallCount `json:"counts"`  // most calls first
	Totals  map[string]int `json:"totals"`  // calls per source
}

var apiCalls = struct {
	sync.Mutex
	next    uint64
	started int64
	ring    []APICallEntry
	counts  map[string]*APICallCount
	totals  map[string]int
}{
	started: time.Now().UnixMilli(),
	counts:  map[string]*APICallCount{},
	totals:  map[string]int{},
}

// recordAPICall notes a finished call and tells the browser.
func recordAPICall(entry APICallEntry) {
	apiCalls.Lock()
	apiCalls.next++
	entry.ID = apiCalls.next
	if len(apiCalls.ring) >= apiCallLogSize {
		copy(apiCalls.ring, apiCalls.ring[1:])
		apiCalls.ring = apiCalls.ring[:len(apiCalls.ring)-1]
	}
	apiCalls.ring = append(apiCalls.ring, entry)
	key := entry.Source + "\x00" + entry.Name
	count := apiCalls.counts[key]
	if count == nil {
		count = &APICallCount{Source: entry.Source, Name: entry.Name}
		apiCalls.counts[key] = count
	}
	count.Calls++
	if !entry.OK {
		count.Errors++
	}
	count.TotalMs += entry.DurationMs
	count.LastAt = entry.At
	apiCalls.totals[entry.Source]++
	apiCalls.Unlock()

	if session := currentSession; session != nil && session.RPCChannel != nil {
		go writeJSONRPCEvent("apiCall", entry, session)
	}
}

func rpcGetApiCallLog() APICallLog {
	apiCalls.Lock()
	defer apiCalls.Unlock()
	log := APICallLog{
		Started: apiCalls.started,
		// Never null in JSON, also right after a reset.
		Entries: append(make([]APICallEntry, 0, len(apiCalls.ring)), apiCalls.ring...),
		Counts:  make([]APICallCount, 0, len(apiCalls.counts)),
		Totals:  make(map[string]int, len(apiCalls.totals)),
	}
	for _, count := range apiCalls.counts {
		log.Counts = append(log.Counts, *count)
	}
	sort.Slice(log.Counts, func(i, j int) bool {
		if log.Counts[i].Calls != log.Counts[j].Calls {
			return log.Counts[i].Calls > log.Counts[j].Calls
		}
		return log.Counts[i].Name < log.Counts[j].Name
	})
	for source, n := range apiCalls.totals {
		log.Totals[source] = n
	}
	return log
}

// rpcResetApiCallLog clears the record and the counts, as if the app had
// just started; the device API counter of the status popover too.
func rpcResetApiCallLog() APICallLog {
	apiCalls.Lock()
	apiCalls.started = time.Now().UnixMilli()
	apiCalls.ring = nil
	apiCalls.counts = map[string]*APICallCount{}
	apiCalls.totals = map[string]int{}
	apiCalls.Unlock()
	deviceAPICalls.Store(0)
	deviceAPILastCall.Store(0)
	return rpcGetApiCallLog()
}

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

func sinceMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

// apiTokenName is a token's name by its ID, for the log.
func apiTokenName(id string) string {
	if id == "" {
		return ""
	}
	apiTokenMu.Lock()
	defer apiTokenMu.Unlock()
	for _, t := range config.APITokens {
		if t.ID == id {
			return t.Name
		}
	}
	return ""
}
