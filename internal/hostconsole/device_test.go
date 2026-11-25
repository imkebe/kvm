package hostconsole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeDeviceAPI answers POST /api/rpc as kvm/device_api.go does, and
// records the calls.
type fakeDeviceAPI struct {
	mu    sync.Mutex
	calls []rpcRequest
	off   bool
}

func (f *fakeDeviceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/rpc" || r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>JetKVM</title>")
		return
	}
	if f.off {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"The device API is off in the advanced settings"}`)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var batch []rpcRequest
	single := false
	if err := json.Unmarshal(body, &batch); err != nil {
		var one rpcRequest
		if json.Unmarshal(body, &one) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		batch, single = []rpcRequest{one}, true
	}
	var out []map[string]any
	for _, req := range batch {
		f.mu.Lock()
		f.calls = append(f.calls, req)
		f.mu.Unlock()
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "factoryReset":
			resp["error"] = map[string]any{"code": -32601, "message": "Method not found", "data": "the device API does not run factoryReset"}
		case "getATXState":
			resp["result"] = map[string]any{"power": true, "hdd": false}
		case "getWakeOnLanDevices":
			resp["result"] = []map[string]string{{"name": "nas", "macAddress": "aa:bb:cc:dd:ee:ff"}}
		case "getKeyboardLayout":
			resp["result"] = "de-DE"
		case "guestClipboardGet":
			resp["result"] = map[string]any{"text": "from target"}
		case "getVideoState":
			resp["result"] = map[string]any{"ready": true, "width": 1920, "height": 1080}
		case "getUsbDevices":
			resp["result"] = map[string]any{"absolute_mouse": true, "keyboard": true}
		case "getScreenshot":
			resp["result"] = map[string]any{
				"image": base64.StdEncoding.EncodeToString(tinyPNG), "mimeType": "image/png",
				"screenWidth": 1920, "screenHeight": 1080, "x": 0, "y": 0, "width": 1920, "height": 1080,
				"imageWidth": 1280, "imageHeight": 720,
			}
		default:
			resp["result"] = map[string]any{"method": req.Method}
		}
		out = append(out, resp)
	}
	w.Header().Set("Content-Type", "application/json")
	if single {
		_ = json.NewEncoder(w).Encode(out[0])
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeDeviceAPI) take() []rpcRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func deviceClient(t *testing.T, f *fakeDeviceAPI) (func(string, map[string]any) (string, bool), *fakeDeviceAPI) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	srv := &server{kvm: testKVMClient(u.Host, ""), sessions: map[string]*session{}}
	m := mcp.NewServer(&mcp.Implementation{Name: "hostconsole", Version: version}, nil)
	srv.register(m)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := m.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return func(name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String(), res.IsError
	}, f
}

func TestDeviceTools(t *testing.T) {
	call, f := deviceClient(t, &fakeDeviceAPI{})

	out, isErr := call("kvm_status", nil)
	if isErr || !strings.Contains(out, "getATXState: {\"hdd\":false,\"power\":true}") || !strings.Contains(out, "getVirtualMediaState:") {
		t.Fatalf("kvm_status: %q", out)
	}
	f.take()

	if out, isErr := call("kvm_power", map[string]any{"action": "hold"}); isErr {
		t.Fatalf("power hold: %q", out)
	}
	if c := f.take(); len(c) != 1 || c[0].Method != "setATXPowerAction" || c[0].Params["action"] != "power-long" {
		t.Fatalf("power hold sent %+v", c)
	}
	if _, isErr := call("kvm_power", map[string]any{"action": "explode"}); !isErr {
		t.Fatal("unknown power action accepted")
	}

	if out, isErr := call("kvm_wake_on_lan", map[string]any{"mac": "NAS"}); isErr || !strings.Contains(out, "aa:bb:cc:dd:ee:ff") {
		t.Fatalf("wol by name: %q", out)
	}
	f.take()

	if out, isErr := call("kvm_virtual_media", map[string]any{"action": "mount_url", "url": "http://x/y.iso", "mode": "disk"}); isErr {
		t.Fatalf("mount_url: %q", out)
	}
	if c := f.take(); c[0].Method != "mountWithHTTP" || c[0].Params["mode"] != "Disk" || c[0].Params["url"] != "http://x/y.iso" {
		t.Fatalf("mount_url sent %+v", c)
	}
	if _, isErr := call("kvm_virtual_media", map[string]any{"action": "mount_file"}); !isErr {
		t.Fatal("mount_file without a filename accepted")
	}

	if _, isErr := call("kvm_usb_devices", map[string]any{"action": "disable", "device": "keyboard"}); !isErr {
		t.Fatal("switching the keyboard off must be refused")
	}
	if out, isErr := call("kvm_usb_devices", map[string]any{"action": "enable", "device": "video"}); isErr {
		t.Fatalf("enable video: %q", out)
	}
	if c := f.take(); c[0].Method != "setUsbDeviceState" || c[0].Params["device"] != "video" || c[0].Params["enabled"] != true {
		t.Fatalf("enable video sent %+v", c)
	}

	out, isErr = call("kvm_keyboard", map[string]any{"text": "Hi!", "keys": []string{"ctrl+alt+delete"}, "delay_ms": 5})
	if isErr || !strings.Contains(out, "de-DE") {
		t.Fatalf("keyboard: %q", out)
	}
	c := f.take()
	if c[0].Method != "executeKeyboardMacro" {
		t.Fatalf("keyboard sent %+v", c)
	}
	steps, _ := json.Marshal(c[0].Params["steps"])
	// H (shift+0x0b), release, i (0x0c), release, ! (shift+0x1e), release,
	// ctrl+alt (0x05) + delete (0x4c), release.
	want := `[{"delay":5,"keys":[11],"modifier":2},{"delay":5,"keys":[],"modifier":0},` +
		`{"delay":5,"keys":[12],"modifier":0},{"delay":5,"keys":[],"modifier":0},` +
		`{"delay":5,"keys":[30],"modifier":2},{"delay":5,"keys":[],"modifier":0},` +
		`{"delay":50,"keys":[76],"modifier":5},{"delay":50,"keys":[],"modifier":0}]`
	if string(steps) != want {
		t.Fatalf("steps\n got %s\nwant %s", steps, want)
	}
	if out, isErr := call("kvm_keyboard", map[string]any{"text": "zażółć"}); !isErr || !strings.Contains(out, "'ż'") {
		t.Fatalf("non-US characters: %q", out)
	}

	if out, isErr := call("kvm_clipboard", nil); isErr || !strings.Contains(out, "from target") {
		t.Fatalf("clipboard get: %q", out)
	}
	if out, isErr := call("kvm_clipboard", map[string]any{"text": ""}); isErr {
		t.Fatalf("clipboard set empty: %q", out)
	}
	f.take()

	if out, isErr := call("kvm_rpc", map[string]any{"method": "factoryReset"}); !isErr || !strings.Contains(out, "does not run factoryReset") {
		t.Fatalf("refused method: %q", out)
	}
}

func TestDeviceAPIOffAndOldFirmware(t *testing.T) {
	call, _ := deviceClient(t, &fakeDeviceAPI{off: true})
	if out, isErr := call("kvm_status", nil); !isErr || !strings.Contains(out, "device API is off") {
		t.Fatalf("off: %q", out)
	}
}

func TestComboSteps(t *testing.T) {
	for combo, want := range map[string]string{
		"enter":           "0:[40]",
		"win+r":           "8:[21]",
		"shift+f10":       "2:[67]",
		"ctrl+shift+?":    "3:[56]",
		"Ctrl+Alt+Del":    "5:[76]",
		"altgr+q":         "64:[20]",
		"alt+printscreen": "4:[70]",
	} {
		steps, err := comboSteps(combo, 50)
		if err != nil {
			t.Errorf("%s: %v", combo, err)
			continue
		}
		got := ""
		b, _ := json.Marshal(steps[0].toJSON()["keys"])
		got = strings.TrimSpace(strings.Join([]string{itoa(int(steps[0].Modifier)), string(b)}, ":"))
		if got != want {
			t.Errorf("%s: got %s, want %s", combo, got, want)
		}
	}
	for _, bad := range []string{"", "hyper+x", "a+b+c+d+e+f+g"} {
		if _, err := comboSteps(bad, 50); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestNetworkSummary(t *testing.T) {
	raw := json.RawMessage(`{"interface_name":"eth0","hostname":"kvm","ipv4_address":"192.0.2.6","ipv6_addresses":[{"address":"x"}],"dhcp_lease":{"routers":["192.0.2.1"],"bootp_file":"pxelinux.0"}}`)
	got := string(networkSummary(raw))
	if !strings.Contains(got, `"hostname":"kvm"`) || !strings.Contains(got, `"routers":["192.0.2.1"]`) ||
		strings.Contains(got, "ipv6_addresses") || strings.Contains(got, "bootp") {
		t.Fatalf("summary %s", got)
	}
}
