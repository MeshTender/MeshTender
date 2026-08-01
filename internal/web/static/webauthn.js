// WebAuthn ceremony helpers. Talks to the JSON endpoints in internal/auth and
// bridges the base64url <-> ArrayBuffer encoding the browser API requires.

function b64urlToBuf(s) {
  s = s.replace(/-/g, "+").replace(/_/g, "/");
  const pad = s.length % 4 ? "=".repeat(4 - (s.length % 4)) : "";
  const bin = atob(s + pad);
  const buf = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
  return buf.buffer;
}

function bufToB64url(buf) {
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Decode the PublicKeyCredentialCreationOptions JSON from the server.
function decodeCreation(opts) {
  opts.challenge = b64urlToBuf(opts.challenge);
  opts.user.id = b64urlToBuf(opts.user.id);
  if (opts.excludeCredentials) {
    opts.excludeCredentials = opts.excludeCredentials.map((c) => ({ ...c, id: b64urlToBuf(c.id) }));
  }
  return opts;
}

function decodeRequest(opts) {
  opts.challenge = b64urlToBuf(opts.challenge);
  if (opts.allowCredentials) {
    opts.allowCredentials = opts.allowCredentials.map((c) => ({ ...c, id: b64urlToBuf(c.id) }));
  }
  return opts;
}

function encodeAttestation(cred) {
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bufToB64url(cred.response.attestationObject),
      clientDataJSON: bufToB64url(cred.response.clientDataJSON),
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

function encodeAssertion(cred) {
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      authenticatorData: bufToB64url(cred.response.authenticatorData),
      clientDataJSON: bufToB64url(cred.response.clientDataJSON),
      signature: bufToB64url(cred.response.signature),
      userHandle: cred.response.userHandle ? bufToB64url(cred.response.userHandle) : null,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

function setStatus(msg) {
  const el = document.getElementById("passkey-status");
  if (el) el.textContent = msg;
}

async function postJSON(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "request failed");
  return data;
}

async function passkeyRegister() {
  const nameEl = document.getElementById("username");
  const username = nameEl ? nameEl.value.trim() : "";
  const dnEl = document.getElementById("displayName");
  const displayName = dnEl ? dnEl.value.trim() : "";
  try {
    setStatus("Starting…");
    const options = await postJSON("/api/register/begin", { username, displayName });
    const cred = await navigator.credentials.create({ publicKey: decodeCreation(options.publicKey) });
    setStatus("Verifying…");
    const result = await fetch("/api/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(encodeAttestation(cred)),
    });
    const data = await result.json().catch(() => ({}));
    if (!result.ok) throw new Error(data.error || "registration failed");
    window.location = data.redirect || "/";
  } catch (e) {
    setStatus("Error: " + e.message);
  }
}

// finishAssertion posts a completed assertion to the server and redirects.
async function finishAssertion(url, cred) {
  setStatus("Verifying…");
  const result = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(encodeAssertion(cred)),
  });
  const data = await result.json().catch(() => ({}));
  if (!result.ok) throw new Error(data.error || "login failed");
  window.location = data.redirect || "/";
}

// addPasskey registers an additional passkey for the already-signed-in user
// (used from the account page) and reloads it on success.
async function addPasskey() {
  try {
    const nameEl = document.getElementById("new_passkey_name");
    const name = nameEl ? nameEl.value.trim() : "";
    setStatus("Starting…");
    const options = await postJSON("/api/register/begin", { name });
    const cred = await navigator.credentials.create({ publicKey: decodeCreation(options.publicKey) });
    setStatus("Verifying…");
    const result = await fetch("/api/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(encodeAttestation(cred)),
    });
    const data = await result.json().catch(() => ({}));
    if (!result.ok) throw new Error(data.error || "could not add passkey");
    window.location = "/account?pk=" + encodeURIComponent("Passkey added.");
  } catch (e) {
    setStatus("Error: " + e.message);
  }
}

