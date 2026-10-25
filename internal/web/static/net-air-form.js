// net-air-form.js — a net's On the air settings: choosing the channel kind
// shows only that kind's fields (a hashtag channel's name; a private channel's
// label and key). The server reads only the chosen kind's fields, so hidden
// ones don't need clearing.
(function () {
  "use strict";

  function sync(form) {
    var checked = form.querySelector('input[name="channel_kind"]:checked');
    var kind = checked ? String(checked.value) : "";
    form.querySelectorAll("[data-air-field]").forEach(function (el) {
      el.classList.toggle("d-none", el.getAttribute("data-air-field") !== kind);
    });
  }

  document.addEventListener("change", function (e) {
    var t = e.target;
    if (!(t instanceof HTMLInputElement) || t.name !== "channel_kind") return;
    var form = t.closest("[data-air-form]");
    if (form) sync(form);
  });
})();
