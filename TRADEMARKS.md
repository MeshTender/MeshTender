# MeshTender brand and trademark policy

MeshTender's **source code** is free software under the GNU Affero General Public
License v3.0 (see [`LICENSE`](LICENSE)). Its **name and visual identity** are not.
This file says exactly which is which, and what you have to do if you run your own
copy.

Nothing here restricts your rights under the AGPL. You may use, study, modify,
and redistribute the code, and run it as a network service, on the terms in
`LICENSE`. What you may not do is present your copy as though it were MeshTender.

## What is reserved

The following are not licensed to you by the AGPL grant, and no right to use them
is granted by this file either:

- The name **MeshTender**, and confusingly similar names.
- The MeshTender wordmark and logo mark, in any format, colorway, or tracing —
  including the copies committed to this repository:
  - `internal/web/templates/brand.html` (the `icon-logo` mark)
  - `internal/web/static/favicon.svg`
- The domain **meshtender.com** and its subdomains.

The mark files are committed here on purpose, and it is worth explaining why,
because it looks like a contradiction. The published container image is meant to
be reproducible: anyone can rebuild the commit that `GET /version` reports and
check that they get the digest we shipped (see "Verifying a build" in the README).
That check only means something if every input to the image is in this repository.
Injecting the artwork at deploy time from somewhere private would break the one
mechanism that lets an outsider verify what the hosted instance is running — which
is a far more valuable thing than a tidy licensing boundary. So the artwork ships,
and the carve-out is legal rather than physical.

This is permitted by the license, not a hole in it. AGPLv3 section 7 lets a
copyright holder add terms "declining to grant rights under trademark law for use
of some trade names, trademarks, or service marks" (§7(e)) and "requiring that
modified versions of such material be marked in reasonable ways as different from
the original version" (§7(c)). That is all this file does.

## What is licensed to you

Everything else in the repository, under the AGPL: all Go source, SQL migrations,
templates, first-party CSS and JavaScript, and documentation. Third-party
dependencies carry their own permissive licenses, recorded in
[`THIRD-PARTY-NOTICES.md`](THIRD-PARTY-NOTICES.md).

## If you run a modified copy

You must:

1. **Rename it.** Remove the MeshTender name from everything a user or operator
   can see — page titles, navigation, emails, docs, the `/version` endpoint. The
   name lives in the templates and the Go source rather than in configuration, so
   this means editing them; `grep -rn MeshTender` finds every occurrence.
2. **Remove the mark.** Delete or replace the two artwork files listed above. Do
   not recolor, trace, or adapt them.
3. **Publish your source.** AGPL section 13 requires that users interacting with
   your instance over a network be able to get the source it is running, including
   your modifications. Point `SourceURL` in `internal/web/version.go` at your fork:
   it is what the footer link on every page and the `/version` endpoint report, so
   leaving it as ours would advertise source you are not running.
4. **Keep the attribution.** Say somewhere visible that your service is based on
   MeshTender, with a link to <https://github.com/jleight/meshtender>. Copyright
   notices in the source stay as they are (AGPL §5(a)).

You may not imply that your instance is run, endorsed, reviewed, or supported by
the MeshTender project or its maintainer.

## What you may do without asking

- Say that your software or service is "based on MeshTender" or "a fork of
  MeshTender", accurately and without styling it as a brand of your own.
- Use the name to refer to this project in writing — articles, documentation,
  comparisons, talks, bug reports.
- Reproduce the logo unmodified when illustrating a discussion *of* MeshTender.

## Contact

For anything this file does not cover, including permission to use the name or
mark, ask first: <https://github.com/jleight/meshtender/issues>.

Copyright © 2026 Jonathon Leight. All rights in the MeshTender name and marks are
reserved.
