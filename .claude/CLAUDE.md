# CLAUDE.md — MeshTender

Guide for AI agents working on this codebase. Read this before writing any code.
Rules adapted from CoreScope's AGENTS.md; the emphasis is **performance,
security, testing, and never guessing at MeshCore protocol behavior**.

## Stack

Go single binary, server-rendered HTML, PostgreSQL. No SPA, no JS framework, no
bundler, no build step for assets.

- `cmd/meshtender/` — entry point (flags: `--seed`, `--reset`).
- `internal/web/` — shared HTTP foundation: the `Renderer` (html/template onto a
  base layout), `CommonMiddleware`, the host `Dispatcher`, security headers/CSP,
  static assets (`internal/web/static/`). Deliberately imports no surface package.
- `internal/auth/`, `internal/marketing/`, `internal/core/` — the three host
  surfaces (auth host, public root host, app host). Each embeds its own
  `templates/*.html` and builds a `web.Env`.
- `internal/store/` — PostgreSQL via `pgx`; raw SQL (no ORM/sqlc); goose
  migrations in `internal/store/migrations/`. `internal/testdb` clones an
  ephemeral migrated DB per test.
- `internal/mesh/`, `internal/identity/`, `internal/geo/` — MeshCore packet
  exchange, server identity, geofencing.
- MeshCore binding: `github.com/meshcore-go/meshcore-go`.

Run/lint/etc. via mise: `mise run dev` (serves on the configured hosts),
`mise run lint`, `mise run seed`, `mise run reset`, `mise run e2e`. Dev uses
`leighthaus.dev` + local mkcert TLS. **The dev server owns :8080 — run your own
test instance on :8090** and kill only your own PID.

**When a mise task exists for what you're doing, run the task — don't hand-roll
its command.** The tasks encode the correct setup (env, flags, container
lifecycle) and clean up after themselves. This matters most for `mise run e2e`:
never reimplement the headless-browser container by hand (it leaks orphaned
containers holding :9222). Filter to specific tests with `mise run e2e --run
<regex>` (see `mise run e2e --help`). Plain `go test`/`go build`/`go vet` for a
package or `-run` are fine — those aren't tasks.

**MeshCore, not Meshtastic.** Don't introduce Meshtastic concepts (e.g. a
`node_id`). MeshCore identifies contacts by public key.

## Rules — read these first

### 1. Read the MeshCore firmware/source — never guess at protocol behavior
Anything touching packet format, routing, path/hash encoding, advert flags,
region/flood commands, `setperm`/permissions, firmware variants
(Repeater / Companion / KISS modem), or device behavior is decided by the
**MeshCore firmware (C++)** and the `meshcore-go` library — not by our comments,
not by memory, not by a guess.

- Read `meshcore-go` source: `go list -m -f '{{.Dir}}' github.com/meshcore-go/meshcore-go`.
- For firmware truth, clone it and read the C++:
  `git clone --depth 1 https://github.com/meshcore-dev/MeshCore.git` (key files:
  `src/Mesh.h`, `src/Packet.cpp`, `src/helpers/AdvertDataHelpers.h`,
  `docs/packet_format.md`, `docs/cli_commands.md`).
- Guessing has burned us repeatedly (path hash sizes, direct-vs-flood login,
  `region def`/flood command syntax, KISS-modem-as-transport). Open the source.

### 2. Plan before implementing; understand before fixing
Non-trivial work: present a plan and wait for sign-off before coding (use plan
mode). When something misbehaves, investigate the root cause — read the data, the
source, the firmware — before changing code. Don't "fix" by guessing.

### 3. No commit without tests
Every change that touches logic gets tests. Every bug fix gets a regression test
that reproduces the bug first. Prefer test-first (red → green → refactor). Before
pushing, all of these pass:
```
go build ./...
go vet ./...
gofmt -l internal/    # must print nothing
golangci-lint run     # staticcheck + gosec + correctness (.golangci.yml)
go test ./...         # -race in CI
```
DB-backed tests use `internal/testdb` (ephemeral, per-test). **Never point tests
at the dev DB, and never run TRUNCATE/DROP/reset against it.** CI supplies
`MESHTENDER_TEST_DATABASE_URL` (a throwaway Postgres service).

