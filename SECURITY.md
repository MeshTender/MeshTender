# Security policy

MeshTender holds full administrative access to other people's radio hardware. A
repeater owner grants that access with `setperm <server_pubkey> 3`, because
MeshCore has no weaker role that can run commands at all — so every permission
limit a user sees in MeshTender is enforced *by this software*, not by the device.
That makes a security bug here more consequential than the size of the project
suggests, and it's why this file exists.

## Reporting a vulnerability

**Please report privately first.** Two routes, either is fine:

- **GitHub private vulnerability reporting** — the "Report a vulnerability"
  button under this repository's Security tab. Preferred: it keeps the discussion,
  the fix, and the advisory in one place.
- **Email** — <security@meshtender.com>.

Please don't open a public issue for something exploitable, and don't post it to a
MeshCore channel or chat. A public report on this particular application tells
everyone how to reach other people's repeaters before there is a fix.

Useful to include, in rough order of value:

1. What an attacker can do, in one sentence.
2. Steps to reproduce, or a proof of concept.
3. The build you tested — `GET /version` reports the commit, or name your local
   commit if you ran it yourself.
4. Whether you've told anyone else.

You'll get an acknowledgment within **3 days**. This is a one-person project, so a
fix timeline depends on the finding; I'll tell you what it looks like rather than
leaving you guessing, and I'll let you know when it's deployed.

## What matters most here

Findings in these areas are the ones this application exists to prevent, and I
want them most:

- **Running commands you weren't granted** — any path that reaches a repeater
  outside the per-user allowlist, the per-org ceiling, or the owner's per-repeater
  opt-in. The effective-permission intersection is the product's core promise.
- **The server identity** — anything that extracts, exports, or uses the Ed25519 /
  X25519 seed, including through the encrypted backup envelope or the admin
  export/restore flow.
- **Impersonation or session flaws** — the cross-host handoff between the auth, app,
  and root surfaces (`docs/auth-cross-host.md`), passkey and password flows, or
  account recovery by email.
- **Audit log integrity** — commands that execute without being attributed, or that
  can be attributed to someone else. The log is what makes trusting a single
  hosted instance defensible.
- **Escaping the browser's constraints** — a CSP bypass, stored or reflected XSS,
  or anything that turns the WebSerial console into a way to reach a modem the
  user didn't intend.
- **A build that doesn't match its source** — if you rebuild the commit `/version`
  reports and get a different image digest, that is a report I want, not a
  curiosity. See "Verifying a build" in the README.

## Scope

**In scope:** this source tree, and the hosted service at meshtender.com.

**Out of scope**, in the sense that I can't fix it here:

- **MeshCore firmware and protocol.** The lack of granular repeater permissions is
  the upstream design this app works around, not a bug in it; the same goes for
  packet format, routing, and radio behavior. Report those to
  [MeshCore](https://github.com/meshcore-dev/MeshCore).
- **Third-party dependencies.** Report upstream — but do tell me if MeshTender is
  exploitable through one, because that's mine to mitigate.
- Scanner output with no demonstrated impact, missing headers that aren't
  exploitable, or best-practice advice unattached to a finding.
- Social engineering, physical access to hardware, and anything requiring you to
  already control a user's device or radio.

## Testing, and what not to do

You're welcome to test against **your own account and your own repeaters**. Please
don't:

- Touch data, repeaters, or accounts that aren't yours.
- Run denial-of-service or load tests against meshtender.com.
- Transmit in ways that break the radio regulations where you are — spectrum is
  shared, and a mesh is other people's infrastructure.
- Automate account creation or send bulk mail through the recovery flow.

Stay inside that and I'll treat your testing as authorized, won't pursue anything
over it, and will work with you on the fix. If you're unsure whether something
crosses the line, ask first — <security@meshtender.com>.

## Disclosure

Coordinated. Publish whenever you like once a fix is deployed, or after 90 days if
one isn't — tell me if you plan to, so users hear about it from me at the same
time. I'll credit you in the advisory and release notes under whatever name you
prefer, or not at all if you'd rather.

There is no bug bounty. This is a free service run by one person, and I'd rather be
straight about that than imply a payout that isn't coming.

## Supported versions

The deployed instance tracks `main`, and that is the only version receiving fixes.
If you run your own copy, update it — and note that a fork is responsible for its
own users, including telling them about a vulnerability you inherited from here.
