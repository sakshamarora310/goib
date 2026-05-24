# Notes

## Code Comment Maintenance

Keep code comments focused on behavior that is easy to break or misunderstand: WAPI GET-versus-write routing, config validation order, cache freshness and stale-while-revalidate decisions, refresh leases, dynamic completion, and detached refresh subprocess handoff.

When changing one of those flows, update the nearby code comment in the same patch. Comments should explain why the behavior exists and what must stay in sync, not restate obvious Go statements.

## README Maintenance

The README includes a concise product blurb, bullet-point Features section, module table, global-switch summary, library links, and security-scanner links. Keep it in sync when adding or removing major CLI behavior, but avoid duplicating the detailed command reference.

## Security Scanning

Security scanning lives in GitHub Actions and Dependabot. Keep README security commands in sync when adding or changing `go test`, `govulncheck`, `gosec`, Trivy, or dependency update automation.

## Release Maintenance

GitHub releases are tag-driven through GoReleaser. The release workflow publishes Linux amd64 tarball, RPM, DEB, Windows amd64 ZIP, and checksum assets. GoReleaser RPM and DEB packages install `/usr/local/bin/ib` and `/etc/bash_completion.d/ib`; Copr installs `/usr/bin/ib` and the same packaged Bash completion loader, so that loader must resolve `ib` from `PATH` rather than hardcoding one install path. Windows release builds must keep CGO enabled and use MinGW because the SQLite cache depends on `go-sqlite3`. Keep README install commands, `.goreleaser.yaml`, `gib.spec`, and packaged completion paths in sync when changing release behavior.

Before every release, scrutinize the README installation sections against the release that will be published. Confirm Copr, GitHub tarball, RPM, DEB, Windows ZIP, completion, install paths, and asset filenames match `.goreleaser.yaml`, `gib.spec`, and the release tag. README commands should use direct stable `releases/latest/download` asset URLs by default; do not reintroduce GitHub API parsing, asset enumeration, or stale hardcoded release URLs. When network access is available, validate the live asset list with `gh release view --json tagName,assets` rather than relying only on local release config.

## Performance and Caching Docs

`docs/performance-caching.md` explains cache freshness, stale-while-revalidate, read/write routing, search workers, and SQLite cache tables. Keep that page plus `docs/assets/cache-decision-flow.svg`, `docs/assets/cache-workers.svg`, and `docs/assets/sqlite-cache-tables.svg` in sync when cache behavior, read-server routing, worker behavior, or cache schema changes. The diagrams use a dark Nord, angular, gradient style with compact boxes, small arrowheads, and small animated traffic markers only on the read/write worker flow.

Search can open the SQLite cache once per zone while scanning many zones. Keep cache schema initialization and Windows ACL hardening memoized per process/path; otherwise native Windows search pays repeated filesystem security and schema setup overhead for every zone worker.

Multi-zone search should preload record-cache rows for the selected zones with one SQLite connection before worker fan-out. This keeps native Windows search from repeatedly opening the cache file for fresh rows; missing or expired rows still fall back to the normal per-zone cache/WAPI path.

The WAPI HTTP transport should keep enough idle per-host connections for search workers. Size `MaxIdleConnsPerHost` and `MaxConnsPerHost` from `dns_search_worker_limit` so Windows does not repeatedly pay TCP/TLS setup costs during parallel `/allrecords` refreshes.

## Global Cache and Search Settings

The config file stores global cache/search tuning in the `[meta]` section:

```ini
cache_ttl = 300
dns_search_worker_limit = 16
records_cache_swr_ttl = 259200
max_background_worker_wait = 3
completion_cache_prefetch = true
audit_logging_enabled = false
audit_logging_method = file
audit_log_file =
sso_enabled = false
sso_azure_tenant_id =
sso_azure_client_id =
sso_azure_scopes = openid profile email offline_access
sso_azure_allowed_groups =
global_group = ibusers
```

