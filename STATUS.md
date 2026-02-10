# riftgate — Project Status

Last updated: 2026-02-09

## Current Phase

**Phase 1: Go Client Core (Signaling + WebRTC)** — Not started

See ARCHITECTURE.md §Implementation Plan for the full 7-phase roadmap.

## Completed

- Detailed architecture design (ARCHITECTURE.md) with 7-phase implementation plan
- Project scaffolding: Go module, directory structure, all package stubs
- Coding guidelines and AI agent instructions (AGENTS.md)
- Project README

## What's Next

Per the implementation plan (ARCHITECTURE.md Phase 1):

1. Signaling protocol message types (`internal/signaling/protocol.go`)
2. WebSocket signaling client (`internal/signaling/client.go`)
3. WebRTC peer connection + ICE config (`internal/webrtc/`)
4. Config and WireGuard key management (`internal/config/`)
5. WireGuard tunnel setup (`internal/tunnel/`)
6. TUN-to-DataChannel bridge (`internal/bridge/`)
7. Agent orchestrator + CLI (`internal/agent/`, `cmd/riftgate/`)

## Code Status

All `.go` files are empty package stubs. No functional code, no dependencies
added to `go.mod`, no tests written.

| Package | Files | Status |
|---------|-------|--------|
| `cmd/riftgate` | main.go | Empty `main()` |
| `internal/agent` | agent.go | Stub |
| `internal/bridge` | bridge.go | Stub |
| `internal/config` | config.go, keys.go | Stub |
| `internal/signaling` | client.go, protocol.go | Stub |
| `internal/tunnel` | config.go, device.go, tun.go | Stub |
| `internal/webrtc` | datachan.go, ice.go, peer.go | Stub |
| `worker/` | — | Directory not created yet (Phase 3+) |

## Open Questions / Decisions

- None at this time.

## Changelog

- **2026-02-09**: Project bootstrapped — architecture doc, scaffolding, README, coding guidelines.
