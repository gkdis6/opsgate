// opsgate passkey ceremonies. The server decides everything; this script only
// moves WebAuthn options and results between the server and the browser API.
"use strict";
(function () {
  const root = document.getElementById("passkey");
  const button = document.getElementById("passkey-start");
  const status = document.getElementById("passkey-status");
  if (!root || !button || !status) return;

  const b64 = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf)))
    .replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const bytes = (s) => Uint8Array.from(
    atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4)),
    (c) => c.charCodeAt(0));
  const ids = (list) => (list || []).map((c) => Object.assign({}, c, { id: bytes(c.id) }));

  function show(text, isError) {
    status.textContent = text;
    status.className = isError ? "error" : "muted";
    status.hidden = false;
  }

  async function post(url, body) {
    const res = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || "Request failed (" + res.status + ")");
    return data;
  }

  async function register() {
    const invite = root.dataset.invite;
    const options = (await post("/passkey/register/begin", { invite })).publicKey;
    options.challenge = bytes(options.challenge);
    options.user.id = bytes(options.user.id);
    options.excludeCredentials = ids(options.excludeCredentials);
    const cred = await navigator.credentials.create({ publicKey: options });
    await post("/passkey/register/finish", {
      invite,
      credential: {
        id: cred.id, rawId: b64(cred.rawId), type: cred.type,
        response: {
          clientDataJSON: b64(cred.response.clientDataJSON),
          attestationObject: b64(cred.response.attestationObject),
          transports: cred.response.getTransports ? cred.response.getTransports() : [],
        },
      },
    });
    button.hidden = true;
    show("Passkey created. You can close this page and sign in with it.", false);
  }

  async function login() {
    const options = (await post("/oauth/passkey/begin", {})).publicKey;
    options.challenge = bytes(options.challenge);
    options.allowCredentials = ids(options.allowCredentials);
    const cred = await navigator.credentials.get({ publicKey: options });
    const r = cred.response;
    const done = await post("/oauth/passkey/finish", {
      credential: {
        id: cred.id, rawId: b64(cred.rawId), type: cred.type,
        response: {
          clientDataJSON: b64(r.clientDataJSON),
          authenticatorData: b64(r.authenticatorData),
          signature: b64(r.signature),
          userHandle: r.userHandle ? b64(r.userHandle) : "",
        },
      },
    });
    window.location.assign(done.next);
  }

  if (!window.PublicKeyCredential || !navigator.credentials) {
    button.disabled = true;
    show("This browser does not support passkeys. Open this page in Safari, Chrome, Edge or Firefox.", true);
    return;
  }
  button.addEventListener("click", async () => {
    button.disabled = true;
    show("Waiting for your passkey…", false);
    try {
      await (root.dataset.mode === "register" ? register() : login());
    } catch (e) {
      button.disabled = false;
      show(e && e.name === "NotAllowedError" ? "Cancelled or timed out. Try again. In an app's built-in browser, open this page in Safari or Chrome." : (e.message || String(e)), true);
    }
  });
})();
