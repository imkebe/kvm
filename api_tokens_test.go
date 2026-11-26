package kvm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupAPITokenTest(t *testing.T, mode string) {
	t.Helper()
	previousConfig, previousPath, previousNow := config, configPath, apiTokenNow
	config = &Config{LocalAuthMode: mode, DeviceAPIEnabled: true}
	configPath = t.TempDir() + "/kvm_config.json"
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	apiTokenNow = func() time.Time { return now }
	t.Cleanup(func() {
		config, configPath, apiTokenNow = previousConfig, previousPath, previousNow
		apiTokenMu.Lock()
		apiTokenUsed = map[string]int64{}
		apiTokenMu.Unlock()
	})
}

func setNow(t time.Time) { apiTokenNow = func() time.Time { return t } }

func TestAPITokenCreateListDelete(t *testing.T) {
	setupAPITokenTest(t, "noPassword")
	created, err := rpcCreateAPIToken("  laptop  ", []string{"terminal", "device", "device"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "jkvm_"+created.Info.ID+"_") || len(created.Token) < 50 {
		t.Fatalf("token %q", created.Token)
	}
	if created.Info.Name != "laptop" || strings.Join(created.Info.Scopes, ",") != "device,terminal" {
		t.Fatalf("info %+v", created.Info)
	}
	if want := apiTokenNow().Add(30 * 24 * time.Hour).Unix(); created.Info.ExpiresAt != want {
		t.Fatalf("expires %d, want %d", created.Info.ExpiresAt, want)
	}
	secret := strings.SplitN(created.Token, "_", 3)[2]
	if len(secret) < 40 || strings.Contains(config.APITokens[0].Hash, secret) || config.APITokens[0].Hash != apiTokenHash(secret) {
		t.Fatal("the config holds the secret")
	}
	list, _ := rpcGetAPITokens()
	if len(list) != 1 || list[0].Expired || list[0].Prefix != "jkvm_"+created.Info.ID {
		t.Fatalf("list %+v", list)
	}
	for _, bad := range []struct {
		name   string
		scopes []string
		days   int
	}{{"", []string{"device"}, 0}, {"x", nil, 0}, {"x", []string{"root"}, 0}, {"x", []string{"device"}, -1}, {"x", []string{"device"}, 99999}} {
		if _, err := rpcCreateAPIToken(bad.name, bad.scopes, bad.days); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := rpcDeleteAPIToken(created.Info.ID); err != nil {
		t.Fatal(err)
	}
	if err := rpcDeleteAPIToken(created.Info.ID); err == nil {
		t.Fatal("deleted twice")
	}
	if _, err := checkAPIToken(created.Token); err != errAPITokenInvalid {
		t.Fatalf("deleted token: %v", err)
	}
}

func TestAPITokenExpiryAndClock(t *testing.T) {
	setupAPITokenTest(t, "noPassword")
	start := apiTokenNow()
	expiring, _ := rpcCreateAPIToken("week", []string{"device"}, 7)
	forever, _ := rpcCreateAPIToken("forever", []string{"device"}, 0)

	if _, err := checkAPIToken(expiring.Token); err != nil {
		t.Fatalf("fresh token: %v", err)
	}
	setNow(start.Add(7*24*time.Hour - time.Second))
	if _, err := checkAPIToken(expiring.Token); err != nil {
		t.Fatalf("a second before expiry: %v", err)
	}
	setNow(start.Add(7 * 24 * time.Hour))
	if _, err := checkAPIToken(expiring.Token); err != errAPITokenExpired {
		t.Fatalf("at expiry: %v", err)
	}
	list, _ := rpcGetAPITokens()
	if !list[0].Expired || list[1].Expired {
		t.Fatalf("expired flags %+v", list)
	}

	// A clock that has not been set cannot judge an expiry; a token without
	// one still works.
	setNow(time.Date(1970, 1, 1, 0, 5, 0, 0, time.UTC))
	if _, err := checkAPIToken(expiring.Token); err != errAPITokenClock {
		t.Fatalf("unset clock: %v", err)
	}
	if _, err := checkAPIToken(forever.Token); err != nil {
		t.Fatalf("no expiry, unset clock: %v", err)
	}
	if _, err := rpcCreateAPIToken("x", []string{"device"}, 1); err == nil {
		t.Fatal("an expiry was counted from an unset clock")
	}

	// A wrong secret with a real id is refused.
	if _, err := checkAPIToken(forever.Token[:len(forever.Token)-2] + "xx"); err != errAPITokenInvalid {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestAPITokenScopesOnRoutes(t *testing.T) {
	setupAPITokenTest(t, "noPassword")
	device, _ := rpcCreateAPIToken("device", []string{"device"}, 0)
	terminal, _ := rpcCreateAPIToken("terminal", []string{"terminal"}, 0)
	metrics, _ := rpcCreateAPIToken("metrics", []string{"metrics"}, 30)
	router := setupRouter()
	do := func(method, path, token string) *httptest.ResponseRecorder {
		var req *http.Request
		if method == http.MethodPost {
			req = httptest.NewRequest(method, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	// noPassword mode no longer opens the APIs without a token.
	if rec := do(http.MethodPost, deviceAPIPath, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("device API without a token: %d", rec.Code)
	}
	if rec := do(http.MethodPost, deviceAPIPath, device.Token); rec.Code != http.StatusOK {
		t.Fatalf("device token: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPost, deviceAPIPath, terminal.Token); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "device scope") {
		t.Fatalf("terminal token on the device API: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodGet, osServicesTerminalStatusPath, device.Token); rec.Code != http.StatusForbidden {
		t.Fatalf("device token on the terminal API: %d", rec.Code)
	}
	if rec := do(http.MethodGet, osServicesTerminalStatusPath, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("terminal status without a token: %d", rec.Code)
	}
	if rec := do(http.MethodGet, osServicesTerminalStatusPath, terminal.Token); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("terminal token on the terminal API: %d %s", rec.Code, rec.Body)
	}
	rec := do(http.MethodGet, "/metrics", metrics.Token)
	if rec.Code != http.StatusOK || rec.Header().Get(apiTokenHeaderExp) == "" {
		t.Fatalf("metrics token: %d, expiry header %q", rec.Code, rec.Header().Get(apiTokenHeaderExp))
	}
	if rec := do(http.MethodGet, "/metrics", device.Token); rec.Code != http.StatusForbidden {
		t.Fatalf("device token on metrics: %d", rec.Code)
	}
	if rec := do(http.MethodPost, deviceAPIPath, "jkvm_00000000_nope"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", rec.Code)
	}
}
