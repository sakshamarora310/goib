package ibcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

const (
	defaultCacheTTLSeconds                = 300
	defaultDNSSearchWorkerLimit           = 16
	defaultRecordsCacheSWRSeconds         = 3 * 24 * 60 * 60
	defaultMaxBackgroundWorkerWaitSeconds = 3
	configCacheTTLKey                     = "cache_ttl"
	configDNSSearchWorkerLimitKey         = "dns_search_worker_limit"
	configRecordsCacheSWRKey              = "records_cache_swr_ttl"
	configMaxBackgroundWorkerWaitKey      = "max_background_worker_wait"
	configCompletionCachePrefetchKey      = "completion_cache_prefetch"
	configGlobalGroupKey                  = "global_group"
	configAuditLoggingEnabledKey          = "audit_logging_enabled"
	configAuditLoggingMethodKey           = "audit_logging_method"
	configAuditLogFileKey                 = "audit_log_file"
	configSSOEnabledKey                   = "sso_enabled"
	configSSOTenantIDKey                  = "sso_azure_tenant_id"
	configSSOClientIDKey                  = "sso_azure_client_id"
	configSSOScopesKey                    = "sso_azure_scopes"
	configSSOAllowedGroupsKey             = "sso_azure_allowed_groups"
)

const defaultSSOScopes = "openid profile email offline_access"

type Profile struct {
	Name        string
	Server      string
	ReadServer  string
	Username    string
	Password    string
	WAPIVersion string
	DNSView     string
	DefaultZone string
	VerifySSL   bool
	Timeout     int
}

type ConfigSettings struct {
	CacheTTLSeconds                int
	DNSSearchWorkerLimit           int
	RecordsCacheSWRSeconds         int
	MaxBackgroundWorkerWaitSeconds int
	CompletionCachePrefetch        bool
	completionCachePrefetchSet     bool
	GlobalGroup                    string
	AuditLoggingEnabled            bool
	auditLoggingEnabledSet         bool
	AuditLogMethod                 string
	AuditLogFile                   string
	SSOEnabled                    bool
	ssoEnabledSet                 bool
	SSOTenantID                   string
	SSOClientID                   string
	SSOScopes                     string
	SSOAllowedGroups              string
}

func defaultConfigSettings() ConfigSettings {
	return ConfigSettings{
		CacheTTLSeconds:                defaultCacheTTLSeconds,
		DNSSearchWorkerLimit:           defaultDNSSearchWorkerLimit,
		RecordsCacheSWRSeconds:         defaultRecordsCacheSWRSeconds,
		MaxBackgroundWorkerWaitSeconds: defaultMaxBackgroundWorkerWaitSeconds,
		CompletionCachePrefetch:        true,
		completionCachePrefetchSet:     true,
		AuditLoggingEnabled:            false,
		auditLoggingEnabledSet:         true,
		AuditLogMethod:                 defaultAuditLogMethod(),
		SSOEnabled:            false,
		ssoEnabledSet:         true,
		SSOScopes:             defaultSSOScopes,
	}
}

func (s ConfigSettings) complete() ConfigSettings {
	defaults := defaultConfigSettings()
	if s.CacheTTLSeconds <= 0 {
		s.CacheTTLSeconds = defaults.CacheTTLSeconds
	}
	if s.DNSSearchWorkerLimit <= 0 {
		s.DNSSearchWorkerLimit = defaults.DNSSearchWorkerLimit
	}
	if s.RecordsCacheSWRSeconds <= 0 {
		s.RecordsCacheSWRSeconds = defaults.RecordsCacheSWRSeconds
	}
	if s.MaxBackgroundWorkerWaitSeconds <= 0 {
		s.MaxBackgroundWorkerWaitSeconds = defaults.MaxBackgroundWorkerWaitSeconds
	}
	if !s.completionCachePrefetchSet {
		s.CompletionCachePrefetch = defaults.CompletionCachePrefetch
		s.completionCachePrefetchSet = true
	}
	if !s.auditLoggingEnabledSet {
		s.AuditLoggingEnabled = defaults.AuditLoggingEnabled
		s.auditLoggingEnabledSet = true
	}
	if !s.ssoEnabledSet {
		s.SSOEnabled = defaults.SSOEnabled
		s.ssoEnabledSet = true
	}
	s.AuditLogMethod = normalizeAuditLogMethod(s.AuditLogMethod)
	if s.AuditLogMethod == "" {
		s.AuditLogMethod = defaults.AuditLogMethod
	}
	s.SSOTenantID = strings.TrimSpace(s.SSOTenantID)
	s.SSOClientID = strings.TrimSpace(s.SSOClientID)
	s.SSOScopes = normalizeSpaceList(s.SSOScopes)
	if s.SSOScopes == "" {
		s.SSOScopes = defaults.SSOScopes
	}
	s.SSOAllowedGroups = normalizeCommaList(s.SSOAllowedGroups)
	return s
}

