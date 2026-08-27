# Third-Party Notices

This file covers the third-party software MeshTender depends on, all of
which is permissively licensed. MeshTender's own license is the GNU
Affero General Public License v3.0 (see LICENSE); its name and marks are
reserved (see TRADEMARKS.md).

**This file is generated. Do not edit it by hand** — run `mise run licenses --update`.
Front-end and artwork entries come from `internal/licenses/manifest.go`; the Go
module list is scanned from the module graph.

<!-- BEGIN GENERATED: assets — edit internal/licenses/manifest.go, then run `mise run licenses --update` -->

## Vendored front-end assets and artwork

The following third-party code and artwork is redistributed as part of
MeshTender — compiled into the binary via `go:embed` and served to browsers.

### htmx 2.0.10 — 0BSD

- Homepage: <https://htmx.org>
- Source: https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js
- File: `internal/web/static/htmx.min.js` (sha256 `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de`)

```
Zero-Clause BSD
=============

Permission to use, copy, modify, and/or distribute this software for
any purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED “AS IS” AND THE AUTHOR DISCLAIMS ALL
WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES
OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE
FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY
DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN
AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT
OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

### Leaflet 1.9.4 — BSD-2-Clause

- Homepage: <https://leafletjs.com>
- Source: https://cdn.jsdelivr.net/npm/leaflet@1.9.4/dist/
- File: `internal/web/static/leaflet.js` (sha256 `db49d009c841f5ca34a888c96511ae936fd9f5533e90d8b2c4d57596f4e5641a`)
- File: `internal/web/static/leaflet.css` (sha256 `498bd934faeb2cb455d6db2d9304d18d5aea69afe43fd2ac933c3f3753724617`)
- Modified: leaflet.css carries a hand-restored @preserve banner; upstream ships the stylesheet without one. Body is byte-identical to upstream.

```
BSD 2-Clause License

Copyright (c) 2010-2023, Volodymyr Agafonkin
Copyright (c) 2010-2011, CloudMade
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### Leaflet-Geoman 2.20.0 — MIT

- Homepage: <https://geoman.io>
- Source: https://cdn.jsdelivr.net/npm/@geoman-io/leaflet-geoman-free@2.20.0/dist/
- File: `internal/web/static/leaflet-geoman.js` (sha256 `50bce5ec0c880d7edc912254f645aa77364fd6c29d66ef92296f855b8b615498`)
- File: `internal/web/static/leaflet-geoman.css` (sha256 `51e45cbdf47dccb437bb34c9aa96b2017957a2471e17f41a72f4ec15a3b8c3f2`)
- Modified: Both files carry hand-restored banners: the upstream esbuild bundle strips its own. Bodies are byte-identical to leaflet-geoman.min.js and leaflet-geoman.css upstream.
- Note: This is the free MIT package (@geoman-io/leaflet-geoman-free). Geoman also sells a commercially licensed product — do not upgrade into it.

```
MIT License

Copyright (c) 2017 Sumit Kumar

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### Leaflet.markercluster 1.5.3 — MIT

- Homepage: <https://github.com/Leaflet/Leaflet.markercluster>
- Source: https://cdn.jsdelivr.net/npm/leaflet.markercluster@1.5.3/dist/
- File: `internal/web/static/leaflet.markercluster.js` (sha256 `b687c3bd8b9239b1dbe4bc4241c2940426cf15ca8543c73e5d4e31e3346fab25`)
- File: `internal/web/static/leaflet.markercluster.css` (sha256 `882ea5266422a7ff57e5641f78a7e8464f81b575f0665634808d60ae6f5ed41d`)
- Modified: The stylesheet is upstream MarkerCluster.css + MarkerCluster.Default.css concatenated, plus a banner; the script is upstream plus a banner. Both bodies are byte-identical to upstream.

```
Copyright 2012 David Leaver

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

### Tabler 1.4.0 — MIT

- Homepage: <https://tabler.io>
- Source: https://cdn.jsdelivr.net/npm/@tabler/core@1.4.0/dist/
- File: `internal/web/static/tabler.min.js` (sha256 `b60c76160e97624574dbb8cf10abe6aee9a6493b60096fdfc15dd1dd2bd99eb9`)
- File: `internal/web/static/tabler.min.css` (sha256 `7ef750bd10546a695d0b12767ad8048bd8f3ec5de7daefb1067f9d0daa3d1c9a`)
- Note: These two files are byte-identical to the public MIT @tabler/core@1.4.0 npm artifacts, verified by SHA-256. Tabler's paid add-ons (Illustrations, Emails, Avatars) are a Personal License that forbids open-source redistribution — nothing from them may enter this repository. The license text here is from the tabler/tabler dev branch: upstream publishes no v1.4.0 git tag and the npm package ships no LICENSE file.