`cache_ttl` is in seconds and controls both zone-list cache entries and per-zone record-cache entries. Freshness is calculated from `cached_at + cache_ttl`; the cache does not store a separate fresh-expiry timestamp. If the setting is missing or invalid, `ib` uses and writes the default value of `300`.

`dns_search_worker_limit` controls how many zones `ib dns search --global` can load in parallel. If the setting is missing or invalid, `ib` uses and writes the default value of `16`.

`records_cache_swr_ttl` is in seconds and controls how long expired per-zone record-cache entries can be served stale while `ib` refreshes them in the background. If the setting is missing or invalid, `ib` uses and writes the default value of `259200` seconds (3 days).

`max_background_worker_wait` is in seconds and controls how long list/search record loading waits for an existing background refresh for the same profile, DNS view, and zone before doing foreground WAPI refresh work. If the setting is missing or invalid, `ib` uses and writes the default value of `3`.

`completion_cache_prefetch` controls whether shell completion can start background cache refresh helpers. The default is `true`; accepted values include `true`/`false`, `enabled`/`disabled`, `yes`/`no`, and `on`/`off`. When enabled, most DNS completion checks the active DNS view and current zone, and network CIDR completion checks the selected IPAM network view, then starts lease-protected zone-list, current-zone record, network-list, or container-list refresh helpers if those cache rows are missing or stale. PTR delete completion skips the current forward-zone record refresh and reads cached reverse-zone PTR rows. When disabled, completion only reads whatever is already in the selected cache: stale cached zone names, record names, or network CIDRs can still be offered, missing cache returns no dynamic candidates for that attempt, and completion does not start detached refresh subprocesses.

`audit_logging_enabled` controls write-action audit logging. The default is `false`. When enabled, `ib` emits JSON Lines events only after successful create, edit, and delete actions for DNS records, DNS zones, PTR side effects, and config profiles. Read, list, search, cache, completion, and failed/cancelled actions are not audited. Each event includes UTC `ts`, `local_time`, `timezone`, app, event type, host, OS user, profile, action, operation, target type, target, result, and redacted data.

`audit_logging_method` selects the audit sink. Linux supports `file` and `syslog`; Windows supports `windows_eventlog` and `file`; other platforms support `file`. The method prompt uses a `huh` select in interactive terminals and keeps the same indentation as other config prompts in fallback mode. File logging writes one JSON object per line to `audit_log_file`, defaulting to `~/.ib/audit.jsonl` for local profiles and `/etc/ib/audit.jsonl` for Linux global profiles. Selecting `file` warns that users with write access can modify or remove local log entries, offers a back-out to audit method selection, and opens the chosen path for append/create before saving. Audit sink failures print a warning and do not fail the already-completed write action.

`global_group` is written only for Linux global profiles created with `ib config new --global-config`. It records the group used to protect `/etc/ib/config`, `/etc/ib/key`, and `/etc/ib/cache.sqlite3`.

## Config Profiles

`ib config delete PROFILE` removes the non-default profile from the config file and clears local zone-cache, record-cache, and record-refresh lock rows for that profile. Cache rows for other profiles are left intact.

On Linux, `ib config new --global-config [PROFILE]` writes the profile to `/etc/ib/config`, stores the shared Fernet key in `/etc/ib/key`, and uses `/etc/ib/cache.sqlite3` for shared cache rows. The command asks for a Linux group, validates that it exists, sets `/etc/ib` to group-owned `2770`, sets config/key files to `0640`, and sets the cache DB to `0660`. Normal commands merge `/etc/ib/config` with `~/.ib/config`; local profiles and metadata override matching global names, and global-only profiles use `/etc/ib/key` plus `/etc/ib/cache.sqlite3` when selected. Missing or unreadable `/etc/ib/config` is skipped silently so local profiles still work. Non-Linux builds return a clear Linux-only error for `--global-config`.