func configSettingsFromSections(sections map[string]map[string]string) (ConfigSettings, bool) {
	settings := defaultConfigSettings()
	missing := false
	meta, ok := sections["meta"]
	if !ok {
		return settings, true
	}
	settings.CacheTTLSeconds, missing = positiveIntSetting(meta, configCacheTTLKey, settings.CacheTTLSeconds, missing)
	settings.DNSSearchWorkerLimit, missing = positiveIntSetting(meta, configDNSSearchWorkerLimitKey, settings.DNSSearchWorkerLimit, missing)
	settings.RecordsCacheSWRSeconds, missing = positiveIntSetting(meta, configRecordsCacheSWRKey, settings.RecordsCacheSWRSeconds, missing)
	settings.MaxBackgroundWorkerWaitSeconds, missing = positiveIntSetting(meta, configMaxBackgroundWorkerWaitKey, settings.MaxBackgroundWorkerWaitSeconds, missing)
	settings.CompletionCachePrefetch, settings.completionCachePrefetchSet, missing = boolSetting(meta, configCompletionCachePrefetchKey, settings.CompletionCachePrefetch, missing)
	settings.GlobalGroup = strings.TrimSpace(meta[configGlobalGroupKey])
	settings.AuditLoggingEnabled, settings.auditLoggingEnabledSet, missing = boolSetting(meta, configAuditLoggingEnabledKey, settings.AuditLoggingEnabled, missing)
	if raw, ok := meta[configAuditLoggingMethodKey]; ok && strings.TrimSpace(raw) != "" {
		settings.AuditLogMethod = normalizeAuditLogMethod(raw)
		if settings.AuditLogMethod == "" {
			missing = true
		}
	} else {
		missing = true
	}
	if raw, ok := meta[configAuditLogFileKey]; ok {
		settings.AuditLogFile = strings.TrimSpace(raw)
	} else {
		missing = true
	}
	settings.SSOEnabled, settings.ssoEnabledSet, missing = boolSetting(meta, configSSOEnabledKey, settings.SSOEnabled, missing)
	if raw, ok := meta[configSSOTenantIDKey]; ok {
		settings.SSOTenantID = strings.TrimSpace(raw)
	} else {
		missing = true
	}
	if raw, ok := meta[configSSOClientIDKey]; ok {
		settings.SSOClientID = strings.TrimSpace(raw)
	} else {
		missing = true
	}
	if raw, ok := meta[configSSOScopesKey]; ok {
		settings.SSOScopes = strings.TrimSpace(raw)
	} else {
		missing = true
	}
	if raw, ok := meta[configSSOAllowedGroupsKey]; ok {
		settings.SSOAllowedGroups = strings.TrimSpace(raw)
	} else {
		missing = true
	}
	return settings.complete(), missing
}

func positiveIntSetting(values map[string]string, key string, fallback int, missing bool) (int, bool) {
	raw, ok := values[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return fallback, true
	}
	return parsed, missing
}

func boolSetting(values map[string]string, key string, fallback bool, missing bool) (bool, bool, bool) {
	raw, ok := values[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, false, true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, true, missing
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, true, missing
	default:
		return fallback, false, true
	}
}

