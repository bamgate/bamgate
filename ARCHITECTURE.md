# WireGuard Tunnel over WebRTC — Project Plan

## Overview

A lightweight, self-hosted WireGuard-based VPN tool that allows a single user to securely access their home network from anywhere — without ever exposing the home network's public IP to the internet. The system uses **WebRTC data channels** as the transport layer for NAT traversal and connectivity, with **Cloudflare Workers** providing signaling, auth, and a custom TURN-like relay fallback. No VPS required. The entire infrastructure runs on Cloudflare's free tier.

This is also a learning project — a deep dive into WebRTC internals, NAT traversal, and building real infrastructure on Cloudflare Workers.

## Goals

- **Zero exposed ports**: The home network never opens any inbound ports. All connections are outbound.
- **No VPS required**: All relay/coordination infrastructure runs on Cloudflare's free tier.
- **WireGuard-based**: All traffic between peers is encrypted via WireGuard. Every relay layer only ever sees opaque encrypted blobs.
- **WebRTC transport**: Use WebRTC data channels for connectivity. ICE handles NAT traversal automatically — direct when possible, relayed when not.
- **Self-hosted TURN**: A custom TURN-like relay built on Cloudflare Workers + Durable Objects, so there's no dependency on third-party TURN providers.
- **Simple to deploy on new networks**: A single CLI command should join a new device/network to the mesh.
- **Cross-platform**: Linux CLI client and Android app, sharing a common Go core library.
- **Shareable**: Easy for other people to fork and deploy on their own Cloudflare account.

## How WebRTC Fits In

### Why WebRTC?

Instead of building custom STUN discovery, custom UDP hole punching, and a custom relay protocol from scratch, we use WebRTC which bundles all of this into one stack:

| Custom approach (before) | WebRTC approach (now) |
|---|---|
| Manual STUN queries | ICE agent does this automatically |
| Custom hole punch logic | ICE handles candidate pairing and connectivity checks |
| Custom WebSocket relay | TURN relay (we build our own on CF Workers) |
| Custom UDP-over-WebSocket framing | WebRTC data channel handles framing |
| Custom signaling protocol | Still needed — SDP offer/answer exchange via CF Worker |

### How WebRTC Data Channels Work (Quick Primer)

WebRTC was designed for browser-to-browser communication (video calls, etc.), but the **data channel** feature is a generic bidirectional pipe for arbitrary data — perfect for tunneling WireGuard packets.

Under the hood, a data channel uses:
- **ICE**: Discovers all possible network paths (local, server-reflexive via STUN, relay via TURN) and picks the best one
- **DTLS**: Encrypts the transport (on top of WireGuard's own encryption — double encrypted, but that's fine)
- **SCTP**: Provides reliable or unreliable delivery over the DTLS connection

For our purposes, we want **unreliable, unordered** data channels — mimicking raw UDP behavior. This avoids head-of-line blocking that would occur with reliable/ordered delivery. WireGuard handles its own reliability at a higher layer.

### Connection Flow with WebRTC