`ib config use PROFILE` selects from the same merged profile set shown by `ib config list`. When the selected profile exists only in `/etc/ib/config`, the command writes a local metadata override with `default_profile = PROFILE` without copying the global profile or writing `/etc/ib/config`. This lets each user choose a different default global profile.

`ib config new` and `ib config edit` validate the entered server before asking for credentials. Unreachable servers print a warning and return to the server prompt. Trusted HTTPS certificates continue with `verify_ssl = true`; untrusted HTTPS certificates print certificate subject, issuer, validity, and SHA256 fingerprint before asking whether to trust the certificate for the profile. Accepting saves `verify_ssl = false`.

After username and password entry, `ib config new` and `ib config edit` validate credentials with a small WAPI `grid` read before WAPI version setup. Authentication and authorization failures are summarized and return to the credential prompts without printing raw server response bodies.

`ib config new` and `ib config edit` ask whether audit logging should be enabled after DNS view and default-zone selection. Existing configs default to the saved audit settings; new profiles default to disabled. Config profile create/edit/delete audit data redacts password values and any key, credential, token, or secret-looking fields.

`ib auth configure` enables an Azure SSO gate for WAPI requests after a normal Infoblox profile exists. `ib auth login` uses browser-based authorization code + PKCE with a localhost callback and caches encrypted Azure tokens under the local `~/.ib` directory. Azure tokens are not sent to Infoblox WAPI; the Azure identity authorizes local CLI use and enriches audit logs, while the saved Infoblox service account still performs WAPI calls. Optional `sso_azure_allowed_groups` values are Azure group object IDs and require group claims in the ID token.

`ib config new` and `ib config edit` Step 05 (`Read Endpoint`) automatically discovers Grid Master Candidates from the primary Grid Master. Candidates with Read-Only API disabled are reported with an indented green `INFO:` line and are not saved. Candidates with Read-Only API enabled must also pass a direct WAPI GET probe before being saved as `read_server`. If no candidate exists or no candidate passes the probe, `read_server` is left blank so both reads and writes use the primary server.

When `read_server` is set, the WAPI client routes GET requests to the GCM read endpoint and keeps POST, PUT, and DELETE requests on the primary server.

During `ib config new` and `ib config edit`, DNS View and Default DNS Zone are only prompted when there are multiple choices. If Infoblox returns exactly one DNS view, that view is selected automatically. If exactly one eligible primary forward zone remains after filtering out secondary zones, that zone is selected automatically.

## DNS Search Progress

For interactive table output, `ib dns search` uses a Bubble Tea progress view on stderr while the search is running. The view shows the search stage, configured worker count, completed zones, match count, and each worker's current zone/cache source. The final record table is still printed normally on stdout after the progress view exits.

For persistent troubleshooting output, `IB_SEARCH_DEBUG=1` or `IB_CACHE_DEBUG=1` disables the transient progress view and prints per-zone search cache sources on stderr. Use this when checking Windows cache behavior after the table has rendered.

The worker state `Checking cache` covers the whole per-zone record load until the worker finishes. That includes opening the SQLite cache, reading and decoding cached JSON records, checking fresh/stale expiry, acquiring the stale-while-revalidate refresh lease and launching the detached refresh subprocess when needed, and, for entries outside SWR, doing foreground serial or `/allrecords` refresh work.

If many global-search workers sit at `Checking cache`, inspect whether most per-zone record caches are stale. A stale-but-inside-SWR cache hit returns cached records after cache lease acquisition and detached subprocess launch, but it does not wait for Infoblox serial checks or `/allrecords`. The worker label remains visible while cached data is read, JSON-decoded, normalized, de-duplicated, sorted, and handed to the matcher, so large cached zones can still spend noticeable time in this state without waiting on Infoblox.

For non-interactive stderr, `-o json`, or `-o csv`, the progress view is disabled so scripts and machine-readable output remain clean.

## DNS List and Search Scope

