# MeshTender

A Go web app for tending [MeshCore](https://meshcore.co.uk) meshes. MeshTender holds a single
server-wide MeshCore identity (Ed25519 + X25519). Repeater owners grant it admin by running
`setperm <server_pubkey> 3` on their repeater. Because every action flows through the one server
identity, MeshTender can mediate *which users* may control a repeater — so you can share a repeater
with other people without ever handing out keys.

## How the hardware confirm/control path works

The server owns all MeshCore crypto, but the radio (a MeshCore **KISS modem**) is plugged into the
*user's* computer. So:

```
server  ──(WebSocket, KISS bytes)──▶  browser  ──(WebSerial)──▶  KISS modem  ──(LoRa)──▶  repeater
        ◀──────────────────────────           ◀───────────────              ◀────────────
```

The server builds a signed `AnonReq` login packet, KISS-frames it, and streams the bytes to the
browser, which writes them to the modem over WebSerial. The reply travels back the same way for the
server to decrypt — proving MeshTender can reach the repeater. Confirmation is optional; the
repeater works either way. This is implemented as a custom `hardware.Transport`
(`internal/wsbridge`) over a WebSocket.

## Stack

- Go 1.26, [meshcore-go](https://github.com/meshcore-go/meshcore-go) for the MeshCore protocol/crypto
- Postgres (pgx + goose migrations)
- Accounts are username-only (no email/PII), with an optional display name; passkey auth (WebAuthn
  via `go-webauthn`) with a password fallback (bcrypt); sessions via `scs`
- Sharing is via **single-use share links** — the owner mints one labeled link per person; the
  recipient signs in and accepts (consuming it). No user directory; used links are kept as an audit
  trail showing who accepted
- **Mesh-friendly transmission**: each session uses strictly increasing per-command timestamps (the
  repeater dedupes same-timestamp commands) and a 1-message/second rate limit, so a user can't flood
  the shared LoRa mesh through their modem. The first contact (login) floods with 3-byte routing path
  hashes (reliable propagation); once the repeater's reply reveals the route home, subsequent commands
  use **direct routing** along that path — traversing only those repeaters instead of flooding the
  whole mesh — with automatic fallback to flood if the path goes stale. All sends, retries, timestamps,
  and routing are handled by one `mesh.Exchanger`
- **Command console**: authorized users send firmware CLI commands to a repeater over their modem
  (same WebSocket↔WebSerial bridge as confirm). Owners run anything; shared users run only the
  commands the owner granted them. Every send is recorded in a per-repeater audit log (who/when/ack)
- **Command catalog**: all repeater CLI commands, seeded from the firmware, modeled per-parameter
  (`set.tx`, `set.radio`, …) with a `risky` tag and default-set flags. There is no global on/off —
  the owner can run anything; the flags seed what *others* are offered
- **Instance capabilities** (not tiers): `cap_manage_users` (grant/revoke capabilities) and
  `cap_manage_catalog` (edit the catalog), managed at `/admin`. The first registered account is
  bootstrapped with both. These are separate from per-repeater access (sharing)
- **Organizations** (the trust-first tier above sharing): any user creates an org and becomes its
  admin; orgs are publicly listed and any signed-in user joins from the org page and can be promoted. An org has a **versioned,
  two-tier (admin/member) permission policy**. An owner *contributes* a repeater to an org and
  **consents** to the policy version; effective commands = the org's *current* set ∩ the version the
  owner *consented to*, per the user's tier (admins ⊇ members). Policy additions require the owner to
  re-consent (shown as a diff + changelog); removals apply immediately. Org-admins operate distant
  nodes over the mesh from their own modem. Withdraw / leave revokes access instantly. Members reach
  contributed repeaters from the org page; the org page also shows a **map** of repeater locations.
- **Repeater location** (opt-in): when adding a repeater the owner may consent to storing its lat/lon,
  which is fetched (`get lat`/`get lon`) during the modem test and shown on org maps. Off by default.
- Server-rendered `html/template` + htmx; hand-written JS only for the WebSerial page
- `coder/websocket`

## Running locally

One process serves all three hosts (root, auth, app) over TLS. Dev can't use `*.localhost` —
`localhost` is a public suffix, so a WebAuthn RP ID of `localhost` is rejected from a subdomain and
passkeys won't work — so dev uses a real registrable domain whose subdomains resolve to `127.0.0.1`,
with a locally-trusted [mkcert](https://github.com/FiloSottile/mkcert) certificate. `.env.example`
is preconfigured for `leighthaus.dev`; point its subdomains at `127.0.0.1` (real DNS or
`/etc/hosts`), or swap in your own dev domain by editing the host + `RP_ID`/`RP_ORIGIN` vars there.

```sh
docker compose up -d                 # Postgres

# One-time: trust a local CA and mint a cert for the dev domain + its subdomains.
brew install mkcert && mkcert -install
mkcert -cert-file ./certs/dev.pem -key-file ./certs/dev-key.pem "*.leighthaus.dev" leighthaus.dev

cp .env.example .env                 # then set MESHTENDER_MASTER_KEY=$(openssl rand -hex 32)
set -a; . ./.env; set +a
go run ./cmd/meshtender               # migrates on boot; serves HTTPS on :8080
```

Then open <https://app.leighthaus.dev:8080> (dashboard) or <https://leighthaus.dev:8080> (public
root). WebSerial requires a secure context, which the mkcert HTTPS above provides — a real deployment
must likewise serve the confirm/control pages over HTTPS.

### Configuration (env)

| Var | Purpose |
|---|---|
| `MESHTENDER_DATABASE_URL` | Postgres DSN (required) |
| `MESHTENDER_MASTER_KEY` | 64 hex chars (32 bytes); AES-GCM key encrypting the identity seed at rest (required) |
| `MESHTENDER_ADDR` | listen address (default `:8080`) |
| `MESHTENDER_ROOT_HOST` / `_AUTH_HOST` | host topology — **required** (root = public discovery, auth = sign-in); the server refuses to start without both |
| `MESHTENDER_PRIMARY_HOST` / `_WWW_HOST` | app host and the www→root redirector (`_WWW_HOST` defaults to `www.` + root) |
| `MESHTENDER_TLS_CERT` / `_TLS_KEY` | cert/key for in-process HTTPS (see mkcert above); omit only behind a TLS-terminating proxy |
| `MESHTENDER_RP_ID` / `_RP_ORIGIN` / `_RP_NAME` | WebAuthn relying-party settings (must line up with the hosts) |
| `MESHTENDER_TRUSTED_PROXIES` | proxies whose `X-Forwarded-For`/`X-Real-IP` are trusted when resolving the client IP — comma-separated CIDRs/IPs, or `private` for the RFC1918/link-local/ULA ranges. Loopback is always trusted. Verify with the admin **Reverse proxy test** page. |

> **Note:** `MESHTENDER_MASTER_KEY` is coupled to the stored identity — changing it makes the
> existing `server_identity` row undecryptable. Keep it stable.

## Tests

```sh
go test ./...
```

That's it — **just make sure Docker is running.** DB-backed tests (store queries and the
end-to-end confirm/console round-trips) spin up a throwaway `postgres:17` container automatically
via [testcontainers](https://golang.testcontainers.org/). The harness migrates a single template
database once, then clones a fresh database per test (`CREATE DATABASE … TEMPLATE …`), so every
test gets pristine, isolated state and tears it down when it finishes. No env vars, no manual
database setup, nothing to wipe.

To run against an **existing** Postgres instead of a container (this is how CI reuses its service
container), set `MESHTENDER_TEST_DATABASE_URL` to a DSN on that server. The connecting role needs
`CREATEDB`, and the harness only ever creates/drops its own `mt_tmpl_*` / `mt_test_*` databases:

```sh
MESHTENDER_TEST_DATABASE_URL="postgres://meshtender:meshtender@localhost:5432/postgres?sslmode=disable" \
  go test ./internal/core/ -run TestConfirmRoundTrip -v
```

## Layout

```
cmd/meshtender        main / wiring
internal/config       env config
internal/store        Postgres + goose migrations + queries
internal/identity     load-or-generate server identity, AES-GCM seal at rest
internal/mesh         build AnonReq login packet, decode repeater RESPONSE
internal/auth         WebAuthn + password + sessions
internal/wsbridge     WebSocket ↔ hardware.Transport bridge
internal/web          routing, templates, static assets, confirm orchestration
```

## Known follow-ups

- An abandoned passkey signup leaves an orphan account holding that username (can't log in, blocks
  re-signup). Add cleanup or upsert-on-begin.
- Passkey `navigator.credentials` create/get is only verifiable in a real browser / virtual
  authenticator — not covered by automated tests.