func (a *App) readConfigSettings() (ConfigSettings, bool, error) {
	sections, err := readINI(a.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfigSettings(), true, nil
		}
		return ConfigSettings{}, false, err
	}
	settings, missing := configSettingsFromSections(sections)
	return settings, missing, nil
}

func (a *App) configSettings() ConfigSettings {
	merged, err := a.readMergedConfig(false)
	if err != nil {
		return defaultConfigSettings()
	}
	if len(merged.FileData) == 0 {
		return defaultConfigSettings()
	}
	return merged.Settings.complete()
}

func (a *App) cacheTTL() time.Duration {
	return time.Duration(a.configSettings().CacheTTLSeconds) * time.Second
}

func (a *App) dnsSearchWorkerLimit() int {
	return a.configSettings().DNSSearchWorkerLimit
}

func (a *App) recordsCacheSWRTTL() time.Duration {
	return time.Duration(a.configSettings().RecordsCacheSWRSeconds) * time.Second
}

func (a *App) maxBackgroundWorkerWait() time.Duration {
	return time.Duration(a.configSettings().MaxBackgroundWorkerWaitSeconds) * time.Second
}

func (a *App) completionCachePrefetchEnabled() bool {
	return a.configSettings().CompletionCachePrefetch
}

func normalizeProfileName(profileName string) (string, error) {
	name := strings.TrimSpace(profileName)
	if name == "" {
		return "", cliError("profile name is required")
	}
	if !profileNameRE.MatchString(name) {
		return "", cliError("profile name may only contain letters, numbers, dots, underscores, and dashes")
	}
	return name, nil
}

func (p Profile) complete() Profile {
	if p.WAPIVersion == "" {
		p.WAPIVersion = defaultWAPIVersion
	}
	if p.DNSView == "" {
		p.DNSView = "default"
	}
	if p.Timeout == 0 {
		p.Timeout = defaultTimeoutSeconds
	}
	return p
}

func (p Profile) values() map[string]string {
	p = p.complete()
	return map[string]string{
		"server":       p.Server,
		"read_server":  p.ReadServer,
		"username":     p.Username,
		"password":     p.Password,
		"wapi_version": p.WAPIVersion,
		"dns_view":     p.DNSView,
		"default_zone": p.DefaultZone,
		"verify_ssl":   strconv.FormatBool(p.VerifySSL),
		"timeout":      strconv.Itoa(p.Timeout),
	}
}

func profileFromValues(name string, values map[string]string) Profile {
	timeout, _ := strconv.Atoi(strings.TrimSpace(values["timeout"]))
	profile := Profile{
		Name:        name,
		Server:      values["server"],
		ReadServer:  values["read_server"],
		Username:    values["username"],
		Password:    values["password"],
		WAPIVersion: values["wapi_version"],
		DNSView:     values["dns_view"],
		DefaultZone: values["default_zone"],
		VerifySSL:   parseBool(values["verify_ssl"], true),
		Timeout:     timeout,
	}
	return profile.complete()
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func normalizeSpaceList(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizeCommaList(value string) string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	cleaned := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		item := strings.TrimSpace(field)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		cleaned = append(cleaned, item)
	}
	return strings.Join(cleaned, ",")
}

func (a *App) ensureConfigDir() error {
	mode := os.FileMode(0o700)
	if a.activeConfigIsGlobal() {
		mode = 0o2770
	}
	if err := os.MkdirAll(a.ConfigDir, mode); err != nil {
		return err
	}
	return a.protectConfigDirForScope(false)
}

