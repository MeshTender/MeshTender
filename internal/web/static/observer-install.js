// observer-install.js: on the observer install page, shows the board list for
// the chosen role and points the <esp-web-install-button> at the install
// manifest for the chosen board. esp-web-tools reads the button's manifest
// attribute when it's clicked, so updating it here is all a change needs.
(function () {
  "use strict";
  const form = document.querySelector("[data-fw-install]");
  const button = document.querySelector("esp-web-install-button");
  if (!form || !button) return;

  const role = form.querySelector("#fw-role");
  const groups = Array.from(form.querySelectorAll("[data-fw-role]"));
  const version = form.querySelector("[data-fw-version]");
  const published = form.querySelector("[data-fw-published]");
  const publishedWrap = form.querySelector("[data-fw-published-wrap]");
  const manifestURL = String(form.dataset.fwManifest || "");
  if (!role || !manifestURL.includes("{env}")) return;

  function sync() {
    let board = null;
    for (const g of groups) {
      const on = g.dataset.fwRole === role.value;
      g.hidden = !on;
      if (on) board = g.querySelector("select");
    }
    const opt = board && board.selectedOptions[0];
    if (!opt) return;
    button.setAttribute("manifest", manifestURL.replace("{env}", encodeURIComponent(opt.value)));
    if (version) version.textContent = String(opt.dataset.version || "");
    const date = String(opt.dataset.published || "");
    if (published) published.textContent = date;
    if (publishedWrap) publishedWrap.hidden = date === "";
  }

  role.addEventListener("change", sync);
  for (const g of groups) {
    const select = g.querySelector("select");
    if (select) select.addEventListener("change", sync);
  }
  sync();
})();
