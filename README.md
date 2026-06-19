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
  admin; others join via a multi-use member link and can be promoted. An org has a **versioned,
  two-tier (admin/member) permission policy**. An owner *contributes* a repeater to an org and
  **consents** to the policy version; effective commands = the org's *current* set ∩ the version the
  owner *consented to*, per the user's tier (admins ⊇ members). Policy additions require the owner to
  re-consent (shown as a diff + changelog); removals apply immediately. Org-admins operate distant
  nodes over the mesh from their own modem. Withdraw / leave revokes access instantly.
- Server-rendered `html/template` + htmx; hand-written JS only for the WebSerial page
- `coder/websocket`

## Running locally

```sh
docker compose up -d                 # Postgres
cp .env.example .env                 # then set MESHTENDER_MASTER_KEY=$(openssl rand -hex 32)
set -a; . ./.env; set +a
go run ./cmd/meshtender               # migrates on boot, serves http://localhost:8080
```

WebSerial requires a secure context: it works on `http://localhost` for dev, but a real deployment
must serve the confirm/control pages over HTTPS.

### Configuration (env)

| Var | Purpose |
|---|---|
| `MESHTENDER_DATABASE_URL` | Postgres DSN (required) |
| `MESHTENDER_MASTER_KEY` | 64 hex chars (32 bytes); AES-GCM key encrypting the identity seed at rest (required) |
| `MESHTENDER_ADDR` | listen address (default `:8080`) |
| `MESHTENDER_RP_ID` / `_RP_ORIGIN` / `_RP_NAME` | WebAuthn relying-party settings |
| `MESHTENDER_RADIO_FREQ_HZ` / `_BW_HZ` / `_SF` / `_CR` | default LoRa params suggested when adding a repeater |

> **Note:** `MESHTENDER_MASTER_KEY` is coupled to the stored identity — changing it makes the
> existing `server_identity` row undecryptable. Keep it stable.

## Tests

```sh
go test ./...                         # unit tests (crypto round-trip, seal/open) — no DB needed
```

The end-to-end confirm round-trip (browser + modem + repeater simulated in-process) is gated on a
**dedicated** test database, since it truncates all tables. The database name **must end in
`_test`** — the test refuses to run otherwise, so it can never wipe your dev data:

```sh
docker exec <pg-container> psql -U meshtender -c 'CREATE DATABASE meshtender_test OWNER meshtender;'
MESHTENDER_TEST_DATABASE_URL="postgres://meshtender:meshtender@localhost:5432/meshtender_test?sslmode=disable" \
  go test ./internal/web/ -run TestConfirmRoundTrip -v
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
