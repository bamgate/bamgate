# bamgate — Ideas & Future Work

Backlog of ideas, improvements, and future features. Roughly ordered by priority
within each category. See STATUS.md "What's Next" for the current short-term
priority list.

## Code Health

- **CI workflow on push/PR** — `go test -race`, `golangci-lint`, `go vet` in `.github/workflows/ci.yml`
- **Agent tests** — peer lifecycle, ICE restart logic, route acceptance, forwarding watchdog (`internal/agent/agent_test.go`)
- **Refactor agent.go** — the file is ~1,867 lines with 21 struct fields, 38
  methods, 68 mutex lock/unlock sites, and 17+ distinct responsibilities. Key
  refactoring steps:
  1. **Extract `peerManager` type** — move the `peers` map, `mu` mutex, and all
     ~15 methods that operate on peer state (`handlePeers`, `handleOffer`,
     `handleAnswer`, `handleICECandidate`, `flushPendingCandidates`,
     `initiateConnection`, `createRTCPeer`, `onDataChannelOpen`, `removePeer`,
     `handleICEStateChange`, `attemptICERestart`, etc.) into a dedicated type.
     This would cut agent.go roughly in half and reduce the Agent struct to ~12
     fields.
  2. **Introduce explicit `peerPhase` enum** — replace the implicit state machine
     (currently deduced from ~6 boolean/nullable fields: `rtcPeer != nil`,
     `pendingRestart`, `needsRestart`, `restartTimer != nil`, `iceRestarts`,
     ICE connection state) with an explicit enum: `Skeleton | Connecting |
     Connected | DisconnectedGrace | Restarting | NeedsRebuild`. Makes the ~12
     state transitions self-documenting and simplifies guard conditions.
  3. **Split `handleOffer()`** — at 126 lines with 7 critical sections, extract
     `detectZombiePeer()`, `resolveOfferGlare()`, `applyOfferSDP()`.
  4. **Extract `forwardingManager` type** — lines 1624-1848 (224 lines, 5
     methods, 3 fields) deal exclusively with IP forwarding, NAT masquerade,
     and the forwarding watchdog. Move to a self-contained type with
     `Start()`/`Stop()` methods.
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

### Rewrite Cloudflare Worker in TypeScript (drop Go/Wasm)

The worker is currently split across Go/Wasm (hub + TURN relay + custom STUN
parser) and JavaScript (auth, JWT, SQLite, WebSocket lifecycle, Wasm bridge).
This split introduces significant accidental complexity without a proportional
benefit.

**Problem:** The Go/Wasm code handles only signaling routing (207 lines) and
TURN relay (713 lines) — purely reactive data processing. All interesting
platform work (auth, storage, crypto, WebSocket lifecycle) must stay in JS
because of Cloudflare platform API constraints (`ctx.storage.sql`, `crypto.subtle`,
WebSocket Hibernation API, `Response` constructor). The result is a 10-function
JS<->Wasm bridge, a TinyGo build dependency, a custom STUN parser (631 lines)
because pion/stun doesn't compile under TinyGo, and `worker.mjs` at 893 lines
mixing 5+ concerns with no tests. The shared-types benefit (`pkg/protocol/`) is
not realized — the worker does its own inline JSON parsing.

**Proposal:** Rewrite the entire worker in TypeScript, eliminating Wasm.

**What we eliminate:**
- TinyGo as a build dependency (+ wasm-opt, custom wasm_exec.js)
- The 571 KB app.wasm binary
- The 10-function JS<->Wasm bridge
- `worker/main.go` (Wasm glue)
- `worker/go.mod` (separate Go module)
- The constraint of no goroutines / limited stdlib in TinyGo Wasm

**What we gain:**
- Direct access to all CF APIs — no indirection or bridge
- `async/await` for Web Crypto (currently blocked from Go by Promise handling)
- Testable with Vitest + Miniflare (Cloudflare's local testing framework)
- Wrangler's built-in TypeScript support (zero config)
- Type checking on CF API surface via `@cloudflare/workers-types`
- Fewer total lines: estimated ~1,500 TS vs ~3,000 current (893 JS + 920 Go +
  631 STUN parser + 45 glue + 518 STUN tests)

**Estimated TypeScript structure:**

| File | Lines (est.) | Purpose |
|------|-------------|---------|
| `src/worker.ts` | ~150 | Fetch handler, DO class, WebSocket lifecycle |
| `src/auth.ts` | ~300 | Register, refresh, JWT sign/verify, device management |
| `src/hub.ts` | ~150 | Peer registry, signaling message routing |
| `src/turn.ts` | ~500 | TURN relay: allocations, permissions, channels, forwarding |
| `src/stun.ts` | ~450 | STUN/TURN message parser/builder (DataView/Uint8Array) |
| `src/types.ts` | ~50 | Shared types (signaling messages, peer info) |
| Tests | ~600 | Vitest + Miniflare |

**Migration plan (incremental):**
1. Set up TypeScript + Vitest + Miniflare toolchain alongside existing worker
2. Port `stun/stun.go` to `src/stun.ts` — pure data transformation, easy to
   test in isolation, validates the approach
3. Port `hub.go` to `src/hub.ts` — simplest Go file, mostly map operations
4. Port `turn.go` to `src/turn.ts` — most complex piece, but well-specified
5. Split `worker.mjs` into `src/worker.ts` + `src/auth.ts`, converting to TS
6. Remove Go/Wasm: `worker/main.go`, `worker/hub.go`, `worker/turn.go`,
   `worker/stun/`, `worker/go.mod`, `wasm_exec.js`, build targets
7. Update `internal/deploy/assets/` embedded files and deployment logic
8. Fix the TURN relay address overflow bug (turn.go:139) during the port —
   `byte(nextRelayHost)` wraps at >255 allocations

**What we lose:**
- "One language" across client and server (mitigated: TypeScript is not a big
  leap from Go for someone comfortable with static types)
- Shared `pkg/protocol/` types (mitigated: not actually shared today, and the
  protocol is 6 stable message types)
- The Wasm learning goal (but the current setup is mostly "JS with extra steps"
  — the Wasm boundary adds complexity without enabling meaningful Wasm learning)

**Known bugs fixed by the rewrite:**
- `worker/turn.go:139` — relay address overflow at >255 allocations (fix during
  port by using proper bounds checking or wider address space)
- `worker.mjs` O(n) WebSocket lookup via `_findWebSocket` (replace with a
  `Map<wsId, WebSocket>` in the TS rewrite)

<!-- Add new feature ideas here -->
