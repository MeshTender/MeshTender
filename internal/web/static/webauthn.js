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

async function passkeyLogin() {
  const username = document.getElementById("username").value.trim();
  try {
    setStatus("Starting…");
    const options = await postJSON("/api/login/begin", { username });
    const cred = await navigator.credentials.get({ publicKey: decodeRequest(options.publicKey) });
    setStatus("Verifying…");
    const result = await fetch("/api/login/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(encodeAssertion(cred)),
    });
    const data = await result.json().catch(() => ({}));
    if (!result.ok) throw new Error(data.error || "login failed");
    window.location = data.redirect || "/";
  } catch (e) {
    setStatus("Error: " + e.message);
  }
}
