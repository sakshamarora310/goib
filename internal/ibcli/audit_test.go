package ibcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureAuditEvents(t *testing.T, app *App) *[]auditEvent {
	t.Helper()
	events := []auditEvent{}
	app.auditSink = func(_ ConfigSettings, line []byte) error {
		var event auditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode audit event: %v\n%s", err, line)
		}
		events = append(events, event)
		return nil
	}
	return &events
}

func enableAuditForTest(t *testing.T, app *App, method string) {
	t.Helper()
	defaultProfile, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	settings := defaultConfigSettings()
	settings.AuditLoggingEnabled = true
	settings.auditLoggingEnabledSet = true
	settings.AuditLogMethod = method
	if method == auditLogMethodFile {
		settings.AuditLogFile = filepath.Join(app.ConfigDir, "audit.jsonl")
	}
	if err := app.writeConfigProfilesWithSettings(defaultProfile, profiles, settings); err != nil {
		t.Fatalf("write audit settings: %v", err)
	}
}

func TestAuditEventIncludesUTCAndLocalTimeAndRedactsSecrets(t *testing.T) {
	app := testApp(t)
	events := captureAuditEvents(t, app)
	originalNow := auditNow
	auditNow = func() time.Time {
		return time.Date(2026, 5, 24, 10, 30, 0, 0, time.FixedZone("AEST", 10*60*60))
	}
	t.Cleanup(func() { auditNow = originalNow })

	settings := defaultConfigSettings()
	settings.AuditLoggingEnabled = true
	settings.auditLoggingEnabledSet = true
	settings.AuditLogMethod = auditLogMethodFile
	app.emitAuditEventWithSettings(settings, "demo", "create", "config.profile.create", "CONFIG_PROFILE", "demo", map[string]any{
		"username": "admin",
		"password": "secret-password",
		"nested": map[string]any{
			"api_token": "abc123",
			"server":    "https://infoblox.example",
		},
	})

	if len(*events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(*events))
	}
	event := (*events)[0]
	if event.TS != "2026-05-24T00:30:00Z" {
		t.Fatalf("ts = %q, want UTC timestamp", event.TS)
	}
	for _, value := range []string{event.LocalTime, event.Timezone, event.Host, event.User} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("audit event has blank environment field: %#v", event)
		}
	}
	if event.Action != "create" || event.Operation != "config.profile.create" || event.Target != "demo" {
		t.Fatalf("unexpected audit event identity: %#v", event)
	}
	if event.Data["password"] != auditRedactedValue {
		t.Fatalf("password was not redacted: %#v", event.Data)
	}
	nested, ok := event.Data["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested data type = %T", event.Data["nested"])
	}
	if nested["api_token"] != auditRedactedValue || nested["server"] != "https://infoblox.example" {
		t.Fatalf("nested audit data redaction mismatch: %#v", nested)
	}
}

func TestAuditEventIncludesSSOIdentity(t *testing.T) {
	app := testApp(t)
	events := captureAuditEvents(t, app)
	settings := defaultConfigSettings()
	settings.AuditLoggingEnabled = true
	settings.auditLoggingEnabledSet = true
	settings.AuditLogMethod = auditLogMethodFile
	settings.SSOEnabled = true
	settings.ssoEnabledSet = true
	settings.SSOTenantID = "99999999-8888-7777-6666-555555555555"
	settings.SSOClientID = "11111111-2222-3333-4444-555555555555"
	identity := ssoIdentity{
		Provider:  ssoProviderAzure,
		Username:  "alex@example.com",
		TenantID:  settings.SSOTenantID,
		ClientID:  settings.SSOClientID,
		ObjectID:  "12345678-1234-1234-1234-123456789abc",
		Subject:   "subject-value",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	app.setSSOIdentity(ssoSettingsKey(settings), identity)

	app.emitAuditEventWithSettings(settings, "demo", "create", "dns.record.create", "DNS_RECORD", "app.example.com", nil)

	if len(*events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(*events))
	}
	event := (*events)[0]
	if event.SSOProvider != ssoProviderAzure || event.SSOUser != "alex@example.com" || event.SSOTenant != settings.SSOTenantID {
		t.Fatalf("sso audit fields = %#v", event)
	}
	if event.SSOObjectID != identity.ObjectID || event.SSOSubject != identity.Subject {
		t.Fatalf("sso subject fields = %#v", event)
	}
}

func TestAuditFileSinkWritesJSONL(t *testing.T) {
	app := testApp(t)
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	settings := defaultConfigSettings()
	settings.AuditLoggingEnabled = true
	settings.auditLoggingEnabledSet = true
	settings.AuditLogMethod = auditLogMethodFile
	settings.AuditLogFile = logPath

	app.emitAuditEventWithSettings(settings, "default", "delete", "dns.record.delete", "DNS_RECORD", "app.example.com", map[string]any{
		"deleted_values": map[string]any{"name": "app.example.com"},
	})

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit log lines = %d, want 1:\n%s", len(lines), raw)
	}
	var event auditEvent
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode JSONL event: %v\n%s", err, lines[0])
	}
	if event.Operation != "dns.record.delete" || event.Result != "success" {
		t.Fatalf("unexpected audit file event: %#v", event)
	}
}

func TestDNSCreateEmitsAuditEventAndReadCommandDoesNot(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "record:a" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode("record:a/ref")
	}))
	defer primary.Close()
	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	enableAuditForTest(t, app, auditLogMethodFile)
	events := captureAuditEvents(t, app)

	if err := app.Execute([]string{"dns", "create", "a", "app", "192.0.2.10", "--noptr"}); err != nil {
		t.Fatalf("dns create: %v", err)
	}
	if len(*events) != 1 {
		t.Fatalf("audit events after create = %d, want 1: %#v", len(*events), *events)
	}
	event := (*events)[0]
	if event.Action != "create" || event.Operation != "dns.record.create" || event.TargetType != "DNS_RECORD" {
		t.Fatalf("unexpected create audit event: %#v", event)
	}
	values, ok := event.Data["new_values"].(map[string]any)
	if !ok {
		t.Fatalf("new_values type = %T", event.Data["new_values"])
	}
	if values["name"] != "app.example.com" || values["value"] != "192.0.2.10" {
		t.Fatalf("new_values mismatch: %#v", values)
	}

	if err := app.Execute([]string{"config", "list"}); err != nil {
		t.Fatalf("config list: %v", err)
	}
	if len(*events) != 1 {
		t.Fatalf("read command emitted audit event: %#v", *events)
	}
}

func TestAuditSinkFailureWarnsOnly(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode("record:a/ref")
	}))
	defer primary.Close()
	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	enableAuditForTest(t, app, auditLogMethodFile)
	var stderr bytes.Buffer
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	app.auditSink = func(ConfigSettings, []byte) error {
		return errors.New("sink unavailable")
	}

	if err := app.Execute([]string{"dns", "create", "a", "app", "192.0.2.10", "--noptr"}); err != nil {
		t.Fatalf("dns create with failed audit sink: %v", err)
	}
	if !strings.Contains(stderr.String(), "WARNING: audit logging failed: sink unavailable") {
		t.Fatalf("audit warning missing:\n%s", stderr.String())
	}
}