`ib dns list [ZONE]` lists only the resolved current zone by default. Use `-r` or `--recursive` to include child authoritative zones under that zone. For IPv4 reverse DNS, a larger CIDR such as `10.128.48.0/23` is treated as a scope and lists records from matching child reverse zones such as `10.128.48.0/24` and `10.128.49.0/24`. Use `-t`/`--type` to filter comma-separated record types, `-e`/`--exclude` to hide records whose name, value, or comment matches the excluded keyword, `-s`/`--sort` to sort by `name`, `type`, `value`, `zone`, `ttl`, or `comment`, and `-C`/`--columns` to print selected output columns.

`ib dns search KEY` searches only the resolved current zone by default. If KEY is a complete forward FQDN and no explicit `--zone` scope is supplied, search infers the longest matching authoritative zone from the selected DNS view and also matches the relative owner name in that inferred zone. Without `--global`, the inferred zone becomes the search scope. With `--global`, every searchable zone is still scanned, but the relative-owner alias is only applied to the inferred forward zone so unrelated zones with the same short owner name are not included. Use `-z ZONE` to choose a different root zone, and add `-r` or `--recursive` to include child authoritative zones under that root. `--global` still cannot be combined with `--recursive`. Search supports the same `-t`/`--type`, `-e`/`--exclude`, `-s`/`--sort`, and `-C`/`--columns` record filters as list.

For record sorting, a blank `--sort` uses `name`, and a leading minus sorts descending, for example `--sort=-name` or `-s -ttl`. Sorting by `name` or `value` uses numeric IP ordering when the selected field contains an IP address, so `192.0.2.2` sorts before `192.0.2.10`. IP values sort before non-IP values in both ascending and descending order. When `--sort` is omitted, plain `ib dns list` keeps the historical default order for forward zones and uses numeric IP ordering for reverse-zone records that display IP names.

For record columns, the default output remains `type,name,value,zone,ttl,comment`. `--columns name,value` filters and orders output fields for table, JSON, and CSV. Duplicate or unknown column names are rejected.

`ib dns zone list [SEARCH]` supports the same output-control pattern for authoritative zones. `--type` filters zone formats (`FORWARD`, `IPV4`, `IPV6`), `--exclude` hides zones whose name or comment matches a keyword, `--sort` accepts `zone`, `view`, `format`, `ns_group`, or `comment` with `-field` for descending order, and `--columns` selects and orders zone output fields for table, JSON, and CSV. It accepts `--view`/`-v` for listing another DNS view, but does not accept `--zone`/`-z` because the command lists zones rather than records inside a selected zone.

`ib net list [SEARCH]` and `ib net search KEY` match IPv4 network and network container rows by type, CIDR, network view, and comment. Without `--network-view`, they merge unscoped network/container rows with network/container rows from each discovered IPAM network view before filtering, so all visible networks and containers are represented. When the CIDR field matches, the result expands the same network-view hierarchy so matching real parent and child networks or containers are shown together. List/search only display network or container objects returned by Infoblox or the selected cache; covered child CIDRs such as a `/24` inside a larger parent are not synthesized. Existing IPAM cache rows are returned immediately even after normal SWR expiry, with a background refresh queued and a table-only info note; use `--refresh` to force foreground WAPI refresh before printing. Default output uses `network,type,comment`; `network_view` remains available through `--columns`. Table output color-codes CIDR values by prefix size and color-codes `NETWORK` and `CONTAINER` values in the type column; JSON and CSV remain unstyled.

Most `ib dns` subcommands inherit `--zone`/`-z` and `--view`/`-v`. These are per-command context overrides and take precedence over `ib dns zone use`, `ib dns view use`, `IB_ZONE`, `IB_VIEW`, and configured defaults without saving anything to the profile. `ib dns zone list` suppresses the zone override in help and completion and rejects it if typed manually.