func (a *App) getOrCreateConfigKey() (string, error) {
	raw, err := os.ReadFile(a.ConfigKeyFile)
	if err == nil {
		return strings.TrimSpace(string(raw)), a.protectConfigKeyFileForScope(true)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err := a.ensureConfigDir(); err != nil {
		return "", err
	}
	key, err := generateFernetKey()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(a.ConfigKeyFile, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, a.protectConfigKeyFileForScope(true)
}

func (a *App) readConfigKey() (string, error) {
	raw, err := os.ReadFile(a.ConfigKeyFile)
	if err != nil {
		return "", cliError("missing encryption key file at %s; run: ib config new [PROFILE]", a.ConfigKeyFile)
	}
	_ = a.protectConfigKeyFileForScope(false)
	return strings.TrimSpace(string(raw)), nil
}

func (a *App) encryptPassword(password string) (string, error) {
	if strings.HasPrefix(password, encryptedWindowsDPAPIPrefix) {
		return password, nil
	}
	if strings.HasPrefix(password, encryptedPasswordPrefix) {
		return password, nil
	}
	return a.encryptCurrentPassword(password)
}

func (a *App) decryptPassword(password string) (string, error) {
	if strings.HasPrefix(password, encryptedWindowsDPAPIPrefix) {
		return decryptWindowsDPAPIPassword(password)
	}
	return a.decryptFernetPassword(password)
}

func (a *App) decryptFernetPassword(password string) (string, error) {
	if !strings.HasPrefix(password, encryptedPasswordPrefix) {
		return password, nil
	}
	key, err := a.readConfigKey()
	if err != nil {
		return "", err
	}
	return decryptFernet(key, password)
}

func readINI(path string) (map[string]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sections := map[string]map[string]string{}
	current := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			current = strings.TrimSpace(line[1:strings.Index(line, "]")])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		if current == "" {
			continue
		}
		index := strings.Index(line, "=")
		if index < 0 {
			index = strings.Index(line, ":")
		}
		if index < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:index]))
		value := strings.TrimSpace(line[index+1:])
		sections[current][key] = value
	}
	return sections, scanner.Err()
}

func (a *App) readConfigProfiles(decrypt bool) (string, map[string]Profile, bool, error) {
	sections, err := readINI(a.ConfigFile)
	if err != nil {
		return "", nil, false, err
	}

	profiles := map[string]Profile{}
	for section, values := range sections {
		if !strings.HasPrefix(section, "profile:") {
			continue
		}
		name, err := normalizeProfileName(strings.TrimPrefix(section, "profile:"))
		if err != nil {
			return "", nil, false, err
		}
		profile := profileFromValues(name, values)
		if decrypt && profile.Password != "" {
			profile.Password, err = a.decryptPassword(profile.Password)
			if err != nil {
				return "", nil, false, cliError("cannot decrypt password in %s: %v", a.ConfigFile, err)
			}
		}
		profiles[name] = profile
	}

	if len(profiles) > 0 {
		meta, ok := sections["meta"]
		if !ok {
			return "", nil, false, cliError("missing [meta] section in %s", a.ConfigFile)
		}
		defaultProfile, err := normalizeProfileName(meta["default_profile"])
		if err != nil {
			return "", nil, false, err
		}
		return defaultProfile, profiles, false, nil
	}

	if meta, ok := sections["meta"]; ok {
		if rawDefault := strings.TrimSpace(meta["default_profile"]); rawDefault != "" {
			defaultProfile, err := normalizeProfileName(rawDefault)
			if err != nil {
				return "", nil, false, err
			}
			return defaultProfile, profiles, false, nil
		}
	}

	if values, ok := sections["default"]; ok {
		profile := profileFromValues(defaultProfileName, values)
		if decrypt && profile.Password != "" {
			profile.Password, err = a.decryptPassword(profile.Password)
			if err != nil {
				return "", nil, false, err
			}
		}
		return defaultProfileName, map[string]Profile{defaultProfileName: profile}, true, nil
	}

	return "", nil, false, cliError("no profiles configured in %s; run: ib config new [PROFILE]", a.ConfigFile)
}

type configFileData struct {
	Location        configLocation
	DefaultProfile  string
	Profiles        map[string]Profile
	Legacy          bool
	Settings        ConfigSettings
	SettingsMissing bool
}

type mergedConfigData struct {
	DefaultProfile   string
	Profiles         map[string]Profile
	ProfileLocations map[string]configLocation
	FileData         map[configScope]configFileData
	Settings         ConfigSettings
	SettingsLocation configLocation
}

