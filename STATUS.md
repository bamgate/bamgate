# bamgate — Project Status

Last updated: 2026-02-18 (session 28)

## Current Phase

**Phase 1: Go Client Core (Signaling + WebRTC)** — Complete
**Phase 2: WireGuard Integration** — Complete (tested end-to-end, tunnel working)
**Phase 3: Cloudflare Worker (Signaling Server)** — Complete (deployed, multi-peer tested)
**Phase 4: TURN Relay on Durable Objects** — Complete (implemented, needs deploy + manual test)
**Phase 5: CLI Polish & Resilience** — Complete
**Phase 5.5: UX & Operational Improvements** — Complete
**Phase 6: Automated Setup & Deployment** — Complete
**Phase 7: GitHub OAuth Authentication** — Complete (replaced shared AUTH_TOKEN with GitHub OAuth + JWT)

See ARCHITECTURE.md §Implementation Plan for the full 7-phase roadmap.

## Completed

| Feature | Package / Files | Notes |
|---------|----------------|-------|
| Signaling protocol | `pkg/protocol/` | 6 message types, JSON type discriminator, TinyGo-compatible |
| Signaling client | `internal/signaling/` | WebSocket, reconnect w/ exponential backoff |
| Signaling hub | `internal/signaling/hub.go` | Reusable `http.Handler` for tests + `bamgate-hub` |
| WebRTC peer connection | `internal/webrtc/` | ICE, unreliable/unordered data channels, SDP exchange |
| WireGuard keys | `internal/config/keys.go` | Curve25519, RFC 7748 clamping, base64 + TOML round-trip |
| TOML config | `internal/config/config.go` | Load/save, XDG paths, split config.toml (0644) + secrets.toml (0640), `[cloudflare]` section |
| WireGuard TUN + device | `internal/tunnel/` | TUN creation, UAPI config, device lifecycle, custom Bind |
| Bridge (TUN <-> WebRTC) | `internal/bridge/` | Custom `conn.Bind` routing packets over data channels |
| Agent orchestrator | `internal/agent/` | Peer lifecycle, ICE restart (3 retries), NAT/forwarding, watchdog |
| CLI (Cobra) | `cmd/bamgate/` | `setup`, `login`, `up`, `down`, `restart`, `devices`, `worker` (install/update/uninstall/info), `status`, `logs`, `genkey`, `update`, `uninstall` |
| Standalone hub | `cmd/bamgate-hub/` | Lightweight signaling server for LAN testing |
| Control server | `internal/control/` | Unix socket JSON status API, smart path resolution |
| Subnet routing | config + protocol + agent | `[device] routes`, propagated via signaling, AllowedIPs per peer |
| `--accept-routes` (legacy) | config + agent + CLI | Blanket opt-in for remote subnet routes (deprecated by per-peer selections) |
| Peer capability advertisement | `pkg/protocol/`, signaling, worker | Metadata map on JoinMessage/PeerInfo carries routes, DNS, search domains |
| Per-peer selections | `internal/config/`, `internal/agent/` | `[peers.<name>]` config sections, fine-grained opt-in per capability per peer |
| Peer DNS advertisement | config + agent + tunnel | `dns`/`dns_search` in device config, advertised via metadata, applied via resolvectl/resolver |
| `bamgate devices` | `cmd/bamgate/cmd_devices.go` | Merged device list (server + live), `configure` subcommand with TUI, `revoke` |
| Control plane extensions | `internal/control/` | `GET /peers/offerings`, `POST /peers/configure` endpoints |
| IP forwarding + NAT | `internal/tunnel/` | Netlink forwarding + nftables MASQUERADE, auto-detected interface |
| Cloudflare Worker | `worker/` | Go/Wasm DO: signaling hub, WebSocket Hibernation, bearer auth, rehydration |
| GitHub OAuth + JWT auth | `worker/src/worker.mjs`, `internal/auth/` | GitHub Device Auth flow, JWT access tokens, refresh token rotation, device registration |
| Device management CLI | `cmd/bamgate/cmd_devices.go` | `bamgate devices list`, `bamgate devices revoke` |
| TURN relay | `worker/turn.go`, `internal/turn/` | TURN-over-WebSocket for symmetric NAT, HMAC-SHA1 credentials |
| STUN parser | `worker/stun/` | Minimal TinyGo-compatible STUN/TURN message codec (~500 lines) |
| Embedded worker assets | `internal/deploy/assets.go` | `//go:embed` worker.mjs + app.wasm + wasm_exec.js |
| Cloudflare API client | `internal/deploy/cloudflare.go` | REST v4: deploy, settings, bindings, migrations |
| `bamgate setup` | `cmd/bamgate/cmd_setup.go` | Interactive wizard: CF API token or invite code path |
| `bamgate install` | `cmd/bamgate/cmd_install.go` | Binary + caps + optional systemd/launchd service |
| Systemd service | `contrib/bamgate.service` | Root daemon, `Restart=on-failure` |
| Netlink TUN config | `internal/tunnel/netlink.go` | Raw netlink syscalls, no `ip` binary dependency |
| macOS support | `internal/tunnel/*_darwin.go` | Platform files, PF NAT, ifconfig/route/sysctl |
| Android app | `mobile/`, `android/` | gomobile AAR, Jetpack Compose app |
| Android CI | `.github/workflows/release.yml` | gomobile AAR -> Gradle APK, uploaded to GitHub release |
| ~~QR invite codes~~ | ~~`cmd/bamgate/cmd_invite.go`~~ | ~~Removed — replaced by GitHub OAuth Device Auth flow~~ |
| Install script + self-update | `install.sh`, `cmd_update.go` | `curl\|sh` install, `bamgate update` self-update |
| Vendored anet | `third_party/anet/` | Patched `wlynxg/anet` for Android pion/transport compat |
| Integration test suite | `internal/agent/*_test.go` | 16 tests: fake TUN/WG + real signaling + real WebRTC, no root needed |
| Docker e2e tests | `test/e2e/` | 3-peer mesh with real TUN + WireGuard + WebRTC, `make e2e` |