`ib dns zone use` and `ib dns view use` store shell-session context in PID-scoped files. Unix builds also inspect `/proc` to recover the shell grandparent PID when completion or subprocess handoff changes the immediate parent. Native Windows builds do not use `/proc`; they use `IB_SHELL_PID` when a shell integration provides it and otherwise fall back to the immediate parent process. `IB_ZONE`, `IB_VIEW`, and command flags remain the reliable explicit context overrides in Windows shells without native completion integration.

Profile passwords use `enc:v1:` Fernet tokens with the selected key file on Unix (`~/.ib/key` locally or `/etc/ib/key` for Linux global profiles). Native Windows writes new passwords as `enc:v2:windows-dpapi:` tokens protected by user-scope DPAPI and best-effort owner ACL hardening. Windows can still decrypt existing `enc:v1:` profiles when the key file exists; those passwords are migrated to DPAPI the next time a decrypted profile is written.

DNS record table output always includes a `Current Context:` footer line. When the table has more than five records, the `Total records` badge is shown on the same line.

IPAM network table output from `ib net` prints a compact `Current Context:` footer after the table with only `Profile` and `Rows`. JSON and CSV output remain row-only.

`ib dns zone info ZONE` normalizes SOA serial numbers to integer text so JSON numeric/scientific notation from WAPI does not leak into table, JSON, or CSV output.

`ib dns delete TYPE NAME` prompts with a Charmbracelet Huh confirmation before deleting a selected record. Use `-y` or `--yes` to skip the confirmation. If the user cancels either the duplicate-record picker or confirmation prompt, `ib` prints `INFO: delete cancelled` and exits without issuing DELETE. If multiple forward records match the same FQDN and requested type, interactive table mode first uses a Charmbracelet Huh select picker showing type, name, value, zone, comment, and `_ref`; the selected record is then confirmed before DELETE. Non-interactive mode and `-o json`/`-o csv` fail safely unless `-y` is provided.

`ib dns create ptr IP_ADDRESS PTR_TARGET` treats the first argument after `ptr` as a full IPv4 or IPv6 address. If `--zone` is omitted, `ib` finds the best matching reverse DNS zone in the selected DNS view before posting the PTR record; `--zone` remains an explicit reverse-zone override. The created PTR still uses the primary server for the write, and reverse-zone discovery uses the primary read path so write workflows do not depend on a read-only GCM endpoint.

When WAPI returns HTML or another non-JSON payload with a successful HTTP status, `ib` reports a contextual WAPI error instead of the raw Go JSON parser error. This most often means the configured server, WAPI version, credentials, reverse proxy, or login/SSO page is answering the request rather than the Infoblox WAPI JSON endpoint.

## Shell Completion

`ib config completion bash`, `ib config completion zsh`, and `ib config completion fish` emit lightweight shell integrations that call the live `ib` binary during tab completion. `ib config completion windows` installs a PowerShell profile integration on native Windows only, covering standard profile paths, common OneDrive Documents paths, and discovered `$PROFILE.CurrentUserCurrentHost` paths. The PowerShell script reconstructs the typed command from both AST elements and tokenized input before calling Cobra, so nested commands like `ib dns <Tab>` request `ib __complete dns ""` rather than root completions. Profiles, zones, records, flags, and output formats are resolved dynamically by `ib __complete` or `ib __completeNoDesc`, so users do not need to regenerate or reinstall completion when profile, zone, or record data changes. Regenerate or reinstall the shell integration only when the completion script template itself changes.

`ib dns search KEY -t <tab><tab>` and `ib dns list -t <tab><tab>` complete supported record type filters such as `a`, `host`, and `txt`. Comma-separated filters are completed from the current segment, so `-t a,` offers remaining types as `a,host`, `a,txt`, and so on.

`ib dns search KEY -s <tab><tab>` and `ib dns list -s <tab><tab>` complete sort fields in ascending and descending forms, including `name`, `type`, `value`, `zone`, `ttl`, `comment`, `-name`, and `-ttl`.