func (a *App) readConfigFileData(location configLocation, decrypt bool) (configFileData, bool, error) {
	if _, err := os.Stat(location.File); err != nil {
		if os.IsNotExist(err) {
			return configFileData{}, false, nil
		}
		return configFileData{}, false, err
	}
	var data configFileData
	err := a.withConfigLocation(location, func() error {
		defaultProfile, profiles, legacy, err := a.readConfigProfiles(decrypt)
		if err != nil {
			return err
		}
		settings, settingsMissing, err := a.readConfigSettings()
		if err != nil {
			return err
		}
		data = configFileData{
			Location:        location,
			DefaultProfile:  defaultProfile,
			Profiles:        profiles,
			Legacy:          legacy,
			Settings:        settings,
			SettingsMissing: settingsMissing,
		}
		return nil
	})
	if err != nil {
		return configFileData{}, true, err
	}
	return data, true, nil
}

func (a *App) readMergedConfig(decrypt bool) (mergedConfigData, error) {
	merged := mergedConfigData{
		Profiles:         map[string]Profile{},
		ProfileLocations: map[string]configLocation{},
		FileData:         map[configScope]configFileData{},
		Settings:         defaultConfigSettings(),
	}
	for _, location := range a.readConfigLocations() {
		data, exists, err := a.readConfigFileData(location, decrypt)
		if err != nil {
			if skipUnreadableGlobalConfig(location, err) {
				continue
			}
			return mergedConfigData{}, err
		}
		if !exists {
			continue
		}
		merged.FileData[location.Scope] = data
		merged.DefaultProfile = data.DefaultProfile
		merged.Settings = data.Settings
		merged.SettingsLocation = location
		for name, profile := range data.Profiles {
			merged.Profiles[name] = profile
			merged.ProfileLocations[name] = location
		}
	}
	return merged, nil
}

func skipUnreadableGlobalConfig(location configLocation, err error) bool {
	return location.Scope == globalConfigScope && os.IsPermission(err)
}

func (a *App) writeConfigProfiles(defaultProfile string, profiles map[string]Profile) error {
	settings, _, err := a.readConfigSettings()
	if err != nil {
		settings = defaultConfigSettings()
	}
	return a.writeConfigProfilesWithSettings(defaultProfile, profiles, settings)
}

func (a *App) writeConfigProfilesWithSettings(defaultProfile string, profiles map[string]Profile, settings ConfigSettings) error {
	return a.writeConfigProfilesWithSettingsMode(defaultProfile, profiles, settings, false)
}

func (a *App) writeConfigProfilesWithExternalDefault(defaultProfile string, profiles map[string]Profile, settings ConfigSettings) error {
	return a.writeConfigProfilesWithSettingsMode(defaultProfile, profiles, settings, true)
}

func (a *App) writeConfigProfilesPreservingDefault(defaultProfile string, profiles map[string]Profile, settings ConfigSettings) error {
	if _, ok := profiles[defaultProfile]; ok {
		return a.writeConfigProfilesWithSettings(defaultProfile, profiles, settings)
	}
	return a.writeConfigProfilesWithExternalDefault(defaultProfile, profiles, settings)
}

