# P2POS Web Admin

Vue + Vite + Wrangler frontend for browser-side admin operations over libp2p.

Current UI supports:
- connect/disconnect to bootstrap peer via browser libp2p
- load a single `p2pos-admin://...` config bundle (bootstrap + cluster + admin credentials)
- build membership snapshot JSON
- push snapshot over `/p2pos/membership-push/1.0.0`

## Requirements

- Node.js 18+
- npm
- Wrangler (already in `devDependencies`)

## Local Development

```bash
cd web
npm install
npm run dev
```

## Build

```bash
npm run build
```

Output goes to `web/dist/`.

## Deploy (Cloudflare Workers)

```bash
npm run build
npm run deploy
```

The worker serves static assets from `dist/` via the `ASSETS` binding (`wrangler.toml`).

## Bundle Import

Use the single bundle string printed by `install.sh` on first bootstrap install:

`p2pos-admin://<base64-json>`

It contains:
- bootstrap address
- cluster id
- admin private key
- admin proof

## Recommended with Go Node

Use Go node with official AutoTLS enabled:
- node side: `auto_tls.mode=auto` (or `on`)
- node side: set `auto_tls.port` for browser WSS (e.g. `4101`), separate from node `listen` port
- browser side: dial forge-backed `wss` address

This gives stable browser connectivity without manual certificate management.

## Security Notes

- Admin private key is entered client-side and used in browser memory.
- No backend session or persistent secret storage is required by this frontend.
- Protect browser environment and avoid loading untrusted extensions when operating admin keys.
