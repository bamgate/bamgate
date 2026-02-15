# bamgate — Ideas & Future Work

Backlog of ideas, improvements, and future features. Roughly ordered by priority
within each category. See STATUS.md "What's Next" for the current short-term
priority list.

## Code Health

- **CI workflow on push/PR** — `go test -race`, `golangci-lint`, `go vet` in `.github/workflows/ci.yml`
- **Agent tests** — peer lifecycle, ICE restart logic, route acceptance, forwarding watchdog (`internal/agent/agent_test.go`)
- **Split agent.go** — extract `peer.go`, `routes.go`, `watchdog.go` from the 1,249-line god file
- **Checksum verification in install.sh** — download `checksums.txt` from release, verify tarball SHA256 before installing
- **Data path benchmarks** — `Benchmark*` tests for bridge send/receive, STUN parse/build, UAPI config generation
- **Pin linter config** — `.golangci.yml` with explicit linter list, timeout, exclusions

## Android

- Invite code paste fallback (no camera)
- Config import/export
- Battery optimization whitelist prompt
- Network change handling (ConnectivityManager -> ICE restart)
- Always-on VPN support
- Per-app VPN (`addDisallowedApplication`)
- Connection quality indicator

## macOS

- `bamgate install` — write launchd plist to `/Library/LaunchDaemons/`
- `bamgate up -d` — `launchctl bootstrap system <plist>`
- `bamgate down` — `launchctl bootout system/com.bamgate`
- BSD ioctl/routing sockets (replace ifconfig/route/sysctl shell commands)

## Infrastructure

- Rate limiting on Worker `/connect` and `/turn` endpoints
- End-to-end testing with systemd on a fresh machine
- macOS end-to-end testing (`setup` -> `up` -> `status`)

## Features

<!-- Add new feature ideas here -->