### 4. Validate UI/interactive changes in a real browser
Server-rendered HTML, WebSerial (console/confirm), Leaflet maps, and the CSP can't
be fully covered by Go tests. For UI or client-JS changes, run the app (on :8090)
and check it in a browser **with the devtools console open** (CSP violations only
show there). If you can't validate it, say so — don't claim it works.

**Browser tests (`mise run e2e`).** Regressions worth locking in get a `chromedp`
test. These live in their own black-box package `internal/e2e/` (one
`<feature>_test.go` per feature, sharing `harness_test.go`) behind a
`//go:build browser` tag, so `go test ./...` never needs a browser — only
`mise run e2e` runs them. That task starts a `chromedp/headless-shell` **Docker
container** (no local Chrome install) and runs the `browser`-tagged suite against
an in-process test server; the browser reaches the server via
`host.docker.internal`, and the harness fails the test on any CSP violation. If
the container isn't up, the tests **skip** (never fail). Reuse the `e2eServer`
harness (`newE2EServer`, `login`, `newRepeater`, `startBrowser`,
`setSessionCookie`) — it authenticates via the public `/session/callback` handoff
and depends only on exported app APIs, so keep it that way (no reaching into
package internals). Give elements a stable `data-testid`/class hook rather than
selecting on Bootstrap layout classes. CI runs these non-gating
(`.woodpecker/e2e.yaml`); `E2E_DEVTOOLS_URL`/`E2E_BROWSER_HOST` override the
addresses there.

**Tabler markup gotchas — the CSS decides the DOM you must write.** A component
can silently demand a particular child structure, and getting it wrong renders
badly without failing anything (no error, no CSP violation, no test). Check the
component's rule in `internal/web/static/tabler.min.css` before writing new
markup for it. The one that has already bitten us:

- **`.alert` is `display:flex; flex-direction:row; gap:1rem`.** Every child node
  becomes a side-by-side COLUMN. So the natural way to write an alert — prose
  with an inline `<code>`/`<strong>` in the middle, or a heading above a list —
  breaks the box into gutters instead of flowing or stacking. **Wrap the whole
  body in a single `<div>`:**
  ```html
  <div class="alert alert-info" role="alert">
    <div>… prose with <code>inline</code> markup …</div>
  </div>
  ```
  The only intentional multi-column case is Tabler's icon layout
  (`<div>{{template "icon-alert" "alert-icon"}}</div>` followed by the body div).
  `TestAlertBodiesAreSingleChild` (`internal/web/alert_layout_test.go`) enforces
  this; it parses the templates structurally, so extend it there if you find
  another flex-container component with the same trap.

- **A `<button>` centers its text.** Invisible on a normal button, wrong the
  moment you use one as a full-width row (`d-flex w-100`, e.g. a `list-group-item`
  that expands a collapse): the label sits centered while every neighboring row is
  left-aligned. **Add `text-start`.** `TestFullWidthFlexButtonsAreTextStart`, in
  the same file, enforces it.

### 5. CI is the gate, not the first line
Woodpecker (`.woodpecker/`): `test` + `lint` + `vuln` (govulncheck) must pass
before `build`; `deploy` follows `build` on `main`. A reachable CVE blocks the
build. Catch problems locally first; if CI catches something you didn't, close the
local-testing gap.

### 6. Git hygiene — stage one batch at a time, don't commit
This is a single-developer project with no PR workflow. **Stage your changes but do
NOT commit or push** — the developer commits from a git GUI. Stage exactly the
files for one logical change with explicit `git add <file> …` (never `-A`/`.`).

**One batch at a time.** When a task spans multiple logical commits, do NOT stage
everything at once — that co-mingles the batches in the single index and makes them
impossible to commit separately. Instead:
1. Stage exactly one batch, give a one-line description the developer can base a
   commit message on, and **stop** — hand off and wait.
