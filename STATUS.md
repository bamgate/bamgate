# riftgate — Project Status

Last updated: 2026-02-10

## Current Phase

**Phase 1: Go Client Core (Signaling + WebRTC)** — Complete
**Phase 2: WireGuard Integration** — Complete (tested end-to-end, tunnel working)
**Phase 3: Cloudflare Worker (Signaling Server)** — In progress

See ARCHITECTURE.md §Implementation Plan for the full 7-phase roadmap.

## Completed

- Detailed architecture design (ARCHITECTURE.md) with 7-phase implementation plan
- Project scaffolding: Go module, directory structure, all package stubs
- Coding guidelines and AI agent instructions (AGENTS.md)
- Project README
- **Signaling protocol** (`pkg/protocol/protocol.go`): `Message` interface with 6 concrete message types (`JoinMessage`, `OfferMessage`, `AnswerMessage`, `ICECandidateMessage`, `PeersMessage`, `PeerLeftMessage`), JSON marshal/unmarshal with type discriminator registry. Shared package with zero external dependencies (TinyGo-compatible).
- **Signaling client** (`internal/signaling/client.go`): WebSocket client with channel-based receive, context-aware lifecycle, automatic reconnection with exponential backoff
- **Signaling hub** (`internal/signaling/hub.go`): Exported `Hub` type implementing `http.Handler` — relays SDP offers/answers and ICE candidates between connected peers, tracks peer presence, broadcasts join/leave events to all connected peers. Extracted from test suite for reuse by `cmd/riftgate-hub` and tests.
- **Signaling tests** (`protocol_test.go`, `client_test.go`): Full test suite with signaling hub, covering round-trip serialization, peer exchange, peer-left notifications, reconnection, context cancellation, and error cases. All tests pass with `-race`.
- **WebRTC ICE configuration** (`internal/webrtc/ice.go`): `ICEConfig` struct with STUN/TURN server support, default public STUN servers (Cloudflare + Google), conversion to pion ICE server format
- **WebRTC data channel** (`internal/webrtc/datachan.go`): Data channel configuration for unreliable, unordered delivery (`ordered: false`, `maxRetransmits: 0`) — critical for WireGuard UDP-like behavior
- **WebRTC peer connection** (`internal/webrtc/peer.go`): `Peer` struct wrapping pion `RTCPeerConnection` with SDP offer/answer exchange (`CreateOffer`, `HandleOffer`, `SetAnswer`), trickle ICE candidate relay (`AddICECandidate`), data channel lifecycle management, connection state callbacks, and graceful shutdown
- **WebRTC integration tests** (`internal/webrtc/peer_test.go`): 4 tests covering SDP offer/answer exchange, bidirectional data transfer over data channels, unreliable/unordered configuration verification, and ICE connection state callbacks. All tests pass with `-race`.
- **WireGuard key management** (`internal/config/keys.go`): `Key` type (32-byte Curve25519) with `GeneratePrivateKey()` (RFC 7748 §5 clamping via `crypto/curve25519`), `PublicKey()` derivation, `ParseKey()` base64 decoding, `MarshalText`/`UnmarshalText` for seamless TOML/JSON integration, `IsZero()` zero-value check. Verified against RFC 7748 §6.1 test vectors.
- **TOML config management** (`internal/config/config.go`): `Config` struct with `NetworkConfig` (name, server_url, auth_token, turn_secret), `DeviceConfig` (name, private_key, address), `STUNConfig` (servers), `WebRTCConfig` (ordered, max_retransmits). `LoadConfig`/`SaveConfig` with `BurntSushi/toml`, `DefaultConfigPath()` respecting `$XDG_CONFIG_HOME`, default STUN servers, 0600 file permissions for secrets.
- **Config tests** (`keys_test.go`, `config_test.go`): 21 tests covering key generation, clamping correctness, RFC 7748 known vector, base64 round-trips, MarshalText/UnmarshalText, TOML save/load round-trip, defaults application, missing file errors, nested directory creation, Key serialization in TOML. All tests pass with `-race`.
- **WireGuard TUN device** (`internal/tunnel/tun.go`): Wrapper around `wireguard-go`'s `tun.CreateTUN` with default interface name (`riftgate0`) and MTU (1420).
- **WireGuard UAPI config** (`internal/tunnel/config.go`): `DeviceConfig` and `PeerConfig` structs (with `Endpoint` field for custom Bind routing), `BuildUAPIConfig` / `BuildPeerUAPIConfig` / `BuildRemovePeerUAPIConfig` for generating wireguard-go's IPC configuration strings. Keys hex-encoded per UAPI spec.
- **WireGuard device lifecycle** (`internal/tunnel/device.go`): `Device` struct wrapping wireguard-go's `device.Device` + TUN interface. `NewDevice` creates and starts the WireGuard device with a custom `conn.Bind`. `AddPeer`/`RemovePeer` for dynamic peer management. Adapts wireguard-go's logger to `slog`.
- **Tunnel tests** (`internal/tunnel/config_test.go`): 9 tests covering hex key encoding, UAPI config generation (device-only, with peers, peer ordering, multiple allowed IPs, keepalive, remove peer). All tests pass with `-race`.
- **Bridge / custom conn.Bind** (`internal/bridge/bridge.go`): Custom `conn.Bind` implementation that transports WireGuard encrypted packets over WebRTC data channels instead of UDP. `Bind` struct manages a set of data channels (one per remote peer), routes `Send` calls to the correct data channel via `Endpoint` peer ID, and queues incoming data channel messages for wireguard-go's receive loop. `Endpoint` struct implements `conn.Endpoint` with peer ID-based addressing. Includes `SetDataChannel`/`RemoveDataChannel` for dynamic peer management, graceful `Close` with channel-based unblocking, and automatic close-channel reset in `Open()` to survive wireguard-go's `BindUpdate` cycle.
- **Bridge tests** (`internal/bridge/bridge_test.go`): 12 tests covering Open/Receive, Close unblocking, Send to real WebRTC data channels, Send to unknown peer, ParseEndpoint, BatchSize, SetMark, RemoveDataChannel, multiple peers, bidirectional data channel receive, Endpoint methods, and Reset lifecycle. All tests pass with `-race`.
- **Agent orchestrator** (`internal/agent/agent.go`): Top-level coordinator tying signaling + WebRTC + bridge + WireGuard. Manages full lifecycle: TUN creation, WireGuard device setup with custom Bind, signaling connection with reconnection, peer discovery and WebRTC connection establishment, dynamic WireGuard peer management as data channels open/close. Uses lexicographic peer ID ordering to determine offer/answer roles. Exchanges WireGuard public keys in offer/answer messages. Configures TUN interface IP via `ip` command.
- **CLI `riftgate up`** (`cmd/riftgate/main.go`): Minimal CLI that loads TOML config, validates required fields, sets up signal handling (SIGINT/SIGTERM), and runs the agent until shutdown. Supports `--config` and `-v` flags.
- **Standalone signaling hub** (`cmd/riftgate-hub/main.go`): Lightweight HTTP server running the signaling `Hub` for local/LAN testing. Supports `-addr` flag. Graceful shutdown on signals.
- **LAN testing guide** (`docs/testing-lan.md`): Step-by-step instructions for testing the full tunnel between two Linux machines.
- **End-to-end LAN tunnel verified**: Two Linux machines successfully ping each other through the WireGuard-over-WebRTC tunnel using the local signaling hub. Three critical bugs fixed during testing:
  - Hub was not broadcasting join notifications to existing peers (first peer never discovered later peers)
  - Offer/answer messages did not carry WireGuard public keys (answering peer couldn't configure WireGuard)
  - Bridge `Bind.Open()` did not reset the close channel after wireguard-go's `Close→Open` BindUpdate cycle (receive loop was dead on arrival)
- **Shared protocol package** (`pkg/protocol/`): Extracted signaling protocol types from `internal/signaling/` into a standalone package with zero external dependencies (only `encoding/json` and `fmt`). TinyGo-compatible, shared between Go client and Cloudflare Worker.
- **Cloudflare Worker signaling server** (`worker/`): Go/Wasm Durable Object implementing the signaling hub. Architecture: JS shell (`src/worker.mjs`) exports Worker `fetch` handler and `SignalingRoom` DO class using WebSocket Hibernation API. DO class bridges WebSocket events to Go/Wasm callbacks via `syscall/js`. Go hub logic (`hub.go`) manages peer state, routes signaling messages (offer/answer/ice-candidate), broadcasts join/leave events. Wasm entry point (`main.go`) registers callbacks and blocks. Bearer token auth on `/connect` endpoint via `AUTH_TOKEN` env var.
- **Bearer token auth**: Added `AuthToken` field to `signaling.ClientConfig` and `Authorization: Bearer <token>` header to WebSocket dial. Agent passes `config.Network.AuthToken` to signaling client.

## What's Next

**Phase 3 nearly complete** — Cloudflare Worker signaling server built and tested locally:

1. ~~Set up Wrangler project with Worker + Durable Object (TinyGo → Wasm build pipeline)~~ — Done
2. ~~Implement Worker: auth middleware, WebSocket upgrade, routing to correct DO~~ — Done
3. ~~Implement DO signaling: peer join/leave, SDP relay, ICE candidate relay~~ — Done
4. ~~Add bearer token auth to signaling client~~ — Done
5. ~~Build with TinyGo and test locally with `wrangler dev`~~ — Done (408KB Wasm binary, all signaling flows verified)
6. Deploy to Cloudflare and test full tunnel end-to-end across the internet

## Testing Phase 2

See [docs/testing-lan.md](docs/testing-lan.md) for the full LAN testing guide.

**Tested and working** on two Linux machines (2026-02-10): ICE connects via host candidates on the LAN, WebRTC data channel opens, WireGuard handshake completes, bidirectional ping works.

## Code Status

| Package | Files | Status |
|---------|-------|--------|
| `cmd/riftgate` | main.go | **Implemented** — minimal `up` command |
| `cmd/riftgate-hub` | main.go | **Implemented** — standalone signaling server |
| `internal/agent` | agent.go | **Implemented** — orchestrator |
| `internal/bridge` | bridge.go, bridge_test.go | **Implemented + tested** |
| `internal/config` | config.go, keys.go, config_test.go, keys_test.go | **Implemented + tested** |
| `internal/signaling` | client.go, hub.go, client_test.go | **Implemented + tested** |
| `pkg/protocol` | protocol.go, protocol_test.go | **Implemented + tested** |
| `internal/tunnel` | config.go, device.go, tun.go, config_test.go | **Implemented + tested** |
| `internal/webrtc` | ice.go, datachan.go, peer.go, peer_test.go | **Implemented + tested** |
| `worker/` | hub.go, main.go, src/worker.mjs | **Implemented + tested locally** — TinyGo 408KB Wasm, verified with wrangler dev |

## Dependencies

| Library | Version | Purpose |
|---------|---------|---------|
| `github.com/coder/websocket` | v1.8.14 | WebSocket client/server for signaling |
| `github.com/pion/webrtc/v4` | v4.2.3 | WebRTC stack (ICE, DTLS, SCTP, data channels) |
| `github.com/BurntSushi/toml` | v1.6.0 | TOML config file parsing |
| `golang.org/x/crypto` | v0.37.0 | Curve25519 key derivation (WireGuard keys) |
| `golang.zx2c4.com/wireguard` | v0.0.0-20250521 | Userspace WireGuard device + TUN interface |

## Open Questions / Decisions

- None at this time.

## Changelog

- **2026-02-10**: Phase 3 implementation — Cloudflare Worker signaling server. Extracted protocol types to shared `pkg/protocol/` package (TinyGo-compatible). Scaffolded `worker/` with Go/Wasm Durable Object: JS shell with WebSocket Hibernation API bridges to Go hub logic via `syscall/js`. Added bearer token auth to signaling client and agent. Built with TinyGo (408KB Wasm binary). Tested full signaling flow locally with `wrangler dev`: peer join, peers list, offer/answer relay, ICE candidate relay, peer-left notification — all working. Key finding: `no_bundle = true` + `find_additional_modules = true` required to avoid esbuild bundling `wasm_exec.js` (which causes infinite recursion in Workers runtime).
- **2026-02-10**: End-to-end LAN tunnel verified. Fixed three bugs blocking the tunnel: hub not broadcasting join events to existing peers, offer/answer messages missing WireGuard public keys, and bridge Bind receive loop dying after wireguard-go's BindUpdate cycle. Added `Endpoint` field to `PeerConfig` for Bind routing. Added `PublicKey` field to `OfferMessage`/`AnswerMessage`. Added LAN testing guide (`docs/testing-lan.md`). Released v0.2.0.
- **2026-02-10**: Phase 2 complete — WireGuard integration. Implemented tunnel package (TUN creation, UAPI config, device lifecycle), bridge package (custom `conn.Bind` over WebRTC data channels), agent orchestrator, CLI `riftgate up` command, standalone signaling hub binary (`riftgate-hub`). Extracted signaling Hub from tests for reuse. 35 tests across all packages, all passing with `-race`.
- **2026-02-09**: Implemented config package — TOML config management (load/save with BurntSushi/toml, XDG path support, 0600 permissions) and WireGuard key generation (Curve25519 via crypto/curve25519, RFC 7748 clamping). Phase 1 complete.
- **2026-02-09**: Implemented WebRTC peer connection manager with ICE configuration, unreliable/unordered data channels, SDP offer/answer exchange, and integration tests.
- **2026-02-09**: Implemented signaling protocol types and WebSocket client with full test suite.
- **2026-02-09**: Project bootstrapped — architecture doc, scaffolding, README, coding guidelines.
