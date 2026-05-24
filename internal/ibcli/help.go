package ibcli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	helpPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#38bdf8")).
			Padding(1, 2)
	helpTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	helpSubtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
	helpSectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	helpCommandStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	helpExampleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a7f3d0"))
)

func (a *App) installHelp(root *cobra.Command) {
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), a.renderHelp(cmd))
		})
		cmd.SetUsageFunc(func(cmd *cobra.Command) error {
			fmt.Fprint(cmd.ErrOrStderr(), a.renderUsage(cmd))
			return nil
		})
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
}

func (a *App) renderHelp(cmd *cobra.Command) string {
	var blocks []string
	blocks = append(blocks, a.helpHeader(cmd))
	if isDNSCommand(cmd) {
		blocks = append(blocks, a.helpContext())
	}
	blocks = append(blocks, a.usageBlock(cmd))
	if details := a.commandDetails(cmd); details != "" {
		blocks = append(blocks, details)
	}
	if cmd.HasAvailableSubCommands() {
		blocks = append(blocks, a.childCommandsBlock(cmd))
	}
	if flags := localOptionsBlock(cmd); flags != "" {
		blocks = append(blocks, flags)
	}
	if flags := globalOptionsBlock(cmd); flags != "" {
		blocks = append(blocks, flags)
	}
	if examples := examplesBlock(cmd); examples != "" {
		blocks = append(blocks, examples)
	}
	if hint := helpDetailHint(cmd); hint != "" {
		blocks = append(blocks, hint)
	}
	return strings.Join(blocks, "\n") + "\n"
}

func (a *App) renderUsage(cmd *cobra.Command) string {
	var blocks []string
	lines := []string{cmd.UseLine()}
	if cmd.HasAvailableSubCommands() {
		lines = append(lines, cmd.CommandPath()+" <"+childArgumentName(cmd)+">")
	}
	blocks = append(blocks, sectionWithLines("Usage", lines))
	if cmd.HasAvailableSubCommands() {
		blocks = append(blocks, a.childCommandsBlock(cmd))
	}
	if details := usageDetails(cmd); details != "" {
		blocks = append(blocks, details)
	}
	return strings.Join(blocks, "\n") + "\n"
}

func (a *App) helpHeader(cmd *cobra.Command) string {
	title := helpTitleStyle.Render(cmd.CommandPath())
	if cmd.Short == "" {
		return title
	}
	return title + helpSubtitleStyle.Render(" - "+cmd.Short)
}

func (a *App) usageBlock(cmd *cobra.Command) string {
	lines := []string{cmd.UseLine()}
	if cmd.HasAvailableSubCommands() {
		lines = append(lines, cmd.CommandPath()+" <"+childArgumentName(cmd)+">")
	}
	return sectionWithLines("Usage", lines)
}