2. The developer reviews `git diff --cached` and commits that batch in their GUI.
3. Continue only when the developer says so; then stage the next batch.

Order batches so each is independently green (builds/tests pass) once committed.
Provide file-list `git add` commands only as reference — do not run the next
batch's staging until told to.

## Performance
The product is server-rendered over Postgres; perf work lives mostly in SQL and
request handlers. Before writing code, ask what the worst-case row count is.

- **Bounded, paginated queries.** Lists use keyset pagination (see
  `ListUsersPage`, the org/repeater cursor helpers) — never `SELECT *` an
  unbounded table into memory or a page.
- **No N+1 queries.** Batch or join; don't call the store in a loop per row.
- **Index-aware SQL.** Filter/sort on indexed columns; add an index with the
  migration when a new access path needs one.
- **Maps/Sets for lookups, not nested scans.** O(n²) is fine only when n is small
  and provably bounded (say so in a comment).
- **Cache expensive work and invalidate surgically**, not globally.
- Perf claims need proof — a benchmark, a measurement, or a test that enforces the
  characteristic. "It's faster" without data doesn't count.

## Security
Security is a first-class concern; we run a strict CSP and gate CI on vuln scans.

- **No inline JS.** The CSP forbids inline `on*=` handlers and un-nonce'd inline
  `<script>`. Use the delegated `data-*` handlers in `internal/web/static/ui.js`
  (`data-confirm`, `data-copy`/`-target`/`-prev`, `data-autosubmit`,
  `data-consent-target`, `data-gated`); put page logic in `addEventListener` inside
  a `<script nonce="{{.Nonce}}">` block or a self-hosted `.js`. `TestTemplatesHaveNoInlineJS`
  enforces this — don't work around it.
- **Self-host all assets.** No external scripts/styles/fonts (CSP `*-src 'self'`);
  the only external resource is CARTO map tiles (`img-src`). No SRI needed.
- **html/template autoescapes** — don't defeat it. Reach for `template.HTML`/`JS`/`URL`
  only with server-controlled, non-user data, and comment why.
- Cookies stay `HttpOnly` + `SameSite=Lax` + `Secure` (gated on TLS). State-changing
  actions are POST (SameSite blocks cross-site POST).
- **Never commit secrets or private data** — this includes a public marketing
  surface. No keys, tokens, passwords, personal data, or real IPs.
- Keep dependencies patched; `govulncheck` gates the build.

## Engineering principles

- **DRY.** Before writing new code, grep for an existing implementation. Two copies
  of the same logic = extract one shared function. (We reused `org_links` →
  `user_links`, the QR helper, `validLinkURL`, etc. — follow that instinct.)
- **SOLID / single responsibility.** Small functions that do one thing. Extend via
  parameters/options, not `if caller == "x"` branches inside shared code. Pass
  dependencies in (testable) rather than reaching into globals.
- **YAGNI.** Build the simplest thing that solves the current problem. Delete
  speculative "just in case" code.
- **Boy Scout rule.** When you touch a file and see duplication, dead code, or
  unclear names, clean it up in the same change.
- **Named types at boundaries.** Prefer named Go structs with explicit JSON tags for
  store/domain returns and any JSON API responses. `map[string]any` is fine only as
  a template render payload (`Renderer.Render`'s `data`), not across domain
  boundaries.
- **Cast/null-check at the boundary in JS.** The little client JS we have runs with
  no types — coerce values from the DOM/attributes and guard before method calls.

## Reusable server data → the client (CSP-safe)
When a page needs server values in JS, pass them via `data-*` attributes or a
nonce'd inline `<script>` that assigns a `window.*` global (e.g. `MESHTENDER_WS`,
`SETUP_ORGS`) — then a self-hosted script reads it. Keep shareable UI state
(filters, selected profile, preview location) in the URL/query where reasonable, as
the org directory, config, and map pages already do.
