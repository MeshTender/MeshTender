// Shared behaviour for the profile/org link editors. Both the account page and
// the org page render a `<form data-link-editor>` with repeatable `.link-row`s;
// this script adds/removes rows, renumbers the primary-contact radios, adapts
// each row to its selected platform (placeholder + whether the optional label
// applies), and validates client-side before submit. The server re-validates and
// is authoritative — this is a convenience that keeps people from round-tripping
// on an obvious mistake. Per-platform config arrives CSP-safely via the
// window.MESHTENDER_LINK_PLATFORMS global set by a nonce'd inline script.
(function () {
  'use strict';

  var CONFIG =
    window.MESHTENDER_LINK_PLATFORMS && typeof window.MESHTENDER_LINK_PLATFORMS === 'object'
      ? window.MESHTENDER_LINK_PLATFORMS
      : {};

  // cfgFor coerces to a safe default (a generic URL field) for an unknown key.
  function cfgFor(key) {
    var c = CONFIG[key];
    if (!c || typeof c !== 'object') return { kind: 'url', placeholder: '', label: true };
    return c;
  }

  function clearError(row) {
    var v = row.querySelector('.link-value');
    if (v) v.classList.remove('is-invalid');
    var m = row.querySelector('.link-error');
    if (m) m.remove();
  }

  function showError(row, text) {
    var v = row.querySelector('.link-value');
    if (!v) return;
    v.classList.add('is-invalid');
    var m = document.createElement('div');
    m.className = 'invalid-feedback d-block link-error';
    m.textContent = text;
    v.insertAdjacentElement('afterend', m);
  }

  // applyPlatform syncs a row's value placeholder and label visibility to its
  // selected platform. When the label field doesn't apply (branded platforms),
  // it's hidden and cleared so a stale label isn't submitted.
  function applyPlatform(row) {
    var sel = row.querySelector('.link-platform');
    var value = row.querySelector('.link-value');
    if (!sel || !value) return;
    var cfg = cfgFor(sel.value);
    value.setAttribute('placeholder', cfg.placeholder || '');
    var labelCol = row.querySelector('.link-label-col');
    if (labelCol) {
      if (cfg.label) {
        labelCol.classList.remove('d-none');
      } else {
        labelCol.classList.add('d-none');
        var li = labelCol.querySelector('.link-label');
        if (li) li.value = '';
      }
    }
    clearError(row);
  }

  function isURLish(val) {
    return val.indexOf('://') !== -1 || val.indexOf('//') === 0;
  }

  // httpURLOK reports whether candidate parses as an http(s) URL with a host.
  function httpURLOK(candidate) {
    try {
      var u = new URL(candidate);
      return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname;
    } catch (e) {
      return false;
    }
  }

  // validate mirrors the server's per-kind rules loosely; returns an error string
  // or '' when acceptable. Empty values are fine — empty rows are dropped server-side.
  function validate(kind, raw) {
    var val = (raw == null ? '' : String(raw)).trim();
    if (val === '') return '';
    switch (kind) {
      case 'email':
        return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(val) ? '' : 'Enter a valid email address.';
      case 'key':
        return /^[0-9a-f]{64}$/i.test(val) ? '' : 'Enter a valid MeshCore public key (64-character hex).';
      case 'text':
        return /\s/.test(val) ? 'Enter a valid username (no spaces).' : '';
      case 'handle':
        // A pasted URL must be http(s); otherwise any space-free handle is fine
        // (the server canonicalises it and checks the host).
        if (isURLish(val)) {
          var c = val.indexOf('://') === -1 ? 'https:' + val : val;
          return httpURLOK(c) ? '' : 'Enter a valid username or profile URL.';
        }
        return /\s/.test(val) ? 'Enter a valid username or profile URL.' : '';
      default: // 'url'
        var candidate = val.indexOf('://') === -1 ? 'https://' + val.replace(/^\/\//, '') : val;
        return httpURLOK(candidate) ? '' : 'Each link must be a valid http:// or https:// URL.';
    }
  }

  function initEditor(root) {
    var rows = root.querySelector('[data-link-rows]');
    var tpl = root.querySelector('[data-link-tpl]');
    var add = root.querySelector('[data-link-add]');
    if (!rows || !tpl || !add) return;
    var form = root.tagName === 'FORM' ? root : root.closest('form');

    function eachRow(fn) {
      rows.querySelectorAll('.link-row').forEach(fn);
    }

    add.addEventListener('click', function () {
      rows.appendChild(tpl.content.cloneNode(true));
      var added = rows.querySelector('.link-row:last-child');
      if (added) applyPlatform(added);
    });

    rows.addEventListener('click', function (e) {
      var btn = e.target.closest('.remove-link');
      if (btn) {
        var r = btn.closest('.link-row');
        if (r) r.remove();
      }
    });

    rows.addEventListener('change', function (e) {
      var t = e.target;
      if (t && t.classList && t.classList.contains('link-platform')) {
        var r = t.closest('.link-row');
        if (r) applyPlatform(r);
      }
    });

    rows.addEventListener('input', function (e) {
      var r = e.target.closest('.link-row');
      if (r) clearError(r);
    });

    // Sync server-rendered rows on load.
    eachRow(applyPlatform);

    if (form) {
      form.addEventListener('submit', function (e) {
        var firstBad = null;
        eachRow(function (row, i) {
          clearError(row);
          // Renumber the primary-contact radio (user editor only) to DOM order so
          // the checked one's value matches its row index in the submitted arrays.
          var radio = row.querySelector('input[name="link_primary"]');
          if (radio) radio.value = i;

          var sel = row.querySelector('.link-platform');
          var value = row.querySelector('.link-value');
          if (!sel || !value) return;
          var err = validate(cfgFor(sel.value).kind, value.value);
          if (err) {
            showError(row, err);
            if (!firstBad) firstBad = value;
          }
        });
        if (firstBad) {
          e.preventDefault();
          firstBad.focus();
        }
      });
    }
  }

  // initAll wires up every [data-link-editor] under root exactly once. Guarded so
  // re-running (on htmx swaps) never double-binds an already-initialized editor.
  function initAll(root) {
    var scope = root && root.querySelectorAll ? root : document;
    scope.querySelectorAll('[data-link-editor]').forEach(function (el) {
      if (el.dataset.linkEditorReady) return;
      el.dataset.linkEditorReady = '1';
      initEditor(el);
    });
  }

  initAll(document);
  // The org page loads the editor into a modal via htmx; init the swapped-in form.
  document.body && document.addEventListener('htmx:afterSwap', function (e) {
    initAll(e.target || document);
  });
})();
