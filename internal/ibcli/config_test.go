package ibcli

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	app := &App{
		ConfigDir:           dir,
		ConfigFile:          filepath.Join(dir, "config"),
		ConfigKeyFile:       filepath.Join(dir, "key"),
		LocalConfigDir:      dir,
		LocalConfigFile:     filepath.Join(dir, "config"),
		LocalConfigKeyFile:  filepath.Join(dir, "key"),
		GlobalConfigDir:     globalDir,
		GlobalConfigFile:    filepath.Join(globalDir, "config"),
		GlobalConfigKeyFile: filepath.Join(globalDir, "key"),
		Output:              tableOutput,
		Stdout:              os.Stdout,
		Stderr:              os.Stderr,
		Stdin:               strings.NewReader(""),
	}
	app.backgroundRecordRevalidator = func(Profile, string) error { return nil }
	app.backgroundZoneRefresher = func(Profile) error { return nil }
	app.backgroundNetRefresher = func(Profile, string, string, string) error { return nil }
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app
}

func writePlainTestConfig(t *testing.T, path string, defaultProfile string, profiles map[string]Profile, globalGroup string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	var builder strings.Builder
	builder.WriteString("[meta]\n")
	builder.WriteString("default_profile = " + defaultProfile + "\n")
	builder.WriteString("cache_ttl = 300\n")
	builder.WriteString("dns_search_worker_limit = 16\n")
	builder.WriteString("records_cache_swr_ttl = 259200\n")
	builder.WriteString("max_background_worker_wait = 3\n")
	builder.WriteString("completion_cache_prefetch = true\n")
	builder.WriteString("audit_logging_enabled = false\n")
	builder.WriteString("audit_logging_method = " + defaultAuditLogMethod() + "\n")
	builder.WriteString("audit_log_file = \n")
	builder.WriteString("sso_enabled = false\n")
	builder.WriteString("sso_azure_tenant_id = \n")
	builder.WriteString("sso_azure_client_id = \n")
	builder.WriteString("sso_azure_scopes = " + defaultSSOScopes + "\n")
	builder.WriteString("sso_azure_allowed_groups = \n")
	if globalGroup != "" {
		builder.WriteString("global_group = " + globalGroup + "\n")
	}
	builder.WriteString("\n")
	for name, profile := range profiles {
		profile = profile.complete()
		builder.WriteString("[profile:" + name + "]\n")
		builder.WriteString("server = " + profile.Server + "\n")
		builder.WriteString("read_server = " + profile.ReadServer + "\n")
		builder.WriteString("username = " + profile.Username + "\n")
		builder.WriteString("password = " + profile.Password + "\n")
		builder.WriteString("wapi_version = " + profile.WAPIVersion + "\n")
		builder.WriteString("dns_view = " + profile.DNSView + "\n")
		builder.WriteString("default_zone = " + profile.DefaultZone + "\n")
		builder.WriteString("verify_ssl = " + fmt.Sprint(profile.VerifySSL) + "\n")
		builder.WriteString("timeout = " + fmt.Sprint(profile.Timeout) + "\n\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

func plainTestProfile(name, server string) Profile {
	return Profile{
		Name:        name,
		Server:      server,
		Username:    name + "-user",
		Password:    name + "-secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		DefaultZone: name + ".example",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	got := info.Mode().Perm()
	if want&os.ModeSetgid != 0 {
		got |= info.Mode() & os.ModeSetgid
	}
	if got != want {
		t.Fatalf("mode %s = %#o, want %#o", path, got, want)
	}
}

func TestExecutableIsGoTestBinary(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/tmp/go-build123/internal/ibcli.test", want: true},
		{path: "/tmp/go-build123/ib.test.exe", want: true},
		{path: "/home/rwahyudi/bin/ib", want: false},
		{path: "", want: false},
	} {
		if got := executableIsGoTestBinary(test.path); got != test.want {
			t.Fatalf("executableIsGoTestBinary(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestReadSessionZoneUsesShellPIDEnv(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(sessionParentPIDEnv, "12345")
	app := testApp(t)
	if err := writeSessionValue(sessionFileForPID("active-zones", "active-zone-session", 12345), map[string]any{
		"zone":       "latrobe-test.edu.au",
		"profile":    defaultProfileName,
		"parent_pid": 12345,
	}); err != nil {
		t.Fatalf("write session zone: %v", err)
	}

	if got := app.readSessionZone(defaultProfileName); got != "latrobe-test.edu.au" {
		t.Fatalf("session zone = %q, want latrobe-test.edu.au", got)
	}
}

func TestReadSessionZoneFallsBackToGrandparentPID(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	parentPID := processParentPID(os.Getppid())
	if parentPID <= 0 {
		t.Skip("parent process ID not available")
	}
	app := testApp(t)
	if err := writeSessionValue(sessionFileForPID("active-zones", "active-zone-session", parentPID), map[string]any{
		"zone":       "latrobe-test.edu.au",
		"profile":    defaultProfileName,
		"parent_pid": parentPID,
	}); err != nil {
		t.Fatalf("write session zone: %v", err)
	}

	if got := app.readSessionZone(defaultProfileName); got != "latrobe-test.edu.au" {
		t.Fatalf("session zone = %q, want latrobe-test.edu.au", got)
	}
}

func writeConfigForSettings(t *testing.T, app *App, settings ConfigSettings) {
	t.Helper()
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfilesWithSettings("default", profiles, settings); err != nil {
		t.Fatalf("write config settings: %v", err)
	}
}

func TestProfileCompleteUsesDefaultTimeout(t *testing.T) {
	profile := Profile{}.complete()
	if profile.Timeout != 15 {
		t.Fatalf("timeout = %d, want 15", profile.Timeout)
	}
}

func TestLoadConfigUsesGlobalConfigWhenOnlyGlobalExists(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, defaultProfileName, map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      "https://global.example",
			Username:    "global-user",
			Password:    "global-secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "global.example",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}, "ibusers")

	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load global config: %v", err)
	}
	if profile.Server != "https://global.example" || profile.Username != "global-user" {
		t.Fatalf("profile = %#v", profile)
	}
	if app.ConfigFile != app.GlobalConfigFile {
		t.Fatalf("active config file = %q, want %q", app.ConfigFile, app.GlobalConfigFile)
	}
	if got := app.cachePath(); got != filepath.Join(app.GlobalConfigDir, cacheFileName) {
		t.Fatalf("cache path = %q", got)
	}
}

func TestLoadConfigPrefersLocalDefaultOverGlobal(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.LocalConfigFile, defaultProfileName, map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      "https://local.example",
			Username:    "local-user",
			Password:    "local-secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "local.example",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}, "")
	writePlainTestConfig(t, app.GlobalConfigFile, defaultProfileName, map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      "https://global.example",
			Username:    "global-user",
			Password:    "global-secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "global.example",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}, "ibusers")

	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if profile.Server != "https://local.example" {
		t.Fatalf("server = %q, want local", profile.Server)
	}
	if app.ConfigFile != app.LocalConfigFile {
		t.Fatalf("active config file = %q, want %q", app.ConfigFile, app.LocalConfigFile)
	}
}

func TestLoadConfigExplicitProfileUsesGlobalOnlyProfile(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local": {
			Name:        "local",
			Server:      "https://local.example",
			Username:    "local-user",
			Password:    "local-secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "local.example",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}, "")
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": {
			Name:        "shared",
			Server:      "https://global.example",
			Username:    "global-user",
			Password:    "global-secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "global.example",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}, "ibusers")

	profile, err := app.loadConfigProfile("shared", true)
	if err != nil {
		t.Fatalf("load shared profile: %v", err)
	}
	if profile.Name != "shared" || app.ConfigFile != app.GlobalConfigFile {
		t.Fatalf("profile = %#v, active config = %q", profile, app.ConfigFile)
	}
	if got := app.cachePath(); got != filepath.Join(app.GlobalConfigDir, cacheFileName) {
		t.Fatalf("cache path = %q, want global cache", got)
	}
}

func TestMergedConfigAddsGlobalProfilesAndLocalOverrides(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
		"common": plainTestProfile("common", "https://global-common.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local":  plainTestProfile("local", "https://local.example"),
		"common": plainTestProfile("common", "https://local-common.example"),
	}, "")

	merged, err := app.readMergedConfig(false)
	if err != nil {
		t.Fatalf("read merged config: %v", err)
	}
	if merged.DefaultProfile != "local" {
		t.Fatalf("default profile = %q, want local metadata default", merged.DefaultProfile)
	}
	for _, name := range []string{"local", "shared", "common"} {
		if _, ok := merged.Profiles[name]; !ok {
			t.Fatalf("merged profiles missing %q: %#v", name, merged.Profiles)
		}
	}
	if got := merged.Profiles["common"].Server; got != "https://local-common.example" {
		t.Fatalf("common server = %q, want local override", got)
	}
	if got := merged.ProfileLocations["shared"].File; got != app.GlobalConfigFile {
		t.Fatalf("shared profile location = %q, want global", got)
	}
	if got := merged.ProfileLocations["common"].File; got != app.LocalConfigFile {
		t.Fatalf("common profile location = %q, want local", got)
	}
}

func TestConfigUseCanSelectGlobalOnlyProfile(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"ipamdev": plainTestProfile("ipamdev", "https://ipamdev.example"),
		"shared":  plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local": plainTestProfile("local", "https://local.example"),
	}, "")
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "use", "ipamdev"}); err != nil {
		t.Fatalf("config use global profile: %v", err)
	}
	localDefault, localProfiles, _, err := app.withLocalConfigProfiles()
	if err != nil {
		t.Fatalf("read local profiles: %v", err)
	}
	if localDefault != "ipamdev" {
		t.Fatalf("local default = %q, want ipamdev", localDefault)
	}
	if _, ok := localProfiles["ipamdev"]; ok {
		t.Fatalf("global profile was copied into local config: %#v", localProfiles)
	}
	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load selected global profile: %v", err)
	}
	if profile.Name != "ipamdev" || profile.Server != "https://ipamdev.example" {
		t.Fatalf("loaded profile = %#v", profile)
	}
	if app.ConfigFile != app.GlobalConfigFile {
		t.Fatalf("active config file = %q, want global %q", app.ConfigFile, app.GlobalConfigFile)
	}
}

