# bamgate — Ideas & Future Work

Backlog of ideas, improvements, and future features. Roughly ordered by priority
within each category. See STATUS.md "What's Next" for the current short-term
priority list.

## Code Health

- **CI workflow on push/PR** — `go test -race`, `golangci-lint`, `go vet` in `.github/workflows/ci.yml`
- **Agent tests** — peer lifecycle, ICE restart logic, route acceptance, forwarding watchdog (`internal/agent/agent_test.go`)
- **Split agent.go** — extract `peer.go`, `routes.go`, `watchdog.go` from the 1,249-line god file
- **Checksum verification in install.sh** — download `checksums.txt` from release, verify tarball SHA256 before installing
- **Data path benchmarks** — `Benchmark*` tests for bridge send/receive, STUN parse/build, UAPI config generation
- **Pin linter config** — `.golangci.yml` with explicit linter list, timeout, exclusions

## Android

- Invite code paste fallback (no camera)
- Config import/export
- Battery optimization whitelist prompt
- ~~Network change handling (ConnectivityManager -> ICE restart)~~ — **Done.** `ConnectivityManager.NetworkCallback` + `ACTION_USER_PRESENT` receiver in VPN service, `Agent.NotifyNetworkChange()` with debounce, `signaling.ForceReconnect()` for instant reconnection.
- Always-on VPN support
- Per-app VPN (`addDisallowedApplication`)
- Connection quality indicator

## macOS

- `bamgate install` — write launchd plist to `/Library/LaunchDaemons/`
- `bamgate up -d` — `launchctl bootstrap system <plist>`
- `bamgate down` — `launchctl bootout system/com.bamgate`
- BSD ioctl/routing sockets (replace ifconfig/route/sysctl shell commands)

## Infrastructure

- ~~**Config file readable by non-root users**~~ — **Done.** Config split into `config.toml` (0644, non-secrets) and `secrets.toml` (0600, secrets). Commands like `bamgate qr` now work without `sudo`. Old monolithic configs are auto-migrated on first `bamgate up` or `bamgate setup`.
- **Worker update mechanism** — There is no way to check if the deployed Cloudflare Worker is out of date or to update it after initial `bamgate setup`. We should add version tracking (embed a version string in the Worker, expose it via a `/version` endpoint) and a CLI command (`bamgate worker update` or similar) that compares the running Worker version against the version embedded in the current binary and re-deploys if needed.
- Rate limiting on Worker `/connect` and `/turn` endpoints
- End-to-end testing with systemd on a fresh machine
- macOS end-to-end testing (`setup` -> `up` -> `status`)

## Features

### OAuth Authentication (GitHub)

Replace the shared-secret auth model with GitHub OAuth for identity, plus
Worker-minted JWTs for session auth. Each device gets its own rolling refresh
token. The old `AUTH_TOKEN` mechanism remains as a backward-compatible fallback.

**Motivation:** Currently all devices share one static `AUTH_TOKEN`. There is no
per-device identity, no revocation, and a leaked token compromises the entire
network. OAuth gives us per-device credentials, automatic rotation, and the
ability to revoke individual devices.

#### Token hierarchy

| Token | Issued by | Lifetime | Storage |
|-------|-----------|----------|---------|
| GitHub access token | GitHub | Transient (used once during setup) | Memory only |
| Refresh token | Worker DO | 30 days, rolling (renewed on each use) | Client: `config.toml`, Server: DO SQLite (hashed) |
| Access JWT | Worker DO | 1 hour | Client: memory, validated by Worker locally |

#### GitHub OAuth App

A single shared GitHub OAuth App registered by the bamgate project. The client
ID is hardcoded in the binary. Users authenticate via the **Device Authorization
Grant** (RFC 8628) — no browser redirect needed, works on headless machines.
Scope: `read:user` (minimal — just need user ID and username).

#### First device setup flow

1. `bamgate setup` initiates GitHub Device Auth flow
2. User opens `https://github.com/login/device`, enters the displayed code
3. CLI polls GitHub until authorized, receives a transient GitHub access token
4. CLI deploys Cloudflare Worker (same as today)
5. CLI calls `POST /auth/register` with the GitHub token + device name
6. Worker calls GitHub API to verify identity, registers user as network **owner**
7. Worker creates device record in DO SQLite, returns:
   access JWT, refresh token, device ID, assigned IP address, TURN secret
8. CLI saves device ID + refresh token to `config.toml`

