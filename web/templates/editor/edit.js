// The edit page's side of LawCraft's host bridge (?host=parent). The page sends the
// current version in (wordcraft:open), stores each save (wordcraft:save) as a new
// version, shows unsaved changes (wordcraft:dirty), keeps the edit lock renewed, and
// says so when the browser cannot run the editor (wordcraft:failed). Togavsu's wrapper
// adds togavsu:run (commands the page asks for on open: the firm's details, court
// profiles, the matter's court, and the matter's caption New Pleading opens with) and
// togavsu:profile (a court profile saved in the editor, written back to Togavsu
// unverified).
(function () {
  "use strict";
  var root = document.getElementById("editor");
  if (!root || root.dataset.mode !== "edit") return;
  var fileId = root.dataset.fileId;
  var base = "/files/" + encodeURIComponent(fileId);
  var api = "/api/v1/files/" + encodeURIComponent(fileId);
  var frame = document.getElementById("frame");
  var state = document.getElementById("state");
  var message = document.getElementById("message");
  var csrf = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";
  var dirty = false, lost = false, closing = false, runId = 0;
  var RENEW_MS = 60 * 1000;

  function show(text) { message.textContent = text; message.hidden = !text; }
  function setState(text, cls) { state.textContent = text; state.className = "tag" + (cls ? " " + cls : ""); }
  function headers(extra) {
    var h = { "X-CSRF-Token": csrf };
    for (var k in extra || {}) h[k] = extra[k];
    return h;
  }
  function post(msg, transfer) { frame.contentWindow.postMessage(msg, location.origin, transfer || []); }
  function problem(res) {
    return res.json().then(function (b) { return b.message || res.statusText; }, function () { return res.statusText; });
  }
  function download(name, bytes) {
    var url = URL.createObjectURL(new Blob([bytes]));
    var a = document.createElement("a");
    a.href = url; a.download = name; document.body.appendChild(a); a.click(); a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 10000);
  }

  function open() {
    fetch(base + "/edit/document", { credentials: "same-origin" }).then(function (res) {
      if (!res.ok) return problem(res).then(function (m) { throw new Error(m); });
      var name = decodeURIComponent(res.headers.get("X-File-Name") || root.dataset.fileName);
      return res.arrayBuffer().then(function (buf) {
        post({ type: "wordcraft:open", name: name, bytes: buf }, [buf]);
        return fetch(base + "/edit/context", { credentials: "same-origin" });
      });
    }).then(function (res) {
      if (!res.ok) return;
      return res.json().then(function (c) {
        // One job per command: the editor stops a job at its first failing command, so a
        // court profile it refuses must not drop the matter's court or the caption.
        (c.commands || []).forEach(function (cmd) { post({ type: "togavsu:run", id: ++runId, commands: [cmd] }); });
      });
    }).catch(function (e) { show("The document could not be opened: " + e.message); });
  }

  var DOCS = /\.(docx|docm|doc|odt|rtf|html?|md|markdown|txt)$/i;
  function save(m) {
    var name = String(m.name || root.dataset.fileName);
    if (!DOCS.test(name)) {
      // PDF and picture exports are copies, not versions: they download.
      download(name, m.bytes);
      return;
    }
    setState("Saving…");
    fetch(base + "/edit/save", {
      method: "POST", credentials: "same-origin", body: m.bytes,
      headers: headers({ "Content-Type": "application/octet-stream", "X-File-Name": encodeURIComponent(name) })
    }).then(function (res) {
      if (!res.ok) return problem(res).then(function (msg) { throw new Error(msg); });
      return res.json().then(function (v) { setState("Saved as version " + v.number); show(""); });
    }).catch(function (e) {
      setState("Not saved", "dirty");
      show("The save did not go through: " + e.message + ". Your changes are still in the editor.");
    });
  }

  function saveProfile(profile) {
    // A profile saved in the editor is written back; it stays unverified until a person approves it.
    fetch("/api/v1/court-profiles", { credentials: "same-origin" }).then(function (res) { return res.json(); }).then(function (list) {
      var found = (list.data || []).filter(function (c) { return c.body && c.body.id === profile.id; })[0];
      var req = found
        ? fetch("/api/v1/court-profiles/" + encodeURIComponent(found.id), { method: "PATCH", credentials: "same-origin", headers: headers({ "Content-Type": "application/json" }), body: JSON.stringify({ body: profile }) })
        : fetch("/api/v1/court-profiles", { method: "POST", credentials: "same-origin", headers: headers({ "Content-Type": "application/json" }), body: JSON.stringify({ name: profile.name || profile.id, body: profile }) });
      return req.then(function (res) {
        if (!res.ok) return problem(res).then(function (msg) { show("The court profile was not saved to Togavsu: " + msg); });
        show("Court profile " + (profile.name || profile.id) + " saved to Togavsu, unverified until a person approves it.");
      });
    });
  }

  function renew() {
    if (lost || closing) return;
    fetch(api + "/lock/renew", { method: "POST", credentials: "same-origin", headers: headers() }).then(function (res) {
      if (res.ok) return;
      lost = true;
      problem(res).then(function (m) { show("You no longer hold the edit lock (" + m + "). Save a copy with Download, then reopen the file."); });
    });
  }

  function release() {
    if (closing) return;
    closing = true;
    fetch(api + "/lock", { method: "DELETE", credentials: "same-origin", keepalive: true, headers: headers() });
  }

  window.addEventListener("message", function (e) {
    if (e.origin !== location.origin || e.source !== frame.contentWindow) return;
    var m = e.data || {};
    switch (m.type) {
      case "wordcraft:ready": open(); break;
      case "wordcraft:save": save(m); break;
      case "wordcraft:dirty":
        dirty = !!m.dirty;
        setState(dirty ? "Unsaved changes" : "", dirty ? "dirty" : "");
        break;
      case "wordcraft:failed":
        frame.hidden = true;
        show("This browser cannot run the editor: it needs WebGPU or WebGL2. Download the file to edit it on this computer.");
        release();
        break;
      case "togavsu:profile": if (m.profile && m.profile.id) saveProfile(m.profile); break;
      case "togavsu:result":
        if (m.error) show("The editor could not load part of the firm's details: " + m.error);
        break;
    }
  });
  window.addEventListener("beforeunload", function (e) {
    if (dirty) { e.preventDefault(); e.returnValue = ""; return ""; }
  });
  // Closing the tab does not release the lock: it stops renewing and lapses after the
  // Document editor tab's timeout (5 minutes). Close editor releases it at once.
  var close = document.getElementById("close");
  if (close) close.addEventListener("click", function () {
    if (dirty && !confirm("Leave the editor? Your unsaved changes will be lost.")) return;
    dirty = false;
    release();
    location.href = document.referrer && new URL(document.referrer).origin === location.origin ? document.referrer : "/practice/matters";
  });
  setInterval(renew, RENEW_MS);
})();