func TestConfigUseGlobalProfileCreatesLocalMetadataOverride(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "use", "shared"}); err != nil {
		t.Fatalf("config use global-only profile: %v", err)
	}
	raw, err := os.ReadFile(app.LocalConfigFile)
	if err != nil {
		t.Fatalf("read local override config: %v", err)
	}
	if !strings.Contains(string(raw), "default_profile = shared") {
		t.Fatalf("local override missing shared default:\n%s", raw)
	}
	if strings.Contains(string(raw), "[profile:shared]") {
		t.Fatalf("global profile should not be copied into metadata override:\n%s", raw)
	}
	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load selected global profile: %v", err)
	}
	if profile.Name != "shared" || app.ConfigFile != app.GlobalConfigFile {
		t.Fatalf("profile = %#v, active config = %q", profile, app.ConfigFile)
	}
}

func (a *App) withLocalConfigProfiles() (string, map[string]Profile, bool, error) {
	var defaultProfile string
	var profiles map[string]Profile
	var legacy bool
	err := a.withConfigLocation(a.localConfigLocation(), func() error {
		var err error
		defaultProfile, profiles, legacy, err = a.readConfigProfiles(false)
		return err
	})
	return defaultProfile, profiles, legacy, err
}

func TestMergedConfigSkipsUnreadableGlobalConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix file permission semantics only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read files without read permission")
	}
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local": plainTestProfile("local", "https://local.example"),
	}, "")
	if err := os.Chmod(app.GlobalConfigFile, 0o000); err != nil {
		t.Fatalf("make global config unreadable: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(app.GlobalConfigFile, 0o600)
	})

	merged, err := app.readMergedConfig(false)
	if err != nil {
		t.Fatalf("read merged config with unreadable global config: %v", err)
	}
	if _, ok := merged.Profiles["shared"]; ok {
		t.Fatalf("unreadable global profile was included: %#v", merged.Profiles)
	}
	if _, ok := merged.Profiles["local"]; !ok {
		t.Fatalf("local profile missing after skipping unreadable global config: %#v", merged.Profiles)
	}
	if merged.DefaultProfile != "local" {
		t.Fatalf("default profile = %q, want local", merged.DefaultProfile)
	}
}

func TestLoadConfigUsesLocalScopeForLocalOverride(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "common", map[string]Profile{
		"common": plainTestProfile("common", "https://global-common.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "common", map[string]Profile{
		"common": plainTestProfile("common", "https://local-common.example"),
	}, "")

	profile, err := app.loadConfigProfile("common", true)
	if err != nil {
		t.Fatalf("load common profile: %v", err)
	}
	if profile.Server != "https://local-common.example" {
		t.Fatalf("server = %q, want local override", profile.Server)
	}
	if app.ConfigFile != app.LocalConfigFile {
		t.Fatalf("active config = %q, want local", app.ConfigFile)
	}
	if got := app.cachePath(); got != filepath.Join(app.LocalConfigDir, cacheFileName) {
		t.Fatalf("cache path = %q, want local cache", got)
	}
}