```
The MIT License (MIT)

Copyright (c) 2018-2026 The Tabler Authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

### Bootstrap 5.3.7 — MIT

- Homepage: <https://getbootstrap.com>
- Source: bundled inside @tabler/core@1.4.0 dist/js/tabler.min.js
- Note: Not vendored directly: Tabler's bundle embeds Bootstrap, which its own banner declares partway through tabler.min.js. It ships to every user, so it is attributed here.

```
The MIT License (MIT)

Copyright (c) 2011-2025 The Bootstrap Authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

### Tabler Icons — MIT

- Homepage: <https://tabler.io/icons>
- Source: https://github.com/tabler/tabler-icons (icon path data, various versions)
- File: `internal/web/templates/icons.html` (sha256 `2f23e95b43f6c6bba164f7c470ddc5f69d5376f20b088dc919b74a27dd74e20e`)
- Modified: Icon path data copied into Go template definitions rather than vendored as SVG files; the transparent 24x24 guard path upstream emits is dropped.
- Note: All 45 icons in icons.html are Tabler Icons; several are renamed locally (antenna<-antenna-bars-5, copy<-squares, list<-list-details, plug<-plug-connected, terminal<-terminal-2, alert<-alert-triangle, brand-signal<-message-circle-2). Version is unpinned because the set was collected across releases.