`ib dns search KEY -C <tab><tab>` and `ib dns list --columns <tab><tab>` complete record output columns. Comma-separated completion skips already selected columns, so `--columns name,` offers `name,type`, `name,value`, and the remaining columns.

`ib dns list <tab><tab>` offers command and inherited flags such as `-C`, `-e`, `-s`, and `-o` even before a zone argument is typed. Non-flag text after `ib dns list` completes zone names from the resolved active DNS view. For IPv4 reverse DNS, it also includes matching IPAM CIDR scopes and derived `/24` child candidates when only a larger cached parent is present.

`ib dns zone list -t <tab><tab>`, `ib dns zone list -s <tab><tab>`, and `ib dns zone list -C <tab><tab>` complete zone formats, sort fields, and output columns. `ib dns zone list -<tab><tab>` completes `--view`/`-v` and zone-list filters, but not `--zone`/`-z`.

For Bash, `ib <tab><tab>` should complete root commands such as `config`, `dns`, and `help`. If it does not, regenerate and reload the shell integration with `ib config completion bash > ~/.ib-complete.bash` and start a new shell or run `. ~/.ib-complete.bash`.

For Bash, `ib dns create <tab><tab>` completes supported record types (`a`, `aaaa`, `cname`, `host`, `mx`, `ptr`, `srv`, and `txt`) from the first argument, including prefix forms such as `ib dns create p<tab>` to complete `ptr`. `ib dns delete ptr <tab><tab>` completes cached PTR owner IPs from reverse DNS zones rather than record names from the active forward zone. This behavior lives in the generated Bash completion wrapper and Cobra completion path, so regenerate and reload `~/.ib-complete.bash` after changing either side.

Global options still complete while using `ib dns create`: `ib dns create -<tab>` offers options such as `--output`, `-o`, and `--help`, and output format values complete after `--output` or `-o`.

For `ib config new` and `ib config edit`, question 7 (`Default DNS Zone`) uses the Bubble Tea filter list when there are multiple choices and keeps an eight-row zone list area visible even when fewer rows are currently matched. Question 6 (`Default DNS View`) still sizes to the available DNS view choices when a picker is needed.

## Zone and Record Cache Workflow

Zone-list data and zone-record data are cached in the selected SQLite cache (`~/.ib/cache.sqlite3` locally or `/etc/ib/cache.sqlite3` for Linux global profiles), but they refresh differently.

Zone-list data is cached in `zone_cache` per profile and DNS view. Normal commands read the matching row and return it immediately if it is still fresh under `cached_at + cache_ttl`. If the row is missing or expired, normal commands query Infoblox `zone_auth` in the foreground, write the fresh zone list back to SQLite, and return it. Zone cache does not currently use stale-while-revalidate for normal commands.

Successful DNS zone create/delete operations clear and refresh the zone-list cache in the background through the hidden `ib config cache refresh-zones` helper. Deleting a zone removes that zone's record cache instead of trying to refresh records for a zone that no longer exists.

Zone record data is cached in `record_cache` per profile, DNS view, and zone.

Successful `ib dns create`, `ib dns edit`, and `ib dns delete` operations remove the affected zone's record-cache row and synchronously launch the detached refresh subprocess when no matching refresh lease is active. The write command does not wait for `/allrecords`; the subprocess repopulates the cache in the background. A/AAAA workflows that create, update, or may indirectly delete PTR records queue refreshes for both the forward zone and the reverse zone; when an A/AAAA edit moves to a new address, `ib` also removes the old matching PTR before refreshing the old reverse-zone cache.

When a command queries zone records:

