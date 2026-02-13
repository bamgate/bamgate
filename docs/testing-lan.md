# LAN Testing Guide

Test a full WireGuard tunnel between two Linux machines using a local signaling hub.

## Prerequisites

- `bamgate` binary on both machines
- `bamgate-hub` binary on one machine (the signaling server)
- Both machines on the same LAN
- `sudo` access on both (TUN device creation requires `CAP_NET_ADMIN`)
- `wg` tools installed for key generation (package: `wireguard-tools`)

## Step 1: Generate WireGuard key pairs

On **each machine**, generate a key pair:

```bash
wg genkey | tee private.key | wg pubkey > public.key
cat private.key
```

You only need each machine's own private key for its config. The public keys are
exchanged automatically via signaling.

## Step 2: Create config files

On **Machine A** (home-server), create `~/.config/bamgate/config.toml`:

```toml
[network]
server_url = "ws://<HUB_MACHINE_IP>:8080"

[device]
name = "home-server"
private_key = "<MACHINE_A_PRIVATE_KEY>"
address = "10.0.0.1/24"
```

On **Machine B** (laptop), create `~/.config/bamgate/config.toml`:

```toml
[network]
server_url = "ws://<HUB_MACHINE_IP>:8080"

[device]
name = "laptop"
private_key = "<MACHINE_B_PRIVATE_KEY>"
address = "10.0.0.2/24"
```

Replace `<HUB_MACHINE_IP>` with the LAN IP of whichever machine runs the signaling
hub (e.g. `192.168.1.100`). Replace the private keys with the output from Step 1.

The `device.name` values just need to be different — they determine offer/answer
roles via lexicographic ordering.

## Step 3: Start the signaling hub

On either machine (or a third):

```bash
./bamgate-hub -addr :8080
```

This runs the WebSocket signaling relay that peers connect to for SDP and ICE
candidate exchange. It does not need root.

## Step 4: Start bamgate on both machines

On **Machine A**:

```bash
sudo ./bamgate -v
```

On **Machine B**:

```bash
sudo ./bamgate -v
```

The `-v` flag enables debug logging so you can watch signaling, ICE negotiation,
and data channel setup in real time.

## Step 5: Verify the tunnel

Check the TUN interface is up on both machines:

```bash
ip addr show bamgate0
```

Test connectivity:

```bash
# From Machine A:
ping 10.0.0.2

# From Machine B:
ping 10.0.0.1

# SSH across the tunnel:
ssh user@10.0.0.2
```

## What to watch for in the logs

With `-v`, you should see this sequence on each peer:

1. **TUN device created** — `bamgate0` interface created
2. **TUN interface configured** — IP address assigned
3. **agent started** — connected to signaling hub
4. **received peer list** / **discovered peer** — peer discovery via signaling
5. **initiating connection** or **received offer** — WebRTC SDP negotiation
6. **data channel open, bridging WireGuard** — tunnel is live, packets flowing

## Teardown

Press Ctrl+C on each `bamgate` process. It handles SIGINT/SIGTERM gracefully and
tears down the TUN interface, WireGuard device, and WebRTC connections.

## Troubleshooting

- **Config file not found**: Ensure `~/.config/bamgate/config.toml` exists with
  correct permissions, or pass `--config /path/to/config.toml` explicitly.
- **TUN creation fails**: Make sure you're running with `sudo`. The `ip` command
  must also be available (usually in the `iproute2` package).
- **Peers don't discover each other**: Verify both peers can reach the signaling
  hub URL. Check firewall rules allow TCP port 8080 (or whatever port the hub
  listens on).
- **ICE connection fails**: On a LAN, peers should connect directly via host
  candidates. If firewalls block UDP between the machines, ICE will fail (there's
  no TURN relay yet — that's Phase 4).
- **Ping doesn't work after "data channel open"**: Check that `bamgate0` has the
  expected IP on both sides with `ip addr show bamgate0`. Check WireGuard status
  with `sudo wg show` if `wg` tools are installed.