func (a *App) writeConfigProfilesWithSettingsMode(defaultProfile string, profiles map[string]Profile, settings ConfigSettings, allowExternalDefault bool) error {
	defaultProfile, err := normalizeProfileName(defaultProfile)
	if err != nil {
		return err
	}
	if _, ok := profiles[defaultProfile]; !ok && !allowExternalDefault {
		return cliError("default profile %q does not exist", defaultProfile)
	}
	settings = settings.complete()
	if a.activeConfigIsGlobal() {
		group := settings.GlobalGroup
		if group == "" {
			group = a.globalConfigGroup
		}
		group, err = a.prepareGlobalConfigGroup(group)
		if err != nil {
			return err
		}
		settings.GlobalGroup = group
	}
	if err := a.ensureConfigDir(); err != nil {
		return err
	}
	if err := a.protectConfigDirForScope(true); err != nil {
		return err
	}

	var builder strings.Builder
	builder.WriteString("[meta]\n")
	builder.WriteString("default_profile = " + defaultProfile + "\n")
	builder.WriteString(configCacheTTLKey + " = " + strconv.Itoa(settings.CacheTTLSeconds) + "\n")
	builder.WriteString(configDNSSearchWorkerLimitKey + " = " + strconv.Itoa(settings.DNSSearchWorkerLimit) + "\n")
	builder.WriteString(configRecordsCacheSWRKey + " = " + strconv.Itoa(settings.RecordsCacheSWRSeconds) + "\n")
	builder.WriteString(configMaxBackgroundWorkerWaitKey + " = " + strconv.Itoa(settings.MaxBackgroundWorkerWaitSeconds) + "\n")
	builder.WriteString(configCompletionCachePrefetchKey + " = " + strconv.FormatBool(settings.CompletionCachePrefetch) + "\n")
	builder.WriteString(configAuditLoggingEnabledKey + " = " + strconv.FormatBool(settings.AuditLoggingEnabled) + "\n")
	builder.WriteString(configAuditLoggingMethodKey + " = " + settings.AuditLogMethod + "\n")
	builder.WriteString(configAuditLogFileKey + " = " + settings.AuditLogFile + "\n")
	builder.WriteString(configSSOEnabledKey + " = " + strconv.FormatBool(settings.SSOEnabled) + "\n")
	builder.WriteString(configSSOTenantIDKey + " = " + settings.SSOTenantID + "\n")
	builder.WriteString(configSSOClientIDKey + " = " + settings.SSOClientID + "\n")
	builder.WriteString(configSSOScopesKey + " = " + settings.SSOScopes + "\n")
	builder.WriteString(configSSOAllowedGroupsKey + " = " + settings.SSOAllowedGroups + "\n")
	if a.activeConfigIsGlobal() {
		builder.WriteString(configGlobalGroupKey + " = " + settings.GlobalGroup + "\n")
	}
	builder.WriteString("\n")

	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	keys := []string{"server", "read_server", "username", "password", "wapi_version", "dns_view", "default_zone", "verify_ssl", "timeout"}
	for _, name := range names {
		profile := profiles[name].complete()
		encryptedPassword, err := a.encryptPassword(profile.Password)
		if err != nil {
			return err
		}
		profile.Password = encryptedPassword
		values := profile.values()
		builder.WriteString("[profile:" + name + "]\n")
		for _, key := range keys {
			builder.WriteString(key + " = " + values[key] + "\n")
		}
		builder.WriteString("\n")
	}

	if err := os.WriteFile(a.ConfigFile, []byte(builder.String()), 0o600); err != nil {
		return err
	}
	return a.protectConfigFileForScope(true)
}

func (a *App) loadConfig(required bool) (Profile, error) {
	return a.loadConfigProfile("", required)
}

func (a *App) loadConfigProfile(profileName string, required bool) (Profile, error) {
	original := a.currentConfigLocation()
	originalGroup := a.globalConfigGroup
	merged, err := a.readMergedConfig(false)
	if err != nil {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		return Profile{}, err
	}
	if len(merged.Profiles) == 0 {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		if required {
			return Profile{}, cliError("no configuration file found. Run: ib config new [PROFILE] or ib config new --global-config [PROFILE]")
		}
		return Profile{}, os.ErrNotExist
	}
	selected := merged.DefaultProfile
	if strings.TrimSpace(profileName) != "" {
		selected, err = normalizeProfileName(profileName)
		if err != nil {
			a.useConfigLocation(original)
			a.globalConfigGroup = originalGroup
			return Profile{}, err
		}
	}
	location, ok := merged.ProfileLocations[selected]
	if !ok {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		if required {
			return Profile{}, cliError("profile %q does not exist in local config %s or global config %s", selected, a.localConfigLocation().File, a.globalConfigLocation().File)
		}
		return Profile{}, os.ErrNotExist
	}
	data, _, err := a.readConfigFileData(location, true)
	if err != nil {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		return Profile{}, err
	}
	profile, ok := data.Profiles[selected]
	if !ok {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		return Profile{}, profileNotFoundError{profile: selected, path: location.File}
	}
	a.activateConfigLocation(location, data.Settings)
	profile, err = a.prepareLoadedProfile(selected, profile, location.File)
	if err != nil {
		a.useConfigLocation(original)
		a.globalConfigGroup = originalGroup
		return Profile{}, err
	}
	if location.Scope == localConfigScope && (data.Legacy || data.SettingsMissing) {
		rewriteProfiles := data.Profiles
		if data.Legacy {
			rewriteProfiles = map[string]Profile{data.DefaultProfile: profile}
		}
		if err := a.writeConfigProfilesPreservingDefault(data.DefaultProfile, rewriteProfiles, data.Settings); err != nil {
			a.useConfigLocation(original)
			a.globalConfigGroup = originalGroup
			return Profile{}, err
		}
	}
	return profile, nil
}

