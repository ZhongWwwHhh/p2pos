# P2POS

P2POS is a libp2p-based cluster node with:
- membership-oriented cluster control
- periodic update checks
- browser-friendly transports (WebSocket)
- optional official AutoTLS (`libp2p.direct` via `p2p-forge`)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ZhongWwwHhh/Ops-System/dev/install.sh | sudo bash
```

The installer is interactive. It supports:
- joining an existing system (provide `system_pubkey`)
- bootstrapping a new system (generates system/admin/node materials)

After install:
- binary: `./p2pos-linux` in install directory
- config: `./config.json`
- service: `p2pos.service`

## Service Commands

```bash
systemctl status p2pos
systemctl restart p2pos
systemctl stop p2pos
journalctl -u p2pos -f
```

## Build From Source

```bash
go build ./...
./build.sh
```

## Key Generation Helper

The binary provides a keygen subcommand used by installer flow:

```bash
./p2pos keygen --new-system --cluster-id default --admin-valid-to 9999-12-31T00:00:00Z
```

Output uses `KEY=VALUE` lines and includes:
- node private key + peer ID
- optional system keypair
- optional admin private key + admin proof

## Configuration

Example `config.json`:

```json
{
  "init_connections": [
    { "type": "dns", "address": "init.p2pos.zhongwwwhhh.cc" }
  ],
  "listen": ["0.0.0.0:4100", "[::]:4100"],
  "network_mode": "auto",
  "auto_tls": {
    "mode": "auto",
    "user_email": "",
    "port": 4101,
    "cache_dir": ".autotls-cache",
    "forge_auth": ""
  },
  "cluster_id": "default",
  "system_pubkey": "",
  "members": [],
  "admin_proof": {
    "cluster_id": "",
    "peer_id": "",
    "role": "",
    "valid_from": "",
    "valid_to": "",
    "sig": ""
  },
  "node_private_key": "",
  "update_feed_url": "https://api.github.com/repos/ZhongWwwHhh/Ops-System/releases/latest"
}
```

## AutoTLS (Official libp2p.direct Flow)

Current implementation uses official `p2p-forge/client` integration.

When AutoTLS is active (`auto_tls.mode` resolves to `on`), node startup will:
- create forge cert manager
- add forge-managed WSS listen addresses
- use forge TLS config for websocket transport
- announce forge-managed addresses through `AddrsFactory`

Minimal config:

```json
"auto_tls": {
  "mode": "auto",
  "user_email": "ops@example.com",
  "port": 4101,
  "cache_dir": ".autotls-cache",
  "forge_auth": ""
}
```

`auto_tls.mode` values:
- `auto` (default): automatically enable AutoTLS when node is detected as public
- `on`: force enable AutoTLS and attempt cert flow immediately (for first bootstrap node cold-start)
- `off`: force disable AutoTLS
- `port`: dedicated TLS/WebSocket listen port for browser access (separate from `listen` tcp/quic port)

Notes:
- `forge_auth` is optional. Set it only if your forge registration endpoint requires access token.
- AutoTLS is designed for browser-facing secure websocket connectivity.
- Node must be publicly reachable for certificate/domain registration flow to succeed.
- Cert cache lives under `auto_tls.cache_dir`.

## Browser Connectivity

Browser libp2p clients can only use browser transports (for example `ws`/`wss`/`webtransport`).

For reliable browser access:
- use AutoTLS (`wss` on forge domain)
- ensure bootstrap peer is reachable from browser network

## Web Admin

Frontend project is under `web/` (Vue + Vite + Wrangler).

```bash
cd web
npm install
npm run dev
```

Build and deploy:

```bash
npm run build
npm run deploy
```

More details: `web/README.md`

## Troubleshooting

- `database is locked` on SQLite:
  - reduce concurrent write pressure
  - avoid external file watchers that keep hard locks on DB artifacts
- AutoTLS not becoming active:
  - verify public reachability
  - check registration/ACME logs in `journalctl -u p2pos -f`
- service not auto-restarting:
  - verify `Restart=always` exists in `/etc/systemd/system/p2pos.service`
  - run `systemctl daemon-reload && systemctl restart p2pos`
