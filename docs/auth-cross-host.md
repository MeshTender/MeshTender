# Cross-host identity & sessions

MeshTender serves three kinds of host from one binary (`web.Dispatcher`):

- **auth host** — login/signup/WebAuthn (`internal/auth`)
- **app host** + **custom org domains** — the authenticated product (`internal/core`)
- **root host** — public marketing & org/repeater discovery (`internal/marketing`)

Cookies are **host-only by design** (`__Host-` prefix over HTTPS, no `Domain`
attribute — see `internal/auth/service.go`). Each host gets its own independent
session; one host's cookie is never sent to another. This file records the rules
that let the public discovery surface show logged-in-aware UI (e.g. "you're
already a member — open it") **without** weakening that isolation. Do not break
these without revisiting the whole model.

## Rules

1. **Never widen cookie scope.** No `Domain` attribute; keep the `__Host-`
   prefix over HTTPS. Identity is propagated by giving each host its *own*
   host-only cookie, not by sharing one.

2. **The root cookie is a minimal, read-only identity beacon — not a session.**
   It carries only `login_id` (and a cached `user_id` for rendering). It grants
   no powers. Never cache facts like org membership in it; resolve those live
   per-request (e.g. `Store.OrgRole`) so they can't go stale.

3. **The root surface is strictly side-effect-free GET.** No state-changing
   request of *any* method lives on the root host — all mutations stay on the
   app host. This is what makes CSRF on root a non-issue: there is nothing to
   forge. A side-effecting GET (logout link, a `/join` that actually joins,
   identity-tied writes) breaks this rule.

4. **No credentialed CORS on personalized responses.** SSR is the only
   personalization path for the root surface. Never serve a personalized
   endpoint with `Access-Control-Allow-Credentials: true` + a permissive
   origin — that's what lets a cross-origin attacker read a viewer's identity.

5. **The beacon must be authenticated.** The root identity cookie is set only in
   exchange for a valid single-use handoff code minted for the currently
   logged-in app user (reuse `CreateAuthCode`/`ConsumeAuthCode`). Never "set a
   cookie for whoever calls the endpoint" — that is login-CSRF / fixation.

6. **XSS hygiene on root** (GET-only does *not* mitigate XSS — it only widens
   the payoff of one): `html/template` autoescaping only, zero `template.HTML`/
   raw HTML on the root surface, keep `HttpOnly` on all session cookies, and
   serve a CSP (no inline script, locked-down `connect-src`). Blast radius stays
   on root because host-only cookies stop any pivot to the app session.

## Global logout model

A parent **`logins`** row is the source of truth for a login (`id`, `user_id`,
`created_at`, `revoked_at`, optional device label). Every per-host session (app,
auth, root beacon, custom org domains) stores `login_id` in its scs `data` blob
and is validated against the parent on each request.

- **Logout = one write:** set `revoked_at` on the `logins` row. Every host drops
  to anonymous on its next request (`ValidateSession` destroys any session whose
  backing login is revoked) — including custom org domains, which the old
  redirect-chain logout never covered.
- Per-device logout = revoke one row; "log out everywhere" = revoke all of a
  user's rows.
- Do **not** add per-host token columns to the scs `sessions` table — scs
  resolves a cookie by its single `token` PK, and the host set is unbounded
  (every custom domain is another host).

### Logout is a per-host POST, never a cross-host GET chain

Sign-out is a **POST** on the host that holds the session (app, auth, or a custom
org domain). That handler revokes the shared login row and lands the visitor on
the public root. Because a single real sign-in maps to exactly one login row
(the handoff callbacks reuse it via `loginWithID`), revoking it once on *any* of
those hosts drops all of them on their next request — so there is **no** redirect
chain from the app host to an auth-host `/logout`. The auth host's own POST
`/logout` still exists to cover the auth-local case: a visitor who authenticated
on the auth host (e.g. for account settings) with no app session signs out there
directly.

`/logout` is **POST-only** on every host — state-changing actions are POST
(rule 3's spirit applied everywhere), so a forged cross-site GET like
`<img src=".../logout">` cannot sign anyone out. The template picks the right
target via `LogoutURL` (relative `/logout` on mutating surfaces); the **root
host** is side-effect-free GET and therefore has no `/logout` at all — its chrome
hides the sign-out control and the user signs out from the app dashboard.
