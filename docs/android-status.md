# Android App Status

**Goal:** Android app using gomobile AAR with Jetpack Compose UI, connecting to the existing Cloudflare Worker signaling server.

**Architecture:** Go core library compiled to AAR via gomobile. Kotlin/Jetpack Compose UI. Android `VpnService` creates TUN FD and passes it to Go. Socket protection via gomobile callback interface (`VpnService.protect()`). Setup via QR code scan from `bamgate invite`.

## Phase A+B: Go Bindings + Minimal Android App

### Go Side

| # | Item | Files | Status |
|---|------|-------|--------|
| 1 | Android TUN support | `internal/tunnel/tun_android.go` | **Done** |
| 2 | Android network stubs | `internal/tunnel/netlink_android.go` | **Done** |
| 3 | Android NAT stub | `internal/tunnel/nat_android.go` | **Done** |
| 4 | Fix build tags (`linux && !android`) | `internal/tunnel/tun_linux.go`, `netlink.go`, `nat.go`, `netlink_test.go` | **Done** |
| 5 | Agent TUN FD injection | `internal/agent/agent.go` — `WithTunFD(fd)` option | **Done** |
| 6 | Socket protection callback | `internal/agent/agent.go`, `protectednet.go` — `WithSocketProtector()` + `protectedNet` wrapping pion/transport | **Done** |
| 7 | gomobile binding layer | `mobile/bamgate.go` — `Tunnel`, `RedeemInvite()`, `Logger`, `SocketProtector` | **Done** |
| 8 | QR code in `bamgate invite` | `cmd/bamgate/cmd_invite.go` — `bamgate://invite?server=&code=` QR in terminal | **Done** |

### Android Side

| # | Item | Files | Status |
|---|------|-------|--------|
| 9 | Project scaffolding | `android/` — Gradle 8.9, Kotlin 2.1, Compose BOM 2024.12, min API 24 | **Done** |
| 10 | AAR integration | `android/app/libs/bamgate.aar` — gomobile-built AAR | **Done** (built via CI) |
| 11 | VpnService | `BamgateVpnService.kt` — TUN FD, foreground notification, protect() callback | **Done** |
| 12 | Home screen | `HomeScreen.kt` — connect/disconnect toggle, status text | **Done** |
| 13 | QR scan screen | `SetupScreen.kt` + `QrScannerScreen.kt` — CameraX + ML Kit barcode | **Done** |
| 14 | Config storage | `ConfigRepository.kt` — DataStore for TOML config, invite deep link | **Done** |
| 15 | VPN permission | `MainActivity.kt` — VpnService.prepare() consent, deep link handler | **Done** |
| 16 | Build & test | — | Needs device test |
| 17 | CI pipeline | `.github/workflows/release.yml` — `android` job | **Done** |

## Key Design Decisions

- **gomobile AAR**: Export a thin `mobile/` package with gomobile-compatible types (string, int, []byte, error, interfaces). No structs with complex fields at the boundary.
- **TUN FD injection**: `agent.WithTunFD(fd int)` option. If FD > 0, use it instead of `tunnel.CreateTUN()`. Skip `configureTUN()` — Android VpnService already configured the interface.
- **Socket protection**: `SocketProtector` interface with `Protect(fd int) bool` method. Implemented in Kotlin calling `VpnService.protect()`. Passed to agent which injects into signaling WebSocket dialer and pion/webrtc SettingEngine.
- **Build tags**: `//go:build linux && !android` on existing Linux files. `//go:build android` on new Android files.
- **QR code content**: `bamgate://invite?server=<host>&code=<code>` URL scheme. Android app scans, redeems invite via HTTP, generates WireGuard keys, saves config.
- **Min Android version**: API 24 (Android 7.0).
- **Monorepo**: Android project lives in `android/` subdirectory of this repo.

See [IDEAS.md](../IDEAS.md) for future Android ideas (Phase C/D: onboarding polish, reliability, per-app VPN, etc.).
