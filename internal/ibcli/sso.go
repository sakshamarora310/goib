package ibcli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	ssoProviderAzure     = "azure"
	ssoTokenCacheName    = "sso-token.json"
	ssoLoginTimeout      = 5 * time.Minute
	ssoTokenSkew         = 5 * time.Minute
	azureAuthorizeFormat = "https://login.microsoftonline.com/%s/oauth2/v2.0/authorize"
	azureTokenFormat     = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
)

type ssoIdentity struct {
	Provider    string   `json:"provider"`
	Username    string   `json:"username"`
	Name        string   `json:"name,omitempty"`
	Email       string   `json:"email,omitempty"`
	TenantID    string   `json:"tenant_id,omitempty"`
	ClientID    string   `json:"client_id,omitempty"`
	ObjectID    string   `json:"object_id,omitempty"`
	Subject     string   `json:"subject,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	GroupOverage bool     `json:"group_overage,omitempty"`
	ExpiresAt    int64    `json:"expires_at,omitempty"`
}

type ssoTokenCache struct {
	Provider     string      `json:"provider"`
	TenantID     string      `json:"tenant_id"`
	ClientID     string      `json:"client_id"`
	Scopes       string      `json:"scopes"`
	AccessToken  string      `json:"access_token,omitempty"`
	IDToken      string      `json:"id_token,omitempty"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	ExpiresAt    int64       `json:"expires_at"`
	Identity     ssoIdentity `json:"identity"`
}

type azureTokenResponse struct {
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int    `json:"expires_in"`
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type ssoCallbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

func (a *App) authCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Azure SSO",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAuthStatus()
		},
	}

	configureCmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure Azure SSO for WAPI command gating",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID, _ := cmd.Flags().GetString("tenant-id")
			clientID, _ := cmd.Flags().GetString("client-id")
			scopes, _ := cmd.Flags().GetString("scopes")
			allowedGroups, _ := cmd.Flags().GetString("allowed-groups")
			return a.runAuthConfigure(authConfigureOptions{
				TenantID:             tenantID,
				ClientID:             clientID,
				Scopes:               scopes,
				AllowedGroups:        allowedGroups,
				AllowedGroupsChanged: cmd.Flags().Changed("allowed-groups"),
			})
		},
	}
	configureCmd.Flags().String("tenant-id", "", "Azure tenant ID or domain")
	configureCmd.Flags().String("client-id", "", "Azure app registration client ID")
	configureCmd.Flags().String("scopes", "", "Azure scopes for login")
	configureCmd.Flags().String("allowed-groups", "", "comma-separated Azure group object IDs allowed to use ib")
	cmd.AddCommand(configureCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Open a browser and sign in with Azure SSO",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAuthLogin()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Remove cached Azure SSO tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAuthLogout()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show Azure SSO configuration and cached login status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAuthStatus()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "disable",
		Short: "Disable Azure SSO gating",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAuthDisable()
		},
	})
	return cmd
}

type authConfigureOptions struct {
	TenantID             string
	ClientID             string
	Scopes               string
	AllowedGroups        string
	AllowedGroupsChanged bool
}

