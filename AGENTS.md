# AGENTS.md — riftgate

## Project Overview

riftgate (binary: `riftgate`) is a WireGuard VPN tunnel over WebRTC. It lets a single
user access their home network from anywhere without exposing the home network's public
IP. The relay/signaling infrastructure runs on Cloudflare Workers (free tier).

The entire project is written in **Go**:
- **Go client** — CLI + shared core library (Linux + Android via gomobile), built with standard Go.
- **Go Wasm worker** — Cloudflare Worker + Durable Object (signaling + TURN relay),
  compiled to WebAssembly via **TinyGo**. Uses `syumai/workers` for CF Workers integration.

See `ARCHITECTURE.md` for the full design document.
See `STATUS.md` for current project progress, what's been completed, and what to work on next.

## Dependencies

- Always use the **latest stable** version of Go and all third-party libraries.
- When adding a new dependency, use `@latest` (e.g., `go get github.com/pion/webrtc/v4@latest`).
- Periodically run `go get -u ./...` and `go mod tidy` to keep dependencies current.

## Build / Lint / Test Commands

### Go Client

```bash
# Build
go build -o riftgate ./cmd/riftgate

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

```bash
# Build Wasm binary (from worker/ directory)
tinygo build -o ./build/app.wasm -target wasm -no-debug ./...

# Dev server
npx wrangler dev

# Deploy
npx wrangler deploy
```

## Code Style

### Formatting and Linting
- Run `gofmt` and `goimports` before committing. All code must be formatted.
- Use `golangci-lint` with default settings. Fix all warnings.

### Imports
- Group imports in three blocks separated by blank lines:
  1. Standard library
  2. Third-party packages
  3. Internal packages (`github.com/...riftgate/...`)
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

### Config
- TOML format for config files.
- Config keys use `snake_case` (e.g., `server_url`, `auth_token`, `turn_secret`).

## Project Structure

```
cmd/riftgate/          # CLI entry point (main package)
internal/
  config/              # TOML config management, key generation
  signaling/           # WebSocket client to CF Worker
  webrtc/              # RTCPeerConnection, data channel, ICE config
  tunnel/              # wireguard-go device + TUN interface
  bridge/              # TUN <-> WebRTC data channel packet forwarding
  agent/               # Top-level orchestrator tying everything together
worker/                # Cloudflare Worker + Durable Object (Go -> Wasm)
```

## Key Libraries

| Library | Purpose |
|---------|---------|
| `github.com/pion/webrtc/v4` | ICE, DTLS, SCTP, data channels |
| `github.com/pion/turn/v4` | TURN client/server reference |
| `golang.zx2c4.com/wireguard` | Userspace WireGuard |
| `golang.zx2c4.com/wireguard/tun` | TUN device management |
| `nhooyr.io/websocket` | Signaling WebSocket connection |
| `github.com/BurntSushi/toml` | TOML config parsing |
| `github.com/syumai/workers` | Go HTTP handler on CF Workers (TinyGo->Wasm) |

## Critical Design Constraints

- **Data channels must be unreliable + unordered** (`ordered: false`, `maxRetransmits: 0`)
  to mimic UDP. WireGuard handles its own reliability.
- **Double encryption is intentional**: DTLS (WebRTC) + WireGuard. Don't try to disable either.
- **Home agent never opens inbound ports**. All connections are outbound to Cloudflare.
- **TURN relay only sees opaque encrypted blobs**. Never log or inspect packet contents.
- **Auth tokens and private keys are secrets**. Never commit them or log them in plaintext.

## Signaling Protocol

JSON over WebSocket with a `"type"` discriminator field. Message types:
`"join"`, `"offer"`, `"answer"`, `"ice-candidate"`, `"peers"`, `"peer-left"`.

See ARCHITECTURE.md "Signaling protocol" section for full message schemas.