func (a *App) loadConfigProfileActive(profileName string, required bool) (Profile, error) {
	if _, err := os.Stat(a.ConfigFile); err != nil {
		if required {
			return Profile{}, cliError("no configuration file found. Run: ib config new [PROFILE]")
		}
		return Profile{}, err
	}
	defaultProfile, profiles, legacy, err := a.readConfigProfiles(true)
	if err != nil {
		return Profile{}, err
	}
	settings, settingsMissing, err := a.readConfigSettings()
	if err != nil {
		return Profile{}, err
	}
	if a.activeConfigIsGlobal() && settings.GlobalGroup != "" {
		a.globalConfigGroup = settings.GlobalGroup
	}
	selected := defaultProfile
	if strings.TrimSpace(profileName) != "" {
		selected, err = normalizeProfileName(profileName)
		if err != nil {
			return Profile{}, err
		}
	}
	profile, ok := profiles[selected]
	if !ok {
		return Profile{}, profileNotFoundError{profile: selected, path: a.ConfigFile}
	}
	profile, err = a.prepareLoadedProfile(selected, profile, a.ConfigFile)
	if err != nil {
		return Profile{}, err
	}
	// Global profiles are usually group-readable but not group-writable. Avoid
	// surprising normal users with a config rewrite while loading /etc/ib/config.
	if (legacy || settingsMissing) && !a.activeConfigIsGlobal() {
		rewriteProfiles := profiles
		if legacy {
			rewriteProfiles = map[string]Profile{defaultProfile: profile}
		}
		if err := a.writeConfigProfilesPreservingDefault(defaultProfile, rewriteProfiles, settings); err != nil {
			return Profile{}, err
		}
	}
	return profile, nil
}

func (a *App) prepareLoadedProfile(selected string, profile Profile, configFile string) (Profile, error) {
	profile = profile.complete()
	if profile.Server == "" || profile.Username == "" || profile.Password == "" {
		return Profile{}, cliError("profile %q is missing server, username, or password in %s", selected, configFile)
	}
	var err error
	profile.Server, err = normalizeServer(profile.Server)
	if err != nil {
		return Profile{}, err
	}
	if profile.ReadServer != "" {
		profile.ReadServer, err = normalizeServer(profile.ReadServer)
		if err != nil {
			return Profile{}, err
		}
	}
	profile.DNSView = a.resolveDNSView(profile)
	return profile, nil
}

type profileNotFoundError struct {
	profile string
	path    string
}

func (e profileNotFoundError) Error() string {
	return fmt.Sprintf("profile %q does not exist in %s", e.profile, e.path)
}

func isProfileNotFound(err error) bool {
	_, ok := err.(profileNotFoundError)
	return ok
}

func (a *App) defaultConfigValues() Profile {
	merged, err := a.readMergedConfig(false)
	if err == nil && len(merged.Profiles) > 0 {
		return merged.Profiles[merged.DefaultProfile].complete()
	}
	return Profile{Name: defaultProfileName}
}

const sessionParentPIDEnv = "IB_SHELL_PID"

func sessionParentPID() int {
	if raw := strings.TrimSpace(os.Getenv(sessionParentPIDEnv)); raw != "" {
		if pid, err := strconv.Atoi(raw); err == nil && pid > 0 {
			return pid
		}
	}
	return os.Getppid()
}