// passkeyLogin runs a username-scoped assertion (for credentials that aren't
// discoverable, where the server needs to know which account to challenge).
async function passkeyLogin() {
  const nameEl = document.getElementById("username");
  const username = nameEl ? nameEl.value.trim() : "";
  try {
    setStatus("Starting…");
    const options = await postJSON("/api/login/begin", { username });
    const cred = await navigator.credentials.get({ publicKey: decodeRequest(options.publicKey) });
    await finishAssertion("/api/login/finish", cred);
  } catch (e) {
    setStatus("Error: " + e.message);
  }
}

// reauthPasskey re-verifies the ALREADY signed-in user before a sensitive action
// (account deletion), then submits the form named by the button's data-form.
//
// Unlike the sign-in ceremonies this posts no username — the server asserts
// against the session's own account — and grants no access by itself: it stamps
// the session as freshly verified, and the form's handler decides what that's
// worth. requestSubmit (not submit) so the form's [data-confirm] gate still runs.
async function reauthPasskey(e) {
  const btn = e.currentTarget;
  const form = document.getElementById(btn.getAttribute("data-form") || "");
  try {
    setStatus("Starting…");
    const options = await postJSON("/account/reauth/passkey/begin", {});
    const cred = await navigator.credentials.get({ publicKey: decodeRequest(options.publicKey) });
    setStatus("Verifying…");
    const result = await fetch("/account/reauth/passkey/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(encodeAssertion(cred)),
    });
    const data = await result.json().catch(() => ({}));
    if (!result.ok) throw new Error(data.error || "verification failed");
    setStatus("Verified.");
    if (form) form.requestSubmit();
  } catch (err) {
    setStatus("Error: " + err.message);
  }
}

// Tracks an in-flight conditional-mediation request so an explicit action can
// supersede the passive autofill prompt without the two colliding.
let conditionalAbort = null;

// passkeyDiscoverable runs a usernameless assertion. mediation "conditional"
// surfaces passkeys passively in the username field's autofill; "optional"
// (or omitted) pops the passkey chooser immediately.
async function passkeyDiscoverable(mediation) {
  try {
    if (mediation !== "conditional") setStatus("Starting…");
    const options = await postJSON("/api/login/discoverable/begin", {});
    const getOpts = { publicKey: decodeRequest(options.publicKey) };
    if (mediation) getOpts.mediation = mediation;
    if (mediation === "conditional") {
      conditionalAbort = new AbortController();
      getOpts.signal = conditionalAbort.signal;
    }
    const cred = await navigator.credentials.get(getOpts);
    await finishAssertion("/api/login/discoverable/finish", cred);
  } catch (e) {
    if (e.name === "AbortError") return; // superseded by an explicit action
    if (mediation === "conditional") return; // autofill simply went unused
    setStatus("Error: " + e.message);
  }
}

// passkeyButton handles the explicit "Sign in with a passkey" button: it uses a
// usernameless prompt by default, falling back to a username-scoped challenge
// when a username is typed (covers older, non-discoverable credentials).
function passkeyButton() {
  if (conditionalAbort) {
    conditionalAbort.abort();
    conditionalAbort = null;
  }
  const el = document.getElementById("username");
  const username = el ? el.value.trim() : "";
  return username ? passkeyLogin() : passkeyDiscoverable("optional");
}

// passkeysUnsupported reports whether this browser has no WebAuthn at all, hiding
// the passkey controls on the sign-in / sign-up forms when so. Both templates wrap
// their passkey button in #passkey-section and id their divider #password-divider, so
// one helper serves both: a button that cannot possibly work is worse than no button.
function passkeysUnsupported() {
  if (window.PublicKeyCredential) return false;
  const section = document.getElementById("passkey-section");
  const divider = document.getElementById("password-divider");
  if (section) section.classList.add("d-none");
  if (divider) divider.classList.add("d-none");
  return true;
}

// initSigninPasskey kicks off a passive conditional-mediation prompt on page
// load, so a passkey can be offered automatically when the browser supports it.
async function initSigninPasskey() {
  if (passkeysUnsupported()) return;
  if (!PublicKeyCredential.isConditionalMediationAvailable) return;
  try {
    if (await PublicKeyCredential.isConditionalMediationAvailable()) {
      passkeyDiscoverable("conditional");
    }
  } catch (_) {
    /* not supported — fall back to the explicit button */
  }
}