func (a *App) runAuthConfigure(options authConfigureOptions) error {
	var saved ConfigSettings
	if err := a.updateAuthSettings(func(settings ConfigSettings) (ConfigSettings, error) {
		settings = settings.complete()
		tenantID := strings.TrimSpace(options.TenantID)
		if tenantID == "" {
			tenantID = settings.SSOTenantID
		}
		clientID := strings.TrimSpace(options.ClientID)
		if clientID == "" {
			clientID = settings.SSOClientID
		}
		scopes := normalizeSpaceList(options.Scopes)
		if scopes == "" {
			scopes = settings.SSOScopes
		}
		if scopes == "" {
			scopes = defaultSSOScopes
		}
		allowedGroups := settings.SSOAllowedGroups
		if options.AllowedGroupsChanged {
			allowedGroups = normalizeCommaList(options.AllowedGroups)
		}
		if tenantID == "" && a.gum != nil && a.gum.interactive() {
			value, err := a.gum.Input("Azure tenant ID or domain", "", false)
			if err != nil {
				return settings, err
			}
			tenantID = value
		}
		if clientID == "" && a.gum != nil && a.gum.interactive() {
			value, err := a.gum.Input("Azure client ID", "", false)
			if err != nil {
				return settings, err
			}
			clientID = value
		}
		if tenantID == "" || clientID == "" {
			return settings, cliError("Azure SSO requires --tenant-id and --client-id")
		}
		settings.SSOEnabled = true
		settings.ssoEnabledSet = true
		settings.SSOTenantID = tenantID
		settings.SSOClientID = clientID
		settings.SSOScopes = scopes
		settings.SSOAllowedGroups = allowedGroups
		saved = settings.complete()
		return saved, nil
	}); err != nil {
		return err
	}
	if a.isTableOutput() {
		a.PrintSuccess("SUCCESS: Azure SSO configured.")
		a.PrintNote("Register a public-client redirect URI for http://localhost/callback in the Azure app.")
		a.PrintNote("Run: ib auth login")
		return nil
	}
	return a.emitObject("Auth", authStatusFields(), authStatusRow(saved, ssoIdentity{}, false, "configured"))
}

func (a *App) runAuthDisable() error {
	if err := a.updateAuthSettings(func(settings ConfigSettings) (ConfigSettings, error) {
		settings.SSOEnabled = false
		settings.ssoEnabledSet = true
		return settings.complete(), nil
	}); err != nil {
		return err
	}
	if a.isTableOutput() {
		a.PrintSuccess("SUCCESS: Azure SSO gating disabled.")
		return nil
	}
	return a.emitObject("Auth", authStatusFields(), map[string]any{"enabled": false, "message": "Azure SSO gating disabled"})
}

func (a *App) updateAuthSettings(update func(ConfigSettings) (ConfigSettings, error)) error {
	exists, err := a.useDefaultConfigLocation()
	if err != nil {
		return err
	}
	if !exists {
		return cliError("no Infoblox profile configured; run: ib config new [PROFILE]")
	}
	defaultProfile, profiles, _, err := a.readConfigProfiles(true)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return cliError("no Infoblox profile configured; run: ib config new [PROFILE]")
	}
	settings, _, err := a.readConfigSettings()
	if err != nil {
		return err
	}
	settings, err = update(settings)
	if err != nil {
		return err
	}
	if err := a.writeConfigProfilesWithSettings(defaultProfile, profiles, settings); err != nil {
		return err
	}
	a.ssoIdentity = nil
	a.ssoIdentityKey = ""
	return nil
}

func (a *App) runAuthLogin() error {
	settings := a.configSettings().complete()
	if !settings.SSOEnabled {
		return cliError("Azure SSO is not configured; run: ib auth configure --tenant-id TENANT --client-id CLIENT")
	}
	if err := validateSSOSettings(settings); err != nil {
		return err
	}
	identity, err := a.loginAzureSSO(settings)
	if err != nil {
		return err
	}
	if a.isTableOutput() {
		a.PrintSuccess("SUCCESS: signed in as " + identity.DisplayName())
		return nil
	}
	return a.emitObject("Auth", authStatusFields(), authStatusRow(settings, identity, true, "signed in"))
}

