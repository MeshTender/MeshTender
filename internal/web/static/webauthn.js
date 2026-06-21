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
  const username = document.getElementById("username").value.trim();
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
    setStatus("Starting…");
    const options = await postJSON("/api/register/begin", {});
    const cred = await navigator.credentials.create({ publicKey: decodeCreation(options.publicKey) });
    setStatus("Verifying…");
    const result = await fetch("/api/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(encodeAttestation(cred)),
    });
    const data = await result.json().catch(() => ({}));
    if (!result.ok) throw new Error(data.error || "could not add passkey");
    window.location = "/account?ok=" + encodeURIComponent("Passkey added.");
  } catch (e) {
    setStatus("Error: " + e.message);
  }
}

// passkeyLogin runs a username-scoped assertion (for credentials that aren't
// discoverable, where the server needs to know which account to challenge).
async function passkeyLogin() {
  const username = document.getElementById("username").value.trim();
  try {
    setStatus("Starting…");
    const options = await postJSON("/api/login/begin", { username });
    const cred = await navigator.credentials.get({ publicKey: decodeRequest(options.publicKey) });
    await finishAssertion("/api/login/finish", cred);
  } catch (e) {
    setStatus("Error: " + e.message);
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

// initSigninPasskey kicks off a passive conditional-mediation prompt on page
// load, so a passkey can be offered automatically when the browser supports it.
async function initSigninPasskey() {
  if (!window.PublicKeyCredential || !PublicKeyCredential.isConditionalMediationAvailable) return;
  try {
    if (await PublicKeyCredential.isConditionalMediationAvailable()) {
      passkeyDiscoverable("conditional");
    }
  } catch (_) {
    /* not supported — fall back to the explicit button */
  }
}

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