```
1. Peer A connects to CF Worker via WebSocket (signaling)
2. Peer A creates an RTCPeerConnection with:
   - ICE servers: [public STUN servers, our CF TURN server]
   - A data channel configured as unreliable/unordered
3. Peer A creates an SDP offer and sends it to CF Worker
4. CF Worker forwards the offer to Peer B (via its WebSocket)
5. Peer B creates an RTCPeerConnection, sets the remote offer, creates an SDP answer
6. Peer B sends the answer back through CF Worker to Peer A
7. Both peers trickle ICE candidates through the CF Worker signaling channel
8. ICE connectivity checks happen automatically:
   a. Direct connection via STUN (hole punch) — tried first
   b. TURN relay via our CF Worker — fallback
9. Data channel opens → start forwarding WireGuard packets through it
10. WireGuard interface on both sides sees a working tunnel
```

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                      Cloudflare Edge                             │
│                                                                  │
│  ┌────────────────┐    ┌────────────────────────────────────┐    │
│  │  Worker         │───▶│  Durable Object (per user/network) │    │
│  │  - Auth         │    │  - WebSocket signaling hub         │    │
│  │  - Routing      │    │  - SDP offer/answer relay          │    │
│  │  - TURN API     │    │  - ICE candidate relay             │    │
│  └────────────────┘    │  - TURN relay for data channel      │    │
│                        │    traffic when direct fails         │    │
│                        └────────────────────────────────────┘    │
└────────────────┬─────────────────────────┬───────────────────────┘
                 │ WSS (signaling)          │ WSS (signaling)
                 │ TURN (relay fallback)    │ TURN (relay fallback)
                 ▼                          ▼
         ┌──────────────┐          ┌──────────────┐
         │  Home Agent   │          │ Remote Client │
         │  RTCPeerConn  │          │ RTCPeerConn   │
         │  + WireGuard  │          │  + WireGuard  │
         └──────┬───────┘          └──────┬───────┘
                │                         │
                │  Direct UDP (STUN)      │
                │◄───────────────────────►│
                │  (when NAT allows it)   │
                │                         │
         ┌──────┴───────┐          ┌──────┴───────┐
         │  WireGuard    │          │  WireGuard    │
         │  TUN iface    │          │  TUN iface    │
         └──────────────┘          └──────────────┘
