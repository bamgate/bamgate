# bamgate — Project Status

Last updated: 2026-02-12 (session 10)

## Current Phase

**Phase 1: Go Client Core (Signaling + WebRTC)** — Complete
**Phase 2: WireGuard Integration** — Complete (tested end-to-end, tunnel working)
**Phase 3: Cloudflare Worker (Signaling Server)** — Complete (deployed, multi-peer tested)
**Phase 4: TURN Relay on Durable Objects** — Complete (implemented, needs deploy + manual test)
**Phase 5: CLI Polish & Resilience** — Complete
**Phase 5.5: UX & Operational Improvements** — Complete
**Phase 6: Automated Setup & Deployment** — Complete

See ARCHITECTURE.md §Implementation Plan for the full 7-phase roadmap.

## Completed

- Detailed architecture design (ARCHITECTURE.md) with 7-phase implementation plan
- Project scaffolding: Go module, directory structure, all package stubs
- Coding guidelines and AI agent instructions (AGENTS.md)
- Project README
- **Signaling protocol** (`pkg/protocol/protocol.go`): `Message` interface with 6 concrete message types (`JoinMessage`, `OfferMessage`, `AnswerMessage`, `ICECandidateMessage`, `PeersMessage`, `PeerLeftMessage`), JSON marshal/unmarshal with type discriminator registry. Shared package with zero external dependencies (TinyGo-compatible).
- **Signaling client** (`internal/signaling/client.go`): WebSocket client with channel-based receive, context-aware lifecycle, automatic reconnection with exponential backoff
- **Signaling hub** (`internal/signaling/hub.go`): Exported `Hub` type implementing `http.Handler` — relays SDP offers/answers and ICE candidates between connected peers, tracks peer presence, broadcasts join/leave events to all connected peers. Extracted from test suite for reuse by `cmd/bamgate-hub` and tests.
- **Signaling tests** (`protocol_test.go`, `client_test.go`): Full test suite with signaling hub, covering round-trip serialization, peer exchange, peer-left notifications, reconnection, context cancellation, and error cases. All tests pass with `-race`.
- **WebRTC ICE configuration** (`internal/webrtc/ice.go`): `ICEConfig` struct with STUN/TURN server support, default public STUN servers (Cloudflare + Google), conversion to pion ICE server format
- **WebRTC data channel** (`internal/webrtc/datachan.go`): Data channel configuration for unreliable, unordered delivery (`ordered: false`, `maxRetransmits: 0`) — critical for WireGuard UDP-like behavior
- **WebRTC peer connection** (`internal/webrtc/peer.go`): `Peer` struct wrapping pion `RTCPeerConnection` with SDP offer/answer exchange (`CreateOffer`, `HandleOffer`, `SetAnswer`), trickle ICE candidate relay (`AddICECandidate`), data channel lifecycle management, connection state callbacks, and graceful shutdown
- **WebRTC integration tests** (`internal/webrtc/peer_test.go`): 4 tests covering SDP offer/answer exchange, bidirectional data transfer over data channels, unreliable/unordered configuration verification, and ICE connection state callbacks. All tests pass with `-race`.
- **WireGuard key management** (`internal/config/keys.go`): `Key` type (32-byte Curve25519) with `GeneratePrivateKey()` (RFC 7748 §5 clamping via `crypto/curve25519`), `PublicKey()` derivation, `ParseKey()` base64 decoding, `MarshalText`/`UnmarshalText` for seamless TOML/JSON integration, `IsZero()` zero-value check. Verified against RFC 7748 §6.1 test vectors.
- **TOML config management** (`internal/config/config.go`): `Config` struct with `NetworkConfig` (name, server_url, auth_token, turn_secret), `DeviceConfig` (name, private_key, address), `STUNConfig` (servers), `WebRTCConfig` (ordered, max_retransmits). `LoadConfig`/`SaveConfig` with `BurntSushi/toml`, `DefaultConfigPath()` respecting `$XDG_CONFIG_HOME`, default STUN servers, 0600 file permissions for secrets.
- **Config tests** (`keys_test.go`, `config_test.go`): 21 tests covering key generation, clamping correctness, RFC 7748 known vector, base64 round-trips, MarshalText/UnmarshalText, TOML save/load round-trip, defaults application, missing file errors, nested directory creation, Key serialization in TOML. All tests pass with `-race`.
- **WireGuard TUN device** (`internal/tunnel/tun.go`): Wrapper around `wireguard-go`'s `tun.CreateTUN` with default interface name (`bamgate0`) and MTU (1420).
- **WireGuard UAPI config** (`internal/tunnel/config.go`): `DeviceConfig` and `PeerConfig` structs (with `Endpoint` field for custom Bind routing), `BuildUAPIConfig` / `BuildPeerUAPIConfig` / `BuildRemovePeerUAPIConfig` for generating wireguard-go's IPC configuration strings. Keys hex-encoded per UAPI spec.
- **WireGuard device lifecycle** (`internal/tunnel/device.go`): `Device` struct wrapping wireguard-go's `device.Device` + TUN interface. `NewDevice` creates and starts the WireGuard device with a custom `conn.Bind`. `AddPeer`/`RemovePeer` for dynamic peer management. Adapts wireguard-go's logger to `slog`.
- **Tunnel tests** (`internal/tunnel/config_test.go`): 9 tests covering hex key encoding, UAPI config generation (device-only, with peers, peer ordering, multiple allowed IPs, keepalive, remove peer). All tests pass with `-race`.
- **Bridge / custom conn.Bind** (`internal/bridge/bridge.go`): Custom `conn.Bind` implementation that transports WireGuard encrypted packets over WebRTC data channels instead of UDP. `Bind` struct manages a set of data channels (one per remote peer), routes `Send` calls to the correct data channel via `Endpoint` peer ID, and queues incoming data channel messages for wireguard-go's receive loop. `Endpoint` struct implements `conn.Endpoint` with peer ID-based addressing. Includes `SetDataChannel`/`RemoveDataChannel` for dynamic peer management, graceful `Close` with channel-based unblocking, and automatic close-channel reset in `Open()` to survive wireguard-go's `BindUpdate` cycle.
- **Bridge tests** (`internal/bridge/bridge_test.go`): 12 tests covering Open/Receive, Close unblocking, Send to real WebRTC data channels, Send to unknown peer, ParseEndpoint, BatchSize, SetMark, RemoveDataChannel, multiple peers, bidirectional data channel receive, Endpoint methods, and Reset lifecycle. All tests pass with `-race`.
- **Agent orchestrator** (`internal/agent/agent.go`): Top-level coordinator tying signaling + WebRTC + bridge + WireGuard. Manages full lifecycle: TUN creation, WireGuard device setup with custom Bind, signaling connection with reconnection, peer discovery and WebRTC connection establishment, dynamic WireGuard peer management as data channels open/close. Uses lexicographic peer ID ordering to determine offer/answer roles. Exchanges WireGuard public keys in offer/answer messages. Configures TUN interface IP via `ip` command.
- **CLI `bamgate up`** (`cmd/bamgate/main.go`): Minimal CLI that loads TOML config, validates required fields, sets up signal handling (SIGINT/SIGTERM), and runs the agent until shutdown. Supports `--config` and `-v` flags.
- **Standalone signaling hub** (`cmd/bamgate-hub/main.go`): Lightweight HTTP server running the signaling `Hub` for local/LAN testing. Supports `-addr` flag. Graceful shutdown on signals.
- **LAN testing guide** (`docs/testing-lan.md`): Step-by-step instructions for testing the full tunnel between two Linux machines.
- **End-to-end LAN tunnel verified**: Two Linux machines successfully ping each other through the WireGuard-over-WebRTC tunnel using the local signaling hub. Three critical bugs fixed during testing:
  - Hub was not broadcasting join notifications to existing peers (first peer never discovered later peers)
  - Offer/answer messages did not carry WireGuard public keys (answering peer couldn't configure WireGuard)
  - Bridge `Bind.Open()` did not reset the close channel after wireguard-go's `Close→Open` BindUpdate cycle (receive loop was dead on arrival)
- **Shared protocol package** (`pkg/protocol/`): Extracted signaling protocol types from `internal/signaling/` into a standalone package with zero external dependencies (only `encoding/json` and `fmt`). TinyGo-compatible, shared between Go client and Cloudflare Worker.
- **Cloudflare Worker signaling server** (`worker/`): Go/Wasm Durable Object implementing the signaling hub. Architecture: JS shell (`src/worker.mjs`) exports Worker `fetch` handler and `SignalingRoom` DO class using WebSocket Hibernation API. DO class bridges WebSocket events to Go/Wasm callbacks via `syscall/js`. Go hub logic (`hub.go`) manages peer state, routes signaling messages (offer/answer/ice-candidate), broadcasts join/leave events. Wasm entry point (`main.go`) registers callbacks and blocks. Bearer token auth on `/connect` endpoint via `AUTH_TOKEN` env var.
- **Bearer token auth**: Added `AuthToken` field to `signaling.ClientConfig` and `Authorization: Bearer <token>` header to WebSocket dial. Agent passes `config.Network.AuthToken` to signaling client.
- **DO hibernation state recovery**: After Cloudflare hibernates the Durable Object, Wasm is re-instantiated with empty state but WebSocket connections survive. Added `_rehydrate()` in JS that restores Go hub peer state from WebSocket attachments via `goOnRehydrate` callback.
- **Deployed to Cloudflare**: Worker live at `https://bamgate.ag94441.workers.dev` (432KB total upload). Tested with 3 peers across the internet (2 home LAN machines + DigitalOcean droplet).
- **Peer-specific AllowedIPs routing**: Fixed critical bug where every WireGuard peer got `AllowedIPs: 0.0.0.0/0`, causing last-added peer's route to override all previous peers. Now exchanges tunnel addresses via signaling (`Address` field in `JoinMessage`/`PeerInfo`), extracts host IP from CIDR, and uses `/32` AllowedIP per peer. Full pipeline: client → signaling → worker hub → peers list → agent → WireGuard config.
- **CLI subcommand framework** (`cmd/bamgate/`): Restructured CLI using Cobra with subcommands: `up` (connect), `init` (setup wizard), `status` (query agent), `genkey` (generate WireGuard key). Global `--config` and `-v` flags inherited by all subcommands.
- **`bamgate init` command**: Interactive setup wizard that generates a WireGuard key pair, prompts for device name (default: hostname), server URL, auth token, tunnel address, and network name. Writes `config.toml` with 0600 permissions. Detects and prompts before overwriting existing config.
- **`bamgate genkey` command**: Generates a new Curve25519 private key (stdout, pipe-friendly) and prints the corresponding public key to stderr.
- **Subnet routing**: Peers can advertise additional subnets via `routes` field in `[device]` config. Routes propagate through signaling (`Routes` field in `JoinMessage`/`PeerInfo`), through the Worker DO, and are added as WireGuard AllowedIPs on remote peers. Dangerous routes (`0.0.0.0/0`, `::/0`) and invalid CIDRs are rejected with warnings.
- **ICE restart / resilience**: Replaced immediate peer teardown on ICE failure with a restart-then-remove strategy. `ICEConnectionStateDisconnected` triggers a 5-second grace period (ICE may self-recover). `ICEConnectionStateFailed` triggers an immediate ICE restart via `CreateOffer` with ICE restart flag. Up to 3 restart attempts before tearing down. `ICEConnectionStateConnected` resets the restart counter. Grace timers are cleaned up on peer removal.
- **`bamgate status` command + control server** (`internal/control/`): Agent listens on Unix socket (`/run/bamgate/control.sock`) serving a JSON status API. Status includes device info, uptime, and per-peer state (address, ICE connection state, ICE candidate type, advertised routes, connected-since timestamp). `bamgate status` connects to the socket and displays a formatted table. Control server starts non-fatally (agent runs without it if socket creation fails).
- **Systemd service** (`contrib/bamgate.service`): Unit file with `Type=simple`, `Restart=on-failure`, capability-based hardening (`CAP_NET_ADMIN`, `CAP_NET_RAW`), filesystem restrictions (`ProtectSystem=strict`, `ProtectHome=read-only`), and runtime directory for the control socket.
- **WebRTC test race fix**: Fixed pre-existing data race in `internal/webrtc/peer_test.go` where pion ICE gathering goroutines could send on closed candidate channels. Added `safeCandidateSender` helper using a `done` channel to guard sends during test teardown.
- **Non-root TUN configuration** (`internal/tunnel/netlink.go`): Replaced `exec.Command("ip", ...)` calls with direct netlink syscalls (`NETLINK_ROUTE` socket) for adding IP addresses and bringing interfaces up. No dependency on the `ip` binary. Uses `golang.org/x/sys/unix` for raw syscall access. 4 tests for message construction.
- **Smart control socket path** (`internal/control/server.go`): `ResolveSocketPath()` checks `/run/bamgate/` (systemd), then `$XDG_RUNTIME_DIR/bamgate/`, then `/tmp/bamgate/` as fallback.
- **`bamgate install` command** (`cmd/bamgate/cmd_install.go`): `sudo bamgate install` copies binary to `/usr/local/bin/`, runs `setcap cap_net_admin,cap_net_raw+eip`. Optional `--systemd` flag installs the service file. Resolves the real user from `$SUDO_USER` and sets `User=`/`Group=` in the service file so the service runs with capabilities, not as root.
- **`bamgate init` URL scheme normalization** (`cmd/bamgate/cmd_init.go`): `normalizeServerURL()` auto-prepends `wss://` for bare hostnames, converts `https://` to `wss://` and `http://` to `ws://`, rejects unsupported schemes. Prompt text updated to show `wss://` example. 9 table-driven tests.
- **`bamgate up -d` daemon mode** (`cmd/bamgate/cmd_up.go`): `-d`/`--daemon` flag runs `sudo systemctl enable --now bamgate` — starts the service and enables it on boot. Checks that the systemd service file is installed first.
- **`bamgate down` command** (`cmd/bamgate/cmd_down.go`): Counterpart to `up -d`. Runs `sudo systemctl disable --now bamgate` — stops the service and disables it from boot.
- **Systemd service runs as user, not root**: The service file generated by `bamgate install --systemd` includes `User=` and `Group=` set to the actual user (from `$SUDO_USER`). Capabilities are granted via `AmbientCapabilities` — no root required.
- **Worker redeployed**: Cloudflare Worker updated with subnet routing support (`Routes` field in peer state, join messages, and rehydration). Version `9bad22b0`.
- **Automatic IP forwarding and NAT** (`internal/tunnel/netlink.go`, `internal/tunnel/nat.go`, `internal/agent/agent.go`): When a device advertises routes (e.g., `routes = ["192.168.1.0/24"]`), bamgate now automatically enables per-interface IPv4 forwarding via netlink `RTM_SETLINK` + `IFLA_AF_SPEC` (avoids procfs writes that need `CAP_DAC_OVERRIDE`) and adds nftables MASQUERADE rules via `github.com/google/nftables` (pure Go, no iptables/nft binaries needed) in a dedicated `bamgate` nftables table. Previous forwarding state is saved and restored on shutdown; the nftables table is removed on shutdown. No new capabilities required — `CAP_NET_ADMIN` is sufficient for both netlink forwarding and nftables. New helpers: `SetForwarding()`, `GetForwarding()`, `FindInterfaceForSubnet()`, `NATManager`. 3 new unit tests for netlink message construction.
- **`--accept-routes` opt-in for remote subnet routes** (`internal/config/config.go`, `cmd/bamgate/cmd_up.go`, `internal/agent/agent.go`): Remote peers can advertise LAN subnets (e.g., `192.168.1.0/24`), but accepting those routes can break the local network if the subnets overlap (e.g., both sides on `192.168.1.0/24`). Route acceptance is now opt-in: by default, only the peer's `/32` tunnel address is added to WireGuard AllowedIPs — advertised subnets are ignored. Enable via `bamgate up --accept-routes` or `accept_routes = true` in `[device]` config. When disabled, a log message notes ignored routes. Advertising routes (forwarding + NAT on the server side) is unaffected.
- **`bamgate setup` command** (`cmd/bamgate/cmd_setup.go`): Unified interactive setup wizard. Runs with `sudo`. Two paths: (1) Cloudflare API token — verifies token, detects existing worker or deploys new one with embedded assets, auto-assigns tunnel addresses; (2) Invite code — redeems code from worker, no CF account needed. Generates WireGuard keys, writes config (chowned to real user via `$SUDO_USER`), installs binary + capabilities, optionally installs systemd service.
- **`bamgate invite` command** (`cmd/bamgate/cmd_invite.go`): Creates short-lived invite codes (10 min, single-use) via authenticated POST to worker `/invite` endpoint. Prints code + server URL for the new device.
- **Cloudflare API client** (`internal/deploy/cloudflare.go`): Full REST API v4 client for Workers deployment. Verify token, list accounts, get subdomain, check worker existence, multipart worker upload (ESModule + Wasm + JS), Durable Object bindings + migrations, enable workers.dev route, read worker settings/bindings.
- **Embedded worker assets** (`internal/deploy/assets.go`): Pre-built `worker.mjs` (16KB), `app.wasm` (414KB), `wasm_exec.js` (17KB) embedded via `//go:embed`. Zero external dependencies for deployment — no Node.js, npm, or TinyGo needed on target machine.
- **Worker invite/network-info endpoints** (`worker/src/worker.mjs`): `POST /invite` (authenticated) creates invite with random 8-char code, 10-min expiry, stored in DO SQLite. `GET /invite/{code}` (unauthenticated) redeems invite, returns server URL + auth token + auto-assigned address. `GET /network-info` (authenticated) returns subnet, assigned devices, next available address. SQLite tables: `invites`, `devices`, `network`.
- **Auto tunnel address assignment**: Worker tracks assigned addresses in SQLite `devices` table. Computes next available IP from subnet (default `10.0.0.0/24`). First device gets `.1`, subsequent devices get next free address. Addresses registered on join and on invite redemption.
- **Config `[cloudflare]` section** (`internal/config/config.go`): New `CloudflareConfig` struct with `api_token`, `account_id`, `worker_name`. Stored in TOML config for re-deploy and management operations.
- **TURN relay over WebSocket** (`internal/turn/`, `worker/turn.go`, `worker/stun/`): Full TURN relay implementation on Cloudflare Workers Durable Objects. Enables connectivity through symmetric NAT (e.g., mobile tethering) where direct ICE/STUN hole punching fails. Architecture: client-side WebSocket proxy dialer (`internal/turn/dialer.go`) intercepts pion/ice's TURN TCP connections and routes them over `wss://worker/turn`; server-side TURN state machine (`worker/turn.go`) handles Allocate (two-phase 401 auth dance), Refresh, CreatePermission, ChannelBind, Send/Data indications, and ChannelData relay between peers via virtual relay addresses. Minimal STUN/TURN message parser (`worker/stun/stun.go`, ~500 lines) written from scratch for TinyGo/Wasm compatibility — handles message encoding/decoding, XOR address family, MESSAGE-INTEGRITY (HMAC-SHA1), FINGERPRINT (CRC32). TURN credential generation uses HMAC-SHA1 REST API convention (`internal/turn/credentials.go`): `username=<expiry>:<peerID>`, `password=base64(HMAC-SHA1(secret,username))`. Agent automatically configures TURN when `turn_secret` is present in config — generates credentials, sets up proxy dialer via `webrtc.SettingEngine.SetICEProxyDialer()`, and adds TURN server to ICE config. ICE tries direct candidates first, falls back to TURN relay transparently. Binary WebSocket support added to worker JS shim for STUN/TURN message forwarding (`jsSendBinary`, `goOnTURNMessage`). TURN secret generated during `bamgate setup`, deployed as plain text binding alongside AUTH_TOKEN, and included in invite redemption responses. Embedded worker assets rebuilt (576KB Wasm binary).
- **TURN credential helpers** (`internal/turn/credentials.go`): `GenerateCredentials()` and `ValidateCredentials()` for TURN REST API time-limited credentials with HMAC-SHA1. `DeriveAuthKey()` for RFC 5389 long-term credential MESSAGE-INTEGRITY key derivation. 9 tests.
- **WebSocket proxy dialer** (`internal/turn/dialer.go`): `WSProxyDialer` implementing `proxy.Dialer` — opens WebSocket to `/turn` endpoint, wraps with `websocket.NetConn()`, returns `*net.TCPAddr`-compatible `net.Conn` wrapper (required by pion/ice's forced type assertion). `TURNServerURL()` and `TURNWebSocketURL()` derive TURN URLs from signaling server URL. 13 tests.

## Phase 5 Implementation Details

Six work items, ordered by dependency.

### 5.1 Subcommand Framework (Small)

Restructure `cmd/bamgate/main.go` from a single implicit `up` command to proper subcommand CLI using stdlib `flag` with manual dispatch (no third-party CLI framework).

**Subcommands:** `up`, `init`, `status`, `genkey`

**Changes:**
- `cmd/bamgate/main.go` — subcommand dispatch based on `os.Args[1]`
- Each subcommand in its own file: `cmd_up.go`, `cmd_init.go`, `cmd_status.go`, `cmd_genkey.go`
- Global flags (`--config`, `-v`) parsed before subcommand dispatch
- Help text shows available commands

### 5.2 `bamgate init` (Medium)

Interactive setup wizard that generates a config file.

**Flow:**
1. Check if config already exists — warn and prompt to overwrite
2. Generate WireGuard private key
3. Prompt for: device name (default: hostname), server URL, auth token, tunnel address
4. Write `config.toml` with 0600 permissions
5. Print the derived public key

**Changes:**
- `cmd/bamgate/cmd_init.go` — interactive prompts via `bufio.Scanner`
- Uses existing `config.SaveConfig()` and `config.GeneratePrivateKey()`

### 5.3 Subnet Routing (Medium)

Allow peers to advertise local subnets (e.g. `192.168.1.0/24`) so remote peers can reach home LAN services.

**Config:** Add `routes` to `[device]` section:
```toml
[device]
routes = ["192.168.1.0/24"]
```

**Signaling:** Add `Routes []string` to `JoinMessage` and `PeerInfo` in `pkg/protocol/`. Routes propagate through the Worker DO automatically.

**Agent:** Include peer's advertised routes in AllowedIPs (in addition to `/32` host IP). Validate CIDRs, reject dangerous routes like `0.0.0.0/0`.

**Changes:**
- `internal/config/config.go` — add `Routes []string` to `DeviceConfig`
- `pkg/protocol/protocol.go` — add `Routes` to `JoinMessage` and `PeerInfo`
- `internal/signaling/client.go` — pass `Routes` in join message
- `internal/agent/agent.go` — propagate routes to AllowedIPs
- `worker/hub.go` — ensure routes field passes through
- Tests for route propagation and validation

### 5.4 ICE Restart / Resilience (Medium-Hard)

Currently, ICE failure tears down the peer entirely. Instead, try an ICE restart first.

**Approach:**
- `ICEConnectionStateDisconnected` → start 5s timer, then ICE restart if not recovered
- `ICEConnectionStateFailed` → immediate ICE restart
- ICE restart: `CreateOffer` with ICE restart flag, send new offer via signaling, exchange new answer
- Limit to 3 restart attempts, then tear down (current behavior)

**Changes:**
- `internal/webrtc/peer.go` — add `RestartICE()` method
- `internal/agent/agent.go` — replace immediate `removePeer` with restart-then-remove strategy, per-peer restart counter/timer

### 5.5 `bamgate status` via Unix Socket (Medium)

**Agent side:** Listen on Unix socket at `/run/bamgate/control.sock`. Serve JSON status API.

**Status response:**
```json
{
  "device": "home-server",
  "address": "10.0.0.1/24",
  "server_url": "https://...",
  "uptime_seconds": 3600,
  "peers": [{
    "id": "laptop",
    "address": "10.0.0.2/24",
    "state": "connected",
    "ice_type": "host",
    "routes": ["192.168.1.0/24"],
    "connected_since": "2026-02-12T10:00:00Z"
  }]
}
```

**Changes:**
- New `internal/control/server.go` — Unix socket HTTP server
- `internal/agent/agent.go` — expose `Status()` method, start control server in `Run()`
- `internal/webrtc/peer.go` — expose selected ICE candidate pair info
- `cmd/bamgate/cmd_status.go` — connect to socket, format output as table

### 5.6 Systemd Service (Small)

Systemd unit file for running `bamgate up` as a persistent home agent.

**Changes:**
- `contrib/bamgate.service` — `Type=simple`, `Restart=on-failure`, capability-based hardening (`CAP_NET_ADMIN`, `CAP_NET_RAW`), `ReadWritePaths=/run/bamgate`

### Execution Order

```
5.1 Subcommand framework     ← foundation
5.2 bamgate init             ← standalone after 5.1
5.3 Subnet routing            ← protocol + agent, independent of CLI
5.4 ICE restart / resilience  ← agent-only, independent of CLI
5.5 bamgate status           ← needs control socket + agent state
5.6 Systemd service           ← just a file, goes last
```

### Files Affected

| Area | New files | Modified files |
|------|-----------|----------------|
| CLI | `cmd/bamgate/cmd_up.go`, `cmd_init.go`, `cmd_status.go`, `cmd_genkey.go` | `cmd/bamgate/main.go` |
| Control | `internal/control/server.go` | — |
| Config | — | `internal/config/config.go` |
| Protocol | — | `pkg/protocol/protocol.go` |
| Signaling | — | `internal/signaling/client.go` |
| WebRTC | — | `internal/webrtc/peer.go` |
| Agent | — | `internal/agent/agent.go` |
| Worker | — | `worker/hub.go` |
| Systemd | `contrib/bamgate.service` | — |

## What's Next

1. **Deploy and test TURN relay** — Redeploy the Cloudflare Worker with TURN support, set `TURN_SECRET`, and test end-to-end with phone tethering (symmetric NAT). Verify `bamgate status` shows `ice_type: relay`.
2. **macOS support — launchd integration** — `up -d`, `down`, and `install --systemd` equivalents for macOS using launchd plists. See remaining items below.
3. **Rate limiting** — Add request rate limiting to the Worker `/connect` and `/turn` endpoints to prevent abuse.
4. **Android client** — gomobile build of the core library for Android.
5. **End-to-end testing with systemd** — Verify the full `install --systemd` → `up -d` → `status` → `down` workflow on a fresh machine.
6. **macOS end-to-end testing** — Test `sudo bamgate setup` → `sudo bamgate up` → `bamgate status` on a real Mac.

### macOS Support Status

**Goal:** Full macOS (darwin) support for the bamgate CLI.

**Phase 1 (DONE):** Core compilation and foreground operation. `GOOS=darwin go build` succeeds. `sudo bamgate up` runs in foreground on macOS.

#### Completed

- **Network interface management** (`internal/tunnel/netlink_darwin.go`): All 6 functions implemented using shell commands (`ifconfig`, `route`, `sysctl`). `netlink.go` gated with `//go:build linux`. Cross-platform `FindInterfaceForSubnet` extracted to `iface.go`.
- **NAT masquerade** (`internal/tunnel/nat_darwin.go`): `NATManager` implemented using macOS PF (`pfctl`) with a `com.bamgate` anchor. `nat.go` gated with `//go:build linux`.
- **TUN device naming**: `DefaultTUNName` is `"bamgate0"` on Linux (`tun_linux.go`), `"utun"` on macOS (`tun_darwin.go`). Agent uses `tunnel.DefaultTUNName` instead of hardcoded name.
- **Control socket path**: `ResolveSocketPath()` uses `/var/run/bamgate/` on macOS, `/run/bamgate/` on Linux.
- **CLI commands**: `setup`, `install`, `up` (foreground) work on macOS. `up -d`, `down`, and `install --systemd` give clear "not yet implemented" messages on macOS.

#### Remaining — launchd integration

| Feature | Status |
|---------|--------|
| `bamgate install` on macOS — write launchd plist to `/Library/LaunchDaemons/` | Not started |
| `bamgate up -d` on macOS — `launchctl bootstrap system <plist>` | Not started |
| `bamgate down` on macOS — `launchctl bootout system/com.bamgate` | Not started |
| Optional: BSD ioctl/routing sockets (replace shell commands) | Not started |

## Testing

See [docs/testing-lan.md](docs/testing-lan.md) for the LAN testing guide.

- **Phase 2 LAN test** (2026-02-10): Two Linux machines, ICE host candidates, WebRTC data channel, WireGuard handshake, bidirectional ping — all working.
- **Phase 3 internet test** (2026-02-10): Three peers (2 home LAN + 1 DigitalOcean droplet) connected via Cloudflare Worker signaling. All peers can ping each other. ~2ms latency on LAN path, internet path varies by region.

## Code Status

| Package | Files | Status |
|---------|-------|--------|
| `cmd/bamgate` | main.go, cmd_up.go, cmd_down.go, cmd_setup.go, cmd_invite.go, cmd_init.go, cmd_init_test.go, cmd_status.go, cmd_genkey.go, cmd_install.go | **Implemented + tested** — Cobra subcommands: setup, up, down, invite, status, genkey, install (init deprecated) |
| `cmd/bamgate-hub` | main.go | **Implemented** — standalone signaling server |
| `internal/agent` | agent.go, agent_test.go | **Implemented + tested** — orchestrator with ICE restart, subnet routing, forwarding/NAT, control server, TURN relay integration |
| `internal/control` | server.go, server_test.go | **Implemented + tested** — Unix socket status API |
| `internal/bridge` | bridge.go, bridge_test.go | **Implemented + tested** |
| `internal/config` | config.go, keys.go, config_test.go, keys_test.go | **Implemented + tested** — Added CloudflareConfig section |
| `internal/signaling` | client.go, hub.go, client_test.go | **Implemented + tested** |
| `pkg/protocol` | protocol.go, protocol_test.go | **Implemented + tested** |
| `internal/tunnel` | config.go, device.go, tun.go, tun_linux.go, tun_darwin.go, iface.go, netlink.go (linux), netlink_darwin.go, nat.go (linux), nat_darwin.go, config_test.go, netlink_test.go | **Implemented + tested** — Cross-platform: Linux (netlink + nftables), macOS (ifconfig/route/sysctl + pfctl) |
| `internal/turn` | credentials.go, credentials_test.go, dialer.go, dialer_test.go | **Implemented + tested** — TURN credential generation/validation, WebSocket proxy dialer for pion/ice |
| `internal/webrtc` | ice.go, datachan.go, peer.go, peer_test.go | **Implemented + tested** — Added optional `webrtc.API` support for proxy dialer |
| `internal/deploy` | cloudflare.go, assets.go, assets/ | **Implemented** — Cloudflare API client, embedded worker assets. TURN_SECRET binding support. |
| `worker/` | hub.go, turn.go, main.go, src/worker.mjs | **Implemented** — TinyGo 576KB Wasm, Cloudflare Workers free tier. Signaling + TURN relay + invite + network-info endpoints. |
| `worker/stun` | stun.go, stun_test.go | **Implemented + tested** — Minimal STUN/TURN message parser/builder (TinyGo-compatible). 20 tests. |

## Dependencies

| Library | Version | Purpose |
|---------|---------|---------|
| `github.com/coder/websocket` | v1.8.14 | WebSocket client/server for signaling |
| `github.com/pion/webrtc/v4` | v4.2.3 | WebRTC stack (ICE, DTLS, SCTP, data channels) |
| `github.com/BurntSushi/toml` | v1.6.0 | TOML config file parsing |
| `golang.org/x/crypto` | v0.37.0 | Curve25519 key derivation (WireGuard keys) |
| `github.com/spf13/cobra` | v1.10.2 | CLI subcommand framework |
| `github.com/google/nftables` | v0.3.0 | Pure Go nftables (netlink-based) for NAT masquerade rules |
| `golang.org/x/sys` | v0.41.0 | Netlink syscalls for TUN configuration, IP forwarding (non-root) |
| `golang.zx2c4.com/wireguard` | v0.0.0-20250521 | Userspace WireGuard device + TUN interface |

## Releases

| Version | Date | Highlights |
|---------|------|------------|
| v1.5.2 | 2026-02-12 | Fix systemd 203/EXEC on Homebrew installs (SELinux user_home_t) |
| v1.5.1 | 2026-02-12 | Document symlink step for Linux Homebrew users |
| v1.5.0 | 2026-02-12 | Remove install command, consolidate into setup with --force, project logo |
| v1.4.0 | 2026-02-12 | Add MIT license, Homebrew tap support, transfer to bamgate org |
| v1.3.0 | 2026-02-12 | Rename project from riftgate to bamgate (밤gate — "night gate") |
| v1.2.2 | 2026-02-12 | Fix self-peer appearing in status after DO hibernation/reconnection |
| v1.2.1 | 2026-02-12 | Fix ICE restart offer storm (glare resolution), add `bamgate version` command |
| v1.2.0 | 2026-02-12 | TURN relay over WebSocket for symmetric NAT traversal |
| v1.1.1 | 2026-02-12 | Fix macOS TUN routing — add subnet route after address assignment |
| v1.1.0 | 2026-02-12 | macOS (darwin) support for amd64 + arm64 |
| v1.0.0 | 2026-02-12 | First major release — automated setup, daemon mode, subnet routing with forwarding/NAT |
| v0.3.0 | 2026-02-11 | Fix AllowedIPs routing, per-peer /32 addresses via signaling |
| v0.2.0 | 2026-02-11 | End-to-end LAN tunnel verified, 3 critical bug fixes |
| v0.1.0 | 2026-02-11 | Initial pre-release — signaling + WebRTC + WireGuard integration |

## Open Questions / Decisions

- None at this time.

## Changelog

- **2026-02-12 (session 10)**: Fix systemd service failing with `status=203/EXEC` on Homebrew-installed binaries. Root cause: Homebrew Cask installs the binary under `/home/linuxbrew/.linuxbrew/Caskroom/...`, and SELinux labels files under `/home` as `user_home_t` — systemd services are denied execution of `user_home_t` binaries. Fix: `installSystemdService()` now detects when the resolved binary path is under `/home/` and copies it to `/usr/local/bin/bamgate` (which receives the correct `bin_t` SELinux label automatically), sets capabilities on the copy, and uses the system path in `ExecStart=`. Non-Homebrew installs are unaffected. Added `copyBinary()` helper for streaming file copy with permissions.
- **2026-02-12 (session 9)**: Remove `install` command — `setup` now handles everything. When config already exists, `sudo bamgate setup` re-applies Linux capabilities and updates the systemd service path (handles `brew upgrade` gracefully). Added `--force` flag to redo full setup. Platform-aware TUN error messages guide users to `sudo bamgate setup` (Linux) or `sudo bamgate up` (macOS). No longer copies binary to `/usr/local/bin` — works with Homebrew-managed binaries in-place. Fixed auth token prefix `rg_` → `bg_`. Updated systemd service docs URL to `bamgate/bamgate`. Added project logo (SVG + PNG) to README. Add MIT license, Homebrew tap via GoReleaser, and GitHub App-based token for cross-repo formula publishing. Project transferred from `kuuji/bamgate` to `bamgate` GitHub org. GoReleaser `homebrew_casks` section auto-publishes cask to `bamgate/homebrew-tap` on release. Release workflow uses `actions/create-github-app-token` to generate ephemeral tokens (no PAT expiry). Modernized `.goreleaser.yaml`: migrated deprecated `brews` to `homebrew_casks`, `format` to `formats`, split archives by build ID so cask filtering works, added macOS quarantine removal hook for unsigned binaries. Users can now install via `brew install bamgate/tap/bamgate`.
- **2026-02-12 (session 8)**: Rename project from riftgate to bamgate (밤gate — "night gate", Korean 밤 = night). Full codebase rename across 49 files (318 lines): Go module path, import paths, binary names, CLI commands, string constants (data channel label, TURN realm, TUN interface name, nftables table, PF anchor), config paths (~/.config/bamgate/), control socket paths (/run/bamgate/), HTTP headers (X-Bamgate-*), systemd service, worker config, documentation. Directory renames: cmd/riftgate → cmd/bamgate, cmd/riftgate-hub → cmd/bamgate-hub, contrib/riftgate.service → contrib/bamgate.service. All tests pass, both binaries build cleanly.
- **2026-02-12 (session 7)**: Fix ICE restart offer storm; fix self-peer showing as "initializing" in `bamgate status` after DO hibernation/rehydration or signaling reconnection (agent now skips its own peer ID in the peers list). When both peers detected ICE failure simultaneously (common with symmetric NAT / phone tethering), both sent ICE restart offers. The incoming offer replaced the existing PeerConnection, destroying a connection that had just recovered — causing an infinite restart loop. Three fixes: (1) reuse existing PeerConnection for incoming offers instead of creating a new one; (2) glare resolution using lexicographic peer ID tiebreaker — the preferred offerer ignores competing offers; (3) `pendingRestart` flag tracks outgoing restart offers to prevent duplicates. Also added `bamgate version` command with build-time version injection via ldflags (GoReleaser sets it from the git tag, local builds show `dev`).
- **2026-02-12 (session 6)**: Phase 4 — TURN relay on Cloudflare Workers. Full TURN-over-WebSocket implementation enabling connectivity through symmetric NAT (mobile tethering, restrictive corporate firewalls). Client-side: WebSocket proxy dialer (`internal/turn/dialer.go`) hooks into pion/ice via `SettingEngine.SetICEProxyDialer()` — ICE automatically tries direct candidates first and falls back to TURN relay transparently. TURN credential generation uses HMAC-SHA1 REST API convention with time-limited credentials (`internal/turn/credentials.go`). Agent automatically configures TURN when `turn_secret` is in config. Server-side: TURN state machine in Go/Wasm (`worker/turn.go`) handles the full Allocate two-phase auth dance (401 challenge → authenticated retry), Refresh, CreatePermission, ChannelBind, Send/Data indications, and ChannelData fast-path relay. Virtual relay addressing — the DO assigns synthetic relay addresses and routes packets between peers' WebSocket connections. Minimal STUN message parser (`worker/stun/stun.go`, ~500 lines) written from scratch for TinyGo compatibility — handles message encoding/decoding, XOR address family, MESSAGE-INTEGRITY (HMAC-SHA1), FINGERPRINT (CRC32). Worker JS shim updated with binary WebSocket support (`jsSendBinary`, `goOnTURNMessage`) and `/turn` endpoint. TURN secret generation integrated into `bamgate setup` and invite flow. Wasm binary grew from 414KB to 576KB. All existing tests pass; 42 new tests across credentials (9), dialer (13), and STUN parser (20).
- **2026-02-12 (session 5)**: macOS (darwin) support — phase 1. The project now compiles and runs on macOS (`GOOS=darwin`). Split all Linux-specific code into platform files with `//go:build` tags: `netlink.go` → Linux netlink syscalls, `netlink_darwin.go` → `ifconfig`/`route`/`sysctl` shell commands for interface management. `nat.go` → Linux nftables, `nat_darwin.go` → macOS PF (`pfctl`) with `com.bamgate` anchor for NAT masquerade. Extracted cross-platform `FindInterfaceForSubnet()` to `iface.go`. Added `tun_linux.go` (`DefaultTUNName = "bamgate0"`) and `tun_darwin.go` (`DefaultTUNName = "utun"` — kernel auto-assigns). Agent uses `tunnel.DefaultTUNName` instead of hardcoded name. Control socket path uses `/var/run/bamgate/` on macOS. CLI commands (`setup`, `install`, `up` foreground) work on macOS with clear messages for not-yet-implemented features (`up -d`, `down` — launchd integration pending). All existing Linux tests pass. Cross-compiles cleanly for `darwin/amd64` and `darwin/arm64`.
- **2026-02-12 (session 4)**: Automatic IP forwarding and NAT for advertised subnet routes. Added `--accept-routes` opt-in flag — remote subnet routes are no longer installed by default to prevent conflicts when both sides share the same LAN subnet. When a device advertises routes (e.g., `192.168.1.0/24`), remote peers could reach the device itself but not other hosts on the advertised subnet — packets were dropped because Linux doesn't forward between interfaces by default and LAN devices couldn't reply without NAT. Bamgate now automatically enables per-interface IPv4 forwarding via netlink `RTM_SETLINK` with `IFLA_AF_SPEC` > `IFLA_INET_CONF` (avoids `/proc/sys` writes that require `CAP_DAC_OVERRIDE` or root) and sets up nftables MASQUERADE rules using `github.com/google/nftables` (pure Go netlink library, no iptables/nft binaries needed) in a dedicated `bamgate` nftables table. `FindInterfaceForSubnet()` auto-detects the outgoing LAN interface. Previous forwarding state is saved and restored on shutdown. No new capabilities needed — existing `CAP_NET_ADMIN` covers both netlink forwarding and nftables.
- **2026-02-12 (session 3)**: Automated setup & deployment. New `bamgate setup` command — single interactive wizard that deploys the Cloudflare Worker signaling server, configures the device, and installs the binary with capabilities. Two setup paths: (1) Cloudflare API token — deploys worker on first run, detects existing worker and retrieves config on subsequent runs; (2) invite code — no CF account needed on second device. New `bamgate invite` command generates short-lived invite codes (10 min, single-use) for adding devices to the network. Worker updated with `/invite` (create/redeem) and `/network-info` endpoints using Durable Object SQLite for invite storage and automatic tunnel address assignment. Auth token stored as plain text binding (readable via CF API). Worker assets embedded in Go binary via `//go:embed` (~438KB). New `internal/deploy/` package with Cloudflare REST API v4 client (token verify, accounts, subdomain, worker upload with multipart modules + DO bindings + migrations, worker settings). Config extended with `[cloudflare]` section (api_token, account_id, worker_name). `bamgate init` deprecated in favor of `setup`.
- **2026-02-12 (session 2)**: UX & operational improvements. Fixed `bamgate init` URL scheme bug (auto-prepend `wss://` for bare hostnames). Added `bamgate up -d` daemon mode (systemctl enable + start) and `bamgate down` (systemctl disable + stop). Fixed systemd service to run as the installing user instead of root — uses `$SUDO_USER` to resolve the real user and sets `User=`/`Group=` in the service file; capabilities granted via `AmbientCapabilities`. Neither `up -d` nor `down` require the user to prefix with sudo (they invoke `sudo systemctl` internally). Non-root TUN configuration via raw netlink syscalls (replaces `exec ip`). Smart control socket path resolution. `bamgate install` command with `--systemd` flag. Redeployed Cloudflare Worker with subnet routing support.
- **2026-02-12**: Phase 5 complete — CLI polish & resilience. Implemented all 6 work items: Cobra subcommand framework (up/init/status/genkey), interactive `bamgate init` wizard, subnet routing (config + protocol + agent + worker), ICE restart resilience (grace period + 3 retries), `bamgate status` via Unix socket control server, systemd service file. Fixed pre-existing WebRTC test race. All tests pass with `-race`.
- **2026-02-10**: Fixed AllowedIPs routing bug — exchanged tunnel addresses via signaling, use per-peer `/32` AllowedIPs instead of `0.0.0.0/0`. All 3 peers can now ping each other simultaneously. Released v0.3.0.
- **2026-02-10**: Phase 3 complete — deployed Cloudflare Worker signaling server. Tested with 3 peers across the internet. Fixed DO hibernation state loss with rehydration from WebSocket attachments. Added bearer token auth.
- **2026-02-10**: Phase 3 implementation — Cloudflare Worker signaling server. Extracted protocol types to shared `pkg/protocol/` package (TinyGo-compatible). Scaffolded `worker/` with Go/Wasm Durable Object: JS shell with WebSocket Hibernation API bridges to Go hub logic via `syscall/js`. Added bearer token auth to signaling client and agent. Built with TinyGo (408KB Wasm binary). Tested full signaling flow locally with `wrangler dev`: peer join, peers list, offer/answer relay, ICE candidate relay, peer-left notification — all working. Key finding: `no_bundle = true` + `find_additional_modules = true` required to avoid esbuild bundling `wasm_exec.js` (which causes infinite recursion in Workers runtime).
- **2026-02-10**: End-to-end LAN tunnel verified. Fixed three bugs blocking the tunnel: hub not broadcasting join events to existing peers, offer/answer messages missing WireGuard public keys, and bridge Bind receive loop dying after wireguard-go's BindUpdate cycle. Added `Endpoint` field to `PeerConfig` for Bind routing. Added `PublicKey` field to `OfferMessage`/`AnswerMessage`. Added LAN testing guide (`docs/testing-lan.md`). Released v0.2.0.
- **2026-02-10**: Phase 2 complete — WireGuard integration. Implemented tunnel package (TUN creation, UAPI config, device lifecycle), bridge package (custom `conn.Bind` over WebRTC data channels), agent orchestrator, CLI `bamgate up` command, standalone signaling hub binary (`bamgate-hub`). Extracted signaling Hub from tests for reuse. 35 tests across all packages, all passing with `-race`.
- **2026-02-09**: Implemented config package — TOML config management (load/save with BurntSushi/toml, XDG path support, 0600 permissions) and WireGuard key generation (Curve25519 via crypto/curve25519, RFC 7748 clamping). Phase 1 complete.
- **2026-02-09**: Implemented WebRTC peer connection manager with ICE configuration, unreliable/unordered data channels, SDP offer/answer exchange, and integration tests.
- **2026-02-09**: Implemented signaling protocol types and WebSocket client with full test suite.
- **2026-02-09**: Project bootstrapped — architecture doc, scaffolding, README, coding guidelines.
