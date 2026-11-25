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

### MapLibre GL JS 5.24.0 — BSD-3-Clause

- Homepage: <https://maplibre.org>
- Source: https://cdn.jsdelivr.net/npm/maplibre-gl@5.24.0/dist/
- File: `internal/web/static/maplibre-gl.js` (sha256 `039b46f7a84489bce207f55cf376e1022cf8d213190ff9a93b02554f1785248f`)
- File: `internal/web/static/maplibre-gl-worker.js` (sha256 `4f64bf26dab4a953ceb06c49a8d6efef32d5015a3f4c872a1f73e13f60ac9dba`)
- File: `internal/web/static/maplibre-gl.css` (sha256 `3f40ab71b5b3fc985eb7f6b8926e0e8fbec2792852e4f12d8247abf0aabb8a94`)
- Modified: All three carry hand-extended or hand-restored @preserve banners (upstream's script banner names the license by URL and states no copyright; the stylesheet ships none) and have their trailing sourceMappingURL comment stripped, since the .map files are not vendored. Bodies are byte-identical to upstream.
- Note: This is the CSP build: maplibre-gl.js is upstream dist/maplibre-gl-csp.js and maplibre-gl-worker.js is dist/maplibre-gl-csp-worker.js. The default bundle spawns its worker from a blob: URL, which the strict CSP forbids; the CSP build takes an explicit same-origin worker URL via maplibregl.setWorkerUrl instead (see basemap.js). Upstream's LICENSE.txt, reproduced whole in the notices, also covers the third-party code MapLibre embeds — mapbox-gl-js v1.13 and earlier (BSD-3-Clause, (c) 2020 Mapbox), glfx.js (MIT) and a portion of d3-color (BSD-3-Clause) — all permissive.

```
Copyright (c) 2023, MapLibre contributors

All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

    * Redistributions of source code must retain the above copyright notice,
      this list of conditions and the following disclaimer.
    * Redistributions in binary form must reproduce the above copyright notice,
      this list of conditions and the following disclaimer in the documentation
      and/or other materials provided with the distribution.
    * Neither the name of MapLibre GL JS nor the names of its contributors
      may be used to endorse or promote products derived from this software
      without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.


-------------------------------------------------------------------------------

Contains code from mapbox-gl-js v1.13 and earlier

Version v1.13 of mapbox-gl-js and earlier are licensed under a BSD-3-Clause license

Copyright (c) 2020, Mapbox
Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice,
  this list of conditions and the following disclaimer.
* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
* Neither the name of Mapbox GL JS nor the names of its contributors
  may be used to endorse or promote products derived from this software
  without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE,
EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.


-------------------------------------------------------------------------------

Contains code from glfx.js

Copyright (C) 2011 by Evan Wallace

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

--------------------------------------------------------------------------------

Contains a portion of d3-color https://github.com/d3/d3-color

Copyright 2010-2016 Mike Bostock
All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

* Neither the name of the author nor the names of contributors may be used to
  endorse or promote products derived from this software without specific prior
  written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR
ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
(INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### Terra Draw 1.32.3 — MIT

- Homepage: <https://github.com/JamesLMilner/terra-draw>
- Source: https://cdn.jsdelivr.net/npm/terra-draw@1.32.3/dist/terra-draw.umd.js
- File: `internal/web/static/terra-draw.js` (sha256 `1d6fcaf87f34ec1515e7f960d39915be83365e1c521bdc10e64e25071df7a18d`)
- Modified: Hand-restored banner (the upstream UMD bundle ships none) and a stripped trailing sourceMappingURL comment. Body is byte-identical to upstream.
- Note: Drives polygon drawing and editing on the region area editor, replacing Leaflet-Geoman. Headless by design — it renders shapes into the map and ships no toolbar UI, so the controls are ours (see regionmap.js and config_region_area.html). The npm package ships no LICENSE file; the text here is the one at the root of the monorepo that publishes it, which the adapter shares.

```
Copyright 2022 James Milner

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

### Terra Draw MapLibre GL adapter 1.4.1 — MIT

- Homepage: <https://github.com/JamesLMilner/terra-draw>
- Source: https://cdn.jsdelivr.net/npm/terra-draw-maplibre-gl-adapter@1.4.1/dist/terra-draw-maplibre-gl-adapter.umd.js
- File: `internal/web/static/terra-draw-maplibre-gl-adapter.js` (sha256 `ac90d8efe1d367e483e11decdffc421a7150d57686aec016b261f3f32e49bec7`)
- Modified: Hand-restored banner (the upstream UMD bundle ships none) and a stripped trailing sourceMappingURL comment. Body is byte-identical to upstream.
- Note: Published from the same monorepo as Terra Draw and under the same root MIT license, which is why the two license texts here are identical.

```
Copyright 2022 James Milner

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

### esp-web-tools 10.5.2 — Apache-2.0

- Homepage: <https://github.com/esphome/esp-web-tools>
- Source: https://registry.npmjs.org/esp-web-tools/-/esp-web-tools-10.5.2.tgz (dist/web/)
- File: `internal/web/static/esp-web-tools/cdc-acm-CgpHa-zp.js` (sha256 `7ea3e4b2f23c41f248ea6e95655c32ec6c3365c4171bd51c4b51f44c1af790af`)
- File: `internal/web/static/esp-web-tools/ch34x-O7hnS6Rn.js` (sha256 `2e636731fb86534111ef4925c43ac952cd45e79c5f1cfddb32b7dc0859d099da`)
- File: `internal/web/static/esp-web-tools/cp210x-CD3hAcBr.js` (sha256 `ed515b542259138bd38767d00229ae5598ebb8b8e16e3840075613ed3e19c6f3`)
- File: `internal/web/static/esp-web-tools/esp32-BlyKoTgy.js` (sha256 `8f83d5c7ea8350ac23d98e8ceb022c9631e25587c7d9f2f798b90e5fbb92510c`)
- File: `internal/web/static/esp-web-tools/esp32c2-Bmu-jpVE.js` (sha256 `62255873404fcaf0b19875282d6caae359bc8da7d1b8dde905e3a3238018ec90`)
- File: `internal/web/static/esp-web-tools/esp32c3-81Rr-aQE.js` (sha256 `19ece1457c26d29d8991d59693633167a7a137114a1dea4967d624b4e0edd58b`)
- File: `internal/web/static/esp-web-tools/esp32c5-Bq6mXcb5.js` (sha256 `c9655ee986616e0d6fb692aa703843ae95d438dd5bb7ad61e6a6f0446e502862`)
- File: `internal/web/static/esp-web-tools/esp32c6-yQoBKrxY.js` (sha256 `74b0d1f8e30c4db9a86f7439ef12a8e70b00989feef22011ba24c9ada3096ec2`)
- File: `internal/web/static/esp-web-tools/esp32c61-DmDn041x.js` (sha256 `ff1ec4a6307d66240e36e7ce57fc09ef44b809e0851b436afd7c453e7eb2701c`)
- File: `internal/web/static/esp-web-tools/esp32h2-B8bZhHv5.js` (sha256 `f84ea8b588ca7b095c40b369dd20e629f59e22f3a19f2083434247c044cdfe10`)
- File: `internal/web/static/esp-web-tools/esp32h4-KyNsS-mo.js` (sha256 `870357b7fe5f78aba2b1e38fe06f99593951d4425bfd2ef6e92b46df3f19a403`)
- File: `internal/web/static/esp-web-tools/esp32p4-o74i42i3.js` (sha256 `90387d53659c1fbcb9eabf421af43318ab812202145bb96dbe1732dc64f4e155`)
- File: `internal/web/static/esp-web-tools/esp32p4-rev1-DEdUM_9Q.js` (sha256 `ed7f0c9e5ffab01004e167caac3014d6d701a07949cfeb0fade23563a87f89c0`)
- File: `internal/web/static/esp-web-tools/esp32s2-O1Tyu6IT.js` (sha256 `d9d5fd8bea8b42b3593651c3503d46bbcc7077e2251606aa2e67a6daf9ebb5a0`)
- File: `internal/web/static/esp-web-tools/esp32s3-WXxMoV8x.js` (sha256 `5adb4f5ac9fc7872d02573b40c6f3da44094e07acbb95d6fe45516680faf956b`)
- File: `internal/web/static/esp-web-tools/esp32s31-Ef8NLP2_.js` (sha256 `fe69e34305ff3d2db13951134734c399e96a39e508f497bb072e04f061ef5fd1`)
- File: `internal/web/static/esp-web-tools/esp8266-DsDItw_h.js` (sha256 `b9977ac1b5e77e88658ba36707a508db844a9994a485030ef3b647367ea2f6dc`)
- File: `internal/web/static/esp-web-tools/ftdi-E6n0R6W9.js` (sha256 `e1dbcb1d42d456bea06d85bd3de570ca091bb53960ea1859050dd7dd5dd53bd8`)
- File: `internal/web/static/esp-web-tools/index-rScOpSrP.js` (sha256 `ef774e2e434f4d4bdde04e91aeb61802428f095e11b6a9f4a6b24b20bf049cb5`)
- File: `internal/web/static/esp-web-tools/install-button.js` (sha256 `fe7632d130e95ecdbb5a890a744299dce5c77139b0160bcdd6eb3b3219e32a23`)
- File: `internal/web/static/esp-web-tools/install-dialog-ChRWE6GU.js` (sha256 `1faf7fab9a051ca0d4d648c6c8604ccbb6c13cd89ce9f1c1c2a26f3b61f0d817`)
- File: `internal/web/static/esp-web-tools/no-port-picked-DPUnbpty.js` (sha256 `565b85439ce3ec6ae1732b69304f9d3478d73f7076e97d36bd66500a866f6363`)
- File: `internal/web/static/esp-web-tools/port-3AmrEwYL.js` (sha256 `c004140848b24c16c4d12102346f090b7ab3a8262fe073e695d7bff2e8337d3b`)
- File: `internal/web/static/esp-web-tools/styles-BiOOVrm2.js` (sha256 `e74c11bc88ea2a0011352b1442b3217c8035f456c5e0463c31b77e329e1e6a1d`)
- Modified: Prepended an attribution banner to every file (the minified build ships none, and its bundled MIT and BSD code needs one). Bodies are byte-identical to upstream dist/web/.
- Note: Installs observer firmware on ESP32 boards over Web Serial (or WebUSB on Android), on the observer install page. It is the browser build: install-button.js defines the <esp-web-install-button> element and loads the other files as ES modules by relative path, so they live together in their own static directory and keep their upstream names. The bundled third-party code is listed in the entries that follow, with versions from the package-lock.json at the 10.5.2 tag. The USB-serial drivers (cdc-acm, ch34x, cp210x, ftdi) are esp-web-tools' own code.

```
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

### Lit 3.3.3 — BSD-3-Clause

- Homepage: <https://lit.dev>
- Source: https://registry.npmjs.org/lit/-/lit-3.3.3.tgz
- Note: Bundled inside esp-web-tools (styles-*.js), which renders its install dialog with it. Covers the Lit packages it pulls in, all under this same Google LLC license: lit 3.3.3, lit-html 3.3.0, lit-element 4.2.0, @lit/reactive-element 2.1.0 and @lit/context 1.1.6.

```
BSD 3-Clause License

Copyright (c) 2017 Google LLC. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

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

### Material Web 2.5.0 — Apache-2.0

- Homepage: <https://github.com/material-components/material-web>
- Source: https://registry.npmjs.org/@material/web/-/web-2.5.0.tgz
- Note: Bundled inside esp-web-tools: the dialog, buttons and list components of its install dialog (@material/web).

```
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright 2018-2023 Google, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

### Improv Wi-Fi serial SDK 3.0.0 — Apache-2.0

- Homepage: <https://github.com/improv-wifi/sdk-serial-js>
- Source: https://registry.npmjs.org/@improv-wifi/serial-sdk/-/serial-sdk-3.0.0.tgz
- Note: Bundled inside esp-web-tools (@improv-wifi/serial-sdk). Our manifests set new_install_improv_wait_time to 0 because the observer firmware doesn't speak Improv, so it never runs, but it still ships.

```
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

### esptool-js 0.7.0 — Apache-2.0

- Homepage: <https://github.com/espressif/esptool-js>
- Source: https://registry.npmjs.org/esptool-js/-/esptool-js-0.7.0.tgz
- Note: Bundled inside esp-web-tools (install-dialog-*.js): the ESP ROM bootloader protocol that writes the firmware.

```
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright 2019 Espressif Systems (Shanghai) CO LTD

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

### esp-flasher-stub 1.2.2 — MIT

- Homepage: <https://github.com/espressif/esp-flasher-stub>
- Source: https://github.com/espressif/esptool-js/tree/v0.7.0/src/targets/stub_flasher
- Note: The flasher stub binaries esptool-js uploads to the chip's RAM to speed up flashing, one per chip (the esp32*.js and esp8266-*.js files). esptool-js copied them from esp-flasher-stub v1.2.2, where they are dual-licensed MIT OR Apache-2.0; we take them under MIT. These are not the GPL-licensed stubs of the Python esptool.

```
MIT License

Copyright (c) 2026 Espressif Systems (Shanghai) CO LTD

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

### pako 2.2.0 — MIT

- Homepage: <https://github.com/nodeca/pako>
- Source: https://registry.npmjs.org/pako/-/pako-2.2.0.tgz
- Note: Bundled inside esp-web-tools via esptool-js, which deflates firmware before sending it to the chip. pako is licensed (MIT AND Zlib): its own code is MIT (this entry), and its port of zlib is under the zlib license (the next entry).

```
(The MIT License)

Copyright (C) 2014-2017 by Vitaly Puzrin and Andrei Tuputcyn

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

### pako zlib port 2.2.0 — Zlib

- Homepage: <https://github.com/nodeca/pako>
- Source: https://registry.npmjs.org/pako/-/pako-2.2.0.tgz (lib/zlib/)
- Note: The zlib-derived part of pako (lib/zlib/), under the zlib license. The upstream package has no separate license file for it, so the text here is the notice at the top of each of those source files.

```
(C) 1995-2013 Jean-loup Gailly and Mark Adler
(C) 2014-2017 Vitaly Puzrin and Andrey Tupitsin

This software is provided 'as-is', without any express or implied
warranty. In no event will the authors be held liable for any damages
arising from the use of this software.

Permission is granted to anyone to use this software for any purpose,
including commercial applications, and to alter it and redistribute it
freely, subject to the following restrictions:

1. The origin of this software must not be misrepresented; you must not
  claim that you wrote the original software. If you use this software
  in a product, an acknowledgment in the product documentation would be
  appreciated but is not required.
2. Altered source versions must be plainly marked as such, and must not be
  misrepresented as being the original software.
3. This notice may not be removed or altered from any source distribution.
```

### atob-lite 2.0.0 — MIT

- Homepage: <https://github.com/hughsk/atob-lite>
- Source: https://registry.npmjs.org/atob-lite/-/atob-lite-2.0.0.tgz
- Note: Bundled inside esp-web-tools via esptool-js (its browser build, a one-line wrapper around atob). Author Hugh Kennedy; the license text names no copyright holder.

```
This software is released under the MIT license:

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

### tslib 2.9.1 — 0BSD

- Homepage: <https://github.com/microsoft/tslib>
- Source: https://registry.npmjs.org/tslib/-/tslib-2.9.1.tgz
- Note: TypeScript's runtime helpers, bundled inside esp-web-tools.

```
Copyright (c) Microsoft Corporation.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
PERFORMANCE OF THIS SOFTWARE.
```

### Tabler 1.7.1 — MIT

- Homepage: <https://tabler.io>
- Source: https://registry.npmjs.org/@tabler/core/-/core-1.7.1.tgz (dist/css/tabler.min.css, dist/css/tabler-themes.min.css, dist/js/tabler.min.js)
- File: `internal/web/static/tabler.min.js` (sha256 `0c804e9d4d33f0c3847fcc1a4d593563894237682fd1d7eeea66a48a4a4a8ad3`)
- File: `internal/web/static/tabler.min.css` (sha256 `413912dcb8c951ba1ec7272b6611259a1fdb225425738da2b83a20d93855fd33`)
- File: `internal/web/static/tabler-themes.min.css` (sha256 `fb2852041d45aceb0fd62825db812722b1616f701a749bf4f28ea096a0416f72`)
- Modified: All three have their trailing sourceMappingURL comment stripped, since the .map files are not vendored. tabler.min.js also gets a hand-restored banner, after Tabler's own, for the Bootstrap and Popper code it bundles: Tabler 1.5+ builds Bootstrap from its own tree and its bundle carries neither project's notice. Bodies are byte-identical to upstream (upstream sha256: tabler.min.css dcf84d7b4319c29ab33450bfe2a7bbf607458d994ef71023f7509e912cafdbd5, tabler-themes.min.css 7f863d1f6918e6d9d9f0e8a540c0d45efa567f88caa711a31d2d2253837a544f, tabler.min.js 2a3b9ecae44d821b21ba4521da33fca1bdc692454745536418c4e078e8ba6abf).
- Note: Tabler's paid add-ons (Illustrations, Emails, Avatars) are a Personal License that forbids open-source redistribution — nothing from them may enter this repository. The license text here is from the repository's @tabler/core@1.7.1 tag; the npm package ships no LICENSE file.

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

### Bootstrap 5.3.8 — MIT

- Homepage: <https://getbootstrap.com>
- Source: bundled inside @tabler/core@1.7.1 dist/js/tabler.min.js (its components report VERSION 5.3.8)
- Note: Not vendored directly: Tabler's bundle embeds Bootstrap's JavaScript. It ships to every user, so it is attributed here and in the banner restored at the top of tabler.min.js.

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

### Popper 2.11.8 — MIT

- Homepage: <https://popper.js.org>
- Source: bundled inside @tabler/core@1.7.1 dist/js/tabler.min.js (@tabler/core depends on @popperjs/core ^2.11.8, the last 2.x release); license text from the @popperjs/core@2.11.8 npm package
- Note: Not vendored directly: Bootstrap's dropdowns and tooltips position with Popper, which Tabler's bundle embeds. It ships to every user, so it is attributed here and in the banner restored at the top of tabler.min.js.

```
The MIT License (MIT)

Copyright (c) 2019 Federico Zivolo

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

### Tabler Icons — MIT

- Homepage: <https://tabler.io/icons>
- Source: https://github.com/tabler/tabler-icons (icon path data, various versions)
- File: `internal/web/templates/icons.html` (sha256 `c303792bf5a7f391bf35662a40fe3fc2243fa783588ab057c23dddc50c4603b6`)
- Modified: Icon path data copied into Go template definitions rather than vendored as SVG files; the transparent 24x24 guard path upstream emits is dropped.
- Note: All 57 icons in icons.html are Tabler Icons; several are renamed locally (antenna<-antenna-bars-5, copy<-squares, list<-list-details, plug<-plug-connected, terminal<-terminal-2, alert<-alert-triangle, brand-signal<-message-circle-2). Version is unpinned because the set was collected across releases.

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
- Source: https://*.basemaps.cartocdn.com (allowlisted in the CSP connect-src), keyed with MESHTENDER_CARTO_KEY
- Note: Vector basemaps (MapLibre GL styles) rendered in the browser at runtime; no code is redistributed, so no license applies. A style pulls four kinds of resource from CARTO — the style JSON, .mvt tiles, a sprite sheet and glyph PBFs — all fetched rather than loaded as images, which is why the CSP allowlists the host in connect-src rather than img-src. Attribution ("(c) OpenStreetMap (c) CARTO") is rendered by basemap.js, and CARTO require it stay visible. Every request carries an API key (MESHTENDER_CARTO_KEY, per deployment) appended by a MapLibre transformRequest, because the URLs inside a style do not carry one. The raster tiles this replaced are deprecated upstream. Terms of use are CARTO's and are not verified by any test.

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
- **github.com/andybalholm/brotli** v1.3.0 — MIT  
  Copyright (c) 2009 The Go Authors. All rights reserved.  
  Copyright (c) 2009, 2010, 2013-2016 by the Brotli Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **github.com/aymerick/douceur** v0.2.0 — MIT  
  Copyright (c) 2015 Aymerick JEHANNE
- **github.com/brianvoe/gofakeit/v7** v7.18.7 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER  
  Copyright (c) [year] [fullname]
- **github.com/coder/websocket** v1.8.15 — ISC  
  Copyright (c) 2025 Coder  
  copyright notice and this permission notice appear in all copies.
- **github.com/eclipse/paho.mqtt.golang** v1.5.1 — BSD-3-Clause  
  Copyright (c) 2007, Eclipse Foundation, Inc. and its licensors.
- **github.com/fxamacker/cbor/v2** v2.10.0 — MIT  
  Copyright (c) 2019-present Faye Amacker
- **github.com/go-chi/chi/v5** v5.5.0 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER  
  Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.
- **github.com/go-viper/mapstructure/v2** v2.5.0 — MIT  
  Copyright (c) 2013 Mitchell Hashimoto
- **github.com/go-webauthn/webauthn** v0.18.2 — BSD-3-Clause  
  Copyright (c) 2025 github.com/go-webauthn/webauthn authors.
- **github.com/go-webauthn/x** v0.3.1 — BSD-3-Clause  
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
- **github.com/gorilla/websocket** v1.5.4-0.20240701034025-d67f41855da4 — BSD-2-Clause  
  Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved.
- **github.com/jackc/pgpassfile** v1.0.0 — MIT  
  Copyright (c) 2019 Jack Christensen
- **github.com/jackc/pgservicefile** v0.0.0-20240606120523-5a60cdf6a761 — MIT  
  Copyright (c) 2020 Jack Christensen
- **github.com/jackc/pgx/v5** v5.13.0 — MIT  
  Copyright (c) 2013-2021 Jack Christensen
- **github.com/jackc/puddle/v2** v2.2.4 — MIT  
  Copyright (c) 2018 Jack Christensen
- **github.com/meshcore-go/meshcore-go** v1.6.0 — MIT  
  Copyright (c) 2026 meshcore-go
- **github.com/mfridman/interpolate** v0.2.0 — MIT  
  Copyright (c) 2014-2017 Buildkite Pty Ltd  
  Copyright (c) 2023 Michael Fridman
- **github.com/microcosm-cc/bluemonday** v1.0.27 — BSD-3-Clause  
  Copyright (c) 2014, David Kitchen <david@buro9.com>
- **github.com/peterstace/simplefeatures** v0.59.0 — MIT  
  Copyright (c) 2019 the contributors.
- **github.com/philhofer/fwd** v1.2.1 — MIT  
  Copyright (c) 2014-2015, Philip Hofer
- **github.com/pressly/goose/v3** v3.30.1 — MIT  
  COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
- **github.com/resend/resend-go/v3** v3.17.0 — MIT  
  Copyright (c) 2023 Derich Pacheco
- **github.com/sethvargo/go-retry** v0.5.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/skip2/go-qrcode** v0.0.0-20200617195104-da1b6568686e — MIT  
  Copyright (c) 2014 Tom Harwood
- **github.com/tinylib/msgp** v1.6.5 — MIT  
  Copyright (c) 2014 Philip Hofer
- **github.com/x448/float16** v0.8.4 — MIT  
  Copyright (c) 2019 Montgomery Edwards⁴⁴⁸ and Faye Amacker
- **github.com/yuin/goldmark** v1.8.7 — MIT  
  Copyright (c) 2019 Yusuke Inuzuka
- **go.uber.org/multierr** v1.11.0 — MIT  
  Copyright (c) 2017-2021 Uber Technologies, Inc.
- **golang.org/x/crypto** v0.59.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/net** v0.62.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/sync** v0.25.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/sys** v0.50.0 — BSD-3-Clause  
  Copyright 2009 The Go Authors.  
  copyright notice, this list of conditions and the following disclaimer
- **golang.org/x/text** v0.44.0 — BSD-3-Clause  
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
- **github.com/chromedp/cdproto** v0.0.0-20260714215040-dc233986426f — MIT  
  Copyright (c) 2016-2025 Kenneth Shaw
- **github.com/chromedp/chromedp** v0.16.0 — MIT  
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
- **github.com/containerd/log** v0.2.0 — Apache-2.0  
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
- **github.com/docker/go-connections** v0.8.2 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2015 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/docker/go-units** v0.5.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2015 Docker, Inc.  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/ebitengine/purego** v0.11.3 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright {yyyy} {name of copyright owner}  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/felixge/httpsnoop** v1.1.0 — MIT  
  Copyright (c) 2016 Felix Geisendörfer (felix@debuggable.com)
- **github.com/go-json-experiment/json** v1.27.2 — BSD-3-Clause  
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
- **github.com/klauspost/compress** v1.20.4 — Apache-2.0  
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
- **github.com/magiconair/properties** v1.18.13 — BSD-2-Clause  
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
- **github.com/moby/moby/api** v1.57.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/moby/client** v0.6.3 — Apache-2.0  
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
- **github.com/moby/sys/user** v0.5.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work
- **github.com/moby/sys/userns** v0.2.1 — Apache-2.0  
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
- **github.com/shirou/gopsutil/v4** v4.26.10 — BSD-3-Clause  
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
- **github.com/testcontainers/testcontainers-go** v0.45.0 — MIT  
  Copyright (c) 2017-2019 Gianluca Arbezzano
- **github.com/testcontainers/testcontainers-go/modules/postgres** v0.45.0 — MIT  
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
- **go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp** v0.72.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel** v1.47.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel/log** v1.47.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel/metric** v1.47.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.opentelemetry.io/otel/trace** v1.47.0 — Apache-2.0  
  (c) You must retain, in the Source form of any Derivative Works  
  Copyright 2009 The Go Authors.  
  Copyright [yyyy] [name of copyright owner]  
  copyright license to reproduce, prepare Derivative Works of,  
  copyright notice that is included in or attached to the work  
  copyright notice, this list of conditions and the following disclaimer
- **go.yaml.in/yaml/v3** v3.0.6 — Apache-2.0  
  Copyright (c) 2006-2010 Kirill Simonov  
  Copyright (c) 2006-2011 Kirill Simonov  
  Copyright (c) 2011-2019 Canonical Ltd  
  Copyright 2011-2016 Canonical Ltd.  
  copyright staring in 2011 when the project was ported over:

<!-- END GENERATED: go-modules -->