func sessionCandidatePIDs() []int {
	seen := map[int]bool{}
	var pids []int
	for _, pid := range []int{sessionParentPID(), os.Getppid(), processParentPID(os.Getppid())} {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}

func sessionFileForPID(kind, prefix string, pid int) string {
	return filepath.Join(sessionBaseDir(kind), fmt.Sprintf("%s-%d.json", prefix, pid))
}

func sessionFile(kind, prefix string) string {
	return sessionFileForPID(kind, prefix, sessionParentPID())
}

func (a *App) readSessionZone(profileName string) string {
	return readSessionValueFromSessionFiles("active-zones", "active-zone-session", "zone", profileName)
}

func (a *App) writeSessionZone(zoneName, profileName string) error {
	payload := map[string]any{
		"zone":       zoneName,
		"profile":    profileName,
		"parent_pid": sessionParentPID(),
	}
	return writeSessionValue(sessionFile("active-zones", "active-zone-session"), payload)
}

func (a *App) readSessionView() string {
	return readSessionValueFromSessionFiles("active-views", "active-view-session", "view", "")
}

func (a *App) writeSessionView(viewName string) error {
	payload := map[string]any{
		"view":       viewName,
		"parent_pid": sessionParentPID(),
	}
	return writeSessionValue(sessionFile("active-views", "active-view-session"), payload)
}

func readSessionValueFromSessionFiles(kind, prefix, key, profileName string) string {
	for _, pid := range sessionCandidatePIDs() {
		if value := readSessionValue(sessionFileForPID(kind, prefix, pid), key, profileName, pid); value != "" {
			return value
		}
	}
	return ""
}

func readSessionValue(path, key, profileName string, parentPID int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if intFromAny(payload["parent_pid"]) != parentPID {
		return ""
	}
	if profileName != "" && strings.TrimSpace(fmt.Sprint(payload["profile"])) != profileName {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(payload[key]))
}

func writeSessionValue(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	if err := protectConfigDir(filepath.Dir(path)); err != nil {
		return err
	}
	return protectPrivateFile(path)
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func (a *App) resolveDNSView(profile Profile) string {
	if view := strings.TrimSpace(a.dnsViewOverride); view != "" {
		return view
	}
	if view := strings.TrimSpace(a.readSessionView()); view != "" {
		return view
	}
	if view := strings.TrimSpace(os.Getenv(defaultViewEnv)); view != "" {
		return view
	}
	if profile.DNSView != "" {
		return profile.DNSView
	}
	return "default"
}

func (a *App) resolveDNSZone(profile Profile, explicit string) (string, error) {
	if explicit != "" {
		return normalizeZoneName(explicit)
	}
	if zone := strings.TrimSpace(a.dnsZoneOverride); zone != "" {
		return normalizeZoneName(zone)
	}
	if zone := a.readSessionZone(profile.Name); zone != "" {
		return normalizeZoneName(zone)
	}
	if zone := strings.TrimSpace(os.Getenv(defaultZoneEnv)); zone != "" {
		return normalizeZoneName(zone)
	}
	if profile.DefaultZone != "" {
		return normalizeZoneName(profile.DefaultZone)
	}
	return "", cliError("DNS zone is required. Use --zone, ib dns zone use, export %s, or set a default zone with: ib config new", defaultZoneEnv)
}

func (a *App) dnsContextLine() string {
	profile := a.defaultConfigValues()
	profileName := profile.Name
	if profileName == "" {
		profileName = defaultProfileName
	}
	view := a.resolveDNSView(profile)
	zone := strings.TrimSpace(a.dnsZoneOverride)
	source := "command override"
	if zone == "" {
		zone = a.readSessionZone(profileName)
		source = "shell session"
	}
	if zone == "" {
		zone = strings.TrimSpace(os.Getenv(defaultZoneEnv))
		source = defaultZoneEnv
	}
	if zone == "" {
		zone = profile.DefaultZone
		source = "configured default"
	}
	if zone == "" {
		zone = "not set"
		source = "not set"
	}
	return contextTitleStyle.Render("Current Context:") + " " + strings.Join([]string{
		renderContextPair("Profile", profileName, contextProfileValueStyle),
		renderContextPair("View", view, contextViewValueStyle),
		renderContextPair("Zone", zone, contextZoneValueStyle) + " " + contextSourceStyle.Render("("+source+")"),
	}, " | ")
}
