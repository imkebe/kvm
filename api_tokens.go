package kvm

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// API tokens: bearer tokens for programs such as the hostconsole MCP server.
// Each token has scopes (which APIs it opens) and an optional expiry. The KVM
// keeps only a SHA-256 hash of the secret; the full token is shown once, when
// it is created.
//
// A token reads "jkvm_<id>_<secret>": id is 8 hex digits and names the token
// in the config, secret is 32 random bytes in base64url.
const (
	apiTokenPrefix    = "jkvm_"
	apiTokenMax       = 32
	apiTokenNameMax   = 64
	apiTokenMaxDays   = 3650
	apiTokenUsedSave  = time.Hour
	apiTokenHeaderExp = "X-JetKVM-Token-Expires"

	APIScopeDevice   = "device"
	APIScopeTerminal = "terminal"
	APIScopeMetrics  = "metrics"
)

// apiTokenExpiresKey: the gin context key of the request token's ExpiresAt.
const apiTokenExpiresKey = "apiTokenExpires"

var apiTokenScopes = []string{APIScopeDevice, APIScopeTerminal, APIScopeMetrics}

// APIToken is a token as the config stores it.
type APIToken struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Hash      string   `json:"hash"`
	Scopes    []string `json:"scopes"`
	CreatedAt int64    `json:"created_at"`
	// ExpiresAt is a Unix time; 0 never expires.
	ExpiresAt  int64 `json:"expires_at"`
	LastUsedAt int64 `json:"last_used_at,omitempty"`
}

// APITokenInfo is a token as the UI sees it: no hash.
type APITokenInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  int64    `json:"created_at"`
	ExpiresAt  int64    `json:"expires_at"`
	LastUsedAt int64    `json:"last_used_at"`
	Expired    bool     `json:"expired"`
}

var (
	apiTokenMu sync.Mutex
	// apiTokenUsed is when each token was last used; the config gets it at
	// most once an hour, so a busy client does not keep writing the flash.
	apiTokenUsed = map[string]int64{}
	apiTokenNow  = time.Now
)

func (t APIToken) info(now int64) APITokenInfo {
	last := t.LastUsedAt
	if used, ok := apiTokenUsed[t.ID]; ok && used > last {
		last = used
	}
	return APITokenInfo{
		ID: t.ID, Name: t.Name, Prefix: apiTokenPrefix + t.ID, Scopes: slices.Clone(t.Scopes),
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: last,
		Expired: t.ExpiresAt != 0 && now >= t.ExpiresAt,
	}
}

// clockSet: before 2025 the KVM has not synchronized its clock yet.
func clockSet(now time.Time) bool { return now.Year() >= 2025 }

func rpcGetAPITokens() ([]APITokenInfo, error) {
	apiTokenMu.Lock()
	defer apiTokenMu.Unlock()
	now := apiTokenNow().Unix()
	out := make([]APITokenInfo, 0, len(config.APITokens))
	for _, t := range config.APITokens {
		out = append(out, t.info(now))
	}
	return out, nil
}

type apiTokenCreated struct {
	Token string       `json:"token"`
	Info  APITokenInfo `json:"info"`
}

// rpcCreateAPIToken makes a token. expiresInDays 0 never expires.
func rpcCreateAPIToken(name string, scopes []string, expiresInDays int) (apiTokenCreated, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > apiTokenNameMax {
		return apiTokenCreated{}, fmt.Errorf("the name must have 1 to %d characters", apiTokenNameMax)
	}
	var clean []string
	for _, s := range scopes {
		if !slices.Contains(apiTokenScopes, s) {
			return apiTokenCreated{}, fmt.Errorf("unknown scope %q", s)
		}
		if !slices.Contains(clean, s) {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return apiTokenCreated{}, errors.New("choose at least one scope")
	}
	slices.Sort(clean)
	if expiresInDays < 0 || expiresInDays > apiTokenMaxDays {
		return apiTokenCreated{}, fmt.Errorf("expiry must be 0 (never) to %d days", apiTokenMaxDays)
	}
	now := apiTokenNow()
	if expiresInDays > 0 && !clockSet(now) {
		return apiTokenCreated{}, errors.New("the KVM clock is not set yet, so an expiry cannot be counted; wait for time synchronization or choose no expiry")
	}

	idBytes := make([]byte, 4)
	secret := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return apiTokenCreated{}, err
	}
	if _, err := rand.Read(secret); err != nil {
		return apiTokenCreated{}, err
	}
	secretText := base64.RawURLEncoding.EncodeToString(secret)
	t := APIToken{
		ID:        hex.EncodeToString(idBytes),
		Name:      name,
		Hash:      apiTokenHash(secretText),
		Scopes:    clean,
		CreatedAt: now.Unix(),
	}
	if expiresInDays > 0 {
		t.ExpiresAt = now.Add(time.Duration(expiresInDays) * 24 * time.Hour).Unix()
	}

	apiTokenMu.Lock()
	defer apiTokenMu.Unlock()
	if len(config.APITokens) >= apiTokenMax {
		return apiTokenCreated{}, fmt.Errorf("at most %d tokens; delete one first", apiTokenMax)
	}
	for _, old := range config.APITokens {
		if old.ID == t.ID {
			return apiTokenCreated{}, errors.New("token id collision; try again")
		}
	}
	configLock.Lock()
	config.APITokens = append(config.APITokens, t)
	configLock.Unlock()
	if err := SaveConfig(); err != nil {
		configLock.Lock()
		config.APITokens = slices.DeleteFunc(config.APITokens, func(x APIToken) bool { return x.ID == t.ID })
		configLock.Unlock()
		return apiTokenCreated{}, fmt.Errorf("failed to save config: %w", err)
	}
	logger.Info().Str("token", t.ID).Strs("scopes", t.Scopes).Int64("expires_at", t.ExpiresAt).Msg("API token created")
	return apiTokenCreated{
		Token: apiTokenPrefix + t.ID + "_" + secretText,
		Info:  t.info(now.Unix()),
	}, nil
}