See [IDEAS.md](IDEAS.md) for the backlog of future work, code health improvements, and feature ideas.

## What's Next

1. **Deploy and test OAuth flow end-to-end** — Redeploy the Worker with the new OAuth/JWT auth, run `bamgate setup` to register via GitHub, verify signaling and TURN relay work with JWT auth.
2. **Deploy and test TURN relay** — Test end-to-end with phone tethering (symmetric NAT). Verify `bamgate status` shows `ice_type: relay`. TURN secret is now auto-generated in DO SQLite (no env binding needed).
3. **macOS support — launchd integration** — `up -d`, `down`, and `install --systemd` equivalents for macOS using launchd plists.
4. **Rate limiting** — Add request rate limiting to the Worker `/connect` and `/turn` endpoints to prevent abuse.
5. **Android client** — Phase A+B complete. `mobile/bamgate.go` updated with `RegisterDevice` using the new auth package. Needs device testing.
6. **End-to-end testing with systemd** — Verify the full `install --systemd` -> `up -d` -> `status` -> `down` workflow on a fresh machine.

## Testing

See [docs/testing-lan.md](docs/testing-lan.md) for the LAN testing guide.

- **Phase 2 LAN test** (2026-02-10): Two Linux machines, bidirectional ping through WireGuard-over-WebRTC tunnel.
- **Phase 3 internet test** (2026-02-10): Three peers (2 home LAN + 1 DigitalOcean droplet) via Cloudflare Worker. ~2ms LAN latency.

## Code Status

