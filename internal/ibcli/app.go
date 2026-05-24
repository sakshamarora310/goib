package ibcli

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	configDirName                 = ".ib"
	configFileName                = "config"
	configKeyFileName             = "key"
	globalConfigDir               = "/etc/ib"
	defaultWAPIVersion            = "v2.12.3"
	defaultTimeoutSeconds         = 15
	defaultProfileName            = "default"
	defaultZoneEnv                = "IB_ZONE"
	defaultViewEnv                = "IB_VIEW"
	disableZoneOverrideAnnotation = "ibcli/disable-zone-override"
	disableDNSContextAnnotation   = "ibcli/disable-dns-context"
	tableOutput                   = "table"
	jsonOutput                    = "json"
	csvOutput                     = "csv"
	recordOutputType              = "RECORD"
	zoneObject                    = "zone_auth"
	viewObject                    = "view"
	gridObject                    = "grid"
	memberObject                  = "member"
	networkObject                 = "network"
	networkContainerObject        = "networkcontainer"
	networkViewObject             = "networkview"
	ipv4AddressObject             = "ipv4address"
	allRecordsObject              = "allrecords"
	wapiPageSize                  = 2000
)

var (
	errUsageDisplayed  = errors.New("usage displayed")
	errDeleteCancelled = errors.New("delete cancelled")

	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	warningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ef4444"))
	noteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#22d3ee"))

	contextTitleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	contextLabelStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cbd5e1"))
	contextProfileValueStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	contextViewValueStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa"))
	contextZoneValueStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	contextSourceStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	recordTotalBadgeStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e0f2fe")).Background(lipgloss.Color("#1d4ed8")).Padding(0, 1)
)

var (
	// Release tooling overrides these values with ldflags. Source builds keep a
	// usable version and fall back to the executable timestamp for the build date.
	Version   = "0.3.5"
	BuildDate = ""
)

// Version output intentionally uses fixed AEST (UTC+10), not local daylight
// saving time.
var aestLocation = time.FixedZone("AEST", 10*60*60)

type App struct {
	ConfigDir           string
	ConfigFile          string
	ConfigKeyFile       string
	LocalConfigDir      string
	LocalConfigFile     string
	LocalConfigKeyFile  string
	GlobalConfigDir     string
	GlobalConfigFile    string
	GlobalConfigKeyFile string
	Output              string
	Stdout              io.Writer
	Stderr              io.Writer
	Stdin               io.Reader
	gum                 *Gum

	dnsZoneOverride             string
	dnsViewOverride             string
	backgroundRecordRevalidator func(Profile, string) error
	backgroundZoneRefresher     func(Profile) error
	backgroundNetRefresher      func(Profile, string, string, string) error
	dnsDeleteRecordSelector     func(string, []TypedRecord) (TypedRecord, bool, error)
	dnsDeleteConfirmer          func(string, TypedRecord) (bool, error)
	auditSink                   func(ConfigSettings, []byte) error
	tlsRootCAs                  *x509.CertPool
	configScope                 configScope
	globalConfigGroup           string
	browserOpener              func(string) error
	ssoIdentity                *ssoIdentity
	ssoIdentityKey             string
}

func NewDefaultApp() (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot locate home directory: %w", err)
	}
	configDir := filepath.Join(home, configDirName)
	app := &App{
		ConfigDir:           configDir,
		ConfigFile:          filepath.Join(configDir, configFileName),
		ConfigKeyFile:       filepath.Join(configDir, configKeyFileName),
		LocalConfigDir:      configDir,
		LocalConfigFile:     filepath.Join(configDir, configFileName),
		LocalConfigKeyFile:  filepath.Join(configDir, configKeyFileName),
		GlobalConfigDir:     globalConfigDir,
		GlobalConfigFile:    filepath.Join(globalConfigDir, configFileName),
		GlobalConfigKeyFile: filepath.Join(globalConfigDir, configKeyFileName),
		Output:              tableOutput,
		Stdout:              os.Stdout,
		Stderr:              os.Stderr,
		Stdin:               os.Stdin,
	}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app, nil
}

func backgroundRefreshExecutable() (string, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	// Go test binaries re-run test code when executed; never use them as
	// detached cache refresh helpers.
	if executableIsGoTestBinary(executable) {
		return "", false, nil
	}
	return executable, true, nil
}

func executableIsGoTestBinary(executable string) bool {
	base := filepath.Base(executable)
	return strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe")
}

func (a *App) Execute(args []string) error {
	a.startCompletionCachePrefetch(args)
	if a.completeRecordSortValue(args) || a.completeZoneSortValue(args) || a.completeNetSortValue(args) || a.completeZoneListFlagNames(args) {
		return nil
	}
	root := a.RootCommand()
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetIn(a.Stdin)
	root.SetArgs(normalizeSortArgs(args))
	cmd, err := root.ExecuteC()
	if err == nil {
		return nil
	}
	if errors.Is(err, errUsageDisplayed) {
		return err
	}
	if isUsageError(err) && cmd != nil {
		if usageErr := cmd.Usage(); usageErr != nil {
			return usageErr
		}
		return errUsageDisplayed
	}
	return rewriteRootModuleError(err)
}

