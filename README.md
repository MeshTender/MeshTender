# MeshTender

A Go web app for tending [MeshCore](https://meshcore.io/) meshes — sharing repeater
administration with other people, keeping a site's documentation and maintenance history with the
node instead of in someone's head, and publishing an organization's recommended configuration so
every repeater in a mesh is set up the same way.

MeshTender holds a single server-wide MeshCore identity (Ed25519 + X25519). A repeater owner grants
it admin by running `setperm <server_pubkey> 3` on their repeater. Because every action flows
through that one server identity, MeshTender can mediate *which users* may control a repeater — so
a repeater can be shared without ever handing out keys, and access can be revoked instantly.

## Why it holds full admin, and why you shouldn't host your own

MeshTender asks for the strongest access a MeshCore repeater can grant. That's forced by the
protocol rather than chosen for convenience, and it shapes who should be running the server — and,
in turn, why this is AGPL rather than the MIT license it would otherwise carry. Worth reading before
you deploy a copy of your own.

### The permission problem it exists to solve

MeshCore's repeater ACL has four roles — guest (0), read-only (1), read-write (2), admin (3) — but
only one of them can run commands. A remote CLI command is processed if and only if the sender is
admin (`if (type == PAYLOAD_TYPE_TXT_MSG && … && client->isAdmin())`, `simple_repeater/MyMesh.cpp`).
There is no "may set transmit power but not rewrite the region table" role, and guest is available
to anyone with a blank password. So any tool that runs commands on your repeater on your behalf
holds **full admin**, because nothing weaker can do anything at all.

MeshTender's whole job is to put the missing permission system on top of that single grant: a
per-user command allowlist that denies by default, a per-org ceiling the repeater owner opts into,
an audit log of who ran what and when, and revocation that takes effect immediately without
touching the hardware. An org can share repeater administration under limits MeshCore itself cannot
express.

### Why you shouldn't host your own

Those limits are enforced by the server, which means whoever runs the server is *outside* them:
they hold the key, and every restriction is theirs to lift. That is unavoidable, and it decides
who should be running it.

If an org's admins host their own instance, then that org's members are back to trusting those
admins with unrestricted access to their repeaters — the exact situation MeshTender exists to fix,
now with a UI implying the restrictions are real. The guarantee only holds when whoever runs the
server isn't a party to the trust question the software is meant to settle — which, for an org's
own repeaters, its own admins always are. For most MeshCore projects self-hosting is the obviously
correct answer; here it quietly removes the point of the software.

### Which means trusting me, so here is what that is worth

I'm aware this is a hard sell, and it should be. What I can offer:

- **Distance.** Administering a repeater takes a modem within LoRa range of it. I'm not near your
  mesh, so the identity key on my server is not usefully exploitable by me except *through this
  app* — where it is logged.
- **The audit log.** Every command carries who sent it and when, per repeater, visible to the
  people who granted access.
- **Revocation on the hardware.** You granted access on the device with `setperm`, and you can
  withdraw it there the same way, whatever the server does. See the [docs](https://meshtender.com/docs).
- **Open source.** The thing above is only checkable if you can read what's running, which is why
  this repository exists.
- **Reproducible builds.** Rebuild the commit `/version` reports and confirm the digest matches, so
  "the source is public" and "the source is what's deployed" are separate, checkable claims. See
  [Verifying a build](#verifying-a-build).

None of that is proof. Together it's the strongest position I know how to take given a protocol
with no permissions.

AGPL closes the remaining hole. If someone does run a modified copy as a service, section 13
requires that its users can get *its* source — so their instance can be audited exactly the way
this one can, rather than being an unauditable server wearing familiar software's clothes. The
mechanics of that, and what a fork has to change, are in
[License, self-hosting, and forking](#license-self-hosting-and-forking).

## How the hardware control path works

The server owns all MeshCore crypto, but the radio (a MeshCore **KISS modem**) is plugged into the
*user's* computer. So:

```
server  ──(WebSocket, KISS bytes)──▶  browser  ──(WebSerial)──▶  KISS modem  ──(LoRa)──▶  repeater
        ◀──────────────────────────           ◀───────────────              ◀────────────
```

The server builds a signed `AnonReq` login packet, KISS-frames it, and streams the bytes to the
browser, which writes them to the modem over WebSerial. The reply travels back the same way for the
server to decrypt. Nothing is stored on the user's machine and no key ever leaves the server; the
modem is only a transport. This is a custom `hardware.Transport` (`internal/wsbridge`) over a
WebSocket.

**Mesh-friendly transmission.** Each session uses strictly increasing per-command timestamps (the
repeater dedupes same-timestamp commands) and a 1-message/second rate limit, so a user can't flood
the shared LoRa mesh through their modem. The first contact (login) floods with 3-byte routing path
hashes for reliable propagation; once the repeater's reply reveals the route home, subsequent
commands use **direct routing** along that path — traversing only those repeaters instead of
flooding the whole mesh — with automatic fallback to flood if the path goes stale. Sends, retries,
timestamps, and routing all live in one `mesh.Exchanger`.

There is also a **serial setup path** for a brand-new node: the browser talks the repeater's own
plain-text CLI directly over USB. The repeater's private key is generated in the browser and never
sent to the server — only its public key is (`internal/core/repeater_setup.go`).

## What it does

**Repeaters.** Add a node (over LoRa via a modem, or from scratch over USB serial), send firmware
CLI commands from the **console**, and read the per-repeater audit log of who sent what and when.
Location is opt-in per repeater, as is appearing on an org's public map, as is publishing a
read-only public page at the repeater's stable public ID.

**Registry.** Each repeater carries site **documentation** (a public section and an internal one)
and a **maintenance history** anyone with access can log. Ownership can be **transferred** to a
designated steward, so the docs, history, and public URL survive the original builder moving on.

**Sharing.** Owners create single-use, labeled, expiring **invite links** — one per person. The
recipient signs in and accepts, consuming the link; used links stay as an audit trail. There is no
user directory. Each shared person gets an explicit per-command allowlist (deny by default).

**Organizations.** Any user creates an org and becomes its admin; orgs are publicly listed and any
signed-in user can join and be promoted. A repeater participates in every org its owner belongs to
unless the owner excludes it, and the owner can optionally restrict which commands a given org may
run on a given repeater. Effective permissions = the site catalog's per-tier ceiling ∩ the owner's
optional per-repeater opt-in ∩ the caller's tier (admin ⊇ member). Orgs also get a description,
links, markdown content, a repeater map, and optionally a verified custom domain.

**Configuration ("desired state").** An org publishes named **profiles** (base settings) plus
**regions** — a named hierarchy of geofences that compile into MeshCore `region def` chains, with
per-region and root flood policy (`region allowf` / `region denyf`). A repeater's coordinates select
every region whose polygon contains it. The console's "apply organization configuration" flow shows
the resulting command list — profile steps plus region commands — marking any line the user can't
run and why. Regions are drawn on a Leaflet/Geoman map editor.

**Command catalog.** Every repeater CLI command, seeded from the firmware and modeled per-parameter
(`set.tx`, `set.radio`, …) with feature/operation grouping, a `risky` tag, and per-tier org flags.
An owner can always run anything on their own node; the flags are the ceiling for everyone else.

**Accounts.** Username-only (no required email), an optional public profile at `/u/{username}`
(display name, bio, links — not indexed), timezone,
passkeys (WebAuthn via `go-webauthn`) with a bcrypt password fallback, sessions via `scs`. Email is
optional and used only for recovery (verification + password reset) via Resend; without a
`MAIL_FROM` the whole feature is hidden, and without an API key messages are logged instead of sent.

**Admin.** Site-wide capabilities (`cap_manage_users`, `cap_manage_catalog`) — the first registered
account is bootstrapped with both — plus first-party traffic analytics (no third party, no PII;
visitors counted by a daily-rotating salted hash), CSP violation reports, a reverse-proxy test page,
and encrypted export/restore of the server identity.

## Stack

Go single binary, server-rendered `html/template` + htmx, PostgreSQL. No SPA, no bundler, no asset
build step — migrations, templates, and static assets are all `go:embed`ed.

- Go 1.26.7 (pinned exactly — see "Verifying a build"), [meshcore-go](https://github.com/meshcore-go/meshcore-go)
  for the MeshCore protocol/crypto
- Postgres via `pgx` with raw SQL (no ORM) and goose migrations; `chi` router; `coder/websocket`
- Hand-written JS only where the platform demands it (WebSerial, Leaflet maps, small delegated
  handlers in `ui.js`)

### Vendored front-end assets

The strict CSP forbids external scripts/styles (`*-src 'self'`), so every third-party front-end
library is **self-hosted** in `internal/web/static/` and embedded into the binary. They're served
content-hash fingerprinted with a one-year `immutable` Cache-Control and pre-compressed (gzip +
brotli) by the asset manifest in `internal/web/assets.go`. The only external resource anywhere is
CARTO map tiles, allowlisted in `img-src`. Those tiles take a CARTO API key
(`MESHTENDER_CARTO_KEY`), sent as `?key=` on each tile request; without it CARTO watermark the
tiles. The browser fetches tiles directly, so the key is served in the page.

Which libraries, at which versions, from which upstream artifact, is recorded once in
`internal/licenses/manifest.go` — with a SHA-256 per file — and rendered into
`THIRD-PARTY-NOTICES.md`. That manifest is the list; it isn't duplicated here, because a second
copy would only go stale. Its tests fail if a file in `internal/web/static/` is neither declared
there nor listed as first-party, so a new library can't slip past the audit.

**Updating one:** fetch the minified build from jsdelivr (mirrors npm exactly), e.g.
`https://cdn.jsdelivr.net/npm/htmx.org@<version>/dist/htmx.min.js`, and overwrite the file (keep the
filename). **Strip any trailing `sourceMappingURL` comment** — we don't self-host `.map` files, so
it only 404s with devtools open — and **keep the copyright banner** minifiers like to drop; MIT and
BSD require it. Then update the version and hash in the manifest, run `mise run licenses --update`,
and validate with `mise run e2e`.

## Running locally

One process serves all hosts (root, www, auth, app) on one port. Dev can't use `*.localhost` —
`localhost` is a public suffix, so a WebAuthn RP ID of `localhost` is rejected from a subdomain and
passkeys won't work — so dev uses a real registrable domain whose subdomains resolve to `127.0.0.1`,
with a locally-trusted [mkcert](https://github.com/FiloSottile/mkcert) certificate. `.env.example`
is preconfigured for `leighthaus.dev`; point its subdomains at `127.0.0.1` (real DNS or
`/etc/hosts`), or swap in your own dev domain by editing the host + `RP_ID`/`RP_ORIGIN` vars there.

```sh
docker compose up -d                 # Postgres

# One-time: trust a local CA and create a cert for the dev domain + its subdomains.
brew install mkcert && mkcert -install
mkcert -cert-file ./certs/dev.pem -key-file ./certs/dev-key.pem "*.leighthaus.dev" leighthaus.dev

cp .env.example .env                 # then set MESHTENDER_MASTER_KEY=$(openssl rand -hex 32)
mise run dev                         # migrates on boot; serves HTTPS on :8080
```

Then open <https://app.leighthaus.dev:8080> (dashboard) or <https://leighthaus.dev:8080> (public
root). WebSerial requires a secure context, which the mkcert HTTPS provides — the same reason
meshtender.com is served over HTTPS.

`mise run seed` fills the database with realistic fake data; `mise run reset` truncates everything
except users with credentials, passkeys, sessions, and the server identity. (Both are `go run
./cmd/meshtender --seed|--reset` under the hood.)

### mise tasks

| Task | What it does |
|---|---|
| `mise run dev` | run the server |
| `mise run lint` | `golangci-lint run` (staticcheck + gosec, config in `.golangci.yml`) |
| `mise run seed` / `reset` | seed fake data / truncate non-account data |
| `mise run e2e` | browser tests in a throwaway headless-shell container (`--run <regex>` to filter) |
| `mise run licenses` | audit dependency licenses (`--update` rewrites `THIRD-PARTY-NOTICES.md`) |
| `mise run image` | build the OCI image with ko and print its digest (`--load` to run it locally) |

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
| `MESHTENDER_MAIL_FROM` / `_MAIL_REPLY_TO` | enables the optional recovery-email feature; unset ⇒ no email UI at all |
| `MESHTENDER_RESEND_API_KEY` | enables real delivery; unset ⇒ messages are logged to stderr instead (the dev default) |
| `MESHTENDER_IMAGE_DIGEST` | the image digest this server runs as, reported by `/version` (see [Verifying a build](#verifying-a-build)). Set by the deploy; unset when running from source. A malformed value is a startup error |

> **Note:** `MESHTENDER_MASTER_KEY` is coupled to the stored identity — changing it makes the
> existing `server_identity` row undecryptable. Keep it stable, and keep a copy of the admin
> identity export (which stays sealed under this key).

Cross-host cookie and session rules are documented in [`docs/auth-cross-host.md`](docs/auth-cross-host.md).

## Tests

```sh
go test ./...
```

That's it — **just make sure Docker is running.** DB-backed tests spin up a throwaway `postgres:17`
container automatically via [testcontainers](https://golang.testcontainers.org/). `internal/testdb`
migrates a single template database once, then clones a fresh database per test
(`CREATE DATABASE … TEMPLATE …`), so every test gets pristine, isolated state and can run in
parallel. No env vars, no manual setup, nothing to wipe.

To run against an **existing** Postgres instead of a container, set
`MESHTENDER_TEST_DATABASE_URL` to a DSN on that server. The role needs `CREATEDB`,
and the harness only ever creates/drops its own `mt_tmpl_*` / `mt_test_*` databases:

```sh
MESHTENDER_TEST_DATABASE_URL="postgres://meshtender:meshtender@localhost:5432/postgres?sslmode=disable" \
  go test ./internal/core/ -run TestConsole -v
```

**Browser tests** live in `internal/e2e/` behind a `//go:build browser` tag, so `go test ./...`
never needs a browser. `mise run e2e` starts a `chromedp/headless-shell` container (no local Chrome
install), runs the suite against an in-process server, and fails on any CSP violation. If the
container isn't up, they skip rather than fail.

## Third-party licenses

Every dependency has to be one we can actually comply with when we ship a binary and an image, so
dependencies are limited to permissive licenses — the allowed set is `AllowedSPDX` in
`internal/licenses/manifest.go`, and copyleft terms (GPL, LGPL, AGPL, MPL, SSPL) are out, including
in the test tree. Attribution obligations are met by `THIRD-PARTY-NOTICES.md` and by keeping the
copyright banners in the vendored front-end files.

MeshTender is itself AGPL-3.0, which sounds like a contradiction and isn't: licensing copyleft
*out* doesn't oblige us to accept copyleft *in*. A GPL or AGPL dependency compiled into the binary
would end the sole copyright holder's ability to release MeshTender under any other terms later,
and LGPL/MPL attach per-file obligations that fit a statically linked, fully embedded Go binary
badly. The full reasoning sits at `AllowedSPDX`.

`mise run licenses` enforces it: it scans the whole Go module graph (binary, test, and
`browser`-tagged) with `google/licensecheck`, plus the manifest of things Go tooling can't see —
vendored front-end files, icon artwork, the base image, external services — and fails if anything
falls outside the allowed set or if `THIRD-PARTY-NOTICES.md` has drifted.

## Building the image

There is no Dockerfile. MeshTender is a pure-Go single binary with everything embedded, so
[ko](https://ko.build) compiles it and lays it straight onto a base image — no build context and no
Docker daemon. Configured in `.ko.yaml`.

```sh
mise run image          # build and print the image digest (pushes nothing)
mise run image --load   # load into the local Docker daemon so you can run it
```

## Verifying a build

The published image is **reproducible**: you can rebuild it and confirm you get the digest we
shipped, rather than taking our word that the image matches this source. Every build input is
pinned — the Go toolchain (`GOTOOLCHAIN` in `.config/mise/config.toml`), the ko version, and the
base image *by digest* rather than by its floating `:nonroot` tag — and ko zeroes layer timestamps.
`TestReleasePinsAreConsistent` and `TestBaseImageIsPinnedByDigest`
(`internal/licenses/reproducible_test.go`) fail the build if any of those pins drift apart.

To check what's actually running, start from **`GET /version`** — unauthenticated, because the
people best placed to check our work are the ones without an account:

```console
$ curl -s https://meshtender.com/version
{
  "commit": "62e30036ee0bfb28f6c1a4a3f5ac5f4a52e4b1c9",
  "commitTime": "2026-08-06T17:47:49-04:00",
  "modified": false,
  "go": "go1.26.5",
  "os": "linux",
  "arch": "amd64",
  "executableSHA256": "9f2c…",
  "imageDigest": "sha256:a41b…",
  "source": "https://github.com/MeshTender/MeshTender",
  "license": "AGPL-3.0-only"
}
```

`source` and `license` are the AGPL section 13 source offer in machine-readable form
(the footer of every page carries the same link). Unlike the fields below them they're
compiled-in constants rather than measured facts — an assertion by whoever built the
binary, which `commit` then lets you check.

Then rebuild that commit for that platform and compare digests:

```sh
git clone https://github.com/MeshTender/MeshTender && cd MeshTender
git checkout 62e30036ee0bfb28f6c1a4a3f5ac5f4a52e4b1c9   # the commit /version reported
mise install                                            # installs the pinned Go and ko
mise run image --platform linux/amd64                   # the os/arch /version reported
```

The printed `sha256:…` should equal `imageDigest`. The registry name is not part of a digest, so
this works without any access to our registry — you never have to pull anything of ours.

What each field is worth is deliberately different, and worth knowing when you audit:

| Field | Attested by |
|---|---|
| `commit`, `commitTime`, `modified`, `go`, `os`, `arch` | The Go toolchain, stamped at compile time. Our code doesn't choose these. |
| `executableSHA256` | Measured at runtime, by the process itself, over the file it is running from. The only field about the *running* process rather than about a build. To check it, extract `/ko-app/meshtender` from your own build and hash it. |
| `imageDigest` | Our pipeline. A binary can't derive its own image digest — the digest is computed over the binary — so CI captures it at publish time (`ko build --image-refs`) and the deployment passes it back in as `MESHTENDER_IMAGE_DIGEST`, deploying by that digest rather than by a tag. Treat it as a claim to check, not as proof. |

A build from a modified tree reports `"modified": true`, and its `commit` does **not** describe the
source it was built from — such a build can't be reproduced from that commit, by anyone. Admins see
the same data plus copy-paste reproduction commands at `/admin/build`.

Two things change the digest, and both are intentional:

- **The checkout must be clean and at the exact commit.** Go stamps the commit SHA, the commit
  time, and a dirty-tree flag into the binary, so an edited tree produces a different digest. That
  binds the image to a specific commit — but it does mean `git status` must be empty first.
- **The platform must match.** `mise run image` targets `linux/amd64`; pass
  `--platform linux/arm64` to verify that variant.

## License, self-hosting, and forking

MeshTender is free software under the **GNU AGPL v3.0** ([`LICENSE`](LICENSE)). Its **name and
logo are not** ([`TRADEMARKS.md`](TRADEMARKS.md)). Why that pairing, rather than the MIT license
this project would otherwise carry, is the argument at the top:
[Why it holds full admin](#why-it-holds-full-admin-and-why-you-shouldnt-host-your-own).

Self-hosting is permitted and this file isn't going to pretend otherwise — the case above is an
argument, not a restriction. If you run a modified copy, the AGPL asks you to publish your source
and offer it to your users — `SourceURL` in `internal/web/version.go` is the constant behind the
footer link and the `/version` field, so point it at your fork. Separately, the MeshTender name and
mark are reserved, so your instance needs its own: [`TRADEMARKS.md`](TRADEMARKS.md) says exactly
what to change.

Contributions aren't being accepted yet — the licensing questions that come with them (inbound
terms, and whether relicensing later stays possible) haven't been settled.

## Layout

```
cmd/meshtender       main / wiring; --seed and --reset
cmd/licenses         the license auditor behind `mise run licenses`
internal/config      env config
internal/store       Postgres: goose migrations + raw SQL queries
internal/testdb      ephemeral per-test database (template clone)
internal/identity    load-or-generate server identity, AES-GCM seal at rest, backup envelope
internal/mesh        AnonReq login packet, RESPONSE decode, exchanger (routing, retries, rate limit)
internal/wsbridge    WebSocket ↔ hardware.Transport bridge to the browser's KISS modem
internal/geo         GeoJSON containment/overlap for region geofences
internal/web         HTTP foundation: renderer, middleware, host dispatcher, CSP, static assets
internal/auth        auth host: WebAuthn + password + sessions + account management
internal/marketing   root host: landing, docs, legal, public org/repeater discovery
internal/core        app host: repeaters, console, orgs, configuration, sharing, admin
internal/analytics   first-party request tracking + daily rollups
internal/mail        Resend delivery, with a logging fallback
internal/licenses    dependency license audit + non-Go manifest + reproducibility pins
internal/seed        fake data for local testing
internal/e2e         chromedp browser tests (build tag `browser`)
```