| Package | Files | Status |
|---------|-------|--------|
| `cmd/bamgate` | main.go, cmd_up.go, cmd_down.go, cmd_restart.go, cmd_setup.go, cmd_login.go, cmd_worker.go, cmd_devices.go, cmd_qr.go, cmd_helpers.go, cmd_helpers_test.go, cmd_status.go, cmd_logs.go, cmd_genkey.go, cmd_update.go, cmd_uninstall.go, exec_unix.go, exec_windows.go | **Implemented + tested** — Cobra subcommands: setup (GitHub OAuth), login, up, down, restart, worker (install/update/uninstall/info), devices (list/configure/revoke), qr, status, logs, genkey, update, uninstall |
| `cmd/bamgate-hub` | main.go | **Implemented** — standalone signaling server |
| `internal/agent` | agent.go, deps.go, agent_test.go, agent_integration_test.go, fake_test.go, protectednet.go, protectednet_android.go, protectednet_ifaces.go | **Implemented + tested** — orchestrator with ICE restart, subnet routing, forwarding/NAT, control server, TURN relay integration, Android socket protection, JWT refresh loop. 16 integration tests (fake TUN/WG + real signaling + real WebRTC). Docker e2e tests in `test/e2e/` |
| `internal/auth` | github.go, tokens.go | **Implemented** — GitHub Device Auth flow (RFC 8628), register/refresh/list/revoke API client |
| `internal/control` | server.go, server_test.go | **Implemented + tested** — Unix socket API: status, peer offerings, peer configure |
| `internal/bridge` | bridge.go, bridge_test.go | **Implemented + tested** |
| `internal/config` | config.go, keys.go, config_test.go, keys_test.go | **Implemented + tested** — Split config.toml (0644) + secrets.toml (0640) for non-root CLI access |
| `internal/signaling` | client.go, hub.go, client_test.go | **Implemented + tested** |
| `pkg/protocol` | protocol.go, protocol_test.go | **Implemented + tested** |
| `internal/tunnel` | config.go, device.go, tun.go, tun_linux.go, tun_darwin.go, tun_android.go, iface.go, netlink.go, netlink_darwin.go, netlink_android.go, nat.go, nat_darwin.go, nat_android.go, config_test.go, netlink_test.go | **Implemented + tested** — Cross-platform: Linux (netlink + nftables), macOS (ifconfig/route/pfctl), Android (VpnService FD, no-op stubs) |
| `internal/turn` | credentials.go, credentials_test.go, dialer.go, dialer_test.go | **Implemented + tested** |
| `internal/webrtc` | ice.go, datachan.go, peer.go, peer_test.go | **Implemented + tested** |
| `internal/deploy` | cloudflare.go, assets.go, assets/ | **Implemented** — Cloudflare API client, embedded worker assets |
| `worker/` | hub.go, turn.go, main.go, src/worker.mjs | **Implemented** — TinyGo Wasm, signaling + TURN + OAuth/JWT auth (register, refresh, devices) |
| `worker/stun` | stun.go, stun_test.go | **Implemented + tested** — 20 tests |
| `mobile/` | bamgate.go, tools.go | **Implemented** — gomobile binding layer |
| `android/` | Gradle project, 9 Kotlin files | **Implemented** — Jetpack Compose app |
| `third_party/anet` | android_api_level.go, LICENSE | **Vendored** — Android network interface compat |

## Dependencies

| Library | Version | Purpose |
|---------|---------|---------|
| `github.com/coder/websocket` | v1.8.14 | WebSocket client/server for signaling |
| `github.com/pion/webrtc/v4` | v4.2.6 | WebRTC stack (ICE, DTLS, SCTP, data channels) |
| `github.com/BurntSushi/toml` | v1.6.0 | TOML config file parsing |
| `golang.org/x/crypto` | v0.48.0 | Curve25519 key derivation (WireGuard keys) |
| `github.com/spf13/cobra` | v1.10.2 | CLI subcommand framework |
| `github.com/google/nftables` | v0.3.0 | Pure Go nftables for NAT masquerade |
| `golang.org/x/sys` | v0.41.0 | Netlink syscalls for TUN config, IP forwarding |
| `github.com/pion/transport/v4` | v4.0.1 | Transport abstractions (Android socket protection) |
| `github.com/skip2/go-qrcode` | v0.0.0 | Terminal QR code rendering for `bamgate qr` |
| `github.com/charmbracelet/huh` | v0.8.0 | TUI forms for `bamgate devices configure` |
| `golang.org/x/mobile` | latest | gomobile toolchain for Android AAR |
| `golang.zx2c4.com/wireguard` | v0.0.0-20250521 | Userspace WireGuard device + TUN interface |

## Releases

