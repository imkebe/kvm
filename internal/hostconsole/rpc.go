package hostconsole

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The KVM's device API (kvm/device_api.go): JSON-RPC over POST /api/rpc,
// switched on in the KVM's advanced settings.

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error"`
}

// rpcCall is one call of a batch and its outcome.
type rpcCall struct {
	Method string
	Params map[string]any
	Result json.RawMessage
	Err    error
}

func (r rpcResponse) err() error {
	if r.Error == nil {
		return nil
	}
	if r.Error.Data != "" {
		return fmt.Errorf("%s", r.Error.Data)
	}
	return fmt.Errorf("%s (%d)", r.Error.Message, r.Error.Code)
}

func (k *kvmClient) post(ctx context.Context, body any, timeout time.Duration) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u := k.base
	u.Path = "/api/rpc"
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header = k.header()
	req.Header.Set("Content-Type", "application/json")
	resp, err := k.ws.Do(req) // no client timeout: the context bounds it
	if err != nil {
		return nil, fmt.Errorf("cannot reach the KVM at %s: %w", k.base.Host, err)
	}
	defer resp.Body.Close()
	k.noteExpiry(resp.Header)
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, k.httpError(resp.StatusCode, answer)
	}
	return answer, nil
}

// rpc calls one method and returns its result.
func (k *kvmClient) rpc(ctx context.Context, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	answer, err := k.post(ctx, rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}, timeout)
	if err != nil {
		return nil, err
	}
	var r rpcResponse
	if err := json.Unmarshal(answer, &r); err != nil {
		return nil, fmt.Errorf("unexpected answer to %s: %w", method, err)
	}
	if err := r.err(); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return r.Result, nil
}

// batch runs several calls in one request; each call gets its own outcome.
func (k *kvmClient) batch(ctx context.Context, calls []rpcCall, timeout time.Duration) error {
	reqs := make([]rpcRequest, len(calls))
	for i, c := range calls {
		reqs[i] = rpcRequest{JSONRPC: "2.0", ID: i + 1, Method: c.Method, Params: c.Params}
	}
	answer, err := k.post(ctx, reqs, timeout)
	if err != nil {
		return err
	}
	var rs []rpcResponse
	if err := json.Unmarshal(answer, &rs); err != nil {
		return fmt.Errorf("unexpected batch answer: %w", err)
	}
	for _, r := range rs {
		if r.ID < 1 || r.ID > len(calls) {
			continue
		}
		calls[r.ID-1].Result = r.Result
		calls[r.ID-1].Err = r.err()
	}
	return nil
}
