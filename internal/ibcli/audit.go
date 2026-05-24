package ibcli

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	auditLogMethodFile            = "file"
	auditLogMethodSyslog          = "syslog"
	auditLogMethodWindowsEventLog = "windows_eventlog"
	auditRedactedValue            = "[REDACTED]"
)

var auditNow = time.Now

type auditEvent struct {
	TS         string         `json:"ts"`
	LocalTime  string         `json:"local_time"`
	Timezone   string         `json:"timezone"`
	App        string         `json:"app"`
	EventType  string         `json:"event_type"`
	Host       string         `json:"host"`
	User       string         `json:"user"`
	SSOProvider string         `json:"sso_provider,omitempty"`
	SSOUser     string         `json:"sso_user,omitempty"`
	SSOTenant   string         `json:"sso_tenant,omitempty"`
	SSOSubject  string         `json:"sso_subject,omitempty"`
	SSOObjectID string         `json:"sso_object_id,omitempty"`
	Profile    string         `json:"profile"`
	Action     string         `json:"action"`
	Operation  string         `json:"operation"`
	TargetType string         `json:"target_type"`
	Target     string         `json:"target"`
	Result     string         `json:"result"`
	Data       map[string]any `json:"data"`
}

func defaultAuditLogMethod() string {
	if runtime.GOOS == "windows" {
		return auditLogMethodWindowsEventLog
	}
	return auditLogMethodFile
}

func supportedAuditLogMethods() []string {
	if runtime.GOOS == "windows" {
		return []string{auditLogMethodWindowsEventLog, auditLogMethodFile}
	}
	if runtime.GOOS == "linux" {
		return []string{auditLogMethodFile, auditLogMethodSyslog}
	}
	return []string{auditLogMethodFile}
}

func normalizeAuditLogMethod(method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	switch method {
	case auditLogMethodFile, auditLogMethodSyslog, auditLogMethodWindowsEventLog:
		return method
	default:
		return ""
	}
}

func (a *App) defaultAuditLogFile() string {
	location := a.currentConfigLocation()
	if location.Scope == globalConfigScope {
		return filepath.Join(a.globalConfigLocation().Dir, "audit.jsonl")
	}
	if location.Dir != "" {
		return filepath.Join(location.Dir, "audit.jsonl")
	}
	return filepath.Join(a.localConfigLocation().Dir, "audit.jsonl")
}

func (a *App) activeConfigScopeName() string {
	scope := a.currentConfigLocation().Scope
	if scope == "" {
		scope = localConfigScope
	}
	return string(scope)
}

func (a *App) emitAuditEvent(profileName string, action, operation, targetType, target string, data map[string]any) {
	a.emitAuditEventWithSettings(a.configSettings(), profileName, action, operation, targetType, target, data)
}