#### Subsequent device setup (replaces invite system)

Same GitHub Device Auth flow on the new device. Worker sees the same
`github_user_id` as the owner and auto-authorizes. No invite codes needed — the
GitHub identity **is** the authorization. Each device gets its own refresh token
and IP address.

#### Agent runtime (connecting to signaling)

1. Agent loads refresh token from `config.toml`
2. Calls `POST /auth/refresh` with refresh token + device ID
3. Worker validates refresh token hash, checks expiry and revocation status
4. Worker issues **new** refresh token (old one invalidated — rolling window
   resets to 30 days) and a 1-hour access JWT
5. Agent saves new refresh token to `config.toml`
6. Agent connects to `/connect` and `/turn` with `Authorization: Bearer <JWT>`
7. Worker validates JWT signature + expiry locally (fast, no DB call)
8. Background: agent refreshes JWT every ~50 minutes before expiry

If a device is offline for <30 days, it reconnects seamlessly. If offline for
>30 days, the user must re-authenticate via GitHub in a browser.

#### JWT structure

```json
{
  "sub": "<device_id>",
  "owner": "<github_user_id>",
  "net": "<network_id>",
  "iat": 1700000000,
  "exp": 1700003600
}
```

Signed with HMAC-SHA256 using a key auto-generated by the DO on first
registration and stored in SQLite.

#### DO SQLite schema additions

```sql
CREATE TABLE owners (
  github_id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE devices (
  device_id TEXT PRIMARY KEY,
  device_name TEXT NOT NULL,
  owner_github_id TEXT NOT NULL REFERENCES owners(github_id),
  address TEXT NOT NULL,
  refresh_token_hash TEXT NOT NULL,
  refresh_token_expires_at TEXT NOT NULL,
  revoked INTEGER DEFAULT 0,
  created_at TEXT NOT NULL,
  last_seen_at TEXT
);
```

#### New Worker endpoints

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `POST /auth/register` | GitHub access token | First-time device registration |
| `POST /auth/refresh` | Refresh token | Get new JWT + rotate refresh token |
| `GET /auth/devices` | JWT | List all registered devices |
| `DELETE /auth/devices/:id` | JWT | Revoke a specific device |

#### New CLI commands

- `bamgate devices` — list all devices, their IPs, last-seen time, status
- `bamgate devices revoke <name|id>` — revoke a device's refresh token

#### TURN secret handling

The TURN shared secret remains a symmetric key for HMAC-based credential
generation. New devices receive it as part of the `/auth/register` response
instead of via invite codes.

#### Migration / backward compatibility

- Old `AUTH_TOKEN` binding continues to work — Worker checks JWT first, falls
  back to Bearer token string comparison
- `bamgate setup` offers a choice: "Authenticate with GitHub" or "Enter invite
  code" (legacy)
- Existing deployments are unaffected until the user opts into OAuth
- Future providers (GitLab, Google, etc.) plug in alongside GitHub — the Worker
  only validates its own JWTs, never talks to the provider directly at runtime

### `bamgate logs` command

Users have no way to check daemon logs without knowing the platform-specific
incantation (`journalctl` on Linux, `/var/log/bamgate.log` on macOS). Add a
`bamgate logs` command that does the right thing automatically.

**Current state:**
- Linux: logs go to stderr, captured by systemd journal (`journalctl -u bamgate`)
- macOS: logs go to `/var/log/bamgate.log` (configured in the generated launchd plist)
- No CLI command, no control socket endpoint, no docs for any of this

**Implementation — shell out to native tools:**

New file `cmd/bamgate/cmd_logs.go`, registered in `main.go`.

| Platform | What `bamgate logs` does |
|----------|--------------------------|
| Linux | Execs `journalctl -u bamgate -n <lines> --no-pager [--follow]` |
| macOS | Execs `tail -n <lines> [-f] /var/log/bamgate.log` |

**Flags:**
- `-f` / `--follow` (bool) — stream logs in real-time
- `-n` / `--lines` (int, default 100) — number of recent lines to show

**Details:**
- Wire child process stdout/stderr to the current terminal so output flows naturally
- On macOS, check that the log file exists first; give a clear error if missing
  (e.g., "bamgate service hasn't been set up yet — run `sudo bamgate setup`")
- On unsupported platforms, print a helpful message pointing to the right location
- No control socket changes, no new dependencies — one new file, one line in `main.go`

<!-- Add new feature ideas here -->