func TestConfigListShowsMergedProfiles(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local": plainTestProfile("local", "https://local.example"),
	}, "")
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"-o", "json", "config", "list"}); err != nil {
		t.Fatalf("config list: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("parse config list json: %v\n%s", err, stdout.String())
	}
	seen := map[string]bool{}
	var localRow map[string]any
	for _, row := range rows {
		name := fmt.Sprint(row["profile"])
		seen[name] = true
		if name == "local" {
			localRow = row
		}
	}
	for _, name := range []string{"local", "shared"} {
		if !seen[name] {
			t.Fatalf("config list missing %q: %#v", name, rows)
		}
	}
	if got := fmt.Sprint(localRow["username"]); got != "local-user" {
		t.Fatalf("local username = %q, want local-user: %#v", got, localRow)
	}
	if got := fmt.Sprint(localRow["wapi_version"]); got != defaultWAPIVersion {
		t.Fatalf("local wapi_version = %q, want %s: %#v", got, defaultWAPIVersion, localRow)
	}
	if got, ok := localRow["verify_ssl"].(bool); !ok || !got {
		t.Fatalf("local verify_ssl = %#v, want true: %#v", localRow["verify_ssl"], localRow)
	}
}

func TestConfigListHighlightsActiveProfileRow(t *testing.T) {
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(originalProfile)
	})
	app := testApp(t)
	writePlainTestConfig(t, app.LocalConfigFile, "active", map[string]Profile{
		"active":  plainTestProfile("active", "https://active.example"),
		"passive": plainTestProfile("passive", "https://passive.example"),
	}, "")
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "list"}); err != nil {
		t.Fatalf("config list: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, activeTableRowStyle.Render("active")) {
		t.Fatalf("config list did not apply active row background:\n%q", output)
	}
	activePadding := lipgloss.NewStyle().Background(lipgloss.Color("#4ade80")).Render(" ")
	if strings.Count(output, activePadding) < 6 {
		t.Fatalf("config list did not color active row padding:\n%q", output)
	}
	if strings.Contains(output, activeTableRowStyle.Render("passive")) {
		t.Fatalf("config list highlighted inactive profile:\n%q", output)
	}
}

func TestConfigListShowsMetadataTable(t *testing.T) {
	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{
		CacheTTLSeconds:                900,
		DNSSearchWorkerLimit:           12,
		RecordsCacheSWRSeconds:         3600,
		MaxBackgroundWorkerWaitSeconds: 7,
		CompletionCachePrefetch:        false,
		completionCachePrefetchSet:     true,
		AuditLoggingEnabled:            true,
		auditLoggingEnabledSet:         true,
		AuditLogMethod:                 auditLogMethodFile,
		AuditLogFile:                   filepath.Join(app.ConfigDir, "audit.jsonl"),
		SSOEnabled:                    true,
		ssoEnabledSet:                 true,
		SSOTenantID:                   "contoso.onmicrosoft.com",
		SSOClientID:                   "11111111-2222-3333-4444-555555555555",
		SSOScopes:                     defaultSSOScopes,
		SSOAllowedGroups:              "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "list"}); err != nil {
		t.Fatalf("config list: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Config Metadata",
		"metadata_source",
		"local",
		"config_file",
		app.LocalConfigFile,
		"default_profile",
		"default",
		"cache_ttl",
		"900",
		"dns_search_worker_limit",
		"12",
		"records_cache_swr_ttl",
		"3600",
		"max_background_worker_wait",
		"7",
		"completion_cache_prefetch",
		"false",
		"audit_logging_enabled",
		"true",
		"audit_logging_method",
		"file",
		"audit_log_file",
		filepath.Join(app.ConfigDir, "audit.jsonl"),
		"sso_enabled",
		"true",
		"sso_azure_tenant_id",
		"contoso.onmicrosoft.com",
		"sso_azure_client_id",
		"11111111-2222-3333-4444-555555555555",
		"sso_azure_scopes",
		defaultSSOScopes,
		"sso_azure_allowed_groups",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config list metadata missing %q:\n%s", want, output)
		}
	}
}

func TestProfileCompletionShowsMergedProfiles(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")
	writePlainTestConfig(t, app.LocalConfigFile, "local", map[string]Profile{
		"local": plainTestProfile("local", "https://local.example"),
	}, "")

	matches := app.completeProfileNames("", true)
	for _, want := range []string{"local", "shared"} {
		if !containsString(matches, want) {
			t.Fatalf("profile completion missing %q: %#v", want, matches)
		}
	}
}

func TestWriteGlobalConfigPersistsGroupAndPermissions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux file mode semantics only")
	}
	app := testApp(t)
	app.useConfigLocation(app.globalConfigLocation())
	oldLookup := lookupGlobalConfigGroupFunc
	oldChown := chownPathGroupFunc
	chowned := map[string]bool{}
	lookupGlobalConfigGroupFunc = func(groupName string) (globalConfigGroupInfo, error) {
		if groupName != "ibusers" {
			return globalConfigGroupInfo{}, fmt.Errorf("unexpected group %q", groupName)
		}
		return globalConfigGroupInfo{Name: "ibusers", GID: os.Getgid()}, nil
	}
	chownPathGroupFunc = func(path string, group globalConfigGroupInfo) error {
		chowned[path] = true
		return nil
	}
	t.Cleanup(func() {
		lookupGlobalConfigGroupFunc = oldLookup
		chownPathGroupFunc = oldChown
	})
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      "https://global.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}

	if err := app.writeConfigProfilesWithSettings(defaultProfileName, profiles, ConfigSettings{GlobalGroup: "ibusers"}); err != nil {
		t.Fatalf("write global profiles: %v", err)
	}
	raw, err := os.ReadFile(app.GlobalConfigFile)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), "global_group = ibusers") {
		t.Fatalf("global config missing group:\n%s", raw)
	}
	for _, path := range []string{app.GlobalConfigDir, app.GlobalConfigFile, app.GlobalConfigKeyFile} {
		if !chowned[path] {
			t.Fatalf("path %q was not group-owned; chowned=%#v", path, chowned)
		}
	}
	assertFileMode(t, app.GlobalConfigDir, 0o770)
	assertFileMode(t, app.GlobalConfigFile, 0o640)
	assertFileMode(t, app.GlobalConfigKeyFile, 0o640)
}

func TestGlobalConfigNewAndEditRequireRoot(t *testing.T) {
	if !globalConfigSupported() {
		t.Skip("global config is only supported on Linux")
	}
	oldEffectiveUserID := globalConfigEffectiveUserIDFunc
	globalConfigEffectiveUserIDFunc = func() int { return 1000 }
	t.Cleanup(func() {
		globalConfigEffectiveUserIDFunc = oldEffectiveUserID
	})

	tests := [][]string{
		{"config", "new", "--global-config", "shared"},
		{"config", "edit", "--global-config", "shared"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			app := testApp(t)
			err := app.Execute(args)
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want root error", args)
			}
			if !strings.Contains(err.Error(), "requires root") || !strings.Contains(err.Error(), "sudo") {
				t.Fatalf("Execute(%v) error = %v, want root/sudo guidance", args, err)
			}
			if app.ConfigFile == app.GlobalConfigFile {
				t.Fatalf("Execute(%v) selected global config before root check", args)
			}
		})
	}
}