func (a *App) runAuthLogout() error {
	if err := os.Remove(a.ssoCachePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	a.ssoIdentity = nil
	a.ssoIdentityKey = ""
	if a.isTableOutput() {
		a.PrintSuccess("SUCCESS: Azure SSO token cache cleared.")
		return nil
	}
	return a.emitObject("Auth", authStatusFields(), map[string]any{"authenticated": false, "message": "token cache cleared"})
}

func (a *App) runAuthStatus() error {
	settings := a.configSettings().complete()
	identity, authenticated := a.cachedSSOIdentity(settings)
	message := "not configured"
	if settings.SSOEnabled {
		message = "login required"
		if authenticated {
			message = "signed in"
		}
	}
	return a.emitObject("Azure SSO Status", authStatusFields(), authStatusRow(settings, identity, authenticated, message))
}

func authStatusFields() []string {
	return []string{"enabled", "authenticated", "user", "tenant_id", "client_id", "expires_at", "allowed_groups", "message"}
}

func authStatusRow(settings ConfigSettings, identity ssoIdentity, authenticated bool, message string) map[string]any {
	settings = settings.complete()
	expiresAt := ""
	if identity.ExpiresAt > 0 {
		expiresAt = time.Unix(identity.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"enabled":        settings.SSOEnabled,
		"authenticated":  authenticated,
		"user":           identity.DisplayName(),
		"tenant_id":      firstNonEmpty(identity.TenantID, settings.SSOTenantID),
		"client_id":      settings.SSOClientID,
		"expires_at":     expiresAt,
		"allowed_groups": settings.SSOAllowedGroups,
		"message":        message,
	}
}

func (a *App) ensureSSOAuthenticated() error {
	settings := a.configSettings().complete()
	if !settings.SSOEnabled {
		return nil
	}
	return a.ensureSSOAuthenticatedWithSettings(settings, a.ssoInteractiveAllowed())
}

func (a *App) ensureSSOAuthenticatedWithSettings(settings ConfigSettings, allowInteractive bool) error {
	settings = settings.complete()
	if err := validateSSOSettings(settings); err != nil {
		return err
	}
	key := ssoSettingsKey(settings)
	if a.ssoIdentity != nil && a.ssoIdentityKey == key && !ssoExpired(a.ssoIdentity.ExpiresAt) {
		return nil
	}
	cache, err := a.readSSOTokenCache()
	if err == nil && cache.matches(settings) {
		if !ssoExpired(cache.ExpiresAt) {
			if err := validateSSOIdentity(settings, cache.Identity); err != nil {
				return err
			}
			a.setSSOIdentity(key, cache.Identity)
			return nil
		}
		if strings.TrimSpace(cache.RefreshToken) != "" {
			if identity, refreshErr := a.refreshAzureSSO(settings, cache.RefreshToken); refreshErr == nil {
				a.setSSOIdentity(key, identity)
				return nil
			}
		}
	}
	if !allowInteractive {
		return cliError("Azure SSO login required; run: ib auth login")
	}
	identity, err := a.loginAzureSSO(settings)
	if err != nil {
		return err
	}
	a.setSSOIdentity(key, identity)
	return nil
}

func (a *App) ssoInteractiveAllowed() bool {
	return a.gum != nil && a.gum.interactive()
}

func validateSSOSettings(settings ConfigSettings) error {
	settings = settings.complete()
	if !settings.SSOEnabled {
		return nil
	}
	if settings.SSOTenantID == "" || settings.SSOClientID == "" {
		return cliError("Azure SSO is enabled but tenant/client settings are missing; run: ib auth configure --tenant-id TENANT --client-id CLIENT")
	}
	return nil
}

func (a *App) setSSOIdentity(key string, identity ssoIdentity) {
	a.ssoIdentity = &identity
	a.ssoIdentityKey = key
}

func (a *App) cachedSSOIdentity(settings ConfigSettings) (ssoIdentity, bool) {
	settings = settings.complete()
	if !settings.SSOEnabled {
		return ssoIdentity{}, false
	}
	key := ssoSettingsKey(settings)
	if a.ssoIdentity != nil && a.ssoIdentityKey == key && !ssoExpired(a.ssoIdentity.ExpiresAt) {
		return *a.ssoIdentity, true
	}
	cache, err := a.readSSOTokenCache()
	if err != nil || !cache.matches(settings) || ssoExpired(cache.ExpiresAt) {
		return ssoIdentity{}, false
	}
	if err := validateSSOIdentity(settings, cache.Identity); err != nil {
		return ssoIdentity{}, false
	}
	return cache.Identity, true
}

func (a *App) auditSSOIdentity(settings ConfigSettings) ssoIdentity {
	identity, ok := a.cachedSSOIdentity(settings)
	if !ok {
		return ssoIdentity{}
	}
	return identity
}

func ssoSettingsKey(settings ConfigSettings) string {
	settings = settings.complete()
	return strings.Join([]string{
		settings.SSOTenantID,
		settings.SSOClientID,
		settings.SSOScopes,
		settings.SSOAllowedGroups,
	}, "\x00")
}

func ssoExpired(expiresAt int64) bool {
	if expiresAt <= 0 {
		return true
	}
	return time.Now().Add(ssoTokenSkew).Unix() >= expiresAt
}

func (c ssoTokenCache) matches(settings ConfigSettings) bool {
	settings = settings.complete()
	return c.Provider == ssoProviderAzure &&
		strings.EqualFold(c.TenantID, settings.SSOTenantID) &&
		strings.EqualFold(c.ClientID, settings.SSOClientID) &&
		normalizeSpaceList(c.Scopes) == settings.SSOScopes
}

func (i ssoIdentity) DisplayName() string {
	for _, value := range []string{i.Username, i.Email, i.Name, i.ObjectID, i.Subject} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *App) loginAzureSSO(settings ConfigSettings) (ssoIdentity, error) {
	settings = settings.complete()
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return ssoIdentity{}, err
	}
	defer listener.Close()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddr.Port <= 0 {
		return ssoIdentity{}, cliError("could not allocate local Azure SSO callback port")
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", tcpAddr.Port)
	state, err := randomURLToken(32)
	if err != nil {
		return ssoIdentity{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return ssoIdentity{}, err
	}
	challenge := pkceChallenge(verifier)
	resultCh := make(chan ssoCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		result := ssoCallbackResult{
			Code:             r.URL.Query().Get("code"),
			State:            r.URL.Query().Get("state"),
			Error:            r.URL.Query().Get("error"),
			ErrorDescription: r.URL.Query().Get("error_description"),
		}
		if result.State != state {
			result.Error = "invalid_state"
			result.ErrorDescription = "Azure SSO returned an unexpected state value."
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, authCallbackHTML(result.Error == "", result.ErrorDescription))
		select {
		case resultCh <- result:
		default:
		}
	})
	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	authURL := azureAuthorizeURL(settings, redirectURI, state, challenge)
	if a.browserOpener == nil {
		a.browserOpener = openBrowser
	}
	if err := a.browserOpener(authURL); err != nil {
		a.PrintWarning("WARNING: could not open browser automatically: " + err.Error())
	}
	a.PrintNote("Azure SSO login URL: " + authURL)
	a.PrintNote("Waiting for Azure SSO callback...")

	select {
	case result := <-resultCh:
		if result.Error != "" {
			return ssoIdentity{}, cliError("Azure SSO failed: %s", firstNonEmpty(result.ErrorDescription, result.Error))
		}
		if strings.TrimSpace(result.Code) == "" {
			return ssoIdentity{}, cliError("Azure SSO did not return an authorization code")
		}
		token, err := a.redeemAzureCode(settings, redirectURI, verifier, result.Code)
		if err != nil {
			return ssoIdentity{}, err
		}
		return a.saveAzureToken(settings, token)
	case <-time.After(ssoLoginTimeout):
		return ssoIdentity{}, cliError("Azure SSO timed out waiting for browser callback")
	}
}

func authCallbackHTML(success bool, detail string) string {
	title := "Azure SSO complete"
	body := "You can return to the ib terminal."
	if !success {
		title = "Azure SSO failed"
		body = firstNonEmpty(detail, "The ib terminal has the details.")
	}
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) + "</title></head><body><h1>" + html.EscapeString(title) + "</h1><p>" + html.EscapeString(body) + "</p></body></html>"
}

func azureAuthorizeURL(settings ConfigSettings, redirectURI, state, challenge string) string {
	values := url.Values{}
	values.Set("client_id", settings.SSOClientID)
	values.Set("response_type", "code")
	values.Set("redirect_uri", redirectURI)
	values.Set("response_mode", "query")
	values.Set("scope", settings.SSOScopes)
	values.Set("state", state)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	return fmt.Sprintf(azureAuthorizeFormat, url.PathEscape(settings.SSOTenantID)) + "?" + values.Encode()
}

func (a *App) redeemAzureCode(settings ConfigSettings, redirectURI, verifier, code string) (azureTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", settings.SSOClientID)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	form.Set("scope", settings.SSOScopes)
	return requestAzureToken(settings, form)
}

func (a *App) refreshAzureSSO(settings ConfigSettings, refreshToken string) (ssoIdentity, error) {
	form := url.Values{}
	form.Set("client_id", settings.SSOClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("scope", settings.SSOScopes)
	token, err := requestAzureToken(settings, form)
	if err != nil {
		return ssoIdentity{}, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return a.saveAzureToken(settings, token)
}

func requestAzureToken(settings ConfigSettings, form url.Values) (azureTokenResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf(azureTokenFormat, url.PathEscape(settings.SSOTenantID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return azureTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return azureTokenResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return azureTokenResponse{}, err
	}
	var token azureTokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return azureTokenResponse{}, cliError("Azure token endpoint returned non-JSON response: %s", responseSnippet(raw))
	}
	if resp.StatusCode >= 400 || token.Error != "" {
		return azureTokenResponse{}, cliError("Azure token request failed: %s", firstNonEmpty(token.ErrorDescription, token.Error, resp.Status))
	}
	if strings.TrimSpace(token.IDToken) == "" {
		return azureTokenResponse{}, cliError("Azure token response did not include an ID token; include openid in sso_azure_scopes")
	}
	return token, nil
}

func (a *App) saveAzureToken(settings ConfigSettings, token azureTokenResponse) (ssoIdentity, error) {
	identity, err := identityFromIDToken(token.IDToken, settings.SSOClientID)
	if err != nil {
		return ssoIdentity{}, err
	}
	identity.Provider = ssoProviderAzure
	identity.ClientID = settings.SSOClientID
	if token.ExpiresIn > 0 {
		accessExpiresAt := time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
		if identity.ExpiresAt <= 0 || accessExpiresAt < identity.ExpiresAt {
			identity.ExpiresAt = accessExpiresAt
		}
	}
	if identity.ExpiresAt <= 0 {
		identity.ExpiresAt = jwtExpiresAt(token.IDToken)
	}
	if err := validateSSOIdentity(settings, identity); err != nil {
		return ssoIdentity{}, err
	}
	cache := ssoTokenCache{
		Provider:     ssoProviderAzure,
		TenantID:     settings.SSOTenantID,
		ClientID:     settings.SSOClientID,
		Scopes:       settings.SSOScopes,
		AccessToken:  token.AccessToken,
		IDToken:      token.IDToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    identity.ExpiresAt,
		Identity:     identity,
	}
	if err := a.writeSSOTokenCache(cache); err != nil {
		return ssoIdentity{}, err
	}
	a.setSSOIdentity(ssoSettingsKey(settings), identity)
	return identity, nil
}

func identityFromIDToken(idToken, clientID string) (ssoIdentity, error) {
	claims, err := jwtClaims(idToken)
	if err != nil {
		return ssoIdentity{}, err
	}
	if !claimAudienceContains(claims["aud"], clientID) {
		return ssoIdentity{}, cliError("Azure ID token audience does not match configured client ID")
	}
	expiresAt := int64FromClaim(claims["exp"])
	if expiresAt <= 0 || ssoExpired(expiresAt) {
		return ssoIdentity{}, cliError("Azure ID token is expired")
	}
	identity := ssoIdentity{
		Username:  firstNonEmpty(stringClaim(claims, "preferred_username"), stringClaim(claims, "email"), stringClaim(claims, "upn"), stringClaim(claims, "name")),
		Name:      stringClaim(claims, "name"),
		Email:     firstNonEmpty(stringClaim(claims, "email"), stringClaim(claims, "preferred_username"), stringClaim(claims, "upn")),
		TenantID:  stringClaim(claims, "tid"),
		ObjectID:  stringClaim(claims, "oid"),
		Subject:   stringClaim(claims, "sub"),
		Groups:    stringSliceClaim(claims["groups"]),
		ExpiresAt: expiresAt,
	}
	if identity.Username == "" {
		identity.Username = firstNonEmpty(identity.ObjectID, identity.Subject)
	}
	if boolClaim(claims["hasgroups"]) || mapHasKey(claims["_claim_names"], "groups") {
		identity.GroupOverage = true
	}
	return identity, nil
}

func validateSSOIdentity(settings ConfigSettings, identity ssoIdentity) error {
	settings = settings.complete()
	if !settings.SSOEnabled {
		return nil
	}
	if looksLikeGUID(settings.SSOTenantID) && identity.TenantID != "" && !strings.EqualFold(settings.SSOTenantID, identity.TenantID) {
		return cliError("Azure SSO tenant mismatch: token tenant %s does not match configured tenant %s", identity.TenantID, settings.SSOTenantID)
	}
	allowedGroups := splitCommaList(settings.SSOAllowedGroups)
	if len(allowedGroups) == 0 {
		return nil
	}
	for _, allowed := range allowedGroups {
		for _, group := range identity.Groups {
			if strings.EqualFold(allowed, group) {
				return nil
			}
		}
	}
	if identity.GroupOverage {
		return cliError("Azure SSO token omitted full group claims; configure the app to emit required groups or remove sso_azure_allowed_groups")
	}
	return cliError("Azure SSO user %s is not in an allowed group", identity.DisplayName())
}

func jwtClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, cliError("invalid Azure ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func jwtExpiresAt(token string) int64 {
	claims, err := jwtClaims(token)
	if err != nil {
		return 0
	}
	return int64FromClaim(claims["exp"])
}

func claimAudienceContains(value any, clientID string) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(typed, clientID)
	case []any:
		for _, item := range typed {
			if strings.EqualFold(fmt.Sprint(item), clientID) {
				return true
			}
		}
	}
	return false
}

func stringClaim(claims map[string]any, key string) string {
	value, ok := claims[key]
	if !ok || value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func stringSliceClaim(value any) []string {
	var items []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				items = append(items, text)
			}
		}
	case []string:
		items = append(items, typed...)
	case string:
		if strings.TrimSpace(typed) != "" {
			items = append(items, typed)
		}
	}
	return items
}