```
MIT License

Copyright (c) 2020-2026 Paweł Kuna

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Base image and external services

These are not compiled into the binary. The base image is redistributed as
part of the published container; the service is called by the browser at runtime.

### distroless static-debian12 — Apache-2.0

- Homepage: <https://github.com/GoogleContainerTools/distroless>
- Source: gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab (defaultBaseImage in .ko.yaml)
- Note: Runtime base image, redistributed as part of the published container. The distroless project is Apache-2.0; the image layer also carries Debian-packaged CA certificates and tzdata under their own upstream licenses (Mozilla's CA bundle is MPL-2.0, applying to the certificate data we redistribute unmodified, not to MeshTender).

### CARTO basemaps

- Homepage: <https://carto.com>
- Source: https://{s}.basemaps.cartocdn.com (allowlisted in the CSP img-src), keyed with MESHTENDER_CARTO_KEY
- Note: Raster map tiles fetched by the browser at runtime; no code is redistributed, so no license applies. Attribution ("(c) OpenStreetMap (c) CARTO") is rendered by basemap.js. Requests carry an API key (MESHTENDER_CARTO_KEY, per deployment); CARTO watermark tiles served without one. CARTO have said these raster tiles are deprecated in favour of vector tiles, which Leaflet cannot render. Terms of use are CARTO's and are not verified by any test.

<!-- END GENERATED: assets -->

## Go modules

<!-- BEGIN GENERATED: go-modules — run `mise run licenses --update` -->

Go module dependencies, scanned from the module graph with
[licensecheck](https://github.com/google/licensecheck). Each module's own
license file is the authoritative text; the copyright lines below are
reproduced from it to satisfy the attribution clauses.

### Linked into the MeshTender binary

Redistributed in compiled form. Their notices are reproduced here.

- **filippo.io/edwards25519** v1.2.0 — BSD-3-Clause  
  Copyright (c) 2009 The Go Authors. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/alexedwards/scs/pgxstore** v0.0.0-20251002162104-209de6e426de — MIT  
  Copyright (c) 2016 Alex Edwards
- **github.com/alexedwards/scs/v2** v2.9.0 — MIT  
  Copyright (c) 2016 Alex Edwards
- **github.com/andybalholm/brotli** v1.2.3 — MIT  
  Copyright (c) 2009 The Go Authors. All rights reserved.  
  Copyright (c) 2009, 2010, 2013-2016 by the Brotli Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/aymerick/douceur** v0.2.0 — MIT  
  Copyright (c) 2015 Aymerick JEHANNE
- **github.com/brianvoe/gofakeit/v7** v7.16.0 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER  
  Copyright (c) [year] [fullname]
- **github.com/coder/websocket** v1.8.15 — ISC  
  Copyright (c) 2025 Coder  
  copyright notice and this permission notice appear in all copies.
- **github.com/fxamacker/cbor/v2** v2.9.3 — MIT  
  Copyright (c) 2019-present Faye Amacker
- **github.com/go-chi/chi/v5** v5.3.2 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER  
  Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.
- **github.com/go-viper/mapstructure/v2** v2.5.0 — MIT  
  Copyright (c) 2013 Mitchell Hashimoto
- **github.com/go-webauthn/webauthn** v0.18.0 — BSD-3-Clause  
  Copyright (c) 2025 github.com/go-webauthn/webauthn authors.
- **github.com/go-webauthn/x** v0.3.0 — BSD-3-Clause  
  Copyright (c) 2013-2017 The btcsuite developers  
  Copyright (c) 2014 CloudFlare Inc.  
  Copyright (c) 2015-2024 The Decred developers  
  Copyright (c) 2017 The Lightning Network Developers  
  Copyright (c) 2021-2023 github.com/go-webauthn authors.  
  copyright notice and this permission notice appear in all copies.
- **github.com/golang-jwt/jwt/v5** v5.3.1 — MIT  
  Copyright (c) 2012 Dave Grijalva  
  Copyright (c) 2021 golang-jwt maintainers
- **github.com/google/go-tpm** v0.9.8 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/google/uuid** v1.6.0 — BSD-3-Clause  
  Copyright (c) 2009,2014 Google Inc. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/gorilla/css** v1.0.1 — BSD-3-Clause  
  Copyright (c) 2023 The Gorilla Authors. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/jackc/pgpassfile** v1.0.0 — MIT  
  Copyright (c) 2019 Jack Christensen
- **github.com/jackc/pgservicefile** v0.0.0-20240606120523-5a60cdf6a761 — MIT  
  Copyright (c) 2020 Jack Christensen
- **github.com/jackc/pgx/v5** v5.10.0 — MIT  
  Copyright (c) 2013-2021 Jack Christensen
- **github.com/jackc/puddle/v2** v2.2.2 — MIT  
  Copyright (c) 2018 Jack Christensen
- **github.com/meshcore-go/meshcore-go** v1.1.0 — MIT  
  Copyright (c) 2026 meshcore-go
- **github.com/mfridman/interpolate** v0.0.2 — MIT  
  Copyright (c) 2014-2017 Buildkite Pty Ltd  
  Copyright (c) 2023 Michael Fridman
- **github.com/microcosm-cc/bluemonday** v1.0.27 — BSD-3-Clause  
  Copyright (c) 2014, David Kitchen <david@buro9.com>
- **github.com/peterstace/simplefeatures** v0.59.0 — MIT  
  Copyright (c) 2019 the contributors.
- **github.com/philhofer/fwd** v1.2.0 — MIT  
  Copyright (c) 2014-2015, Philip Hofer
- **github.com/pressly/goose/v3** v3.27.3 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
- **github.com/resend/resend-go/v3** v3.17.0 — MIT  
  Copyright (c) 2023 Derich Pacheco
- **github.com/sethvargo/go-retry** v0.4.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/skip2/go-qrcode** v0.0.0-20200617195104-da1b6568686e — MIT  
  Copyright (c) 2014 Tom Harwood
- **github.com/tinylib/msgp** v1.6.4 — MIT  
  Copyright (c) 2014 Philip Hofer
- **github.com/x448/float16** v0.8.4 — MIT  
  Copyright (c) 2019 Montgomery Edwards⁴⁴⁸ and Faye Amacker
- **github.com/yuin/goldmark** v1.8.5 — MIT  
  Copyright (c) 2019 Yusuke Inuzuka
- **go.uber.org/multierr** v1.11.0 — MIT  
  Copyright (c) 2017-2021 Uber Technologies, Inc.
- **golang.org/x/crypto** v0.55.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/net** v0.58.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/sync** v0.22.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/sys** v0.47.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/text** v0.41.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer

### Build, test, and tooling only

Not present in the shipped binary or container. Listed for completeness.

- **dario.cat/mergo** v1.0.2 — BSD-3-Clause  
  Copyright (c) 2012 The Go Authors. All rights reserved.  
  Copyright (c) 2013 Dario Castañé. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/cenkalti/backoff/v4** v4.3.0 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER  
  Copyright (c) 2014 Cenk Altı
- **github.com/cespare/xxhash/v2** v2.3.0 — MIT  
  Copyright (c) 2016 Caleb Spare
- **github.com/chromedp/cdproto** v0.0.0-20260704091341-6ca7914c3938 — MIT  
  Copyright (c) 2016-2025 Kenneth Shaw
- **github.com/chromedp/chromedp** v0.15.1 — MIT  
  Copyright (c) 2016-2025 Kenneth Shaw
- **github.com/chromedp/sysutil** v1.1.0 — MIT  
  Copyright (c) 2016-2017 Kenneth Shaw
- **github.com/containerd/errdefs** v1.0.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright The containerd Authors  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/containerd/errdefs/pkg** v0.3.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright The containerd Authors  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/containerd/log** v0.1.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright The containerd Authors  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/containerd/platforms** v0.2.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright The containerd Authors  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/cpuguy83/dockercfg** v0.3.2 — MIT  
  Copyright (c) 2020 Brian Goff
- **github.com/distribution/reference** v0.6.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright {yyyy} {name of copyright owner}  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/docker/go-connections** v0.8.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2015 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/docker/go-units** v0.5.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2015 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/ebitengine/purego** v0.10.2 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright {yyyy} {name of copyright owner}  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/felixge/httpsnoop** v1.1.0 — MIT  
  Copyright (c) 2016 Felix Geisendörfer (felix@debuggable.com)
- **github.com/go-json-experiment/json** v0.0.0-20260214004413-d219187c3433 — BSD-3-Clause  
  Copyright (c) 2020 The Go Authors. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/go-logr/logr** v1.4.4 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright {yyyy} {name of copyright owner}  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/go-logr/stdr** v1.2.2 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/gobwas/httphead** v0.1.0 — MIT  
  Copyright (c) 2017 Sergey Kamardin
- **github.com/gobwas/pool** v0.2.1 — MIT  
  Copyright (c) 2017-2019 Sergey Kamardin <gobwas@gmail.com>
- **github.com/gobwas/ws** v1.4.0 — MIT  
  Copyright (c) 2017-2021 Sergey Kamardin <gobwas@gmail.com>
- **github.com/google/licensecheck** v0.3.1 — BSD-3-Clause  
  Copyright (c) 2019 The Go Authors. All rights reserved.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/klauspost/compress** v1.19.2 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright (c) 2011 The Snappy-Go Authors. All rights reserved.  
  Copyright (c) 2012 The Go Authors. All rights reserved.  
  Copyright (c) 2015 Klaus Post  
  Copyright (c) 2015, Pierre Curto  
  Copyright (c) 2016 Caleb Spare  
  Copyright (c) 2016 Evan Huus  
  Copyright (c) 2019 Klaus Post. All rights reserved.  
  Copyright (c) 2023 Klaus Post  
  Copyright 2016 The filepathx Authors  
  Copyright 2016-2017 The New York Times Company  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/magiconair/properties** v1.18.11 — BSD-2-Clause  
  Copyright (c) 2013-2020, Frank Schroeder
- **github.com/moby/docker-image-spec** v1.3.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/go-archive** v0.3.3 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/moby/api** v1.55.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/moby/client** v0.5.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/patternmatcher** v0.6.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2012-2017 Docker, Inc.  
  Copyright 2013-2018 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/sys/sequential** v0.7.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/sys/user** v0.4.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/sys/userns** v0.2.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/term** v0.5.2 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2013-2018 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/opencontainers/go-digest** v1.0.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2016 Docker, Inc.  
  Copyright 2019, 2020 OCI Contributors  
  copyright and certain other rights. Our licenses are  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright--then that use is not regulated by the license. Our
- **github.com/opencontainers/image-spec** v1.1.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2016 The Linux Foundation.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/shirou/gopsutil/v4** v4.26.7 — BSD-3-Clause  
  Copyright (c) 2009 The Go Authors. All rights reserved.  
  Copyright (c) 2014, WAKAYAMA Shirou  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/sirupsen/logrus** v1.10.2 — MIT  
  Copyright (c) 2014 Simon Eskildsen
- **github.com/stretchr/testify** v1.12.1 — BSD-3-Clause  
  Copyright (c) 2012-2016 Dave Collins <dave@davec.name>  
  Copyright (c) 2012-2020 Mat Ryer, Tyler Bunnell and contributors.  
  Copyright (c) 2013, Patrick Mezard  
  copyright notice and this permission notice appear in all copies.
- **github.com/testcontainers/testcontainers-go** v0.44.0 — MIT  
  Copyright (c) 2017-2019 Gianluca Arbezzano
- **github.com/testcontainers/testcontainers-go/modules/postgres** v0.44.0 — MIT  
  Copyright (c) 2017-2019 Gianluca Arbezzano
- **github.com/tklauser/go-sysconf** v0.4.0 — BSD-3-Clause  
  Copyright (c) 2018-2022, Tobias Klauser
- **github.com/tklauser/numcpus** v0.12.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **go.opentelemetry.io/auto/sdk** v1.2.1 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp** v0.71.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel** v1.46.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel/metric** v1.46.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel/trace** v1.46.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.yaml.in/yaml/v3** v3.0.5 — Apache-2.0  
  Copyright (c) 2006-2010 Kirill Simonov  
  Copyright (c) 2006-2011 Kirill Simonov  
  Copyright (c) 2011-2019 Canonical Ltd  
  Copyright 2011-2016 Canonical Ltd.  
  copyright staring in 2011 when the project was ported over:

<!-- END GENERATED: go-modules -->