```

### Data Flow (Two Modes)

**Direct (hole punch succeeded via ICE):**
```
App on phone → WireGuard TUN → encrypted packet → WebRTC data channel → UDP → Home WireGuard → Home LAN
```

**Relayed (hole punch failed, using TURN on CF Worker):**
```
App on phone → WireGuard TUN → encrypted packet → WebRTC data channel → TURN (CF DO) → WebRTC data channel → Home WireGuard → Home LAN
```

In both cases, the application code is identical — WebRTC/ICE transparently picks the best path.

## Technology Choices

### Cloudflare Workers + Durable Objects

**Role**: Signaling server + TURN relay

- **Worker**: Handles HTTP auth, WebSocket upgrade, routes to correct Durable Object.
- **Durable Object**: One per user/network. Maintains WebSocket connections from all peers. Relays SDP offers/answers and ICE candidates between peers. Also implements a TURN-like relay for data channel traffic when direct connection fails.
- **Language**: Go, compiled to WebAssembly via **TinyGo**. TinyGo produces small Wasm binaries (~200KB–1MB compressed) that fit comfortably within Cloudflare's free-tier 3MB limit. Standard Go's `GOOS=js GOARCH=wasm` output is too large (2–4MB compressed for even a hello world). TinyGo lacks goroutine support in Wasm, but this is not an issue — CF Workers and Durable Objects are event-driven (one event handler runs at a time), so no concurrency primitives are needed on the server side. A custom JavaScript shim (~180 lines) exports the Worker `fetch` handler and `SignalingRoom` Durable Object class, bridging WebSocket events to Go/Wasm callbacks via `syscall/js`. The Go hub logic manages peer state and message routing; JS handles the Cloudflare API surface (WebSocket Hibernation, `acceptWebSocket`, `getWebSockets`, `serializeAttachment`).
- **Deployment**: Via Wrangler CLI or automated through the tool's `init` command using the CF API.

### Custom TURN-like Relay on Durable Objects

We build a simplified TURN relay rather than running a full `coturn` server. This avoids needing a VPS entirely.

**How it works:**
- The TURN relay is integrated into the same Durable Object that handles signaling.
- When ICE determines that direct connectivity isn't possible, it falls back to our TURN server.
- The relay accepts connections over WebSocket (TURN-over-WebSocket / TURN-over-TLS on port 443).
- It allocates relay addresses and forwards packets between peers.
- This works through any firewall since it's all HTTPS/WSS on port 443.

**Implementation approach:**
- Implement a subset of the TURN protocol (RFC 5766) — specifically the Allocate, CreatePermission, ChannelBind, and Send/Data operations.
- Alternatively, skip TURN protocol compliance entirely and implement a custom relay protocol that `pion/ice` can be configured to use. The `pion` library supports custom ICE transport mechanisms.
- A pragmatic middle ground: implement just enough TURN to satisfy `pion/webrtc`'s ICE agent. The `pion/turn` package has a server implementation that could be referenced or adapted to run on Durable Objects.

**TURN auth:**
- TURN uses time-limited credentials derived from a shared secret (RFC 5389 long-term credentials).
- The Worker generates temporary TURN credentials when a peer connects and includes them in the ICE server configuration.
- The Durable Object validates these credentials when a TURN allocation is requested.

### Go (Client)

**Why Go:**
- `pion/webrtc` — Full, pure-Go WebRTC implementation. This is the core of the project. Handles ICE, DTLS, SCTP, data channels, everything.
- `golang.zx2c4.com/wireguard` — Userspace WireGuard (same code as the official WireGuard Android/iOS apps).
- `gomobile` — Compile Go to Android AAR for the mobile app.
- Single static binary for Linux.

**Key libraries (client — standard Go):**
- `github.com/pion/webrtc/v4` — WebRTC stack (ICE, DTLS, SCTP, data channels)
- `github.com/pion/turn/v4` — TURN client/server (reference for our CF TURN implementation)
- `golang.zx2c4.com/wireguard` — Userspace WireGuard
- `golang.zx2c4.com/wireguard/tun` — TUN device management
- `nhooyr.io/websocket` — WebSocket client for signaling connection

**Key libraries (worker — TinyGo→Wasm):**
- `syscall/js` — Go/Wasm bridge for JavaScript interop (stdlib, no external dependency)
- Custom JS shim — Worker `fetch` handler, `SignalingRoom` DO class with WebSocket Hibernation API

### STUN (NAT Discovery)

- Used by ICE internally — we configure public STUN servers in the RTCPeerConnection config.
- Hardcode defaults: `stun:stun.cloudflare.com:3478`, `stun:stun.l.google.com:19302`
- No need to self-host. These are free, stateless, and widely available.

### WireGuard (Data Plane)

- All actual application traffic encryption.
- Runs as a userspace TUN interface via `wireguard-go`.
- WireGuard packets are fed into the WebRTC data channel as opaque binary blobs.
- WireGuard doesn't know or care whether the data channel is running over a direct UDP connection or a TURN relay.

## Component Breakdown

### Component 1: Cloudflare Worker (Auth + Routing)

**Language**: Go (compiled to WebAssembly via TinyGo)

**Responsibilities:**
- Authenticate users via GitHub OAuth Device Authorization Grant
- Register devices with auto-assigned tunnel addresses
- Issue and validate JWT access tokens (HMAC-SHA256, signed in JS via Web Crypto API)
- Manage refresh token rotation (30-day rolling window)
- Upgrade authenticated WebSocket connections and route to the correct Durable Object
- Generate temporary TURN credentials for authenticated peers
- Serve TURN relay endpoint (the Durable Object acts as the TURN server)
- Device management (list, revoke)

**Endpoints:**
```
POST   /auth/register     — Exchange GitHub token for device credentials + refresh token
POST   /auth/refresh      — Exchange refresh token for new JWT + rotated refresh token
GET    /auth/devices      — List registered devices (JWT required)
DELETE /auth/devices/:id  — Revoke a device (JWT required)
GET    /connect           — WebSocket upgrade → routed to Durable Object (JWT required)
GET    /turn              — TURN-over-WebSocket endpoint → routed to Durable Object (JWT required)
```

**TURN credential generation:**
```go
// generateTURNCredentials creates time-limited TURN credentials from a shared secret.
func generateTURNCredentials(secret, peerID string) (username, credential string) {
	expiry := time.Now().Unix() + 86400 // 24hr expiry
	username = fmt.Sprintf("%d:%s", expiry, peerID)
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	credential = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return username, credential
}
```

### Component 2: Durable Object (Signaling Hub + TURN Relay)

**Language**: Go (compiled to WebAssembly via TinyGo, same build as the Worker)

**Two roles in one Durable Object:**

**Role A — Signaling Hub:**
- Maintains WebSocket connections from all peers in a network
- Relays WebRTC signaling messages between peers:
  - SDP offers and answers
  - ICE candidates (trickle ICE)
- Tracks peer presence (connected/disconnected)
- Broadcasts peer join/leave events

**Signaling protocol (JSON over WebSocket):**
```json
// Peer announces itself
{ "type": "join", "peerId": "home-server", "publicKey": "base64..." }