func int64FromClaim(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func boolClaim(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseBool(typed, false)
	default:
		return false
	}
}

func mapHasKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		_, ok := typed[key]
		return ok
	default:
		return false
	}
}

func looksLikeGUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func splitCommaList(value string) []string {
	normalized := normalizeCommaList(value)
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, ",")
}

func randomURLToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *App) ssoCachePath() string {
	a.ensureConfigPathDefaults()
	return filepath.Join(a.LocalConfigDir, ssoTokenCacheName)
}

func (a *App) writeSSOTokenCache(cache ssoTokenCache) error {
	if err := a.withConfigLocation(a.localConfigLocation(), func() error {
		var err error
		cache.AccessToken, err = a.encryptPassword(cache.AccessToken)
		if err != nil {
			return err
		}
		cache.IDToken, err = a.encryptPassword(cache.IDToken)
		if err != nil {
			return err
		}
		cache.RefreshToken, err = a.encryptPassword(cache.RefreshToken)
		if err != nil {
			return err
		}
		if err := a.ensureConfigDir(); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(cache, "", "  ")
		if err != nil {
			return err
		}
		path := a.ssoCachePath()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
		return protectPrivateFile(path)
	}); err != nil {
		return err
	}
	return nil
}

func (a *App) readSSOTokenCache() (ssoTokenCache, error) {
	path := a.ssoCachePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return ssoTokenCache{}, err
	}
	var cache ssoTokenCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return ssoTokenCache{}, err
	}
	err = a.withConfigLocation(a.localConfigLocation(), func() error {
		var decryptErr error
		cache.AccessToken, decryptErr = a.decryptPassword(cache.AccessToken)
		if decryptErr != nil {
			return decryptErr
		}
		cache.IDToken, decryptErr = a.decryptPassword(cache.IDToken)
		if decryptErr != nil {
			return decryptErr
		}
		cache.RefreshToken, decryptErr = a.decryptPassword(cache.RefreshToken)
		return decryptErr
	})
	return cache, err
}

var errNoBrowser = errors.New("no browser opener found")
