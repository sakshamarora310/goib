# gib

[![Latest release](https://img.shields.io/github/v/release/rwahyudi/gib?color=0ea5e9)](https://github.com/rwahyudi/gib/releases/latest)
![Go 1.24](https://img.shields.io/badge/go-1.24-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e)](LICENSE)

`ib` is a fast, lightweight, operator-focused CLI for managing Infoblox DNS and
IPAM work from the shell.

![ib cli preview](docs/assets/go-record1.gif)

Read-heavy workflows can use a validated Grid Master Candidate, and large
record/IPAM searches use SQLite caching plus bounded workers to stay
responsive.

## Features

- Profile management for creating, editing, switching, and deleting multiple Infoblox
  profiles with encrypted local passwords.
- Safe read/write routing: GET requests can use a validated GCM read endpoint,
  while POST, PUT, and DELETE stay on the primary Grid Master.
- DNS context from configured defaults, shell-session view/zone context,
  environment variables, or one-command `--view` and `--zone` overrides.
- DNS record workflows for listing, searching, creating, editing, and deleting
  records, including filtering, field sorting, selected output columns,
  interactive duplicate selection, and confirmation.
- IPAM read workflows for network views, IPv4 network/container list/search/details,
  address details, and next available IP lookup with network-view selection.
- Large-zone performance through `/allrecords`, SQLite caching,
  worker-limited global search, and stale-while-revalidate refreshes.
- Dynamic shell completion for profiles, views, zones, records, flags, record
  types, and output formats from the live `ib` binary.
- Optional Azure SSO gate for WAPI commands, using browser login while the
  saved Infoblox service account performs the actual WAPI calls.
- Optional JSON Lines audit logging for successful create, edit, and delete
  actions, with Azure SSO identity fields plus file/syslog support on Linux and
  Windows Event Log support on Windows.
- Compact operator output with colorful tables, current-context footers,
  JSON/CSV output, and progress display for larger searches.

## Install

For Fedora or EPEL, Copr is the shortest path. The package is named `gib` and
installs the command as `/usr/bin/ib`.

```bash
sudo dnf install dnf-plugins-core
sudo dnf copr enable rwahyudi/gib
sudo dnf install gib
```

GitHub release assets use stable filenames, so `/releases/latest/download/...`
always points at the newest published version.

Linux tarball:

```bash
curl -fL https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.tar.gz | tar -xz ib
sudo install -m 0755 ib /usr/local/bin/ib
ib -v
ib --help
```

RPM or DEB package:

```bash
curl -fLO https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.rpm
sudo dnf install ./ib_linux_amd64.rpm

curl -fLO https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.deb
sudo apt install ./ib_linux_amd64.deb
```

RPM and DEB packages install `ib` to `/usr/local/bin/ib` and Bash completion to
`/etc/bash_completion.d/ib`. For the tarball, install completion manually:

```bash
sudo mkdir -p /etc/bash_completion.d
ib config completion bash | sudo tee /etc/bash_completion.d/ib >/dev/null
```

Open a new shell after installing completion.

Windows ZIP:

```powershell
New-Item -ItemType Directory -Force "$HOME\bin" | Out-Null
$archive = "$env:TEMP\ib_windows_amd64.zip"
Invoke-WebRequest "https://github.com/rwahyudi/gib/releases/latest/download/ib_windows_amd64.zip" -OutFile $archive
Expand-Archive $archive "$HOME\bin\ib-latest" -Force
Copy-Item "$HOME\bin\ib-latest\ib.exe" "$HOME\bin\ib.exe" -Force
```

Add `$HOME\bin` to the user `PATH` if needed, open a new PowerShell window, and
run `ib config completion windows`. For source builds, see
[Build From Source](docs/build-from-source.md). For publishing, see
[Release Process](docs/release-process.md).

## Setup

Create or edit an Infoblox profile:

```bash
ib config new --default
sudo ib config new --global-config shared
sudo ib config edit --global-config shared
ib config edit
ib config list
```

Profiles store the primary server, auto-detected WAPI version, auto-detected GCM read endpoint when available, credentials, DNS view, default zone, and optional audit logging settings. Config validates server reachability before asking for credentials, then validates the username and password before WAPI setup. Trusted HTTPS certificates are verified; untrusted HTTPS certificates show certificate details and require confirmation before `verify_ssl = false` is saved. If Infoblox returns only one DNS view or one eligible primary forward zone, config selects it automatically. Passwords are encrypted at rest. Unix builds use a key file; native Windows builds use user-scope DPAPI for new writes and can still read existing `enc:v1` key-file profiles.

By default, profiles live under `~/.ib/`. On Linux, `ib config new --global-config [PROFILE]` writes a shared profile under `/etc/ib/` and asks which Linux group should have access. `ib config new --global-config` and `ib config edit --global-config` must be run as root, for example with `sudo`, because they write `/etc/ib/config` and `/etc/ib/key`. Users in the configured group can read `/etc/ib/config` and `/etc/ib/key`, and can read/write the shared `/etc/ib/cache.sqlite3`. Normal commands merge `/etc/ib/config` with `~/.ib/config`; local profiles and local metadata override matching global names, while global-only profiles remain available. If `/etc/ib/config` is missing or not readable, commands continue silently with local config only. Do not commit `~/.ib/config`, `~/.ib/key`, `/etc/ib/config`, `/etc/ib/key`, or cache data.

Audit logging is disabled by default. When enabled during `ib config new` or `ib config edit`, only successful write actions are logged: DNS record create/edit/delete, DNS zone create/delete, PTR side effects from A/AAAA workflows, and config profile create/edit/delete. Read, list, search, cache, and completion operations are not logged. Linux can log to JSON Lines files or syslog; Windows can log to Windows Event Log or a JSON Lines file. File logging defaults to `~/.ib/audit.jsonl` for local profiles, `/etc/ib/audit.jsonl` for Linux global profiles, and the current config directory on Windows. The file option warns that writable logs are weaker audit evidence, lets you return to method selection, and tests that the chosen path can be opened for writing before saving. Audit failures print a warning but do not fail the completed write action. Secret-looking fields such as passwords, keys, credentials, tokens, and secrets are redacted.

Azure SSO can be enabled after a normal Infoblox profile exists. Register a
public-client/native Azure app with a redirect URI for
`http://localhost/callback`, then configure and sign in:

```bash
ib auth configure --tenant-id contoso.onmicrosoft.com --client-id 11111111-2222-3333-4444-555555555555
ib auth login
ib auth status
```

When SSO is enabled, WAPI commands require a valid cached Azure login before
the saved Infoblox service account is used. Azure tokens are not sent to WAPI;
they authorize local CLI use and identify the operator for audit. Tokens are
cached under the local `~/.ib` directory and encrypted with the same local
protection used for passwords. Add `--allowed-groups` with comma-separated
Azure group object IDs when the app emits group claims and you want to restrict
CLI use to specific groups. Audit events include `sso_user`, `sso_tenant`,
`sso_subject`, and `sso_object_id` so changes can be traced back to the
signed-in Azure user.

## DNS 

DNS commands use this context order:

```text
command --zone/--view -> ib dns zone/view use -> IB_ZONE/IB_VIEW -> configured defaults
```

On native Windows, `ib dns zone use` and `ib dns view use` store the same session context files under the user's local app data directory. Run `ib config completion windows` to install the PowerShell integration that passes `IB_SHELL_PID`; shells without that integration should use `IB_ZONE` / `IB_VIEW` or command flags for explicit context.

Override context for one command without saving it:

```bash
ib dns --zone example.com --view "DNS Zone View" list
ib dns --zone example.com create app host 192.0.2.10 -c "Application host"
ib dns --view "DNS Zone View" search app
```

## Global Switches

- `-o, --output table|json|csv` is available from the root command and applies
  to every command. Use `csv` for spreadsheet/script exports, or `json` when
  piping to tools such as `jq`.
- `-z, --zone ZONE` and `-v, --view VIEW` are available on `ib dns` and most
  subcommands. They override the current DNS context for one command only.
  `ib dns zone list` intentionally accepts only `--view` because it lists zones
  in a DNS view rather than records inside one zone.
- `-g, --global` is a search scope switch for `ib dns search`; it searches every
  searchable zone in the selected DNS view.

## Modules

| Module | Purpose | Start here |
| --- | --- | --- |
| `config` | Manage profiles, encrypted credentials, completion, and cache. | `ib config new --default` |
| `auth` | Configure Azure SSO, browser login, token cache, and command gating. | `ib auth configure` |
| `dns` | Manage Infoblox DNS views, zones, records, searches, and context overrides. | `ib dns list` |
| `net` | Manage IPAM network views, IPv4 networks and containers, addresses, and next-IP lookups. | `ib net list` |

## How It Works

`cmd/ib/main.go` starts the Cobra CLI and hands command behavior to `internal/ibcli`. Profile loading decrypts the stored password, resolves the current DNS view/zone, and builds a WAPI client. When Azure SSO is enabled, each WAPI request first checks or refreshes the cached Azure identity and opens a browser login when an interactive user needs to sign in. GET requests can use a configured GCM read endpoint, while create, update, and delete requests always use the primary server.

DNS listing/search and IPAM read workflows prefer SQLite cache rows from the selected local or global config scope. Freshness is calculated from `cached_at + cache_ttl`; stale rows inside `records_cache_swr_ttl` are returned immediately while one detached refresh process updates the cache. DNS records revalidate with the zone serial before refreshing `/allrecords`; IPAM cache refreshes skip serial checks and re-download the relevant WAPI object.

Audit events are emitted only after successful write operations and use one JSON object per event for Splunk/Sentinel-style ingestion. Each event includes UTC time, local time, timezone, host, OS user, profile, action, operation, target, result, and redacted data.
When Azure SSO is enabled, audit events also include the signed-in Azure user
and stable Azure identifiers.

For source builds, development checks, and packaging notes, see
[Build From Source](docs/build-from-source.md). For cache diagrams and worker
behavior, see [Performance & Caching](docs/performance-caching.md).

## Command Reference

### Top Level

| Command | Description |
| --- | --- |
| `ib` | Print top-level usage and end with the current profile, DNS view, and DNS zone context. |
| `ib -v`, `ib --version` | Print the version number and AEST build date. |

### Auth

| Command | Description |
| --- | --- |
| `ib auth` | Show Azure SSO status. |
| `ib auth configure --tenant-id TENANT --client-id CLIENT` | Enable Azure SSO gating for WAPI commands. Add `--allowed-groups` with Azure group object IDs to restrict use. |
| `ib auth login` | Open a browser, complete Azure login, and cache the resulting identity. |
| `ib auth status` | Show whether SSO is configured and whether a cached login is valid. |
| `ib auth logout` | Clear cached Azure SSO tokens. |
| `ib auth disable` | Disable Azure SSO gating without removing the Infoblox profile. |

### Config

| Command | Description |
| --- | --- |
| `ib config` | Show profile overview and short usage. |
| `ib config new [PROFILE]` | Create a profile; validates server reachability/TLS trust, credentials, and primary access, auto-detects WAPI version and a usable GCM read endpoint, selects single DNS view/zone choices automatically, and can enable audit logging. Add `--global-config` on Linux as root to create the profile under `/etc/ib/`. |
| `ib config edit [PROFILE]` | Edit an existing profile; server reachability/TLS trust is rechecked, leaving the password blank keeps the current encrypted password, WAPI version detection updates the prompt default when available, and audit logging can be changed. Add `--global-config` on Linux as root to edit the profile under `/etc/ib/`. |
| `ib config list` | List configured profiles with username, WAPI, SSL, DNS context, and merged config metadata in table output. |
| `ib config use PROFILE` | Set the local default profile override. The profile may be local or a merged Linux global profile from `/etc/ib/config`. |
| `ib config delete PROFILE` | Delete a non-default local profile and clear its cache rows. |
| `ib config completion [bash\|zsh\|fish\|windows]` | Generate or install dynamic shell completion. |
| `ib config cache status` | Show SQLite cache entries with table statistics, or structured statistics with `-o json`. |
| `ib config cache clear` | Clear SQLite cache entries for the selected local/global config scope. |

### DNS

| Command | Description |
| --- | --- |
| `ib dns` | Show DNS help and the current profile/view/zone context. |
| `ib dns list [ZONE]` | List records in the current or provided zone. Add `-r` to include child zones, `-t/--type` to filter record types, `-e/--exclude` to hide matching records, `-s/--sort FIELD` to sort, or `-C/--columns LIST` to print selected columns. |
| `ib dns search KEYWORD` | Search records by name, value, or comment. Complete FQDN keywords can infer the matching forward zone. Use `--global` for all searchable zones, `-r` for child zones under the current/root zone, `-s/--sort FIELD` to sort, or `-C/--columns LIST` to print selected columns. |
| `ib dns next-ip NETWORK` | Compatibility path for next available IPv4 address lookup against a network or container. Prefer `ib net next-ip NETWORK` for IPAM work. |
| `ib dns create TYPE NAME VALUE` | Create a DNS record, for example `ib dns create host app 192.0.2.10 -c "Application host"`. For PTR, use `ib dns create ptr IP_ADDRESS PTR_TARGET`; the reverse zone is auto-detected unless `--zone` is supplied. |
| `ib dns edit TYPE NAME [VALUE]` | Edit an existing DNS record. |
| `ib dns delete TYPE NAME [ZONE]` | Delete a DNS record; prompts for confirmation unless `-y` is used. |
| `ib dns view list` | List DNS views. |
| `ib dns view use VIEW` | Set the active DNS view for the current shell session. |
| `ib dns zone create ZONE` | Create an authoritative DNS zone. |
| `ib dns zone list [SEARCH]` | List authoritative DNS zones. Add `-t/--type` to filter zone formats, `-e/--exclude` to hide matches, `-s/--sort FIELD` to sort, or `-C/--columns LIST` to print selected columns. |
| `ib dns zone info ZONE` | Show DNS zone details, with SOA serial rendered as an integer. |
| `ib dns zone delete ZONE` | Delete an authoritative DNS zone. |
| `ib dns zone use ZONE` | Set the active DNS zone for the current shell session. |

### IPAM

| Command | Description |
| --- | --- |
| `ib net` | Show IPAM command help. |
| `ib net view list` | List IPAM network views. |
| `ib net list [SEARCH]` | List IPv4 networks and containers. Add `--network-view` to filter by IPAM network view, `--refresh` to wait for fresh WAPI data, `-s/--sort FIELD` to sort by `network`, `type`, `network_view`, or `comment`, and `-C/--columns LIST` to print selected columns. |
| `ib net search KEYWORD` | Search IPv4 networks and containers by type, CIDR, network view, or comment. CIDR matches also include related parent and child networks or containers in the same network view; add `--refresh` to wait for fresh WAPI data. |
| `ib net show NETWORK` | Show details for one IPv4 network or container. Use `--network-view` when a CIDR exists in multiple network views. |
| `ib net address IP` | Show IPAM details for an IPv4 address, including network, parent container, status, types, names, MAC address, lease state, and comment when available. |
| `ib net next-ip NETWORK` | Find the next available IPv4 address in a network or container. Use `--network-view` for ambiguous CIDRs, `-n/--num` for multiple addresses, and repeat `-e/--exclude` to skip addresses. |

Common examples:

```bash
ib dns view list
ib dns view use "DNS Zone View"
ib dns zone list
ib dns zone use example.com
ib dns list
ib dns search app
ib dns search ben-dr-vss.net.latrobe.edu.au
ib net view list
ib net list prod --network-view default
ib net address 192.0.2.10 --network-view default
ib net next-ip 192.0.2.0/24 -n 3
ib dns create host app 192.0.2.10 -c "Application host"
ib dns create ptr 192.0.2.10 app.example.com
ib dns edit host app 192.0.2.20 -t 300 -c "Application host"
ib dns delete a app
```

`ib dns list` and `ib dns search` operate on the current zone by default. For search, a complete forward FQDN such as `ben-dr-vss.net.latrobe.edu.au` can infer the longest matching authoritative zone, search that zone, and match the relative owner name such as `ben-dr-vss`. With `--global`, search still scans every searchable zone in the selected view, but the relative-owner match is limited to the inferred forward zone so the matching forward record can appear alongside PTR/reverse matches. Add `-r` or `--recursive` to include child zones. For IPv4 reverse DNS, `ib dns list` also accepts a larger CIDR scope such as `10.128.48.0/23` and lists records from matching child reverse zones such as `10.128.48.0/24` and `10.128.49.0/24`. `ib dns list` also supports `-t/--type` and `-e/--exclude` filters like search. Add `-s` or `--sort` to sort by `name`, `type`, `value`, `zone`, `ttl`, or `comment`; a blank `--sort` sorts by name, and a leading minus sorts descending, for example `--sort=-name`. Add `-C` or `--columns` to print selected columns from `type`, `name`, `value`, `zone`, `ttl`, and `comment`, for example `--columns name,value`.

`ib dns zone list` supports the same output control pattern for zones. `--type` filters zone formats `FORWARD`, `IPV4`, or `IPV6`; `--sort` accepts `zone`, `view`, `format`, `ns_group`, or `comment`; and `--columns` selects from the same zone fields. Use `--view` to list zones from another DNS view; `--zone` and `-z` are not accepted by this command.

`ib net list` and `ib net search` are read-only IPAM workflows. Without `--network-view`, they build one merged dataset from unscoped WAPI `network` and `networkcontainer` results plus both object types for each discovered IPAM network view, then de-duplicate by type, CIDR, and network view so every network and container can be displayed. Add `--network-view` to limit the request to one view. Search text matches type, CIDR, network view, and comment. A CIDR-field match also includes related parent and child networks or containers in the same network view, but list/search only display network or container objects returned by Infoblox or the selected cache; covered child CIDRs are not synthesized. Existing IPAM cache rows are returned immediately even after normal SWR expiry, and table output prints a note when a background refresh is queued; use `--refresh` when the command must wait for fresh WAPI data. Default output prints `network`, `type`, and `comment`, with the network first and the type second. Table output color-codes the `network` CIDR by prefix size and the `type` column so `NETWORK` and `CONTAINER` rows are visually distinct. Add `-s` or `--sort` to sort by `network`, `type`, `network_view`, or `comment`; a blank `--sort` sorts by network, and a leading minus sorts descending. Add `-C` or `--columns` to select from `network`, `type`, `network_view`, and `comment`.

`ib net show`, `ib net next-ip`, and the compatibility `ib dns next-ip` path resolve both networks and containers; when the same CIDR exists as both, the container is preferred. `ib net next-ip` can use cached rows for the object lookup, while `ib dns next-ip` performs a live read-only object lookup. Both send the `next_available_ip` function call to the primary server. `ib dns next-ip` remains available for existing scripts, but `ib net next-ip` is the IPAM-oriented command.

All `ib net` table output prints a compact current-context footer with only the active profile and row count. JSON and CSV keep plain row-only values.

`ib dns delete TYPE NAME` prompts before deleting. Use `-y` or `--yes` to skip the confirmation. If multiple records of that type match, interactive table mode shows a Huh select list so one record can be chosen.

#### Output Controls

Record, zone, and network list-style commands can sort rows, select columns,
and emit machine-readable output:

```bash
ib dns list --sort name --columns name,value,ttl
ib dns list --sort=-name --columns zone,name,value -o csv
ib dns search app --global --sort zone --columns zone,name,value -o csv
ib dns list -o json | jq -r '.[] | [.name, .value] | @tsv'
ib dns zone list --sort zone --columns zone,format,comment -o json | jq '.[]'
ib net list --sort network --columns network,type,comment -o json | jq '.[]'
```

Use `--sort FIELD` for ascending order and `--sort=-FIELD` for descending
order. Record fields are `name`, `type`, `value`, `zone`, `ttl`, and `comment`;
zone fields are `zone`, `view`, `format`, `ns_group`, and `comment`; network
fields are `network`, `type`, `network_view`, and `comment`. Use `--columns` or `-C`
with a comma-separated list to keep only the fields you need. Use `-o csv` for
CSV output, or `-o json` when the next step is a `jq` pipeline.



## Troubleshooting

If a DNS write reports a non-JSON WAPI response, `ib` prints the WAPI object,
HTTP status, content type, and a short response snippet. An HTML snippet usually
means the configured server, WAPI version, credentials, or a proxy/login page is
answering the WAPI request instead of Infoblox JSON.

## Cache

Zone, record, and IPAM caches are stored in `~/.ib/cache.sqlite3` for local profiles or `/etc/ib/cache.sqlite3` for Linux global profiles.

Record and IPAM cache freshness uses `cached_at + cache_ttl`. Expired records and IPAM rows inside `records_cache_swr_ttl` are returned immediately while a single background refresh process updates the cache. `ib net list` and `ib net search` go further: when an IPAM network-view, network, or container cache row exists, they return it even after SWR expiry and queue a background refresh; add `--refresh` to wait for fresh WAPI data instead. DNS records revalidate the zone serial before refreshing `/allrecords`; IPAM rows skip serial checks and refresh the relevant `networkview`, `network`, `networkcontainer`, or `ipv4address` WAPI data.

Multi-zone search preloads matching record-cache rows with one SQLite connection before workers start. Workers still fall back to per-zone cache/WAPI checks for missing or expired rows. The WAPI HTTP client keeps a larger per-host connection pool sized from `dns_search_worker_limit` so parallel search can reuse TLS connections instead of repeatedly reconnecting.

When DNS record cache is missing or already outside the stale window, list/search waits up to `max_background_worker_wait` seconds for an active background refresh of the same profile and cache scope before doing foreground WAPI refresh work. IPAM `net list` and `net search` only do foreground WAPI work when cache is missing or `--refresh` is set.

`ib net next-ip` can use cached network or container rows to find the target `_ref`, but the `next_available_ip` function call is always sent live to the primary server so returned addresses are current.

Shell completion prefetches cache freshness in the background by default. With `completion_cache_prefetch = true`, most DNS completion checks the current DNS view and zone, and network CIDR completion checks the selected IPAM network view, then starts lease-protected zone-list, current-zone record, network-list, or container-list refresh helpers when cache rows are missing or stale. `ib dns create <tab><tab>` offers supported record types and filters typed prefixes such as `p` to `ptr`; `ib dns delete ptr <tab><tab>` completes PTR owner IPs from cached reverse-zone records instead of forward-zone names. `ib dns next-ip`, `ib net next-ip`, and `ib net show` complete both network and container CIDRs from the selected cache when available, including stale rows. When the typed value matches a parent CIDR or CIDR prefix, completion also offers child network/container CIDRs in the same network view; if only a larger parent such as `/23` is cached, completion derives direct `/24` child candidates for selection. Completion does not perform foreground Infoblox refresh work. Set `completion_cache_prefetch = false` in `[meta]` to make completion read the selected cache only and skip background refresh starts.

`ib config cache status` keeps the detailed cache row table and adds a colored
summary footer for table output: cache entries, cached records, fresh entries,
network views, networks, containers, IPv4 addresses, SWR-stale entries, expired entries,
and active refreshes. With `-o json`, it
returns `statistics` and `entries`; with `-o csv`, output remains row-only for
scripts.

Successful DNS record create, edit, and delete operations clear the affected zone's record cache and start a background refresh. DNS zone create/delete also refreshes the zone-list cache in the background; deleted zones have their record cache removed.

Useful cache commands:

```bash
ib config cache status
ib config cache clear
```

To see whether search used cache or WAPI, run with persistent cache-source diagnostics:

```bash
IB_SEARCH_DEBUG=1 ib dns search app --global
```

PowerShell:

```powershell
$env:IB_SEARCH_DEBUG = "1"; ib dns search app --global
```

Deleting a local profile also clears cache rows for that profile.

## Completion

Generate dynamic shell completion:

```bash
ib config completion bash > ~/.ib-complete.bash
. ~/.ib-complete.bash
```

On Windows, install native PowerShell completion for the current user:

```powershell
ib config completion windows
```

Run the Windows installer again after upgrading `ib` if completion behavior
changes. It updates the normal PowerShell profile paths plus common OneDrive
Documents profile locations. If `ib dns <Tab>` offers `dns` instead of DNS
subcommands, replace `ib.exe`, rerun `ib config completion windows`, and open a
new PowerShell window so the updated script is loaded.

The generated or installed completion calls the live `ib` binary, so profiles, zones, records, IPAM networks, flags, and output formats are resolved dynamically.
Installing from RPM or DEB puts the Bash completion file in `/etc/bash_completion.d/ib`.

## License

`gib` is licensed under the MIT License. See [LICENSE](LICENSE). Binary release
archives also include [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for
bundled Go dependency notices. The dependency policy is documented in
[docs/licensing.md](docs/licensing.md).