// SDP offer from peer A to peer B
{ "type": "offer", "from": "laptop", "to": "home-server", "sdp": "v=0\r\n..." }

// SDP answer from peer B to peer A
{ "type": "answer", "from": "home-server", "to": "laptop", "sdp": "v=0\r\n..." }

// ICE candidate trickle
{ "type": "ice-candidate", "from": "laptop", "to": "home-server", "candidate": "candidate:..." }

// Peer list (sent to newly connected peer)
{ "type": "peers", "peers": [{ "peerId": "home-server", "publicKey": "base64..." }] }

// Peer disconnected
{ "type": "peer-left", "peerId": "home-server" }
```

**Role B — TURN Relay:**
- Accepts TURN-over-WebSocket connections from ICE agents
- Implements enough of the TURN protocol for `pion/webrtc`'s ICE to use it as a relay candidate
- Allocates relay transport addresses (virtual, within the DO's context)
- Forwards packets between peers who both have TURN allocations
- Validates time-limited credentials generated by the Worker

**TURN implementation notes:**
- The TURN "server" doesn't need real UDP sockets — it's all virtual within the Durable Object.
- When Peer A sends data to Peer B via TURN, the DO receives it on A's WebSocket and forwards it out B's WebSocket.
- This is conceptually identical to the DERP relay from the original plan, but speaking enough of the TURN protocol that standard WebRTC ICE agents can use it.
- The `pion/turn` package's server implementation can serve as a reference for the message parsing and state machine, adapted to run on Durable Objects with WebSocket transport instead of UDP sockets.

### Component 3: Go Core Library

Shared between Linux CLI and Android app.

**Package structure:**
```
internal/
├── config/          # Config file management
│   ├── config.go    # Load/save TOML config
│   └── keys.go      # WireGuard key generation and storage
│
├── signaling/       # WebSocket client to CF Worker
│   ├── client.go    # Connect, send/receive signaling messages
│   └── protocol.go  # Message types (offer, answer, ice-candidate, etc.)
│
├── webrtc/          # WebRTC peer connection management
│   ├── peer.go      # Create RTCPeerConnection, configure ICE servers
│   ├── datachan.go  # Data channel setup (unreliable, unordered)
│   └── ice.go       # ICE configuration (STUN servers + our TURN server)
│
├── tunnel/          # WireGuard interface management
│   ├── device.go    # wireguard-go device setup
│   ├── tun.go       # TUN interface creation
│   └── config.go    # WireGuard peer/endpoint configuration
│
├── bridge/          # Connects WireGuard to WebRTC
│   └── bridge.go    # Read from TUN → send on data channel, and vice versa
│
└── agent/           # Top-level orchestrator
    └── agent.go     # Ties everything together
