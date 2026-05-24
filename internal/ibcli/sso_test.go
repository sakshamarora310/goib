package ibcli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIdentityFromIDTokenValidatesAudienceAndGroups(t *testing.T) {
	clientID := "11111111-2222-3333-4444-555555555555"
	groupID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	token := unsignedJWT(t, map[string]any{
		"aud":                clientID,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"preferred_username": "alex@example.com",
		"name":               "Alex Example",
		"tid":                "99999999-8888-7777-6666-555555555555",
		"oid":                "12345678-1234-1234-1234-123456789abc",
		"sub":                "subject-value",
		"groups":             []string{groupID},
	})

	identity, err := identityFromIDToken(token, clientID)
	if err != nil {
		t.Fatalf("identity from token: %v", err)
	}
	if identity.DisplayName() != "alex@example.com" || identity.TenantID == "" || identity.ObjectID == "" {
		t.Fatalf("identity = %#v", identity)
	}
	settings := defaultConfigSettings()
	settings.SSOEnabled = true
	settings.ssoEnabledSet = true
	settings.SSOClientID = clientID
	settings.SSOTenantID = identity.TenantID
	settings.SSOAllowedGroups = groupID
	if err := validateSSOIdentity(settings, identity); err != nil {
		t.Fatalf("validate allowed group: %v", err)
	}

	settings.SSOAllowedGroups = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if err := validateSSOIdentity(settings, identity); err == nil || !strings.Contains(err.Error(), "not in an allowed group") {
		t.Fatalf("validate disallowed group error = %v", err)
	}
}

func TestWapiClientRunsSSOPreflightBeforeRequest(t *testing.T) {
	serverHit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHit = true
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	preflightErr := errors.New("sso required")
	client := &WapiClient{
		Server:        server.URL,
		WAPIVersion:  defaultWAPIVersion,
		Username:     "admin",
		Password:     "secret",
		httpClient:   server.Client(),
		beforeRequest: func() error { return preflightErr },
	}
	_, err := client.Request(http.MethodGet, gridObject, url.Values{}, nil)
	if !errors.Is(err, preflightErr) {
		t.Fatalf("request error = %v, want preflight error", err)
	}
	if serverHit {
		t.Fatalf("WAPI server was called after failed SSO preflight")
	}
}

func TestAuthConfigurePersistsSSOSettings(t *testing.T) {
	app := testApp(t)
	writeConfigForSettings(t, app, defaultConfigSettings())

	if err := app.runAuthConfigure(authConfigureOptions{
		TenantID:             "contoso.onmicrosoft.com",
		ClientID:             "11111111-2222-3333-4444-555555555555",
		AllowedGroups:        "group-a, group-b",
		AllowedGroupsChanged: true,
	}); err != nil {
		t.Fatalf("auth configure: %v", err)
	}
	settings, _, err := app.readConfigSettings()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !settings.SSOEnabled || settings.SSOTenantID != "contoso.onmicrosoft.com" || settings.SSOClientID == "" {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.SSOScopes != defaultSSOScopes {
		t.Fatalf("scopes = %q", settings.SSOScopes)
	}
	if settings.SSOAllowedGroups != "group-a,group-b" {
		t.Fatalf("allowed groups = %q", settings.SSOAllowedGroups)
	}
}

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "none", "typ": "JWT"}
	headerRaw, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsRaw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw) + "."
}