// initSignupEmphasis adapts the sign-up form to what this device can actually do.
// The server renders both credential options, so no-JS visitors always see both;
// this only shifts the emphasis.
//
// Three states, because "can't use a platform authenticator" is NOT the same as
// "can't use a passkey" — registration asks only for a preferred resident key with no
// attachment constraint (see RegisterBegin), so a roaming security key works fine:
//
//   1. no WebAuthn at all      → hide the passkey half; it would be a dead control
//   2. WebAuthn, no platform   → leave both as rendered; a security key still works,
//      authenticator              and nothing should imply otherwise
//   3. platform authenticator  → collapse the password half behind a toggle, so the
//      available                  easy, safe path is the default and a password is a
//                                 deliberate choice rather than an equal option
async function initSignupEmphasis() {
  const passkeySection = document.getElementById("passkey-section");
  const passwordSection = document.getElementById("password-section");
  const divider = document.getElementById("password-divider");
  const unavailable = document.getElementById("passkey-unavailable");
  const useWrap = document.getElementById("use-password-wrap");
  const useBtn = document.getElementById("use-password");
  const password = document.getElementById("password");
  const form = document.getElementById("signup-form");
  const passkeyBtn = document.getElementById("signup-passkey-btn");
  // Not the sign-up page (or a markup change) — do nothing rather than guess.
  if (!passkeySection || !passwordSection || !password || !form) return;

  // State 1: WebAuthn is unavailable, so the passkey button cannot work.
  if (passkeysUnsupported()) {
    if (unavailable) unavailable.classList.remove("d-none");
    return;
  }

  let platform = false;
  try {
    if (PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable) {
      platform = await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable();
    }
  } catch (_) {
    platform = false; // treat an unclear answer as state 2 and change nothing
  }
  // State 2: leave the form exactly as rendered.
  if (!platform || !useWrap || !useBtn) return;

  // State 3: collapse the password half.
  //
  // Disabling the input matters as much as hiding it. A hidden-but-required control
  // makes the browser refuse to submit — Chrome reports "an invalid form control is
  // not focusable" and the submission silently dies — which anyone pressing Enter in
  // the username field would hit. Disabled controls are skipped by validation and
  // left out of the POST entirely.
  passwordSection.classList.add("d-none");
  if (divider) divider.classList.add("d-none");
  password.disabled = true;
  useWrap.classList.remove("d-none");

  useBtn.addEventListener("click", function () {
    passwordSection.classList.remove("d-none");
    if (divider) divider.classList.remove("d-none");
    password.disabled = false;
    useWrap.classList.add("d-none");
    password.focus();
  });

  // With the password half collapsed, Enter in the username field should start the
  // passkey ceremony — the visible primary action — instead of posting an empty
  // password form.
  form.addEventListener("submit", function (e) {
    if (password.disabled && passkeyBtn) {
      e.preventDefault();
      passkeyBtn.click();
    }
  });
}

// Bind the passkey buttons by id (only the one on the current page exists). This
// replaces inline onclick handlers so the page carries no inline JS (CSP).
(function () {
  var bindings = [
    ["add-passkey-btn", addPasskey],
    ["passkey-btn", passkeyButton],
    ["signup-passkey-btn", passkeyRegister],
    ["delete-reauth-btn", reauthPasskey],
  ];
  bindings.forEach(function (b) {
    var el = document.getElementById(b[0]);
    if (el) el.addEventListener("click", b[1]);
  });
  if (document.getElementById("signup-form")) initSignupEmphasis();
})();

// Show/hide password toggles (any element with data-pwtoggle="<input id>").
document.addEventListener("click", function (e) {
  const t = e.target.closest("[data-pwtoggle]");
  if (!t) return;
  e.preventDefault();
  const input = document.getElementById(t.getAttribute("data-pwtoggle"));
  if (!input) return;
  const showing = input.type !== "password";
  input.type = showing ? "password" : "text";
  // Keep the control's state in sync for screen readers: pressed = revealed.
  const nowShowing = !showing;
  t.setAttribute("aria-pressed", String(nowShowing));
  t.setAttribute("aria-label", nowShowing ? "Hide password" : "Show password");
  t.setAttribute("title", nowShowing ? "Hide password" : "Show password");
});
