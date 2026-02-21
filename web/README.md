# P2POS Web Admin

Vue + Vite + Wrangler frontend for browser-side admin operations over libp2p.

Current UI supports:
- connect/disconnect to bootstrap peer via browser libp2p
- load admin private key + admin proof
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

## Bootstrap Address Requirements

Browser libp2p requires browser transports. Your bootstrap multiaddr must contain:
- `/ws`
- `/wss`
- or `/webtransport`

The current UI enforces this at connect time.

## Recommended with Go Node

Use Go node with official AutoTLS enabled:
- node side: `auto_tls.enabled=true`
- browser side: dial forge-backed `wss` address

This gives stable browser connectivity without manual certificate management.

## Security Notes

- Admin private key is entered client-side and used in browser memory.
- No backend session or persistent secret storage is required by this frontend.
- Protect browser environment and avoid loading untrusted extensions when operating admin keys.
