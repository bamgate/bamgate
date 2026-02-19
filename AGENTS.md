# AGENTS.md — bamgate

## Project Overview

bamgate (binary: `bamgate`) is a WireGuard VPN tunnel over WebRTC. It lets a single
user access their home network from anywhere without exposing the home network's public
IP. The relay/signaling infrastructure runs on Cloudflare Workers (free tier).

The entire project is written in **Go**:
- **Go client** — CLI + shared core library (Linux + Android via gomobile), built with standard Go.
- **Go Wasm worker** — Cloudflare Worker + Durable Object (signaling + TURN relay),
  compiled to WebAssembly via **TinyGo**. Custom JS shim bridges WebSocket Hibernation
  API events to Go/Wasm callbacks via `syscall/js`.

See `ARCHITECTURE.md` for the full design document.
See `STATUS.md` for current project progress, what's been completed, and what to work on next.

## Releasing

When creating a release (commit + tag + `gh release create`):

1. Update `STATUS.md` **before committing**:
   - Add the new version to the **Releases** table
   - Add a **Changelog** entry for the session
2. Commit, tag (`git tag vX.Y.Z`), and push with `--tags`
3. Create the GitHub release with `gh release create` — this triggers GitHub Actions:
   - **GoReleaser** builds `bamgate` and `bamgate-hub` binaries for linux/darwin
     (amd64 + arm64) and attaches them to the release
   - **Android job** builds the AAR + debug APK and attaches the APK to the release
4. Version is injected into the binary at build time via
   `-ldflags "-X main.version={{.Version}}"` (configured in `.goreleaser.yaml`)

## Dependencies

- Always use the **latest stable** version of Go and all third-party libraries.
- When adding a new dependency, write the code that imports it first, then run
  `go mod tidy` to fetch and record it. Alternatively, run
  `go get github.com/example/pkg@latest` to pre-fetch it, but do not run
  `go mod tidy` until the import exists in source — tidy removes unused deps.
- Periodically run `go get -u ./...` and `go mod tidy` to keep dependencies current.

## Build / Lint / Test Commands

A `Makefile` in the project root provides all common build targets. Run `make help`
for a full listing. Key targets:

```bash
make                # Build the bamgate CLI binary (default)
make install        # Build and install to /usr/local/bin (requires sudo)
make build-hub      # Build the bamgate-hub binary
make build-all      # Build everything (cli + hub + worker + aar)
make test           # Run all Go tests
make lint           # Run golangci-lint
make fmt            # Format all Go code (gofmt + goimports)

make worker         # Build Cloudflare Worker (TinyGo -> Wasm)
make worker-assets  # Copy worker artifacts to internal/deploy/assets/
make worker-dev     # Start wrangler dev server
make worker-deploy  # Deploy worker to Cloudflare

make aar            # Build Android AAR via gomobile
make android        # Build Android debug APK (builds AAR first)
make install-android # Full chain: AAR -> APK -> adb install

make clean          # Remove all build artifacts
```

Overridable variables (pass as `make VAR=value` or export in env):

| Variable | Default | Purpose |
|----------|---------|---------|
| `VERSION` | `git describe --tags --always --dirty` (falls back to `dev`) | Version string injected into binary |
| `TINYGO` | `~/.local/tinygo/bin/tinygo` | TinyGo binary path |
| `GOMOBILE` | `gomobile` | gomobile binary |
| `OUTPUT_DIR` | `.` | Where CLI binaries are written |

### Raw commands (for reference / CI)

```bash
# Build CLI
CGO_ENABLED=0 go build -ldflags '-s -w -X main.version=VERSION' -o bamgate ./cmd/bamgate

# Run all tests
go test ./...

# Run a single test by name
go test ./internal/signaling/ -run TestClientConnect -v

# Run tests in a specific package
go test ./internal/bridge/ -v

# Run tests with race detector
go test -race ./...

# Lint (install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
golangci-lint run ./...

# Format
gofmt -w .
goimports -w .
```

### Cloudflare Worker (TinyGo -> Wasm)

**TinyGo binary location:** `~/.local/tinygo/bin/tinygo`. The system
`tinygo` at `/usr/bin/tinygo` may be an older version that lacks `wasm-opt`
— always use the local install.

```bash
# Build Wasm binary (from worker/ directory)
~/.local/tinygo/bin/tinygo build -o ./build/app.wasm -target wasm -no-debug ./...

# Dev server
npx wrangler dev

# Deploy
npx wrangler deploy
```

