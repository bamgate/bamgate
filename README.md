<p align="center">
  <img src="logo.png" alt="bamgate logo" width="200">
</p>

# bamgate

A WireGuard VPN tunnel over WebRTC. Access your home network from anywhere without exposing your public IP or renting a VPS.

## How it works

bamgate connects two peers through a WireGuard tunnel, using WebRTC data channels as the transport layer. A Cloudflare Worker handles signaling and acts as a TURN relay fallback — all on the free tier.

```mermaid
graph LR
    A[Remote device] <--> B[WireGuard]
    B <--> C[WebRTC data channel]
    C <--> D[WireGuard]
    D <--> E[Home network]
    C <--> F[Cloudflare Worker\nSignaling + TURN relay]
```

- **Direct connection**: ICE/STUN punches through NAT when possible — low latency, no relay.
- **Relayed connection**: When direct fails (symmetric NAT), traffic routes through a TURN relay on Cloudflare Durable Objects. The relay only sees opaque encrypted blobs.

## Install

### Linux and macOS

```bash
curl -fsSL https://raw.githubusercontent.com/bamgate/bamgate/main/install.sh | sh
```

This installs the `bamgate` binary to `/usr/local/bin`. Sudo is requested automatically if needed.

To install a specific version:

```bash
BAMGATE_VERSION=0.5.0 curl -fsSL https://raw.githubusercontent.com/bamgate/bamgate/main/install.sh | sh
```

### Android

Download the latest APK from [GitHub Releases](https://github.com/bamgate/bamgate/releases). Use `bamgate qr` on an existing device to generate a QR code for easy setup.

### Build from source

```bash
go build -o bamgate ./cmd/bamgate
sudo cp bamgate /usr/local/bin/
```

## Quick start

### First device (deploys the Cloudflare Worker)

```bash
sudo bamgate setup
```

This walks you through authenticating with GitHub, deploying the signaling server to Cloudflare, generating WireGuard keys, and configuring the device. You'll need a [GitHub](https://github.com) account and a [Cloudflare API token](https://dash.cloudflare.com/profile/api-tokens).

### Additional devices

On the new device, run setup and authenticate with the same GitHub account:

```bash
sudo bamgate setup
```

The setup wizard detects that a network already exists and joins it. Each device gets an auto-assigned tunnel address.

### Connect

```bash
sudo bamgate up             # foreground
sudo bamgate up -d          # daemon mode (systemd on Linux, launchd on macOS)
```

Check status:

```bash
bamgate status
```

Stop the daemon:

```bash
sudo bamgate down
```

### Manage devices

```bash
bamgate devices                 # list all devices (online status, capabilities)
bamgate devices configure       # interactively accept routes, DNS from online devices
bamgate devices revoke <id>     # revoke a device
```

### Upgrade

```bash
sudo bamgate update
```

Or re-run the install script.

### Uninstall

```bash
sudo bamgate uninstall
```

## Goals

- Zero exposed ports on the home network
- No VPS required — runs entirely on Cloudflare's free tier
- Single static binary for Linux and macOS, plus an Android app
- WireGuard encryption end-to-end, with DTLS as an additional layer
- GitHub OAuth authentication — no shared secrets or invite tokens

## Documentation

- [Architecture](ARCHITECTURE.md) — full design document
- [LAN testing guide](docs/testing-lan.md) — step-by-step local testing

## License

[MIT](LICENSE)