| Version | Date | Highlights |
|---------|------|------------|
| v1.15.3 | 2026-02-18 | Fix zombie peers after signaling reconnect: clean up closed PeerConnections in handlePeers and handleOffer, deduplicate peers list. |
| v1.15.2 | 2026-02-18 | Fix network switch reconnection: full teardown+rebuild instead of ICE restart (stale TURN/data channels), suppress spurious ICE restart attempts during reconnect, detect dead data channels on connected peers. |
| v1.15.1 | 2026-02-18 | Fix Android reconnection: defer ICE restart until signaling reconnects (was silently dropping restart offers). TURN allocation hibernation persistence. |
| v1.15.0 | 2026-02-18 | Fix 9 bugs: ICE candidate buffering, orphaned PeerConnection leak, cleanup order, ICE restart candidate race, 0.0.0.0/0 AllowedIPs security fix, infinite 401 retry, tokenMu data race, fragile 401 detection, jwtRefreshLoop ignores cancellation. Integration test suite (16 tests). Docker e2e tests. |
| v1.14.0 | 2026-02-17 | `bamgate logs` command — view service logs without knowing platform-specific tools |
| v1.13.1 | 2026-02-17 | Fix JWT expiry after suspend/resume, fix reconnect backoff overflow (45k request storm), fix ICE restart glare after sleep |
| v1.13.0 | 2026-02-16 | Merge `peers` into `devices`: unified device list (server + live peers), lipgloss table, interactive revoke/configure, Android DevicesScreen, README rewrite |
| v1.12.0 | 2026-02-16 | Android network change + sleep/wake recovery: proactive ICE restart and signaling reconnect on connectivity change or screen unlock |
| v1.11.0 | 2026-02-16 | `bamgate restart`, `bamgate login`, `bamgate worker` (install/update/uninstall/info), Cloudflare `DeleteWorker` API |
| v1.10.0 | 2026-02-16 | CLI theming, token borrowing from daemon, Android theme + PeersScreen rework, token rotation callback |
| v1.9.1 | 2026-02-16 | Android peer management screen (routes, DNS, search domain checkboxes) |
| v1.9.0 | 2026-02-16 | Peer capability advertisement (DNS, routes, search domains), per-peer opt-in selections, `bamgate peers` TUI |
| v1.8.3 | 2026-02-15 | Fix secrets file corruption, reclaim devices by name, reclaim revoked IPs |
| v1.8.2 | 2026-02-15 | Android Custom Tab for OAuth flow |
| v1.8.1 | 2026-02-15 | Fix config permissions for non-root access, `bamgate config` command |
| v1.8.0 | 2026-02-15 | GitHub OAuth auth, config split for non-root CLI, `bamgate qr` + `bamgate devices` |
| v1.7.1 | 2026-02-14 | New logo, Android icon update |
| v1.7.0 | 2026-02-14 | Show local pushed routes in `bamgate status`, Android APK CI |
| v1.6.0 | 2026-02-14 | Overhaul install: drop Homebrew, add install script + self-update + root daemon, launchd |
| v1.5.3 | 2026-02-12 | Fix ETXTBSY when copying over running binary |
| v1.5.2 | 2026-02-12 | Fix systemd 203/EXEC on Homebrew installs (SELinux) |
| v1.5.1 | 2026-02-12 | Document symlink step for Linux Homebrew users |
| v1.5.0 | 2026-02-12 | Remove install command, consolidate into setup with --force |
| v1.4.0 | 2026-02-12 | MIT license, Homebrew tap, transfer to bamgate org |
| v1.3.0 | 2026-02-12 | Rename from riftgate to bamgate |
| v1.2.2 | 2026-02-12 | Fix self-peer after DO hibernation/reconnection |
| v1.2.1 | 2026-02-12 | Fix ICE restart offer storm, add `bamgate version` |
| v1.2.0 | 2026-02-12 | TURN relay over WebSocket for symmetric NAT |
| v1.1.1 | 2026-02-12 | Fix macOS TUN routing |
| v1.1.0 | 2026-02-12 | macOS (darwin) support |
| v1.0.0 | 2026-02-12 | First major release — automated setup, daemon mode, subnet routing |
| v0.3.0 | 2026-02-11 | Fix AllowedIPs routing, per-peer /32 addresses |
| v0.2.0 | 2026-02-11 | End-to-end LAN tunnel, 3 critical bug fixes |
| v0.1.0 | 2026-02-11 | Initial pre-release |