## Code Style

### General Principles
- Write idiomatic Go. Follow [Effective Go](https://go.dev/doc/effective_go),
  the [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) wiki,
  and [Go Proverbs](https://go-proverbs.github.io/) as baseline style guidance.
- Accept interfaces, return structs.
- Make the zero value useful.
- Don't panic — return errors.
- A little copying is better than a little dependency.
- Clear is better than clever.

### Formatting and Linting
- Run `gofmt` and `goimports` before committing. All code must be formatted.
- Use `golangci-lint` with default settings. Fix all warnings.

### Imports
- Group imports in three blocks separated by blank lines:
  1. Standard library
  2. Third-party packages
  3. Internal packages (`github.com/...bamgate/...`)
- Use `goimports` to manage import ordering automatically.

### Naming
- Follow standard Go conventions: `MixedCaps` / `mixedCaps`, not `snake_case`.
- Package names: short, lowercase, single-word when possible (`bridge`, `tunnel`, `config`).
- Interfaces: name by behavior (`Reader`, `Connector`), not `IReader`.
- Unexported helpers: prefix with lowercase. Keep exported API surface small.
- Acronyms: all-caps (`SDP`, `ICE`, `TUN`, `TURN`, `URL`), except when starting
  unexported names (`sdpOffer`).

### Error Handling
- Always check errors. Never use `_` to discard errors unless there is a comment explaining why.
- Wrap errors with context: `fmt.Errorf("connecting to signaling server: %w", err)`.
- Use `errors.Is` / `errors.As` for sentinel and typed error checks.
- Return errors up the stack; avoid `log.Fatal` except in `main()`.
- For cleanup operations (Close, etc.), handle errors or explicitly comment why ignored.

### Types and Structs
- Prefer value receivers for small immutable types; pointer receivers for mutating
  methods or large structs.
- Use `context.Context` as the first parameter for any function that does I/O or
  may be cancelled.
- Define interfaces where they are consumed, not where they are implemented.

### Concurrency
- Always pass `context.Context` to goroutines and respect cancellation.
- Use `errgroup` for managing groups of goroutines that can fail.
- Protect shared state with `sync.Mutex`; prefer channels for communication between goroutines.
- Document goroutine ownership: which goroutine starts it, which one stops it, and how.

### Logging
- Use structured logging (`slog` from stdlib).
- Log levels: `Debug` for internal state, `Info` for operational events, `Warn` for
  recoverable issues, `Error` for failures.
- Include relevant context in log fields: peer ID, connection state, ICE candidate type.

### Testing
- Test files: `*_test.go` in the same package.
- Test names: `TestFunctionName_scenario` (e.g., `TestBridge_handlesDisconnect`).
- Use `testing.T` helpers; avoid assertion libraries unless the team picks one.
- Table-driven tests for functions with multiple input/output cases.
- Use `t.Helper()` in test helper functions.
- Use `t.Parallel()` for tests that don't share state.
- Focus tests on behavior, not implementation details.
- Prioritize testing error paths, edge cases, and concurrency logic.
- Use `go test -cover` to identify blind spots, not as a gate.

### Config
- TOML format for config files.
- Config file lives at `/etc/bamgate/config.toml` (system-wide, owned by root).
- Config keys use `snake_case` (e.g., `server_url`, `auth_token`, `turn_secret`).

### Installation & Service Model
- bamgate is installed via a shell script (`install.sh`) or `bamgate update` — no package manager.
- The binary lives at `/usr/local/bin/bamgate`.
- The daemon runs as **root** via systemd (Linux) or launchd (macOS).
- No `setcap` or file capabilities — root has all capabilities inherently.
- Config at `/etc/bamgate/config.toml`, service file at `/etc/systemd/system/bamgate.service`
  (Linux) or `/Library/LaunchDaemons/com.bamgate.bamgate.plist` (macOS).

## Project Structure

```
cmd/bamgate/           # CLI entry point (main package)
cmd/bamgate-hub/       # Standalone signaling hub for local/LAN testing
install.sh             # Universal install/upgrade script
contrib/               # systemd service file
docs/                  # Supplementary docs (testing-lan)
mobile/                # gomobile binding package (built by `make aar`)
android/               # Android app (Kotlin/Gradle, built by `make android`)
third_party/anet/      # Vendored fork of wlynxg/anet (go.mod replace)
internal/
  agent/               # Top-level orchestrator tying everything together
  bridge/              # TUN <-> WebRTC data channel packet forwarding
  config/              # TOML config management, key generation
  control/             # Control plane server (unix socket)
  deploy/              # Embedded worker assets + Cloudflare deployment
    assets/            # app.wasm, wasm_exec.js, worker.mjs (go:embed)
  signaling/           # WebSocket client to CF Worker
  tunnel/              # wireguard-go device + TUN interface
  turn/                # TURN credential generation + WebSocket proxy dialer
  webrtc/              # RTCPeerConnection, data channel, ICE config
pkg/protocol/          # Shared signaling protocol types (TinyGo-compatible)
worker/                # Cloudflare Worker + Durable Object (Go -> Wasm)
  src/worker.mjs       # JS glue: Worker fetch + DO class + Wasm bridge
  hub.go               # Go signaling hub logic (syscall/js callbacks)
  main.go              # TinyGo Wasm entry point
  turn.go              # Server-side TURN relay state machine
  stun/                # Minimal STUN/TURN message parser (TinyGo-compatible)
```

## Key Libraries

| Library | Purpose |
|---------|---------|
| `github.com/pion/webrtc/v4` | ICE, DTLS, SCTP, data channels |
| `golang.zx2c4.com/wireguard` | Userspace WireGuard (includes `wireguard/tun`) |
| `github.com/coder/websocket` | Signaling WebSocket connection (formerly `nhooyr.io/websocket`) |
| `github.com/spf13/cobra` | CLI command framework |
| `github.com/BurntSushi/toml` | TOML config parsing |
| `github.com/google/nftables` | Linux firewall / NAT management |
| `github.com/skip2/go-qrcode` | QR code generation for invite flow |
| `golang.org/x/crypto` | Cryptographic primitives (curve25519 key generation) |

## Critical Design Constraints

- **Data channels must be unreliable + unordered** (`ordered: false`, `maxRetransmits: 0`)
  to mimic UDP. WireGuard handles its own reliability.
- **Double encryption is intentional**: DTLS (WebRTC) + WireGuard. Don't try to disable either.
- **Home agent never opens inbound ports**. All connections are outbound to Cloudflare.
- **TURN relay only sees opaque encrypted blobs**. Never log or inspect packet contents.
- **Auth tokens and private keys are secrets**. Never commit them or log them in plaintext.

## Testing

### Quick Reference

```bash
# Run all tests
go test ./...

# Run agent integration tests (the most comprehensive suite)
go test ./internal/agent/ -run TestAgent -v

# Run a single test
go test ./internal/agent/ -run TestAgent_TwoPeers_FullConnection -v

# Run tests multiple times to check for flakiness
go test ./internal/agent/ -run TestAgent -count=5

# Run with race detector (slower but catches data races)
go test -race ./...

# Run Docker e2e tests (3-peer mesh with real TUN + WireGuard)
make e2e
# or: go test -tags e2e -v -timeout 120s ./test/e2e/
```

### What to Run After Changes

| Changed | Run |
|---------|-----|
| `internal/agent/` | `go test ./internal/agent/ -run TestAgent -v` |
| `internal/signaling/` | `go test ./internal/signaling/ -v` |
| `internal/webrtc/` | `go test ./internal/webrtc/ -v` |
| `internal/bridge/` | `go test ./internal/bridge/ -v` |
| `internal/tunnel/` | `go test ./internal/tunnel/ -v` |
| `internal/config/` | `go test ./internal/config/ -v` |
| `pkg/protocol/` | `go test ./pkg/protocol/ -v` |
| Any change | `go test ./...` (full suite, ~2s) |
| Before committing | `go test ./... && golangci-lint run ./...` |
| Before releasing | `make e2e` (Docker e2e, ~90s) |

### Agent Integration Tests

The agent package has the most comprehensive test suite. Tests run without root
privileges using **fake TUN/WireGuard** + **real signaling** (in-process Hub) +
**real WebRTC** (local ICE). No network access required.

**Architecture:**
- `internal/agent/deps.go` — Defines interfaces (`SignalingClient`, `WireGuardDevice`,
  `NetworkManager`, `NATSetup`, `AuthRefresher`, `ConfigPersister`, `TUNProvider`,
  `WireGuardProvider`) and a `Deps` struct with `DefaultDeps()` factory.
- `internal/agent/fake_test.go` — Test doubles for all interfaces. The fakes record
  calls (e.g., `fakeWireGuardDevice.hasPeer()`, `fakeNetworkManager.routes`) so tests
  can verify behavior without kernel access.
- `internal/agent/agent_integration_test.go` — Integration tests.

**Test inventory:**

| Test | What it verifies |
|------|-----------------|
| `TestAgent_TwoPeers_FullConnection` | Two agents discover each other, SDP exchange, data channel, WG peer config |
| `TestAgent_PeerLeft_Cleanup` | Peer departure triggers WG peer removal and state cleanup |
| `TestAgent_ThreePeers` | Three agents all connect (tests offer/answer interleaving) |
| `TestAgent_TokenRefresh` | OAuth token refresh on startup, config persistence |
| `TestAgent_RoutesAccepted` | Route advertisement → kernel route addition |
| `TestAgent_GlareResolution` | Lexicographic offer ordering prevents duplicate connections |
| `TestAgent_SelfSkip` | Agent ignores itself in peers list |
| `TestAgent_ICEDisconnect_GracePeriod` | Grace timer starts on disconnect, cancelled on reconnect |
| `TestAgent_ICEFailed_RestartsICE` | ICE failure triggers restart → successful reconnection |
| `TestAgent_ICERestart_MaxAttempts` | Peer removed after exhausting max ICE restart attempts |
| `TestAgent_ICEDisconnect_GraceExpires` | Grace timer expiry triggers ICE restart |
| `TestAgent_NotifyNetworkChange` | Network change resets ICE state, triggers restarts, debounce |

**Writing new agent tests:**
1. Use `newTestDeps()` to get a `Deps` with all fakes pre-wired.
2. Override `deps.Signaling` with a real `signaling.NewClient` pointed at
   `startTestHub(t)`.
3. Use `startConnectedPair(t)` helper for tests that need two already-connected agents.
4. Use `waitFor(t, timeout, desc, fn)` for async assertions.
5. Use `isShutdownError(err)` when checking agent exit errors — context cancellation,
   WebSocket close, etc. are all normal during test teardown.

### Docker E2E Tests

The `test/e2e/` directory contains end-to-end tests that spin up real bamgate
peers in Docker containers with real TUN devices, real WireGuard encryption,
and real WebRTC data channels. Actual IP packets (ICMP ping) flow through the
encrypted tunnel.

**Prerequisites:**
- Docker with the compose plugin (`docker compose version`)
- `/dev/net/tun` available on the host
- ~90 seconds per run (image build is cached after the first run)

**Running:**
```bash
make e2e
# or: go test -tags e2e -v -timeout 120s ./test/e2e/
```

**Topology:** 4 Docker containers on a shared bridge network:
```
hub (bamgate-hub :8080)     — signaling relay, no special caps
  |
  +— alpha  (bamgate)       — CAP_NET_ADMIN + /dev/net/tun, 10.0.0.1/24
  +— bravo  (bamgate)       — CAP_NET_ADMIN + /dev/net/tun, 10.0.0.2/24
  +— charlie (bamgate)      — CAP_NET_ADMIN + /dev/net/tun, 10.0.0.3/24
```

**Test inventory:**

| Test | What it verifies |
|------|-----------------|
| `TestE2E_ThreePeerMesh` | 3 peers establish full mesh, ping all 6 directions through tunnel |
| `TestE2E_PeerDeparture` | Peer stops → remaining peers still connected → peer rejoins → full mesh restored |

**What's different from agent integration tests:**
- Integration tests use fake TUN/WireGuard + real WebRTC + real signaling.
  They verify agent logic (peer lifecycle, ICE restart, config) but no real
  packets flow.
- E2E tests use real everything: TUN, WireGuard encryption, bridge, WebRTC,
  signaling. Actual IP packets traverse the full tunnel stack.

**Files:**
- `test/e2e/Dockerfile` — Multi-stage build (Go builder → Debian slim runtime)
- `test/e2e/docker-compose.yml` — Hub + 3 peer containers
- `test/e2e/e2e_test.go` — Go test driver (build tag `e2e`)

**SELinux note:** Volume mounts use `:z` label for SELinux compatibility on
Fedora/RHEL. If you see "permission denied" on config files, ensure the `:z`
label is present in the compose file.

## Signaling Protocol

JSON over WebSocket with a `"type"` discriminator field. Message types:
`"join"`, `"offer"`, `"answer"`, `"ice-candidate"`, `"peers"`, `"peer-left"`.

See ARCHITECTURE.md "Signaling protocol" section for full message schemas.