func (a *App) emitAuditEventWithSettings(settings ConfigSettings, profileName string, action, operation, targetType, target string, data map[string]any) {
	settings = settings.complete()
	if !settings.AuditLoggingEnabled {
		return
	}
	if profileName == "" {
		profileName = defaultProfileName
	}
	now := auditNow()
	local := now.Local()
	timezone, _ := local.Zone()
	if timezone == "" {
		timezone = "UTC"
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	sso := a.auditSSOIdentity(settings)
	event := auditEvent{
		TS:         now.UTC().Format(time.RFC3339Nano),
		LocalTime:  local.Format(time.RFC3339Nano),
		Timezone:   timezone,
		App:        "ib",
		EventType:  "audit",
		Host:       host,
		User:       currentAuditUser(),
		SSOProvider: sso.Provider,
		SSOUser:     sso.DisplayName(),
		SSOTenant:   sso.TenantID,
		SSOSubject:  sso.Subject,
		SSOObjectID: sso.ObjectID,
		Profile:    profileName,
		Action:     action,
		Operation:  operation,
		TargetType: targetType,
		Target:     target,
		Result:     "success",
		Data:       redactAuditData(data),
	}
	line, err := json.Marshal(event)
	if err != nil {
		a.PrintWarning("WARNING: audit logging failed: " + err.Error())
		return
	}
	if err := a.writeAuditLine(settings, line); err != nil {
		a.PrintWarning("WARNING: audit logging failed: " + err.Error())
	}
}

func currentAuditUser() string {
	for _, name := range []string{
		os.Getenv("USER"),
		os.Getenv("LOGNAME"),
		os.Getenv("USERNAME"),
	} {
		if strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return "unknown"
}

func (a *App) writeAuditLine(settings ConfigSettings, line []byte) error {
	if a.auditSink != nil {
		return a.auditSink(settings, line)
	}
	switch settings.AuditLogMethod {
	case auditLogMethodSyslog:
		return writeAuditSyslog(line)
	case auditLogMethodWindowsEventLog:
		return writeAuditWindowsEventLog(line)
	default:
		path := strings.TrimSpace(settings.AuditLogFile)
		if path == "" {
			path = a.defaultAuditLogFile()
		}
		return a.writeAuditFile(path, line)
	}
}

func (a *App) writeAuditFile(path string, line []byte) error {
	if strings.TrimSpace(path) == "" {
		path = a.defaultAuditLogFile()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if a.activeConfigIsGlobal() {
		_ = a.protectAuditLogFile(path)
	}
	return nil
}

func (a *App) testAuditLogFileWritable(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return cliError("audit log file is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(nil); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	testFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".write-test-*")
	if err != nil {
		return err
	}
	testPath := testFile.Name()
	if _, err := testFile.Write([]byte("test\n")); err != nil {
		_ = testFile.Close()
		_ = os.Remove(testPath)
		return err
	}
	if err := testFile.Close(); err != nil {
		_ = os.Remove(testPath)
		return err
	}
	if err := os.Remove(testPath); err != nil {
		return err
	}
	if a.activeConfigIsGlobal() {
		return a.protectAuditLogFile(path)
	}
	return nil
}

func (a *App) protectAuditLogFile(path string) error {
	group, err := a.activeGlobalConfigGroup()
	if err != nil {
		return err
	}
	if err := chownPathGroupFunc(path, group); err != nil {
		return err
	}
	return os.Chmod(path, 0o660)
}

func redactAuditData(data map[string]any) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	redacted := make(map[string]any, len(data))
	for key, value := range data {
		if auditSensitiveKey(key) {
			redacted[key] = auditRedactedValue
			continue
		}
		redacted[key] = redactAuditValue(value)
	}
	return redacted
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactAuditData(typed)
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = item
		}
		return redactAuditData(converted)
	case []any:
		redacted := make([]any, 0, len(typed))
		for _, item := range typed {
			redacted = append(redacted, redactAuditValue(item))
		}
		return redacted
	case []map[string]any:
		redacted := make([]any, 0, len(typed))
		for _, item := range typed {
			redacted = append(redacted, redactAuditData(item))
		}
		return redacted
	default:
		return typed
	}
}

func auditSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"password", "secret", "token", "credential", "key"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func profileAuditValues(profile Profile) map[string]any {
	profile = profile.complete()
	values := map[string]any{
		"name":         profile.Name,
		"server":       profile.Server,
		"read_server":  profile.ReadServer,
		"username":     profile.Username,
		"password":     profile.Password,
		"wapi_version": profile.WAPIVersion,
		"dns_view":     profile.DNSView,
		"default_zone": profile.DefaultZone,
		"verify_ssl":   profile.VerifySSL,
		"timeout":      profile.Timeout,
	}
	return redactAuditData(values)
}

func auditRecordValues(recordType string, item map[string]any, zone string) map[string]any {
	values := copyAuditMap(item)
	if strings.TrimSpace(zone) != "" {
		values["zone"] = zone
	}
	return recordOutputRow(recordType, values)
}

func copyAuditMap(values map[string]any) map[string]any {
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func applyAuditPayload(item map[string]any, payload map[string]any) map[string]any {
	updated := copyAuditMap(item)
	for key, value := range payload {
		updated[key] = value
	}
	return updated
}

func (a *App) auditConfigProfileCreate(settings ConfigSettings, profile Profile, scope string) {
	a.emitAuditEventWithSettings(settings, profile.Name, "create", "config.profile.create", "CONFIG_PROFILE", profile.Name, map[string]any{
		"scope":      scope,
		"new_values": profileAuditValues(profile),
	})
}

func (a *App) auditConfigProfileEdit(settings ConfigSettings, oldProfile, newProfile Profile) {
	a.emitAuditEventWithSettings(settings, newProfile.Name, "edit", "config.profile.edit", "CONFIG_PROFILE", newProfile.Name, map[string]any{
		"scope":      a.activeConfigScopeName(),
		"old_values": profileAuditValues(oldProfile),
		"new_values": profileAuditValues(newProfile),
	})
}

func (a *App) auditConfigProfileDelete(settings ConfigSettings, profile Profile, scope string) {
	a.emitAuditEventWithSettings(settings, profile.Name, "delete", "config.profile.delete", "CONFIG_PROFILE", profile.Name, map[string]any{
		"scope":          scope,
		"deleted_values": profileAuditValues(profile),
	})
}

func (a *App) auditDNSRecordCreate(profile Profile, client *WapiClient, recordType, target, zone string, payload map[string]any) {
	a.emitAuditEvent(profile.Name, "create", "dns.record.create", "DNS_RECORD", target, map[string]any{
		"view":       client.View,
		"new_values": auditRecordValues(recordType, payload, zone),
	})
}

func (a *App) auditDNSRecordEdit(profile Profile, client *WapiClient, record TypedRecord, payload map[string]any) {
	target := recordName(record.Item, record.Type)
	if target == "" {
		target = cleanString(record.Item["name"])
	}
	newItem := applyAuditPayload(record.Item, payload)
	a.emitAuditEvent(profile.Name, "edit", "dns.record.edit", "DNS_RECORD", target, map[string]any{
		"view":       client.View,
		"old_values": auditRecordValues(record.Type, record.Item, cleanString(record.Item["zone"])),
		"new_values": auditRecordValues(record.Type, newItem, cleanString(record.Item["zone"])),
	})
}

func (a *App) auditDNSRecordDelete(profile Profile, client *WapiClient, record TypedRecord, target string) {
	if target == "" {
		target = recordName(record.Item, record.Type)
	}
	a.emitAuditEvent(profile.Name, "delete", "dns.record.delete", "DNS_RECORD", target, map[string]any{
		"view":           client.View,
		"deleted_values": auditRecordValues(record.Type, record.Item, cleanString(record.Item["zone"])),
	})
}

func (a *App) auditDNSPTRSideEffect(profile Profile, client *WapiClient, action string, address netip.Addr, ptrdname, zone string, oldRecord *TypedRecord) {
	data := map[string]any{"view": client.View}
	item := map[string]any{
		"zone":     zone,
		"ptrdname": cleanDNSName(ptrdname),
	}
	if strings.Contains(address.String(), ":") {
		item["ipv6addr"] = address.String()
	} else {
		item["ipv4addr"] = address.String()
	}
	switch action {
	case "delete":
		deleted := item
		if oldRecord != nil {
			deleted = oldRecord.Item
		}
		data["deleted_values"] = auditRecordValues("ptr", deleted, zone)
	default:
		if oldRecord != nil {
			data["old_values"] = auditRecordValues("ptr", oldRecord.Item, zone)
			data["new_values"] = auditRecordValues("ptr", applyAuditPayload(oldRecord.Item, item), zone)
		} else {
			data["new_values"] = auditRecordValues("ptr", item, zone)
		}
	}
	a.emitAuditEvent(profile.Name, action, "dns.record.ptr_sync", "DNS_RECORD", address.String(), data)
}