func rpcDeleteAPIToken(id string) error {
	apiTokenMu.Lock()
	defer apiTokenMu.Unlock()
	configLock.Lock()
	before := config.APITokens
	after := slices.DeleteFunc(slices.Clone(before), func(t APIToken) bool { return t.ID == id })
	if len(after) == len(before) {
		configLock.Unlock()
		return errors.New("no such token")
	}
	config.APITokens = after
	configLock.Unlock()
	if err := SaveConfig(); err != nil {
		configLock.Lock()
		config.APITokens = before
		configLock.Unlock()
		return fmt.Errorf("failed to save config: %w", err)
	}
	delete(apiTokenUsed, id)
	logger.Info().Str("token", id).Msg("API token deleted")
	return nil
}

func apiTokenHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

var (
	errAPITokenInvalid = errors.New("the API token is not valid")
	errAPITokenExpired = errors.New("the API token has expired; create a new one in Settings > MCP")
	errAPITokenClock   = errors.New("the KVM clock is not set yet, so a token with an expiry cannot be checked; try again in a minute")
)

// checkAPIToken finds the token and checks its secret and expiry. It returns
// the stored token; the caller checks the scope.
func checkAPIToken(token string) (APIToken, error) {
	rest, ok := strings.CutPrefix(token, apiTokenPrefix)
	if !ok {
		return APIToken{}, errAPITokenInvalid
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || len(id) != 8 || secret == "" {
		return APIToken{}, errAPITokenInvalid
	}
	want := apiTokenHash(secret)
	t, save, err := findAPIToken(id, want)
	if save {
		if err := SaveConfig(); err != nil {
			logger.Warn().Err(err).Msg("failed to save API token use time")
		}
	}
	return t, err
}

// findAPIToken checks the token under the lock; save says the config should
// be written for the use time.
func findAPIToken(id, want string) (APIToken, bool, error) {
	apiTokenMu.Lock()
	defer apiTokenMu.Unlock()
	var found *APIToken
	for i := range config.APITokens {
		if config.APITokens[i].ID == id {
			found = &config.APITokens[i]
			break
		}
	}
	if found == nil || subtle.ConstantTimeCompare([]byte(found.Hash), []byte(want)) != 1 {
		return APIToken{}, false, errAPITokenInvalid
	}
	now := apiTokenNow()
	if found.ExpiresAt != 0 {
		// A clock that is not set, or earlier than the token itself, cannot
		// say whether the token has expired: refuse rather than guess.
		if !clockSet(now) || now.Unix() < found.CreatedAt-86400 {
			return APIToken{}, false, errAPITokenClock
		}
		if now.Unix() >= found.ExpiresAt {
			return APIToken{}, false, errAPITokenExpired
		}
	}
	apiTokenUsed[found.ID] = now.Unix()
	save := false
	if clockSet(now) && now.Unix()-found.LastUsedAt >= int64(apiTokenUsedSave/time.Second) {
		configLock.Lock()
		found.LastUsedAt = now.Unix()
		configLock.Unlock()
		save = true
	}
	return *found, save, nil
}

// warnAPITokenRefused logs a refused token by its ID, never its secret, so
// probing leaves a trace.
func warnAPITokenRefused(remote, token string, err error) {
	id := ""
	if rest, ok := strings.CutPrefix(token, apiTokenPrefix); ok {
		if before, _, found := strings.Cut(rest, "_"); found && len(before) == 8 {
			id = before
		}
	}
	logger.Warn().Str("remote", remote).Str("token", id).Err(err).Msg("API token refused")
}

// apiAuthMiddleware lets a request in with an API token that has the scope,
// or with the browser's sign-in cookie when the KVM has a password. In
// noPassword mode a token is still needed: the APIs are for programs, and a
// program should hold a token that can be taken back.
func apiAuthMiddleware(scope, realm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if bearer, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); ok {
			t, err := checkAPIToken(strings.TrimSpace(bearer))
			if err != nil {
				warnAPITokenRefused(c.ClientIP(), strings.TrimSpace(bearer), err)
				c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", error="invalid_token"`)
				sendErrorJsonThenAbort(c, http.StatusUnauthorized, err.Error())
				return
			}
			if !slices.Contains(t.Scopes, scope) {
				sendErrorJsonThenAbort(c, http.StatusForbidden,
					fmt.Sprintf("the API token %q does not have the %s scope", t.Name, scope))
				return
			}
			if t.ExpiresAt != 0 {
				c.Header(apiTokenHeaderExp, time.Unix(t.ExpiresAt, 0).UTC().Format(time.RFC3339))
			}
			c.Set("apiToken", t.ID)
			c.Set(apiTokenExpiresKey, t.ExpiresAt)
			c.Next()
			return
		}
		if config.LocalAuthMode != "noPassword" {
			switch checkLocalAuthCookie(c) {
			case localAuthCookieValid:
				c.Next()
				return
			case localAuthCookieExpired:
				c.SetCookie("authToken", "", -1, "/", "", false, true)
			}
		}
		c.Header("WWW-Authenticate", `Bearer realm="`+realm+`"`)
		sendErrorJsonThenAbort(c, http.StatusUnauthorized,
			"an API token with the "+scope+" scope is needed (Settings > MCP)")
	}
}
