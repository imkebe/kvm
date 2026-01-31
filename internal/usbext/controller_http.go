package usbext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	// DefaultSocketPath is where the firmware side controller exposes its unix socket.
	DefaultSocketPath = "/run/jetkvm/usb-extension.sock"

	envUsbExtEndpoint = "JETKVM_USBEXT_ENDPOINT"
	envUsbExtSocket   = "JETKVM_USBEXT_SOCKET"

	applyEndpointPath  = "/v1/usb/extensions"
	statusEndpointPath = "/v1/usb/extensions/status"

	httpErrorBodyLimit = 64 << 10 // 64KiB
)

type httpController struct {
	client   *http.Client
	baseURL  string
	log      *zerolog.Logger
	endpoint string
}

// NewDefaultController constructs a controller using environment overrides when present,
// or falling back to the well-known unix socket path.
func NewDefaultController(logger *zerolog.Logger) (Controller, error) {
	endpoint := os.Getenv(envUsbExtEndpoint)
	if endpoint == "" {
		endpoint = os.Getenv(envUsbExtSocket)
	}
	if endpoint == "" {
		endpoint = DefaultSocketPath
	}
	return NewControllerFromEndpoint(endpoint, logger)
}

// NewControllerFromEndpoint builds a controller for either unix sockets (default) or
// TCP/HTTP endpoints when provided. Endpoints beginning with http:// or https:// are
// treated as TCP, all others are considered unix socket paths (optionally prefixed with unix://).
func NewControllerFromEndpoint(endpoint string, logger *zerolog.Logger) (Controller, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("usb extension endpoint cannot be empty")
	}

	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		client := &http.Client{Timeout: 20 * time.Second}
		return &httpController{
			client:   client,
			baseURL:  strings.TrimRight(endpoint, "/"),
			log:      logger,
			endpoint: endpoint,
		}, nil
	}

	socketPath := strings.TrimPrefix(endpoint, "unix://")
	return newUnixHTTPController(socketPath, logger)
}

func newUnixHTTPController(socketPath string, logger *zerolog.Logger) (Controller, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("usb extension socket path cannot be empty")
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
	}

	return &httpController{
		client:   client,
		baseURL:  "http://unix",
		log:      logger,
		endpoint: socketPath,
	}, nil
}

func (c *httpController) ApplyDesiredState(ctx context.Context, desired DesiredState) (Status, error) {
	payload, err := json.Marshal(desired)
	if err != nil {
		return Status{}, fmt.Errorf("marshal desired state: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.makeURL(applyEndpointPath), bytes.NewReader(payload))
	if err != nil {
		return Status{}, fmt.Errorf("build apply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("request usb extension apply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return Status{}, c.decodeError(resp)
	}

	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode usb extension status: %w", err)
	}

	return status, nil
}

func (c *httpController) FetchStatus(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.makeURL(statusEndpointPath), nil)
	if err != nil {
		return Status{}, fmt.Errorf("build status request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("request usb extension status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return Status{}, c.decodeError(resp)
	}

	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode usb extension status: %w", err)
	}

	return status, nil
}

func (c *httpController) makeURL(path string) string {
	if strings.HasSuffix(c.baseURL, "/") {
		return c.baseURL + strings.TrimPrefix(path, "/")
	}
	return c.baseURL + path
}

func (c *httpController) decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, httpErrorBodyLimit))
	// Preserve body for logging even if decoding fails.
	if len(body) == 0 {
		return fmt.Errorf("usb extension controller %s returned status %d", c.endpoint, resp.StatusCode)
	}

	var parsed struct {
		Error   string      `json:"error"`
		Details interface{} `json:"details,omitempty"`
	}

	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		if parsed.Details != nil {
			return fmt.Errorf("usb extension controller error: %s (status %d, details=%v)", parsed.Error, resp.StatusCode, parsed.Details)
		}
		return fmt.Errorf("usb extension controller error: %s (status %d)", parsed.Error, resp.StatusCode)
	}

	return fmt.Errorf("usb extension controller unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