func TestWriteConfigProfilesEncryptsPasswordAndReadsItBack(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	raw, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "secret-password") {
		t.Fatalf("password was written in plaintext:\n%s", raw)
	}
	for _, want := range []string{
		"cache_ttl = 300",
		"dns_search_worker_limit = 16",
		"records_cache_swr_ttl = 259200",
		"max_background_worker_wait = 3",
		"completion_cache_prefetch = true",
		"audit_logging_enabled = false",
		"audit_logging_method = " + defaultAuditLogMethod(),
		"audit_log_file = ",
		"sso_enabled = false",
		"sso_azure_tenant_id = ",
		"sso_azure_client_id = ",
		"sso_azure_scopes = " + defaultSSOScopes,
		"sso_azure_allowed_groups = ",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("config missing %q:\n%s", want, raw)
		}
	}
	defaultProfile, loaded, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if defaultProfile != "default" {
		t.Fatalf("default profile = %q", defaultProfile)
	}
	if loaded["default"].Password != "secret-password" {
		t.Fatalf("loaded password = %q", loaded["default"].Password)
	}
}

func TestGetOrCreateConfigKeyReturnsUnreadableKeyError(t *testing.T) {
	app := testApp(t)
	if err := app.ensureConfigDir(); err != nil {
		t.Fatalf("ensure config dir: %v", err)
	}
	if err := os.WriteFile(app.ConfigKeyFile, []byte("existing-key\n"), 0o000); err != nil {
		t.Fatalf("write unreadable key: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(app.ConfigKeyFile, 0o600)
	})
	if _, err := os.ReadFile(app.ConfigKeyFile); err == nil {
		t.Skip("key file remains readable after chmod 000")
	}

	if _, err := app.getOrCreateConfigKey(); err == nil {
		t.Fatal("getOrCreateConfigKey succeeded with unreadable key")
	}
	if err := os.Chmod(app.ConfigKeyFile, 0o600); err != nil {
		t.Fatalf("restore key permissions: %v", err)
	}
	raw, err := os.ReadFile(app.ConfigKeyFile)
	if err != nil {
		t.Fatalf("read restored key: %v", err)
	}
	if string(raw) != "existing-key\n" {
		t.Fatalf("key file was replaced: %q", raw)
	}
}

func TestLoadConfigBackfillsMissingGlobalSettings(t *testing.T) {
	app := testApp(t)
	raw := `[meta]
default_profile = default

[profile:default]
server = https://infoblox.example
read_server = 
username = admin
password = secret-password
wapi_version = v2.12.3
dns_view = default
default_zone = example.com
verify_ssl = true
timeout = 30
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if profile.Name != "default" {
		t.Fatalf("profile name = %q", profile.Name)
	}
	updated, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	for _, want := range []string{
		"cache_ttl = 300",
		"dns_search_worker_limit = 16",
		"records_cache_swr_ttl = 259200",
		"max_background_worker_wait = 3",
		"completion_cache_prefetch = true",
		"audit_logging_enabled = false",
		"audit_logging_method = " + defaultAuditLogMethod(),
		"audit_log_file = ",
	} {
		if !strings.Contains(string(updated), want) {
			t.Fatalf("updated config missing %q:\n%s", want, updated)
		}
	}
	if strings.Contains(string(updated), "secret-password") {
		t.Fatalf("updated config left password plaintext:\n%s", updated)
	}
}

func TestReadConfigSettingsFallsBackForInvalidValues(t *testing.T) {
	app := testApp(t)
	raw := `[meta]
default_profile = default
cache_ttl = not-a-number
dns_search_worker_limit = -5
records_cache_swr_ttl = 0
max_background_worker_wait = nope
completion_cache_prefetch = maybe
audit_logging_enabled = maybe
audit_logging_method = nowhere
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	settings, missing, err := app.readConfigSettings()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !missing {
		t.Fatalf("missing = false, want true for invalid values")
	}
	if settings.CacheTTLSeconds != defaultCacheTTLSeconds {
		t.Fatalf("cache ttl = %d, want %d", settings.CacheTTLSeconds, defaultCacheTTLSeconds)
	}
	if settings.DNSSearchWorkerLimit != defaultDNSSearchWorkerLimit {
		t.Fatalf("worker limit = %d, want %d", settings.DNSSearchWorkerLimit, defaultDNSSearchWorkerLimit)
	}
	if settings.RecordsCacheSWRSeconds != defaultRecordsCacheSWRSeconds {
		t.Fatalf("records cache swr ttl = %d, want %d", settings.RecordsCacheSWRSeconds, defaultRecordsCacheSWRSeconds)
	}
	if settings.MaxBackgroundWorkerWaitSeconds != defaultMaxBackgroundWorkerWaitSeconds {
		t.Fatalf("max background wait = %d, want %d", settings.MaxBackgroundWorkerWaitSeconds, defaultMaxBackgroundWorkerWaitSeconds)
	}
	if !settings.CompletionCachePrefetch {
		t.Fatalf("completion cache prefetch = false, want true fallback")
	}
	if settings.AuditLoggingEnabled {
		t.Fatalf("audit logging enabled = true, want false fallback")
	}
	if settings.AuditLogMethod != defaultAuditLogMethod() {
		t.Fatalf("audit log method = %q, want %q", settings.AuditLogMethod, defaultAuditLogMethod())
	}
	if settings.SSOEnabled {
		t.Fatalf("sso enabled = true, want false fallback")
	}
	if settings.SSOScopes != defaultSSOScopes {
		t.Fatalf("sso scopes = %q, want %q", settings.SSOScopes, defaultSSOScopes)
	}
}

