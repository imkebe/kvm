package hostconsole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// kvmClient reaches one KVM's terminal API (kvm/os_services_terminal_ws.go)
// and device API (kvm/device_api.go). It sends an API token made in the
// KVM's Settings > MCP as a bearer token; it never signs in, which would
// replace the browser's token and sign the owner out.
type kvmClient struct {
	base  url.URL // http(s)://host
	token string
	http  *http.Client
	// ws has no Timeout: the WebSocket dial refuses one and uses its context.
	ws *http.Client
	// extra headers for every request.
	extra http.Header

	mu sync.Mutex
	// expires is when the token expires, from the KVM's
	// X-JetKVM-Token-Expires header; zero for a token without expiry or
	// before the first answer.
	expires time.Time
}

const tokenExpiresHeader = "X-JetKVM-Token-Expires"

// noteExpiry keeps the token's expiry from an answer.
func (k *kvmClient) noteExpiry(h http.Header) {
	v := h.Get(tokenExpiresHeader)
	if v == "" {
		return
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		k.mu.Lock()
		k.expires = t
		k.mu.Unlock()
	}
}

// expiryNote is a line about the token's expiry when it is near, or "".
func (k *kvmClient) expiryNote(now time.Time) string {
	k.mu.Lock()
	expires := k.expires
	k.mu.Unlock()
	if expires.IsZero() {
		return ""
	}
	left := expires.Sub(now)
	switch {
	case left <= 0:
		return "The API token has expired; create a new one in the KVM's Settings > MCP."
	case left < 7*24*time.Hour:
		return fmt.Sprintf("The API token expires %s (in %.1f days); create a new one in the KVM's Settings > MCP before then.",
			expires.Local().Format("2006-01-02 15:04"), left.Hours()/24)
	}
	return ""
}

func (k *kvmClient) header() http.Header {
	h := k.extra.Clone()
	if h == nil {
		h = http.Header{}
	}
	if k.token != "" {
		h.Set("Authorization", "Bearer "+k.token)
	}
	return h
}

// terminalStatus is GET /os-services/terminal/status.
type terminalStatus struct {
	APIEnabled      bool   `json:"apiEnabled"`
	Enabled         bool   `json:"enabled"`
	TerminalEnabled bool   `json:"terminalEnabled"`
	Connected       bool   `json:"connected"`
	ApprovalPending bool   `json:"approvalPending"`
	Available       bool   `json:"available"`
	OS              string `json:"os"`
	SenderVersion   string `json:"senderVersion,omitempty"`
	Sessions        int    `json:"sessions"`
	APISessions     int    `json:"apiSessions"`
	Max             int    `json:"max"`
}

func (k *kvmClient) status(ctx context.Context) (terminalStatus, error) {
	u := k.base
	u.Path = "/os-services/terminal/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return terminalStatus{}, err
	}
	req.Header = k.header()
	resp, err := k.http.Do(req)
	if err != nil {
		return terminalStatus{}, fmt.Errorf("cannot reach the KVM at %s: %w", k.base.Host, err)
	}
	defer resp.Body.Close()
	k.noteExpiry(resp.Header)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return terminalStatus{}, k.httpError(resp.StatusCode, body)
	}
	var st terminalStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return terminalStatus{}, fmt.Errorf("unexpected status answer: %w", err)
	}
	return st, nil
}

func (k *kvmClient) httpError(code int, body []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	switch {
	case code == http.StatusUnauthorized && k.token == "":
		return errors.New("no API token: create one in the KVM's Settings > MCP")
	case code == http.StatusUnauthorized:
		if e.Error == "" {
			e.Error = "unauthorized"
		}
		return fmt.Errorf("the KVM refused the API token: %s", e.Error)
	case e.Error != "":
		return fmt.Errorf("KVM: %s (HTTP %d)", e.Error, code)
	default:
		return fmt.Errorf("KVM answered HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
}

// dial opens one terminal session's WebSocket.
func (k *kvmClient) dial(ctx context.Context, rows, cols int) (*websocket.Conn, error) {
	u := k.base
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/os-services/terminal/ws"
	u.RawQuery = url.Values{"rows": {fmt.Sprint(rows)}, "cols": {fmt.Sprint(cols)}}.Encode()
	conn, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient: k.ws,
		HTTPHeader: k.header(),
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			return nil, k.httpError(resp.StatusCode, body)
		}
		return nil, fmt.Errorf("cannot open the terminal WebSocket: %w", err)
	}
	conn.SetReadLimit(1 << 22)
	return conn, nil
}