1. If a record-cache entry exists and has not expired, `ib` returns the cached records without checking Infoblox.
2. If the entry has expired but is still inside `records_cache_swr_ttl`, `ib` returns the stale cached records immediately without checking Infoblox.
3. For that stale response, `ib` synchronously checks whether a refresh is already running for the same profile, DNS view, and zone. If one is running, `ib` does not start another subprocess.
4. If no matching refresh is running, `ib` starts a detached `ib config cache revalidate-record` subprocess for that profile, DNS view, and zone before returning the stale data.
5. The stale response does not wait for Infoblox serial checks, `/allrecords`, or subprocess completion.
6. The background subprocess asks Infoblox for the zone SOA serial number.
7. If the cached serial matches the server serial, `ib` treats the cached data as still valid, renews the cached age timestamp, recomputes normal freshness from `cached_at + cache_ttl`, and extends the stale expiry by `records_cache_swr_ttl`.
8. If the serial changed, if the cache entry is missing, or if the cache entry has no serial to compare, the background subprocess downloads fresh `/allrecords` data, stores the new serial when available, and resets all record-cache timestamps.
9. If an expired entry is already outside `records_cache_swr_ttl`, `ib` first checks for an active background refresh for the same profile, DNS view, and zone. If one is active, it waits up to `max_background_worker_wait` seconds, polling every 2ms, then re-reads the cache if the refresh completed. If the wait times out or the cache is still too old, `ib` performs the serial check in the foreground. Matching serials renew `cached_at`, making the cache fresh again under the current `cache_ttl`; changed or missing serials refresh from `/allrecords`.

The in-flight background refresh marker is stored in the selected SQLite cache and expires automatically after 300 seconds. This prevents repeated `ib dns list` or `ib dns search` calls from starting duplicate refreshes while still allowing recovery if a refresh subprocess exits unexpectedly. The wait is scoped to the exact profile, DNS view, and zone; refreshes for other zones do not block the current list/search.

`ib dns list --details` may enrich fresh cached rows with per-record detail calls when TTL/detail fields are missing. Stale SWR responses are returned exactly as cached; the background revalidation updates the cache separately.

`ib dns search` uses the same record-cache workflow. It first loads the searchable zone list, skips secondary zones, then loads records from cache or `/allrecords` for the current zone, an inferred FQDN zone, recursive child-zone scope, or global scope. Multi-zone searches use `dns_search_worker_limit` to bound parallel zone loading.

Shell completion uses these same cache paths but does not perform foreground Infoblox refreshes for zone names, record names, or IPAM network CIDRs. When `completion_cache_prefetch = true`, most DNS completion checks the current DNS context and network CIDR completion checks the selected IPAM network view. If the zone-list cache, current-zone record cache, network-list cache, or container-list cache is missing or stale, completion starts the same lease-protected hidden refresh helpers in the background. Zone completion returns cached zone rows when available, even if stale. Record-name completion returns cached record rows when available, even if stale; PTR delete completion reads cached PTR rows from cached reverse zones and completes the displayed owner IPs without refreshing the active forward zone. Network CIDR completion for `ib dns next-ip`, `ib net next-ip`, and `ib net show` returns cached network and container rows when available, even if stale. When the typed value matches a parent CIDR or CIDR prefix, completion also returns child network/container CIDRs in the same network view; if only a larger parent such as `/23` is cached, completion derives direct `/24` child candidates. Missing cache returns no dynamic candidates for that completion attempt while the background refresh populates SQLite for the next attempt. When `completion_cache_prefetch = false`, completion keeps the selected-cache-only behavior but skips the background refresh start.

`ib config cache status` shows cache age and record stale expiry, but not a `fresh_until` column. Freshness is calculated dynamically from each row's `cached_at` timestamp and the current `cache_ttl` setting. Table output keeps the detailed row table and adds a colored statistics footer with cache entries, cached records, fresh entries, SWR-stale entries, expired entries, and active refreshes. JSON output returns `statistics` plus `entries`; CSV output remains row-only for scripts.

## Notes Maintenance

When CLI behavior, config settings, cache workflow, operator-facing output, or troubleshooting guidance changes, update `NOTES.md` in the same work pass. Keep notes concise and operational; explanatory-only or review-only prompts only need a notes update when they establish durable behavior that future work should preserve.