func TestReadConfigSettingsAllowsDisabledCompletionPrefetch(t *testing.T) {
	app := testApp(t)
	raw := `[meta]
default_profile = default
cache_ttl = 600
dns_search_worker_limit = 8
records_cache_swr_ttl = 1200
max_background_worker_wait = 5
completion_cache_prefetch = disabled
audit_logging_enabled = false
audit_logging_method = file
audit_log_file =
sso_enabled = false
sso_azure_tenant_id =
sso_azure_client_id =
sso_azure_scopes = openid profile email offline_access
sso_azure_allowed_groups =
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	settings, missing, err := app.readConfigSettings()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if missing {
		t.Fatalf("missing = true, want false")
	}
	if settings.CompletionCachePrefetch {
		t.Fatal("completion cache prefetch = true, want false")
	}
}

func TestPromptAuditLoggingFileMethodCanBackOutToSelection(t *testing.T) {
	syslogChoice, ok := auditMethodChoiceNumber(auditLogMethodSyslog)
	if !ok {
		t.Skip("syslog audit method is not supported on this platform")
	}
	fileChoice, ok := auditMethodChoiceNumber(auditLogMethodFile)
	if !ok {
		t.Skip("file audit method is not supported on this platform")
	}
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"y",
		fileChoice,
		"n",
		syslogChoice,
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	settings, err := app.promptAuditLoggingSettings(defaultConfigSettings())
	if err != nil {
		t.Fatalf("prompt audit logging settings: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !settings.AuditLoggingEnabled {
		t.Fatal("audit logging enabled = false, want true")
	}
	if settings.AuditLogMethod != auditLogMethodSyslog {
		t.Fatalf("audit method = %q, want %q", settings.AuditLogMethod, auditLogMethodSyslog)
	}
	output := stdout.String()
	if strings.Count(output, gumPromptIndent+"Audit logging method") < 2 {
		t.Fatalf("audit method prompt should be indented and shown twice after backing out:\n%s", output)
	}
	if !strings.Contains(output, "file audit logging uses a writable log file") {
		t.Fatalf("file audit warning missing:\n%s", output)
	}
	if strings.Contains(output, "Audit log file") {
		t.Fatalf("audit file path should not be requested after backing out:\n%s", output)
	}
}

func TestPromptAuditLoggingFileMethodWriteTestsPath(t *testing.T) {
	fileChoice, ok := auditMethodChoiceNumber(auditLogMethodFile)
	if !ok {
		t.Skip("file audit method is not supported on this platform")
	}
	app := testApp(t)
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"y",
		fileChoice,
		"y",
		logPath,
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	settings, err := app.promptAuditLoggingSettings(defaultConfigSettings())
	if err != nil {
		t.Fatalf("prompt audit logging settings: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if settings.AuditLogMethod != auditLogMethodFile {
		t.Fatalf("audit method = %q, want %q", settings.AuditLogMethod, auditLogMethodFile)
	}
	if settings.AuditLogFile != logPath {
		t.Fatalf("audit log file = %q, want %q", settings.AuditLogFile, logPath)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("audit log write test did not create/open file: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("audit log path is a directory: %s", logPath)
	}
	if !strings.Contains(stdout.String(), "Continue with file audit logging?") {
		t.Fatalf("file warning confirmation missing:\n%s", stdout.String())
	}
}

func TestPromptAuditLoggingFileMethodCanBackOutAfterWriteTestFailure(t *testing.T) {
	syslogChoice, ok := auditMethodChoiceNumber(auditLogMethodSyslog)
	if !ok {
		t.Skip("syslog audit method is not supported on this platform")
	}
	fileChoice, ok := auditMethodChoiceNumber(auditLogMethodFile)
	if !ok {
		t.Skip("file audit method is not supported on this platform")
	}
	app := testApp(t)
	blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badPath := filepath.Join(blockingFile, "audit.jsonl")
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"y",
		fileChoice,
		"y",
		badPath,
		"n",
		syslogChoice,
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	settings, err := app.promptAuditLoggingSettings(defaultConfigSettings())
	if err != nil {
		t.Fatalf("prompt audit logging settings: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if settings.AuditLogMethod != auditLogMethodSyslog {
		t.Fatalf("audit method = %q, want %q", settings.AuditLogMethod, auditLogMethodSyslog)
	}
	output := stdout.String()
	for _, want := range []string{
		"WARNING: audit log file is not writable:",
		"Choose a different audit log file?",
		gumPromptIndent + "Audit logging method",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("audit prompt output missing %q:\n%s", want, output)
		}
	}
}

func auditMethodChoiceNumber(method string) (string, bool) {
	for index, choice := range supportedAuditLogMethods() {
		if choice == method {
			return fmt.Sprint(index + 1), true
		}
	}
	return "", false
}

func TestDNSContextLineUsesCompactColonFormat(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"TestDemo": {
			Name:        "TestDemo",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "DNS Zone View",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("TestDemo", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}

	line := app.dnsContextLine()
	for _, want := range []string{
		"Current Context:",
		"Profile: TestDemo",
		"View: DNS Zone View",
		"Zone: example.com",
		"(configured default)",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("context line missing %q:\n%s", want, line)
		}
	}
	for _, unwanted := range []string{"Current DNS Context", "Profile=", "View=", "Zone="} {
		if strings.Contains(line, unwanted) {
			t.Fatalf("context line contains old format %q:\n%s", unwanted, line)
		}
	}
}

func TestConfigOverviewWithoutProfilesUsesSetupPanel(t *testing.T) {
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Config Usage",
		"Create a profile first; credentials are encrypted.",
		"ib config new [PROFILE]",
		"server/TLS, username, password, WAPI",
		"Audit logging",
		"auto GCM read endpoint",
		"runs before saving; retry on failure",
		"ib config completion [SHELL]",
		"ib config cache status|clear",
		"ib config <command> --help",
		"ib config / configure",
		"Key file",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure overview missing %q:\n%s", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("configure overview wrote stderr:\n%s", stderr.String())
	}
}

func TestConfigOverviewWithoutProfilesKeepsMachineReadableOutput(t *testing.T) {
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"-o", "json", "configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := strings.TrimSpace(stdout.String())
	if output != "[]" {
		t.Fatalf("configure json output = %q, want []", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("configure json wrote stderr:\n%s", stderr.String())
	}
}

func TestConfigOverviewWithProfilesShowsActionPanel(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "DNS Zone View",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Infoblox Profiles (1)",
		"Config Usage",
		"Manage saved profiles, audit logging, shell completion",
		"ib config new [PROFILE]",
		"ib config new --global-config [PROFILE]",
		"ib config edit [PROFILE]",
		"ib config use PROFILE",
		"ib config list",
		"ib config delete PROFILE",
		"ib config completion [SHELL]",
		"ib config cache status|clear",
		"ib config <command> --help",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure overview missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"Validation", "Retry"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("configure overview contains unwanted %q:\n%s", unwanted, output)
		}
	}
}

func TestConfigDeleteClearsProfileCache(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
		"old": {
			Name:        "old",
			Server:      "https://old-infoblox.example",
			Username:    "admin",
			Password:    "old-secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "old.example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	defaultProfile := profiles["default"]
	oldProfile := profiles["old"]
	now := time.Now()
	if err := app.writeCachedZones(defaultProfile, []map[string]any{{"fqdn": "example.com"}}, now); err != nil {
		t.Fatalf("write default zone cache: %v", err)
	}
	if err := app.writeCachedRecords(defaultProfile, "example.com", "2026050801", []map[string]any{{"name": "app.example.com"}}, now); err != nil {
		t.Fatalf("write default record cache: %v", err)
	}
	if err := app.writeCachedZones(oldProfile, []map[string]any{{"fqdn": "old.example.com"}}, now); err != nil {
		t.Fatalf("write old zone cache: %v", err)
	}
	if err := app.writeCachedRecords(oldProfile, "old.example.com", "2026050801", []map[string]any{{"name": "app.old.example.com"}}, now); err != nil {
		t.Fatalf("write old record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(oldProfile, "old.example.com", now, time.Minute)
	if err != nil {
		t.Fatalf("acquire old refresh lease: %v", err)
	}
	if !acquired {
		t.Fatalf("old refresh lease was not acquired")
	}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "delete", "old"}); err != nil {
		t.Fatalf("config delete old: %v", err)
	}
	if !strings.Contains(stdout.String(), "deleted and cache cleared") {
		t.Fatalf("delete output did not mention cache clear:\n%s", stdout.String())
	}
	_, loadedProfiles, _, err := app.readConfigProfiles(false)
	if err != nil {
		t.Fatalf("read profiles after delete: %v", err)
	}
	if _, ok := loadedProfiles["old"]; ok {
		t.Fatalf("old profile still exists after delete: %#v", loadedProfiles)
	}
	rows, err := app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status after delete: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("cache status row count = %d, want only default zone and record rows: %#v", len(rows), rows)
	}
	for _, row := range rows {
		if row["profile"] == "old" {
			t.Fatalf("old profile cache row was not cleared: %#v", rows)
		}
		if row["profile"] != "default" {
			t.Fatalf("unexpected cache row after delete: %#v", row)
		}
	}
	acquired, err = app.tryAcquireRecordRefreshLease(oldProfile, "old.example.com", now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("re-acquire old refresh lease after delete: %v", err)
	}
	if !acquired {
		t.Fatalf("old profile refresh lease was not cleared")
	}
}

func TestConfigureSummaryOmitsPassword(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout

	app.printConfigureSummary(Profile{
		Name:        "default",
		Server:      "https://infoblox.example",
		Username:    "admin",
		Password:    "secret-password",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "DNS Zone View",
		VerifySSL:   false,
		Timeout:     defaultTimeoutSeconds,
	}, true)

	output := stdout.String()
	for _, want := range []string{
		"Profile Saved",
		"The profile is ready for DNS commands.",
		"Profile",
		"default",
		"Password",
		"encrypted",
		"primary server",
		"not set",
		"Verify SSL",
		"no",
		"Edit later",
		"ib config edit default",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure summary missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-password") {
		t.Fatalf("configure summary exposed password:\n%s", output)
	}
}

func TestConfigureSummaryKeepsLongReadEndpointOnOneLine(t *testing.T) {
	longReadEndpoint := "https://grid-master-candidate-readonly-01.network-services.example.edu.au"

	output := renderConfigSuccessPanel(Profile{
		Name:        "default",
		Server:      "https://infoblox.example",
		ReadServer:  longReadEndpoint,
		Username:    "admin",
		Password:    "secret-password",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "DNS Zone View",
		DefaultZone: "example.com",
		VerifySSL:   false,
		Timeout:     defaultTimeoutSeconds,
	}, true)

	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Read endpoint") {
			if !strings.Contains(line, longReadEndpoint) {
				t.Fatalf("read endpoint wrapped away from label:\n%s", output)
			}
			return
		}
	}
	t.Fatalf("configure summary missing read endpoint row:\n%s", output)
}

func TestConfigureNewProfileNamePromptStartsBlankAndDefaultsOnEnter(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Profile name:") {
		t.Fatalf("configure new did not prompt for profile name:\n%s", output)
	}
	if strings.Contains(output, "Profile name [default]") {
		t.Fatalf("profile prompt was prepopulated with default:\n%s", output)
	}
	for _, want := range []string{
		"\n   Checking Infoblox credentials and WAPI access...",
		"\n   SUCCESS: Infoblox connection test passed.",
		"07 Default DNS Zone",
		"08 Audit Logging",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure status line missing indentation %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"\nChecking Infoblox credentials and WAPI access...",
		"\nSUCCESS: Infoblox connection test passed.",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("configure status line is not indented %q:\n%s", unwanted, output)
		}
	}
	defaultProfile, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if defaultProfile != defaultProfileName {
		t.Fatalf("default profile = %q, want %q", defaultProfile, defaultProfileName)
	}
	if _, ok := profiles[defaultProfileName]; !ok {
		t.Fatalf("default profile was not created: %#v", profiles)
	}
}

func TestConfigureNewDefaultsWAPIVersionFromSchema(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/wapi/v1.0/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"supported_versions": []string{"1.0", "2.9", "2.12", "2.12.4"},
			})
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/view"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if got := profiles["demo"].WAPIVersion; got != "v2.12.4" {
		t.Fatalf("WAPI version = %q, want v2.12.4", got)
	}
	output := stdout.String()
	if !strings.Contains(output, "INFO: detected WAPI version v2.12.4.") {
		t.Fatalf("configure output missing detected version message:\n%s", output)
	}
	joined := strings.Join(requests, ",")
	for _, want := range []string{"GET /wapi/v1.0/", "GET /wapi/v2.12.4/grid"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %q: %#v", want, requests)
		}
	}
}

func TestConfigureNewFallsBackWhenWAPIVersionDetectionFails(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if got := profiles["demo"].WAPIVersion; got != defaultWAPIVersion {
		t.Fatalf("WAPI version = %q, want %q", got, defaultWAPIVersion)
	}
	if !strings.Contains(stdout.String(), "INFO: could not auto-detect WAPI version; using "+defaultWAPIVersion+" as the default") {
		t.Fatalf("configure output missing fallback message:\n%s", stdout.String())
	}
}

func TestConfigureNewRetriesUnreachableServerBeforeCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	unreachable := "https://" + listener.Addr().String()
	_ = listener.Close()
	server := newConfigSuccessServer(t)

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		unreachable,
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	warningIndex := strings.Index(output, "WARNING: Infoblox server is not reachable")
	usernameIndex := strings.Index(output, "Username:")
	if warningIndex < 0 {
		t.Fatalf("configure output missing reachability warning:\n%s", output)
	}
	if usernameIndex < 0 || warningIndex > usernameIndex {
		t.Fatalf("server warning should be printed before credentials are requested:\n%s", output)
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if got := profiles["demo"].Server; got != server.URL {
		t.Fatalf("server = %q, want %q", got, server.URL)
	}
}

func TestConfigureNewAcceptsUntrustedHTTPSCertificate(t *testing.T) {
	server := newConfigTLSSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"y",
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"WARNING: HTTPS certificate is not trusted",
		"INFO: Subject:",
		"INFO: Issuer:",
		"INFO: SHA256 fingerprint:",
		"Trust this Infoblox HTTPS certificate for this profile?",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure output missing %q:\n%s", want, output)
		}
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles["demo"].VerifySSL {
		t.Fatalf("VerifySSL = true, want false after accepting untrusted certificate")
	}
}

func TestConfigureNewDeclinesUntrustedHTTPSCertificateAndReprompts(t *testing.T) {
	untrusted := newConfigTLSSuccessServer(t)
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		untrusted.URL,
		"n",
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "WARNING: certificate was not trusted; enter a different Infoblox server.") {
		t.Fatalf("configure output missing certificate decline warning:\n%s", output)
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if got := profiles["demo"].Server; got != server.URL {
		t.Fatalf("server = %q, want %q", got, server.URL)
	}
}

func TestConfigureNewTrustedHTTPSCertificateSkipsTrustPrompt(t *testing.T) {
	server := newConfigTLSSuccessServer(t)
	app := testApp(t)
	trustTLSServer(t, app, server)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "Trust this Infoblox HTTPS certificate") {
		t.Fatalf("trusted certificate should not show trust prompt:\n%s", output)
	}
	if !strings.Contains(output, "INFO: Infoblox server is reachable over HTTPS with a trusted certificate.") {
		t.Fatalf("configure output missing trusted HTTPS message:\n%s", output)
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if !profiles["demo"].VerifySSL {
		t.Fatalf("VerifySSL = false, want true for trusted HTTPS")
	}
}

func TestConfigureNewPlainHTTPDisablesSSLVerification(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "WARNING: Infoblox server is reachable over plain HTTP; SSL verification is not available.") {
		t.Fatalf("configure output missing plain HTTP warning:\n%s", stdout.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles["demo"].VerifySSL {
		t.Fatalf("VerifySSL = true, want false for plain HTTP")
	}
}

func TestConfigureNewRetriesBadCredentialsWithSummary(t *testing.T) {
	server := newConfigCredentialServer(t, http.StatusUnauthorized)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"bad",
		"wrong",
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "WARNING: login failed: Infoblox rejected the username or password (HTTP 401).") {
		t.Fatalf("configure output missing summarized login failure:\n%s", output)
	}
	if strings.Contains(output, "raw server credential failure") || strings.Contains(output, "abc123") {
		t.Fatalf("configure output exposed raw server credential response:\n%s", output)
	}
	if !strings.Contains(output, "Username [bad]:") {
		t.Fatalf("credentials were not re-prompted after login failure:\n%s", output)
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles["demo"].Username != "admin" {
		t.Fatalf("username = %q, want admin", profiles["demo"].Username)
	}
}

func TestConfigureNewRetriesForbiddenCredentialsWithSummary(t *testing.T) {
	server := newConfigCredentialServer(t, http.StatusForbidden)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"limited",
		"secret",
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "WARNING: login failed: Infoblox authenticated the user but denied access (HTTP 403).") {
		t.Fatalf("configure output missing summarized authorization failure:\n%s", output)
	}
	if strings.Contains(output, "raw server credential failure") || strings.Contains(output, "abc123") {
		t.Fatalf("configure output exposed raw server credential response:\n%s", output)
	}
}

func TestConfigureEditBlankPasswordKeepsCurrentPasswordForValidation(t *testing.T) {
	server := newConfigCredentialServer(t, http.StatusUnauthorized)
	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      server.URL,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   false,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "edit"}); err != nil {
		t.Fatalf("configure edit: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "INFO: Infoblox login succeeded.") {
		t.Fatalf("configure output missing login success:\n%s", stdout.String())
	}
	_, savedProfiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if savedProfiles[defaultProfileName].Password != "secret" {
		t.Fatalf("password changed unexpectedly")
	}
}

func TestHighestWAPIVersionComparesNumerically(t *testing.T) {
	got, ok := highestWAPIVersion([]string{"2.9", "v2.12", "2.12.3", "invalid", "2.12.4"})
	if !ok {
		t.Fatal("highest WAPI version was not found")
	}
	if got != "v2.12.4" {
		t.Fatalf("highest WAPI version = %q, want v2.12.4", got)
	}
}

func TestConfigureNewAutoSavesGCMReadServer(t *testing.T) {
	var gcmRequests []string
	gcm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gcmRequests = append(gcmRequests, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/wapi/"+defaultWAPIVersion+"/"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/view"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"name": "default"}}})
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"fqdn": "primary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gcm.Close()

	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/wapi/"+defaultWAPIVersion+"/"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"host_name": gcm.URL, "master_candidate": true, "enable_ro_api_access": true},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer primary.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		primary.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles["demo"].ReadServer != gcm.URL {
		t.Fatalf("read server = %q, want %q", profiles["demo"].ReadServer, gcm.URL)
	}
	if !strings.Contains(strings.Join(primaryRequests, ","), "GET grid") ||
		!strings.Contains(strings.Join(primaryRequests, ","), "GET member") {
		t.Fatalf("primary discovery requests = %#v", primaryRequests)
	}
	gcmJoined := strings.Join(gcmRequests, ",")
	for _, want := range []string{"GET grid", "GET view", "GET zone_auth"} {
		if !strings.Contains(gcmJoined, want) {
			t.Fatalf("GCM requests missing %q: %#v", want, gcmRequests)
		}
	}
}

func TestConfigureAutoSelectsSingleDNSViewAndZone(t *testing.T) {
	server := newConfigSingleViewZoneServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles["demo"].DNSView != "DNS Zone View" {
		t.Fatalf("DNS view = %q, want DNS Zone View", profiles["demo"].DNSView)
	}
	if profiles["demo"].DefaultZone != "primary.example.com" {
		t.Fatalf("default zone = %q, want primary.example.com", profiles["demo"].DefaultZone)
	}
	output := stdout.String()
	for _, want := range []string{
		"INFO: only one DNS view found; using DNS Zone View.",
		"INFO: only one DNS zone found; using primary.example.com.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"\n   Default DNS View\n",
		"Default DNS zone (optional)",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("single view/zone should not prompt with %q:\n%s", unwanted, output)
		}
	}
}

func TestConfigureEditClearsOldReadServerWhenNoUsableGCM(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      server.URL,
			ReadServer:  "https://readonly.example",
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   false,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "edit"}); err != nil {
		t.Fatalf("configure edit: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	_, savedProfiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if savedProfiles[defaultProfileName].ReadServer != "" {
		t.Fatalf("read server = %q, want blank", savedProfiles[defaultProfileName].ReadServer)
	}
}

func TestConfigNewCanRetryDetailsAfterValidationError(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("config new retry: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Configuration failed: Infoblox server is required") {
		t.Fatalf("retry warning missing validation error:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Try entering the details again") {
		t.Fatalf("retry confirmation prompt missing:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "07 Default DNS Zone") {
		t.Fatalf("default zone was not question 7:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Configure a default DNS zone for this profile") {
		t.Fatalf("configure default zone confirmation should not be shown:\n%s", stdout.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if _, ok := profiles["demo"]; !ok {
		t.Fatalf("demo profile was not created after retry: %#v", profiles)
	}
}

func TestConfigureNewConnectionFailureShowsRetryPopup(t *testing.T) {
	failServer := newConfigFailureServer(t)
	successServer := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		failServer.URL,
		"admin",
		"secret",
		"y",
		successServer.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new"}); err != nil {
		t.Fatalf("configure new retry: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"Connection Test Failed",
		"The profile was not saved",
		"Do you want to retry?",
		"Profile Saved",
		"The profile is ready for DNS commands.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure new retry output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigureEditConnectionFailureShowsRetryPopup(t *testing.T) {
	failServer := newConfigFailureServer(t)
	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      failServer.URL,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   false,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		"",
		"n",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	err := app.Execute([]string{"configure", "edit"})
	if err == nil {
		t.Fatalf("configure edit returned nil; want connection failure")
	}
	if !isConnectionTestFailure(err) {
		t.Fatalf("configure edit error = %T %v, want connection test failure", err, err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Connection Test Failed",
		"Infoblox connection test failed",
		"Do you want to retry?",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure edit retry output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigureViewAndZonePromptsAreIndentedAndSequenced(t *testing.T) {
	server := newConfigViewSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"06 DNS View",
		"\n   Default DNS View\n",
		"07 Default DNS Zone",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\nDefault DNS View\n") {
		t.Fatalf("default DNS view prompt was not indented:\n%s", output)
	}
	if strings.Contains(output, "Configure a default DNS zone for this profile") {
		t.Fatalf("configure default zone confirmation should not be shown:\n%s", output)
	}
}

func TestPromptReadServerClearsCurrentWhenDiscoveryHasNoCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}
	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if !changed || selected != "" {
		t.Fatalf("selected=%q changed=%v, want explicit clear", selected, changed)
	}
	if !strings.Contains(stdout.String(), "INFO: no usable Grid Master Candidate found; read queries will use the primary server.") {
		t.Fatalf("no-candidate info line missing:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestPromptReadServerClearsCurrentWhenReadOnlyAPIDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"host_name": "gcm.example.com", "master_candidate": true, "enable_ro_api_access": false},
			},
		})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}
	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if !changed || selected != "" {
		t.Fatalf("selected=%q changed=%v, want explicit clear", selected, changed)
	}
	output := stdout.String()
	if !strings.Contains(output, "   INFO: Grid Master Candidate gcm.example.com has Read-Only API disabled and will not be used.") {
		t.Fatalf("read-only info line was not indented:\nstdout:\n%s\nstderr:\n%s", output, stderr.String())
	}
	if strings.Contains(output, "\nINFO: Grid Master Candidate") || strings.HasPrefix(output, "INFO: Grid Master Candidate") {
		t.Fatalf("read-only info line should be indented:\n%s", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("read-only info line should stay in config output:\n%s", stderr.String())
	}
}

func TestPromptReadServerUsesFirstGCMWithWorkingReadOnlyAPI(t *testing.T) {
	var probeRequests int
	gcm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/grid") {
			http.NotFound(w, r)
			return
		}
		probeRequests++
		_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
	}))
	defer gcm.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"host_name": gcm.URL, "master_candidate": true, "enable_ro_api_access": true},
			},
		})
	}))
	defer primary.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader("y\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      primary.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}

	selected, changed := app.promptReadServer(profile, "")
	if !changed || selected != gcm.URL {
		t.Fatalf("selected=%q changed=%v, want %q", selected, changed, gcm.URL)
	}
	if probeRequests != 1 {
		t.Fatalf("read-only probe requests = %d, want 1", probeRequests)
	}
	output := stdout.String()
	if !strings.Contains(output, "Use "+gcm.URL+" for read-only DNS queries? [Y/n]") {
		t.Fatalf("read-server confirmation prompt missing:\nstdout:\n%s\nstderr:\n%s", output, stderr.String())
	}
	if !strings.Contains(output, "INFO: read-only GET requests will use Grid Master Candidate "+gcm.URL+".") {
		t.Fatalf("success info line missing:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestPromptReadServerClearsCurrentWhenWorkingGCMDeclined(t *testing.T) {
	var probeRequests int
	gcm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/grid") {
			http.NotFound(w, r)
			return
		}
		probeRequests++
		_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
	}))
	defer gcm.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"host_name": gcm.URL, "master_candidate": true, "enable_ro_api_access": true},
			},
		})
	}))
	defer primary.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader("n\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      primary.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}

	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if !changed || selected != "" {
		t.Fatalf("selected=%q changed=%v, want explicit clear", selected, changed)
	}
	if probeRequests != 1 {
		t.Fatalf("read-only probe requests = %d, want 1", probeRequests)
	}
	output := stdout.String()
	for _, want := range []string{
		"Use " + gcm.URL + " for read-only DNS queries? [Y/n]",
		"INFO: Grid Master Candidate " + gcm.URL + " was not selected; checking the next candidate.",
		"INFO: no Grid Master Candidate was selected; read queries will use the primary server.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("decline output missing %q:\nstdout:\n%s\nstderr:\n%s", want, output, stderr.String())
		}
	}
}

func TestPromptReadServerClearsCurrentWhenGCMProbeFails(t *testing.T) {
	gcm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "read-only API unavailable", http.StatusForbidden)
	}))
	defer gcm.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"host_name": gcm.URL, "master_candidate": true, "enable_ro_api_access": true},
			},
		})
	}))
	defer primary.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      primary.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}

	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if !changed || selected != "" {
		t.Fatalf("selected=%q changed=%v, want explicit clear", selected, changed)
	}
	output := stdout.String()
	if !strings.Contains(output, "failed read-only API probe and will not be used") ||
		!strings.Contains(output, "read queries will use the primary server") {
		t.Fatalf("probe failure info missing:\nstdout:\n%s\nstderr:\n%s", output, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("probe failure info should stay in config output:\n%s", stderr.String())
	}
}

func TestPromptDefaultZoneSkipsSecondaryZones(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/zone_auth") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"fqdn": "primary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
				{"fqdn": "secondary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "External"},
			},
		})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.Stdin = strings.NewReader("\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	selected := app.promptDefaultZone(Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}, "")

	if selected != "primary.example.com" {
		t.Fatalf("selected zone = %q, want primary.example.com", selected)
	}
	if strings.Contains(stdout.String(), "secondary.example.com") {
		t.Fatalf("secondary zone was shown in default-zone picker:\n%s", stdout.String())
	}
}

func newConfigSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/view"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newConfigTLSSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(configSuccessHandler())
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func newConfigCredentialServer(t *testing.T, failureStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(failureStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"text": "raw server credential failure token=abc123 username=" + username,
			})
			return
		}
		configSuccessHandler().ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func configSuccessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wapi/v1.0/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"supported_versions": []string{defaultWAPIVersion},
			})
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/view"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	})
}

func trustTLSServer(t *testing.T, app *App, server *httptest.Server) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	app.tlsRootCAs = pool
}

func newConfigViewSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/view"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"name": "default"}, {"name": "DNS Zone View"}}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newConfigSingleViewZoneServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		case strings.HasSuffix(r.URL.Path, "/view"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"name": "DNS Zone View"}}})
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"fqdn":         "primary.example.com",
					"view":         "DNS Zone View",
					"zone_format":  "FORWARD",
					"primary_type": "Grid",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newConfigFailureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/grid") {
			http.Error(w, "temporary grid failure", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}