func (a *App) RootCommand() *cobra.Command {
	showVersion := false
	root := &cobra.Command{
		Use:               "ib",
		Short:             "Infoblox DNS and IPAM command line client",
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				a.PrintVersion()
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), a.renderHelp(cmd))
			a.PrintContext()
			return nil
		},
		Long: strings.TrimSpace(`Infoblox DNS and IPAM command line client.

Run "ib config new [PROFILE]" first to save an Infoblox profile with server,
credentials, DNS view, and default zone.
On Linux, "ib config new --global-config [PROFILE]" can create a shared
profile under /etc/ib for a configured group.

Common usage:
  ib dns view list
  ib dns view use "DNS Zone View"
  ib dns zone list
	  ib dns zone use example.com
	  ib dns list
	  ib dns search app
	  ib net list
	  ib net next-ip 192.0.2.0/24 -n 3
	  ib dns create host app 192.0.2.10 -c "Application host"
	  ib dns create ptr 192.0.2.10 app.example.com
	  ib dns edit host app 192.0.2.20 -t 300 -c "Application host"
	  ib dns delete a app`),
	}

	root.Flags().BoolVarP(
		&showVersion,
		"version",
		"v",
		false,
		"print version and AEST build date",
	)
	root.PersistentFlags().StringVarP(
		&a.Output,
		"output",
		"o",
		tableOutput,
		"output format: table, json, or csv",
	)
	_ = root.RegisterFlagCompletionFunc("output", outputFormatCompletion)
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		a.Output = strings.ToLower(strings.TrimSpace(a.Output))
		switch a.Output {
		case "", tableOutput:
			a.Output = tableOutput
		case jsonOutput, csvOutput:
		default:
			return fmt.Errorf("unsupported output format %q; use table, json, or csv", a.Output)
		}
		if commandDisablesZoneOverride(cmd) && commandFlagChanged(cmd, "zone") {
			if commandDisablesDNSContext(cmd) {
				return cliError("--zone/-z cannot be used with %s", cmd.CommandPath())
			}
			return cliError("--zone/-z cannot be used with ib dns zone list; use --view to choose a DNS view")
		}
		if commandDisablesViewOverride(cmd) && commandFlagChanged(cmd, "view") {
			return cliError("--view/-v cannot be used with %s; use --network-view to choose an IPAM network view", cmd.CommandPath())
		}
		return nil
	}

	root.AddCommand(a.configCommand())
	root.AddCommand(a.authCommand())
	root.AddCommand(a.dnsCommand())
	root.AddCommand(a.netCommand())
	a.installHelp(root)
	return root
}

func commandDisablesZoneOverride(cmd *cobra.Command) bool {
	// Zone-list is grouped under `ib dns`, but it scopes by DNS view instead of
	// a single active zone. The annotation lets help, completion, and execution
	// share the same command-specific suppression rule.
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations != nil && (current.Annotations[disableZoneOverrideAnnotation] == "true" || current.Annotations[disableDNSContextAnnotation] == "true") {
			return true
		}
	}
	return false
}

func commandDisablesViewOverride(cmd *cobra.Command) bool {
	return commandDisablesDNSContext(cmd)
}

func commandDisablesDNSContext(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations != nil && current.Annotations[disableDNSContextAnnotation] == "true" {
			return true
		}
	}
	return false
}

func commandFlagChanged(cmd *cobra.Command, name string) bool {
	for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), cmd.PersistentFlags()} {
		if flags == nil {
			continue
		}
		if flag := flags.Lookup(name); flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

func suppressFlagForCommand(cmd *cobra.Command, name string) bool {
	return (name == "zone" && commandDisablesZoneOverride(cmd)) || (name == "view" && commandDisablesViewOverride(cmd))
}

func normalizeSortArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	if strings.HasPrefix(args[0], "__complete") {
		return normalizeSortCompletionArgs(args)
	}
	// pflag treats a value like "-name" as another flag. Normalize known sort
	// flags before Cobra parses args so operators can type `--sort -name`.
	defaultField := defaultSortFieldForArgs(args)
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--sort" || arg == "-s" {
			if i+1 < len(args) {
				next := args[i+1]
				if strings.TrimSpace(next) == "" {
					normalized = append(normalized, "--sort="+defaultField)
					i++
					continue
				}
				if shouldConsumeSortValue(args, next) {
					normalized = append(normalized, "--sort="+next)
					i++
					continue
				}
			}
			normalized = append(normalized, "--sort="+defaultField)
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}

func normalizeSortCompletionArgs(args []string) []string {
	if len(args) < 4 || (!isRecordListOrSearchArgs(args) && !isZoneListArgs(args) && !isNetListOrSearchArgs(args)) {
		return args
	}
	normalized := append([]string(nil), args...)
	for i := 3; i < len(normalized)-1; i++ {
		// Cobra can complete values after `--sort`, but a bare short value flag
		// such as `-s <tab>` is parsed as missing its argument before completion.
		if normalized[i] == "-s" {
			normalized[i] = "--sort"
		}
	}
	return normalized
}