```

**Bridge — the critical glue:**

The bridge package connects the WireGuard TUN interface to the WebRTC data channel:

```
WireGuard TUN interface ←→ Bridge ←→ WebRTC Data Channel ←→ Remote Peer
```

- Reads outbound packets from the TUN device, sends them on the data channel.
- Receives inbound packets from the data channel, writes them to the TUN device.
- The data channel must be configured as **unreliable and unordered** (`ordered: false`, `maxRetransmits: 0`) to mimic UDP behavior and avoid head-of-line blocking.

**Agent orchestration flow:**
```
1. Load config
2. Connect to CF Worker signaling WebSocket
3. Send "join" message, receive peer list
4. For each peer:
   a. Create RTCPeerConnection with ICE config:
      - STUN servers (public)
      - TURN server (our CF Worker, with generated credentials)
   b. Create data channel (unreliable, unordered)
   c. Create SDP offer, send via signaling
   d. Receive SDP answer via signaling
   e. Exchange ICE candidates via signaling
   f. Wait for data channel to open
   g. Start bridge: TUN ←→ data channel
5. Configure WireGuard interface with peer public keys and allowed IPs
6. Handle peer join/leave events — create/tear down connections dynamically
7. Monitor ICE connection state — log whether connection is direct or relayed
```

### Component 4: Linux CLI

**Binary name**: `bamgate`

**Commands:**
```bash
# First-time setup
# - Authenticates via GitHub Device Authorization Grant
# - For network owner: prompts for Cloudflare API token, deploys Worker
# - For joining device: authenticates with existing Worker URL
# - Registers device, receives JWT + refresh token + tunnel address
# - Generates WireGuard key pair
# - Writes config to /etc/bamgate/config.toml
sudo bamgate setup

# Connect to the network
# - Refreshes JWT via refresh token
# - Starts WireGuard interface
# - Connects to signaling server (JWT in Authorization header)
# - Establishes WebRTC connections to online peers
# - Bridges TUN ←→ data channels
# - Background JWT refresh loop (every 50 min)
sudo bamgate up

# Disconnect
sudo bamgate down

# Show connection status
bamgate status

# Device management
bamgate devices list           # List all registered devices
bamgate devices revoke <id>    # Revoke a device (prevents JWT refresh)
```

**Config file** (`/etc/bamgate/config.toml`):
```toml
[network]
name = "my-network"
server_url = "wss://bamgate-<id>.workers.dev/connect"
turn_secret = "bg_..."        # Auto-assigned by server during registration
device_id = "uuid-..."        # Assigned by server during registration
refresh_token = "hex-..."     # Rolling 30-day token, rotated on every refresh

[device]
name = "home-server"
private_key = "base64..."
address = "10.0.0.1/24"       # Auto-assigned by server

[stun]
servers = [
  "stun:stun.cloudflare.com:3478",
  "stun:stun.l.google.com:19302",
]