func (a *App) commandDetails(cmd *cobra.Command) string {
	switch cmd.CommandPath() {
	case "ib":
		return sectionWithLines("Workflow", []string{
			"ib config new --default  ->  ib dns zone use example.com  ->  ib dns list",
			"ib net list  ->  ib net next-ip 192.0.2.0/24 -n 3",
			`ib dns create host app 192.0.2.10 -c "Application host"`,
		})
	case "ib config":
		a.ensureConfigPathDefaults()
		storage := a.ConfigFile
		key := a.ConfigKeyFile
		if globalConfigSupported() {
			storage = fmt.Sprintf("local %s; Linux global %s merges when present", a.LocalConfigFile, a.GlobalConfigFile)
			key = fmt.Sprintf("local %s; selected global profiles use %s", a.LocalConfigKeyFile, a.GlobalConfigKeyFile)
		}
		return strings.Join([]string{
			sectionWithRows("Configuration Usage", [][]string{
				{"setup", "ib config new [PROFILE]"},
				{"global setup", "ib config new --global-config [PROFILE]"},
				{"edit", "ib config edit [PROFILE]"},
				{"switch", "ib config use PROFILE"},
				{"inspect", "ib config list"},
				{"completion", "ib config completion [bash|zsh|fish|windows]"},
				{"cache", "ib config cache status|clear"},
			}),
			sectionWithRows("Profile Details", [][]string{
				{"prompts", "server reachability/TLS trust, validated credentials, auto WAPI version, auto GCM read endpoint, DNS view, default zone, audit logging"},
				{"storage", storage},
				{"key", key},
				{"password", credentialProtectionDescription()},
			}),
		}, "\n")
	case "ib auth":
		return sectionWithRows("Azure SSO", [][]string{
			{"configure", "ib auth configure --tenant-id TENANT --client-id CLIENT"},
			{"login", "opens the system browser and caches the signed-in Azure identity"},
			{"gating", "WAPI requests require a valid Azure SSO identity before using the saved Infoblox service account"},
			{"tokens", "Azure tokens stay local and are not sent to Infoblox WAPI"},
			{"audit", "successful write audit events include sso_user, sso_tenant, and subject identifiers"},
			{"groups", "--allowed-groups accepts Azure group object IDs when group claims are emitted"},
		})
	case "ib dns":
		return sectionWithRows("Context Overrides", [][]string{
			{"zone", "--zone/-z overrides ib dns zone use, IB_ZONE, and configured default for one command"},
			{"view", "--view/-v overrides ib dns view use, IB_VIEW, and configured view for one command"},
		})
	case "ib config new":
		return sectionWithRows("Create Profile", [][]string{
			{"profile", "optional argument; blank prompt creates profile 'default'"},
			{"prompts", "endpoint reachability, TLS trust, credential validation, auto WAPI, auto GCM, DNS defaults, audit logging"},
			{"test", "connection test must pass before saving"},
			{"retry", "failed connection test shows a retry prompt"},
			{"default", "--default makes this profile the selected default"},
			{"global", "--global-config writes Linux shared config, key, and cache under /etc/ib; requires root"},
			{"example", "ib config new prod --default"},
		})
	case "ib config edit":
		return sectionWithRows("Edit Profile", [][]string{
			{"profile", "optional argument; omit to edit the current default profile"},
			{"password", "leave blank to keep the existing encrypted password"},
			{"audit", "adjust JSON Lines, syslog, or Windows Event Log audit logging"},
			{"test", "connection test must pass before saving changes"},
			{"retry", "failed connection test shows a retry prompt"},
			{"default", "--default makes this profile the selected default"},
			{"global", "--global-config edits Linux shared config, key, and cache under /etc/ib; requires root"},
			{"example", "ib config edit prod"},
		})
	case "ib config list":
		return sectionWithRows("List Profiles", [][]string{
			{"shows", "profile, default, username, server, read endpoint, WAPI, SSL, DNS view, default zone"},
			{"metadata", "table output also shows merged config metadata"},
			{"formats", "-o table, -o json, or -o csv"},
			{"empty", "prints setup guidance when no profiles exist"},
		})
	case "ib config use":
		return sectionWithRows("Select Default Profile", [][]string{
			{"effect", "updates the local default_profile override"},
			{"scope", "can select local profiles or global profiles merged from /etc/ib/config"},
			{"completion", "PROFILE completes from configured profile names"},
			{"example", "ib config use prod"},
		})
	case "ib config delete":
		return sectionWithRows("Delete Profile", [][]string{
			{"allowed", "removes a non-default profile from the config file"},
			{"cache", "clears cache entries for the deleted local profile"},
			{"blocked", "the current default profile cannot be deleted"},
			{"before", "run ib config use OTHER_PROFILE to delete the current default"},
			{"example", "ib config delete old-profile"},
		})
	case "ib dns create":
		return sectionWithRows("Create Record Usage", [][]string{
			{"types", strings.Join(supportedRecordTypes(), ", ")},
			{"zone", "--zone -> ib dns zone use -> IB_ZONE -> configured default"},
			{"ptr", "ib dns create ptr <ip-address> <ptr-target>; reverse zone is auto-detected"},
			{"ttl", "optional; omit to use the zone default"},
			{"example", `ib dns create host app 192.0.2.10 -c "Application host"`},
			{"creates", "HOST app.example.com with IPv4 address 192.0.2.10"},
		})
	case "ib dns list":
		return sectionWithRows("List Records Usage", [][]string{
			{"scope", "current zone by default; use -r to include child zones"},
			{"type", "-t host or --type a,txt filters record types"},
			{"exclude", "-e keyword excludes matching name, value, or comment"},
			{"sort", "-s name or --sort=-name sorts by field; blank --sort uses name"},
			{"columns", "-C name,value prints selected output columns"},
			{"details", "--details loads explicit TTL/detail fields and can be slower"},
		})
	case "ib dns search":
		return sectionWithRows("Search Records Usage", [][]string{
			{"scope", "current zone by default; FQDN can infer zone; use -r child or -g view-wide"},
			{"type", "-t host or --type a,txt filters record types"},
			{"exclude", "-e keyword excludes matching name, value, or comment"},
			{"sort", "-s name or --sort=-name sorts by field; blank --sort uses name"},
			{"columns", "-C name,value prints selected output columns"},
		})
	case "ib dns next-ip":
		return sectionWithRows("Next IP Usage", [][]string{
			{"network", "IPv4 network or container CIDR such as 192.0.2.0/24"},
			{"view", "--network-view chooses the IPAM network view when a CIDR is ambiguous"},
			{"type", "networks and containers can both request next_available_ip"},
			{"num", "-n 3 or --num 3 requests multiple addresses"},
			{"exclude", "-e 192.0.2.10 excludes an address from allocation; repeatable"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net":
		return sectionWithRows("IPAM Usage", [][]string{
			{"views", "ib net view list shows IPAM network views"},
			{"networks", "ib net list [SEARCH] lists IPv4 networks and containers"},
			{"search", "ib net search KEYWORD matches type, CIDR, network view, or comment"},
			{"cidr", "CIDR matches include related parent and child networks or containers in the same view"},
			{"details", "ib net show NETWORK displays one network or container"},
			{"address", "ib net address IP displays IPv4 address state"},
			{"next-ip", "ib net next-ip NETWORK requests available IPv4 addresses"},
		})
	case "ib net view":
		return sectionWithRows("Network View Usage", [][]string{
			{"list", "ib net view list"},
			{"shows", "IPAM network view name and comment"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net view list":
		return sectionWithRows("Network View List Usage", [][]string{
			{"shows", "IPAM network view name and comment"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net list":
		return sectionWithRows("Network List Usage", [][]string{
			{"search", "optional positional search matches type, CIDR, network view, or comment"},
			{"cidr", "CIDR matches include related parent and child networks or containers in the same view"},
			{"view", "omit --network-view to scan all IPAM views, or set it to one view"},
			{"cache", "expired cache is shown immediately; --refresh waits for fresh WAPI data"},
			{"sort", "-s network or --sort=-comment sorts by field; blank --sort uses network"},
			{"columns", "-C network,type,comment prints selected output columns"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net search":
		return sectionWithRows("Network Search Usage", [][]string{
			{"keyword", "matches type, CIDR, network view, or comment"},
			{"cidr", "CIDR matches include related parent and child networks or containers in the same view"},
			{"view", "omit --network-view to scan all IPAM views, or set it to one view"},
			{"cache", "expired cache is shown immediately; --refresh waits for fresh WAPI data"},
			{"sort", "-s network_view or --sort=-network sorts by field"},
			{"columns", "-C network,type,comment prints selected output columns"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net show":
		return sectionWithRows("Network Details Usage", [][]string{
			{"network", "IPv4 CIDR such as 192.0.2.0/24"},
			{"view", "--network-view chooses the IPAM network view when a CIDR is ambiguous"},
			{"type", "shows whether the object is a network or container"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net address":
		return sectionWithRows("Address Details Usage", [][]string{
			{"ip", "IPv4 address such as 192.0.2.10"},
			{"view", "--network-view narrows the lookup to one IPAM network view"},
			{"shows", "network, parent container, status, types, names, MAC, lease state, and comment when available"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib net next-ip":
		return sectionWithRows("Next IP Usage", [][]string{
			{"network", "IPv4 CIDR such as 192.0.2.0/24"},
			{"view", "--network-view chooses the IPAM network view when a CIDR is ambiguous"},
			{"type", "networks and containers can both request next_available_ip"},
			{"num", "-n 3 or --num 3 requests multiple addresses"},
			{"exclude", "-e 192.0.2.10 excludes an address from allocation; repeatable"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib dns zone list":
		return sectionWithRows("Zone List Usage", [][]string{
			{"search", "optional positional search matches zone name or comment"},
			{"type", "-t FORWARD or --type IPV4,IPV6 filters zone formats"},
			{"exclude", "-e keyword excludes matching zone name or comment"},
			{"sort", "-s zone or --sort=-comment sorts by field; blank --sort uses zone"},
			{"columns", "-C zone,comment prints selected output columns"},
		})
	case "ib dns delete":
		return sectionWithRows("Delete Record Usage", [][]string{
			{"forward", "ib dns delete <type> <record-name> [zone]"},
			{"ptr", "ib dns delete ptr <ip-address>"},
			{"confirm", "prompts before deleting; use -y to skip"},
			{"duplicates", "interactive table mode prompts you to choose one matching record"},
			{"example", "ib dns delete a app"},
		})
	case "ib config completion":
		return strings.Join([]string{
			sectionWithRows("Completion Usage", [][]string{
				{"no arg", "print setup instructions"},
				{"bash", "generate Bash completion"},
				{"zsh", "generate Zsh completion"},
				{"fish", "generate Fish completion"},
				{"windows", "install PowerShell completion on Windows"},
			}),
			sectionWithLines("Setup", []string{
				"ib config completion bash > ~/.ib-complete.bash",
				`printf '\n# ib shell completion\n. ~/.ib-complete.bash\n' >> ~/.bashrc`,
				"ib config completion zsh > ~/.ib-complete.zsh",
				"ib config completion fish > ~/.config/fish/completions/ib.fish",
				"ib config completion windows",
			}),
		}, "\n")
	case "ib config cache":
		return sectionWithRows("Cache Usage", [][]string{
			{"status", "show DNS and IPAM cache entries with age and expiry"},
			{"clear", "delete DNS and IPAM cache entries"},
			{"scope", "DNS caches use profile/view/zone; IPAM caches use profile/network view/IP"},
			{"storage", "~/.ib/cache.sqlite3 locally or /etc/ib/cache.sqlite3 for Linux global profiles"},
		})
	case "ib config cache status":
		return sectionWithRows("Cache Status", [][]string{
			{"shows", "kind, profile, view/network view, zone/IP, serial, items, age, stale_expires"},
			{"stats", "table output adds a colored summary footer"},
			{"json", "returns statistics and entries"},
			{"csv", "keeps row-only output for scripts"},
			{"formats", "-o table, -o json, or -o csv"},
		})
	case "ib config cache clear":
		return sectionWithRows("Cache Clear", [][]string{
			{"clears", "local DNS and IPAM cache entries"},
			{"keeps", "configuration profiles, encryption key, and active session files"},
		})
	}
	return ""
}

func (a *App) helpContext() string {
	return a.dnsContextLine()
}

func (a *App) childCommandsBlock(cmd *cobra.Command) string {
	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	var rows [][]string
	for _, child := range children {
		if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() {
			continue
		}
		rows = append(rows, []string{child.Name(), child.Short})
	}
	if len(rows) == 0 {
		return ""
	}
	return sectionWithRows(childSectionTitle(cmd), rows)
}

func childArgumentName(cmd *cobra.Command) string {
	if cmd == cmd.Root() {
		return "module"
	}
	return "command"
}

func childSectionTitle(cmd *cobra.Command) string {
	if cmd == cmd.Root() {
		return "Modules"
	}
	return "Commands"
}

func helpDetailHint(cmd *cobra.Command) string {
	if !cmd.HasAvailableSubCommands() {
		return ""
	}
	return helpSubtitleStyle.Render(fmt.Sprintf(`Use "%s <%s> --help" for more detail.`, cmd.CommandPath(), childArgumentName(cmd)))
}

func usageDetails(cmd *cobra.Command) string {
	switch cmd.CommandPath() {
	case "ib dns search":
		return strings.Join([]string{
			sectionWithLines("Example", []string{"ib dns search app", "ib dns search ben-dr-vss.net.latrobe.edu.au"}),
			helpSubtitleStyle.Render(`Use "ib dns search -h" for more info.`),
		}, "\n")
	case "ib dns next-ip":
		return strings.Join([]string{
			sectionWithLines("Example", []string{"ib dns next-ip 192.0.2.0/24 -n 3"}),
			helpSubtitleStyle.Render(`Use "ib dns next-ip -h" for more info.`),
		}, "\n")
	case "ib net next-ip":
		return strings.Join([]string{
			sectionWithLines("Example", []string{"ib net next-ip 192.0.2.0/24 -n 3"}),
			helpSubtitleStyle.Render(`Use "ib net next-ip -h" for more info.`),
		}, "\n")
	case "ib net search":
		return strings.Join([]string{
			sectionWithLines("Example", []string{"ib net search 10.129.0.0/16"}),
			helpSubtitleStyle.Render(`Use "ib net search -h" for more info.`),
		}, "\n")
	}
	return ""
}

func localOptionsBlock(cmd *cobra.Command) string {
	flagSets := []*pflag.FlagSet{cmd.NonInheritedFlags(), cmd.PersistentFlags()}
	if cmd == cmd.Root() {
		flagSets = []*pflag.FlagSet{cmd.LocalNonPersistentFlags()}
	}
	rows := flagRowsForCommand(cmd, false, flagSets...)
	if len(rows) == 0 {
		return ""
	}
	return sectionWithRows("Options", rows)
}

func globalOptionsBlock(cmd *cobra.Command) string {
	rows := [][]string{{"-h, --help", "show help for this command"}}
	flags := cmd.InheritedFlags()
	if cmd == cmd.Root() {
		flags = cmd.PersistentFlags()
	}
	rows = append(rows, flagRowsForCommand(cmd, false, flags)...)
	return sectionWithRows("Global Options", rows)
}

func flagsBlock(title string, flags *pflag.FlagSet, includeHelp bool) string {
	if flags == nil || !flags.HasAvailableFlags() {
		return ""
	}
	rows := flagRows(flags, includeHelp)
	if len(rows) == 0 {
		return ""
	}
	return sectionWithRows(title, rows)
}

func flagRows(flags *pflag.FlagSet, includeHelp bool) [][]string {
	return flagRowsForSets(includeHelp, flags)
}

func flagRowsForSets(includeHelp bool, sets ...*pflag.FlagSet) [][]string {
	return flagRowsForCommand(nil, includeHelp, sets...)
}

func flagRowsForCommand(cmd *cobra.Command, includeHelp bool, sets ...*pflag.FlagSet) [][]string {
	var rows [][]string
	seen := map[string]bool{}
	for _, flags := range sets {
		if flags == nil {
			continue
		}
		flags.VisitAll(func(flag *pflag.Flag) {
			if seen[flag.Name] || flag.Hidden || (!includeHelp && flag.Name == "help") || suppressFlagForCommand(cmd, flag.Name) {
				return
			}
			seen[flag.Name] = true
			name := "--" + flag.Name
			if flag.Shorthand != "" {
				name = "-" + flag.Shorthand + ", " + name
			}
			if flag.NoOptDefVal == "" && flag.Value.Type() != "bool" {
				name += " " + strings.ToUpper(flag.Value.Type())
			}
			detail := flag.Usage
			if flag.DefValue != "" && flag.DefValue != "false" && flag.DefValue != "-1" {
				detail += " (default " + flag.DefValue + ")"
			}
			rows = append(rows, []string{name, detail})
		})
	}
	return rows
}

func examplesBlock(cmd *cobra.Command) string {
	example := strings.TrimSpace(cmd.Example)
	if example == "" {
		return ""
	}
	return sectionWithLines("Examples", strings.Split(example, "\n"))
}

func sectionWithLines(title string, lines []string) string {
	var builder strings.Builder
	builder.WriteString(helpSectionStyle.Render(title))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		builder.WriteString("\n  ")
		builder.WriteString(helpCommandStyle.Render(line))
	}
	return builder.String()
}

func sectionWithRows(title string, rows [][]string) string {
	var builder strings.Builder
	builder.WriteString(helpSectionStyle.Render(title))
	labelWidth := 0
	for _, row := range rows {
		if len(row) > 0 && len(row[0]) > labelWidth {
			labelWidth = len(row[0])
		}
	}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		label := row[0]
		detail := ""
		if len(row) > 1 {
			detail = row[1]
		}
		builder.WriteString("\n  ")
		builder.WriteString(helpCommandStyle.Render(label))
		if detail != "" {
			builder.WriteString(strings.Repeat(" ", labelWidth-len(label)+2))
			builder.WriteString(helpSubtitleStyle.Render(detail))
		}
	}
	return builder.String()
}

func isDNSCommand(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "ib dns" || strings.HasPrefix(path, "ib dns ")
}
