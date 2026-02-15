# macOS Support Status

**Goal:** Full macOS (darwin) support for the bamgate CLI.

**Phase 1 (DONE):** Core compilation and foreground operation. `GOOS=darwin go build` succeeds. `sudo bamgate up` runs in foreground on macOS.

## Completed

- **Network interface management** (`internal/tunnel/netlink_darwin.go`): All 6 functions implemented using shell commands (`ifconfig`, `route`, `sysctl`). `netlink.go` gated with `//go:build linux`. Cross-platform `FindInterfaceForSubnet` extracted to `iface.go`.
- **NAT masquerade** (`internal/tunnel/nat_darwin.go`): `NATManager` implemented using macOS PF (`pfctl`) with a `com.bamgate` anchor. `nat.go` gated with `//go:build linux`.
- **TUN device naming**: `DefaultTUNName` is `"bamgate0"` on Linux (`tun_linux.go`), `"utun"` on macOS (`tun_darwin.go`). Agent uses `tunnel.DefaultTUNName` instead of hardcoded name.
- **Control socket path**: `ResolveSocketPath()` uses `/var/run/bamgate/` on macOS, `/run/bamgate/` on Linux.
- **CLI commands**: `setup`, `install`, `up` (foreground) work on macOS. `up -d`, `down`, and `install --systemd` give clear "not yet implemented" messages on macOS.

See [IDEAS.md](../IDEAS.md) for remaining macOS work (launchd integration, BSD ioctl/routing sockets).