[webrtc]
ordered = false
max_retransmits = 0
```

### Component 5: Android App

**UI Framework**: Jetpack Compose (Kotlin)
**Core networking**: Go library via gomobile AAR

**Screens:**
- **Home**: Big connect/disconnect toggle. Shows status: disconnected / connecting / connected (direct) / connected (relayed). Shows connected peer name and latency.
- **Setup**: Scan QR code from `bamgate device add` or paste join token. Configures everything automatically.
- **Settings**: Advanced options (custom STUN servers, always-on VPN, per-app VPN).

**Android-specific:**
- `VpnService` API to create the TUN interface that wireguard-go binds to
- Go core library compiled as AAR via gomobile, called from Kotlin via JNI
- Foreground service with persistent notification showing connection state
- Handle Android lifecycle: reconnect on network change (wifi ↔ mobile), survive doze mode
- Battery optimization whitelist prompt during setup

## Implementation Order

The implementation follows a **client-first** approach: build and test the Go client
packages locally before deploying any Cloudflare infrastructure. This maximizes feedback
speed (pure Go, no infra dependency) and proves the hardest integration — pion/webrtc +
signaling — before building a server around it.

### Phase 1: Go Client Core (Signaling + WebRTC)

Build the Go client packages and prove two peers can exchange data over WebRTC, using a
local in-process signaling hub (no Cloudflare dependency yet).

1. Project scaffolding: `go mod init`, create directory structure (`cmd/`, `internal/`), add core dependencies (`pion/webrtc/v4`, `nhooyr.io/websocket`)
2. `internal/signaling/protocol.go`: Define all signaling message types as Go structs (join, offer, answer, ice-candidate, peers, peer-left) with JSON marshal/unmarshal and type discriminator
3. `internal/signaling/client.go`: WebSocket client — connect, send, receive loop into channel, basic reconnection
4. `internal/signaling/`: Write tests using a local in-memory signaling hub (goroutine that relays messages between two clients)
5. `internal/webrtc/`: Wrap `pion/webrtc` — peer connection creation, ICE config with STUN servers, unreliable/unordered data channel setup
6. Integration test: two peers do SDP offer/answer + ICE exchange through the local signaling hub, open a data channel, send bytes back and forth
7. `internal/config/`: TOML config loading/saving, WireGuard key generation (Curve25519)

**Deliverable**: Two Go processes exchange arbitrary bytes over a WebRTC data channel, coordinated through a local in-process signaling hub. No infrastructure required.

### Phase 2: WireGuard Integration

Wire up the actual VPN tunnel on the client side.

1. `internal/tunnel/`: Set up wireguard-go with userspace TUN interface
2. `internal/bridge/`: TUN ←→ data channel packet forwarding (the critical glue)
3. `internal/agent/`: Top-level orchestrator tying signaling → WebRTC → bridge → WireGuard
4. Linux CLI: `bamgate up` / `bamgate down` / `bamgate status`
5. Test: two machines on the same LAN, using the local signaling hub, SSH between WireGuard IPs through the tunnel

**Deliverable**: Full working VPN tunnel between two Linux machines on the same network. Still using a local/test signaling server.

### Phase 3: Cloudflare Worker (Signaling Server)

Now that the client protocol is proven, build the real signaling server.

1. Set up Wrangler project with Worker + Durable Object (TinyGo → Wasm build pipeline, custom JS shim for DO WebSocket Hibernation API)
2. Implement Worker: auth middleware, WebSocket upgrade, routing to correct DO
3. Implement DO signaling: peer join/leave, SDP relay, ICE candidate relay
4. Connect the existing Go signaling client to the deployed Worker — verify two remote peers can exchange signaling messages and establish a WebRTC data channel
5. Test the full tunnel end-to-end across the internet (different networks, same CF Worker)

**Deliverable**: Two machines on different networks can establish a VPN tunnel through the Cloudflare Worker.

### Phase 4: TURN Relay on Durable Objects

Build the relay fallback so it works even through symmetric NAT.

1. Study `pion/turn` server implementation for protocol reference
2. Implement TURN-over-WebSocket in the Durable Object:
   - Allocate request/response
   - CreatePermission
   - ChannelBind
   - Send/Data indications (or ChannelData messages)
3. Implement TURN credential generation in the Worker
4. Configure the Go WebRTC peer connection to use our CF TURN server
5. Test: force relay-only mode (disable STUN) and verify data flows through the DO

**Deliverable**: Data channel works even when direct connection is impossible, falling back to our TURN relay.

### Phase 5: Device Management & Polish

1. `bamgate init`: Automate CF Worker deployment via Cloudflare API
2. `bamgate device add/remove/list`: Device provisioning with join tokens + QR codes
3. Config management: Proper persistence, key storage
4. Reconnection: Auto-reconnect on network change, ICE restart on connectivity loss
5. Logging: Structured logs, connection diagnostics in `bamgate status`
6. Systemd unit file for running the home agent as a service

**Deliverable**: Polished CLI tool that's easy to set up and operates reliably.

### Phase 6: Android App

1. gomobile bindings: Expose core Go library (agent, config) to Kotlin
2. VpnService integration: Android TUN + wireguard-go
3. QR code scanning for device setup
4. Minimal Jetpack Compose UI
5. Background service with notification
6. Test: Access home services from phone on LTE

**Deliverable**: Working Android app.

### Phase 7: Extras (Optional)

- **DNS resolution**: Resolve `*.home` hostnames through the tunnel (custom DNS server on home agent)
- **mDNS forwarding**: Discover home services automatically
- **Web dashboard**: Status page on Cloudflare Pages
- **Multiple networks**: Join more than one network simultaneously
- **Key rotation**: Automatic periodic WireGuard and WebRTC key rotation
- **IPv6 support**: Native IPv6 through the tunnel
- **Split tunneling**: Only route specific subnets through the tunnel

## Key Design Decisions

### Why WebRTC instead of custom NAT traversal?

WebRTC bundles ICE (STUN + TURN + connectivity checks) into a single, well-tested stack. Building custom hole punching is educational but error-prone — ICE handles dozens of edge cases around NAT types, candidate prioritization, connectivity check pacing, and fallback timing. Using `pion/webrtc` gives us a production-grade implementation. Additionally, learning WebRTC deeply has compounding value for other projects.

### Why build a custom TURN server on Cloudflare Workers?

Free third-party TURN servers are rare and unreliable. Paid TURN (like Twilio or Metered) adds cost and an external dependency. Running `coturn` on a VPS defeats the "no VPS" goal. Building a TURN-like relay on Durable Objects keeps everything on Cloudflare's free tier and under our control. It only needs to handle a subset of the TURN protocol — just enough for `pion/webrtc`'s ICE agent to use it.

### Why unreliable/unordered data channels?

WireGuard is a UDP protocol. If we use reliable/ordered data channels (which use SCTP retransmission), we get TCP-like head-of-line blocking. A lost packet would stall all subsequent packets until it's retransmitted. With unreliable/unordered, lost packets are simply lost — which is what WireGuard expects and handles at its own layer. This gives us the closest behavior to raw UDP.

### Why Go?

`pion/webrtc` is the best non-browser WebRTC implementation and it's in Go. `wireguard-go` is the official userspace WireGuard and it's in Go. `gomobile` provides a proven path to Android. The ecosystem alignment is too strong to ignore for this project.

### Why build the Go client before the Cloudflare Worker?

The hardest and most novel integration is `pion/webrtc` + signaling + data channels. Building and testing this locally with an in-process signaling hub gives the fastest feedback loop — no deploy cycles, no infra to debug. It also means the signaling protocol is fully defined and tested before the server is built, so the server implementation is just "relay these known message types." The TURN relay is deferred even further because most consumer NATs work with STUN alone; TURN is the fallback for symmetric NAT and can be added after the happy path is proven.

### Why TinyGo→Wasm for the Cloudflare Worker?

Keeping the entire project in Go — client and server — means shared types, shared signaling protocol code, and one language to maintain. The Worker and Durable Object are compiled from Go to WebAssembly via **TinyGo** and run on Cloudflare's Wasm runtime.

Standard Go (`GOOS=js GOARCH=wasm`) was considered but rejected due to binary size: even a hello world compresses to ~1.5MB gzip, eating half the free-tier 3MB limit before any business logic. With dependencies, it easily exceeds the limit. TinyGo produces binaries 10–20x smaller (~200KB–1MB compressed), fitting comfortably within the free tier.

The main TinyGo limitation — no goroutine support in Wasm — is not a problem for this project. Cloudflare Workers and Durable Objects are event-driven: each HTTP request or WebSocket message is processed as a separate synchronous event, and the DO runtime handles connection multiplexing. The signaling hub (receive JSON → decode → forward to target peer) and TURN relay (receive packet → decode → forward to peer's WebSocket) are both pure synchronous request-response logic per event.

Shared signaling protocol types live in a common Go package (`pkg/protocol/`) importable by both the client (standard Go) and the worker (TinyGo). TinyGo is compatible with most pure-Go packages, which is all the signaling types require. The `syumai/workers` library was evaluated but does not support implementing Durable Objects — only calling DO stubs — so a custom JS shim is used instead.

### Double encryption (WireGuard + DTLS)?

WebRTC data channels are encrypted via DTLS. WireGuard adds its own encryption. This means packets are double-encrypted. The overhead is negligible for the traffic volumes we're dealing with (accessing home services, not streaming 4K video). The benefit is defense in depth — even if DTLS were somehow compromised, WireGuard encryption remains intact.

## Security Considerations

- **Authentication**: GitHub OAuth Device Authorization Grant (RFC 8628). Users authenticate via GitHub — no shared secrets or invite tokens. The Worker verifies the GitHub token, registers the device, and mints a refresh token.
- **Token hierarchy**: GitHub access token (transient, used only during registration) → Refresh token (30-day rolling, stored in config.toml, rotated on every use) → JWT access token (1-hour lifetime, HMAC-SHA256 signed by Worker, kept in memory only).
- **JWT signing**: HMAC-SHA256 via Web Crypto API (`SubtleCrypto`) in the Worker JS shim. Signing keys are stored in DO SQLite (`signing_keys` table) with `kid` rotation support from day one.
- **TURN credentials**: Time-limited (24hr), derived from a shared secret via HMAC. The TURN secret is auto-generated on first registration and stored in DO SQLite (not as an environment binding).
- **WireGuard keys**: Standard Curve25519 key pairs. Private key never leaves the device.
- **Signaling security**: All signaling goes through HTTPS/WSS to Cloudflare. The Worker validates the JWT before allowing WebSocket upgrade.
- **Relay security**: The TURN relay (Durable Object) only sees DTLS-encrypted WebRTC packets, which themselves contain WireGuard-encrypted packets. Two layers of encryption that the relay cannot decrypt.
- **No public IP exposure**: Home server only makes outbound connections. No ports forwarded, no DDNS needed.
- **Device revocation**: Owners can revoke any of their devices via `bamgate devices revoke <id>` or the `/auth/devices/:id` DELETE endpoint. Revoked devices cannot refresh their JWT.

## References

### WebRTC
- [pion/webrtc](https://github.com/pion/webrtc) — Pure Go WebRTC implementation
- [pion/turn](https://github.com/pion/turn) — TURN server/client in Go (reference for our CF implementation)
- [WebRTC for the Curious](https://webrtcforthecurious.com/) — Excellent free book on WebRTC internals
- [RFC 8831 (WebRTC Data Channels)](https://datatracker.ietf.org/doc/html/rfc8831)
- [RFC 5766 (TURN)](https://datatracker.ietf.org/doc/html/rfc5766)
- [RFC 8656 (TURN-bis)](https://datatracker.ietf.org/doc/html/rfc8656)
- [RFC 8445 (ICE)](https://datatracker.ietf.org/doc/html/rfc8445)
- [RFC 5389 (STUN)](https://datatracker.ietf.org/doc/html/rfc5389)

### WireGuard
- [wireguard-go](https://git.zx2c4.com/wireguard-go/) — Userspace WireGuard in Go
- [WireGuard Protocol](https://www.wireguard.com/protocol/) — Protocol specification

### Cloudflare
- [Cloudflare Workers](https://developers.cloudflare.com/workers/)
- [Durable Objects](https://developers.cloudflare.com/durable-objects/)
- [Durable Objects WebSocket API](https://developers.cloudflare.com/durable-objects/api/websockets/)
- [Wrangler CLI](https://developers.cloudflare.com/workers/wrangler/)
- [syumai/workers](https://github.com/syumai/workers) — Go package to run HTTP servers on Cloudflare Workers (TinyGo→Wasm). Evaluated but not used — does not support implementing Durable Objects.

### Related Projects (Reference)
- [Headscale](https://github.com/juanfont/headscale) — Open-source Tailscale control server
- [Netbird](https://github.com/netbirdio/netbird) — Open-source WireGuard mesh VPN (uses WebRTC internally!)
- [Tailscale](https://tailscale.com/blog/how-tailscale-works/) — How Tailscale's relay works