## Open Questions / Decisions

- None at this time.

## Changelog

| Session | Date | Summary |
|---------|------|---------|
| 28 | 2026-02-18 | Fix zombie peers after signaling reconnect: when WebSocket drops and reconnects, closed PeerConnections were left in `a.peers` map causing `InvalidStateError: connection closed` on incoming offers, endless WireGuard `io: read/write on closed pipe` errors, and permanent peer disconnection. Fix: (1) `handlePeers` scans for and removes zombie peers (closed PCs) and `needsRestart` peers before processing; (2) `handleOffer` detects closed PC state and tears down zombie before creating fresh connection; (3) deduplicate peers list to handle hub rehydration artifacts. Also fixed v1.15.2 bugs: full teardown+rebuild on network change, suppress spurious ICE restarts, detect dead data channels. |
| 27 | 2026-02-18 | Fix 9 connection/auth bugs, add integration test suite (16 tests) + Docker e2e tests. **WebRTC bugs:** (1) ICE candidates arriving before SetRemoteDescription silently dropped — buffer in peerState, flush after SDP set; (2) createRTCPeer overwrites existing rtcPeer without closing — capture old peer under lock, close outside; (3) removePeer cleanup order — WG peer first, then bridge, then PC; (4) ICE restart candidates dropped due to ufrag mismatch race — use full ICE gathering (not trickle) for restart offers/answers via GatheringCompletePromise. **Auth/config bugs:** (5) peers without address get 0.0.0.0/0 AllowedIPs (security) — reject peers with no valid address; (6) infinite 401→refresh→retry loop — cap at 3 consecutive auth refreshes; (7) tokenMu race on RefreshToken — protect both jwtToken and RefreshToken under tokenMu; (8) fragile 401 detection via string matching — typed httpStatusError + errors.As; (9) jwtRefreshLoop time.Sleep ignores context — use select. **Test infrastructure:** dependency injection via Deps struct (8 interfaces), fake test doubles, 16 integration tests with real signaling + real WebRTC, Docker e2e (3-peer mesh with real TUN + WireGuard), `make e2e` target |
| 26 | 2026-02-17 | `bamgate logs` command: view daemon logs with `-f`/`--follow` and `-n`/`--lines` flags, shells out to `journalctl` on Linux and `tail /var/log/bamgate.log` on macOS, `syscall.Exec` replaces process for native output, build-tagged `exec_unix.go`/`exec_windows.go` for cross-platform support |
| 25 | 2026-02-17 | Fix three bugs causing broken connections after laptop suspend/resume: (1) signaling reconnect loop retries with expired JWT indefinitely — add `OnAuthFailure` callback to trigger immediate JWT refresh on 401; (2) exponential backoff overflows to zero at high attempt counts (`math.Pow(2, 45000)` → `+Inf` → negative `time.Duration`), causing ~45k requests in minutes — cap exponent and guard against `<= 0`; (3) ICE restart fails with `InvalidModificationError` when PeerConnection is stuck in `have-local-offer` from unanswered previous restart — rollback to `stable` before creating new offer |
| 24 | 2026-02-16 | Consolidate `peers` into `devices`: delete `cmd_peers.go`, rewrite `cmd_devices.go` with merged server device list + live peer data, `lipgloss/table` for ANSI-safe column rendering, interactive `devices revoke` (huh.Select + huh.Confirm), fix current device showing offline, mobile bindings (`ListDevices`, `GetDeviceID`, `CurrentJWT`), Android `DevicesScreen` replacing `PeersScreen`, delete outdated docs (`android-status.md`, `macos-status.md`), full README rewrite, table cell padding for readability |
| 23 | 2026-02-16 | Android network change recovery: `ConnectivityManager.NetworkCallback` + `ACTION_USER_PRESENT` BroadcastReceiver in VPN service notify Go tunnel on network change or screen unlock, `Agent.NotifyNetworkChange()` with 3s debounce triggers immediate ICE restart on all peers and signaling force-reconnect, `signaling.Client.ForceReconnect()` skips exponential backoff for instant reconnection, mobile `Tunnel.NotifyNetworkChange()` exposed via gomobile |
| 22 | 2026-02-16 | `bamgate restart` command (single-step daemon restart), `bamgate login` command (re-authenticate without full setup, preserves config), `bamgate worker` command group (install/update/uninstall/info for Cloudflare Worker management), `DeleteWorker` Cloudflare API method, interactive CF credential prompting for worker commands |
| 21 | 2026-02-16 | CLI theming (lipgloss palette, colored status/peers/devices output, custom huh theme), token borrowing from daemon via `GET /auth/token` control socket endpoint, `TokenUpdateCallback` for Android refresh token persistence, `TunnelHolder` singleton for VPN service/UI tunnel sharing, PeersScreen rework (immutable state, batched Apply & Reconnect), Android dark theme with brand palette, remove deprecated `accept_routes` toggle, mobile `GetConfig()` method |
| 20 | 2026-02-16 | Peer capability advertisement: metadata map on signaling protocol, DNS/search domain config fields, per-peer `[peers.<name>]` selections in config, `bamgate peers` + `bamgate peers configure` TUI (charmbracelet/huh), control plane `/peers/offerings` + `/peers/configure` endpoints, DNS installation via resolvectl (Linux) / /etc/resolver (macOS), Android VPN DNS from peer selections, worker JS shim + Go hub metadata forwarding, Android PeersScreen with checkbox UI for routes/DNS/search domains, sealed Screen navigation in MainActivity |
| 19 | 2026-02-15 | Fix secrets.toml corruption (encode to buffer before writing), reclaim existing device by name on re-registration instead of creating ghost entries, reclaim revoked device IP addresses |
| 18 | 2026-02-15 | Android Custom Tab for GitHub OAuth login (opens browser in-app instead of external browser) |
| 17 | 2026-02-15 | Split config into config.toml (0644) + secrets.toml (0640) so CLI commands work without sudo, `bamgate qr` uses LoadPublicConfig, sudo user gets group read on secrets via SUDO_GID chown, auto-migration of old monolithic config |
| 16 | 2026-02-15 | GitHub OAuth authentication: replace shared AUTH_TOKEN with GitHub Device Auth + Worker-minted JWTs, device registration/refresh/revoke, `bamgate devices` CLI, mobile bindings update |
| 15 | 2026-02-15 | (Session context — planning for OAuth transition) |
| 14 | 2026-02-14 | New ㅂ (bieup) logo in yellow/dark grey, Android adaptive icon update |
| 13 | 2026-02-14 | Show local pushed routes in `bamgate status` header |
| 12 | 2026-02-14 | Android APK CI pipeline (gomobile AAR -> Gradle -> GitHub release) |
| 11 | 2026-02-14 | Overhaul install (drop Homebrew, root daemon, launchd), Android Phase A+B (gomobile + Compose app) |
| 10 | 2026-02-12 | Fix ETXTBSY atomic binary replace, fix systemd SELinux 203/EXEC |
| 9 | 2026-02-12 | Remove install command, consolidate into setup --force, project logo |
| 8 | 2026-02-12 | Rename riftgate -> bamgate across 49 files |
| 7 | 2026-02-12 | Fix ICE restart offer storm (glare resolution), `bamgate version` |
| 6 | 2026-02-12 | TURN relay over WebSocket (client dialer + server state machine + STUN parser) |
| 5 | 2026-02-12 | macOS (darwin) support — platform files, PF NAT, ifconfig/route/sysctl |
| 4 | 2026-02-12 | Auto IP forwarding + nftables NAT, `--accept-routes` opt-in |
| 3 | 2026-02-12 | `bamgate setup` wizard, `bamgate invite`, worker deploy + invite endpoints |
| 2 | 2026-02-12 | UX improvements: URL normalization, daemon mode, netlink TUN config, install command |
| 1 | 2026-02-12 | Phase 5 complete — Cobra CLI, init wizard, subnet routing, ICE restart, status, systemd |
| 0 | 2026-02-09–10 | Phases 1–3: signaling, WebRTC, WireGuard, bridge, agent, Cloudflare Worker deploy |