func defaultSortFieldForArgs(args []string) string {
	if isZoneListArgs(args) {
		return defaultZoneSortField
	}
	if isNetListOrSearchArgs(args) {
		return defaultNetSortField
	}
	return defaultRecordSortField
}

func shouldConsumeSortValue(args []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "--") {
		return false
	}
	if strings.HasPrefix(value, "-") {
		field := strings.TrimPrefix(value, "-")
		if isZoneListArgs(args) {
			return isZoneSortField(field)
		}
		if isNetListOrSearchArgs(args) {
			return isNetSortField(field)
		}
		return isRecordSortField(field)
	}
	return true
}

func isRecordListOrSearchArgs(args []string) bool {
	return containsArgSequence(args, []string{"dns", "list"}) || containsArgSequence(args, []string{"dns", "search"})
}

func isZoneListArgs(args []string) bool {
	return containsArgSequence(args, []string{"dns", "zone", "list"})
}

func isNetListOrSearchArgs(args []string) bool {
	return containsArgSequence(args, []string{"net", "list"}) || containsArgSequence(args, []string{"net", "search"})
}

func containsArgSequence(args []string, sequence []string) bool {
	if len(sequence) == 0 || len(args) < len(sequence) {
		return false
	}
	for index := 0; index <= len(args)-len(sequence); index++ {
		matches := true
		for offset, want := range sequence {
			if args[index+offset] != want {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func (a *App) PrintError(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, errUsageDisplayed) {
		return
	}
	var cliErr *CliError
	if errors.As(err, &cliErr) {
		fmt.Fprintln(a.Stderr, errorStyle.Render(cliErr.Error()))
		return
	}
	fmt.Fprintln(a.Stderr, errorStyle.Render("ERROR: "+err.Error()))
}

func (a *App) PrintSuccess(message string) {
	fmt.Fprintln(a.Stdout, successStyle.Render(message))
}

func (a *App) PrintInfo(message string) {
	fmt.Fprintln(a.Stdout, noteStyle.Render(message))
}

func (a *App) PrintWarning(message string) {
	fmt.Fprintln(a.Stderr, warningStyle.Render(message))
}

func (a *App) PrintNote(message string) {
	fmt.Fprintln(a.Stdout, noteStyle.Render(message))
}

func (a *App) PrintContext() {
	fmt.Fprintln(a.Stdout, a.dnsContextLine())
}

func (a *App) PrintVersion() {
	fmt.Fprintln(a.Stdout, VersionString())
}

func VersionString() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("ib %s (built %s)", version, effectiveBuildDate())
}

func effectiveBuildDate() string {
	buildDate := strings.TrimSpace(BuildDate)
	if buildDate != "" && buildDate != "unknown" {
		return formatBuildDateAEST(buildDate)
	}
	if executable, err := os.Executable(); err == nil {
		if info, err := os.Stat(executable); err == nil {
			return formatBuildTimeAEST(info.ModTime())
		}
	}
	return "unknown"
}

func formatBuildDateAEST(buildDate string) string {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, buildDate); err == nil {
			return formatBuildTimeAEST(parsed)
		}
	}
	return buildDate
}

func formatBuildTimeAEST(buildTime time.Time) string {
	return buildTime.In(aestLocation).Format("2006-01-02T15:04:05-07:00 MST")
}

func renderContextPair(label, value string, valueStyle lipgloss.Style) string {
	return contextLabelStyle.Render(label+":") + " " + valueStyle.Render(value)
}

func exactArgsOrUsage(want int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == want {
			return nil
		}
		if err := cmd.Usage(); err != nil {
			return err
		}
		return errUsageDisplayed
	}
}

func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.HasPrefix(message, "accepts ") ||
		strings.HasPrefix(message, "requires ") ||
		strings.Contains(message, " arg(s)") ||
		strings.Contains(message, "only received")
}

func (a *App) isTableOutput() bool {
	return a.Output == "" || a.Output == tableOutput
}

type CliError struct {
	Message string
}

func (e *CliError) Error() string {
	if strings.HasPrefix(e.Message, "ERROR: ") {
		return e.Message
	}
	return "ERROR: " + e.Message
}

func cliError(format string, args ...any) *CliError {
	return &CliError{Message: fmt.Sprintf(format, args...)}
}

func rewriteRootModuleError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.HasPrefix(message, "unknown command ") && strings.HasSuffix(message, ` for "ib"`) {
		return cliError("%s", strings.Replace(message, "unknown command", "unknown module", 1))
	}
	return err
}

func outputFormatCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	candidates := []string{
		tableOutput + "\tstyled table output",
		jsonOutput + "\tJSON output",
		csvOutput + "\tCSV output",
	}
	toComplete = strings.ToLower(strings.TrimSpace(toComplete))
	if toComplete == "" {
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		value := strings.SplitN(candidate, "\t", 2)[0]
		if strings.HasPrefix(value, toComplete) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, cobra.ShellCompDirectiveNoFileComp
}
