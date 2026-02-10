# riftgate — Project Status

Last updated: 2026-02-09

## Current Phase

**Phase 1: Go Client Core (Signaling + WebRTC)** — In progress

See ARCHITECTURE.md §Implementation Plan for the full 7-phase roadmap.

## Completed

- Detailed architecture design (ARCHITECTURE.md) with 7-phase implementation plan
- Project scaffolding: Go module, directory structure, all package stubs
- Coding guidelines and AI agent instructions (AGENTS.md)
- Project README
- **Signaling protocol** (`internal/signaling/protocol.go`): `Message` interface with 6 concrete message types (`JoinMessage`, `OfferMessage`, `AnswerMessage`, `ICECandidateMessage`, `PeersMessage`, `PeerLeftMessage`), JSON marshal/unmarshal with type discriminator registry
- **Signaling client** (`internal/signaling/client.go`): WebSocket client with channel-based receive, context-aware lifecycle, automatic reconnection with exponential backoff
- **Signaling tests** (`protocol_test.go`, `client_test.go`): Full test suite with in-memory signaling hub, covering round-trip serialization, peer exchange, peer-left notifications, reconnection, context cancellation, and error cases. All tests pass with `-race`.

## What's Next

Per the implementation plan (ARCHITECTURE.md Phase 1), remaining steps:

1. ~~Signaling protocol message types (`internal/signaling/protocol.go`)~~ Done
2. ~~WebSocket signaling client (`internal/signaling/client.go`)~~ Done
3. WebRTC peer connection + ICE config (`internal/webrtc/`)
4. Config and WireGuard key management (`internal/config/`)
5. WireGuard tunnel setup (`internal/tunnel/`)
6. TUN-to-DataChannel bridge (`internal/bridge/`)
7. Agent orchestrator + CLI (`internal/agent/`, `cmd/riftgate/`)

## Code Status

| Package | Files | Status |
|---------|-------|--------|
| `cmd/riftgate` | main.go | Empty `main()` |
| `internal/agent` | agent.go | Stub |
| `internal/bridge` | bridge.go | Stub |
| `internal/config` | config.go, keys.go | Stub |
| `internal/signaling` | protocol.go, client.go, protocol_test.go, client_test.go | **Implemented + tested** |
| `internal/tunnel` | config.go, device.go, tun.go | Stub |
| `internal/webrtc` | datachan.go, ice.go, peer.go | Stub |
| `worker/` | — | Directory not created yet (Phase 3+) |

## Dependencies

| Library | Version | Purpose |
|---------|---------|---------|
| `github.com/coder/websocket` | v1.8.14 | WebSocket client/server for signaling |

## Open Questions / Decisions

- None at this time.

## Changelog

- **2026-02-09**: Implemented signaling protocol types and WebSocket client with full test suite.
- **2026-02-09**: Project bootstrapped — architecture doc, scaffolding, README, coding guidelines.
