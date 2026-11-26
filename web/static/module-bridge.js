// CORE-09 postMessage bridge for module frames hosted on a core page (matter panels).
// Each iframe.module-frame may carry data-module, data-context-token-url, and data-staff.
(function () {
  function go(url) {
    try {
      var w = window.top && window.top !== window ? window.top : window;
      w.location.assign(url);
    } catch (e) {
      window.location.assign(url);
    }
  }
  function allowed(moduleId, page) {
    if (typeof page !== "string" || page.charAt(0) !== "/" || page.indexOf("//") === 0 || page.indexOf("://") >= 0) return false;
    var path = page.split("?")[0];
    if (path.indexOf("%") >= 0 || path.indexOf("\\") >= 0 || path.indexOf("#") >= 0 || path.split("/").some(function (part) { return part === "." || part === ".."; })) return false;
    var pref = "/modules/" + moduleId + "/";
    if (path.indexOf(pref + "tabs/") === 0 || path.indexOf(pref + "widgets/") === 0 || path.indexOf(pref + "matter-items/") === 0) return true;
    var cores = ["/practice/", "/billing/", "/trust/", "/calendar/", "/files/", "/help", "/me/", "/settings/"];
    for (var i = 0; i < cores.length; i++) {
      if (path === cores[i].replace(/\/$/, "") || path.indexOf(cores[i]) === 0) return true;
    }
    return path === "/";
  }
  function frameFor(ev) {
    var nodes = document.querySelectorAll("iframe.module-frame");
    for (var i = 0; i < nodes.length; i++) {
      if (ev.source === nodes[i].contentWindow) return nodes[i];
    }
    return null;
  }
  window.addEventListener("message", function (ev) {
    var frame = frameFor(ev);
    if (!frame || !ev.data || typeof ev.data !== "object") return;
    var moduleId = frame.getAttribute("data-module") || "";
    var staff = frame.getAttribute("data-staff") === "1";
    var d = ev.data, t = d.type;
    if (t === "togavsu:editor.open" && staff && d.file_id) {
      go("/files/" + encodeURIComponent(d.file_id) + "/edit");
      return;
    }
    if (t === "togavsu:navigate" && d.page) {
      if (typeof d.page === "string" && d.page.indexOf("/modules/") === 0 && d.page.indexOf("/modules/" + moduleId + "/") !== 0) {
        var nav = new URLSearchParams({page: d.page, params: JSON.stringify(d.params || {})});
        fetch("/modules/" + encodeURIComponent(moduleId) + "/navigate?" + nav.toString(), {credentials: "same-origin", headers: {"Accept": "application/json"}})
          .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
          .then(function (body) { if (body.url) go(body.url); })
          .catch(function () {});
        return;
      }
      if (!allowed(moduleId, d.page)) return;
      var u = d.page;
      if (d.params && typeof d.params === "object") {
        var q = new URLSearchParams();
        Object.keys(d.params).forEach(function (k) { q.set(k, d.params[k]); });
        u += (u.indexOf("?") >= 0 ? "&" : "?") + q.toString();
      }
      go(u);
      return;
    }
    if (t === "togavsu:context.refresh" && d.request_id) {
      var refresh = frame.getAttribute("data-context-token-url") || "";
      if (!refresh) {
        refresh = window.location.pathname.replace(/\/$/, "") + "/context-token" + window.location.search;
      }
      fetch(refresh, {credentials: "same-origin", headers: {"Accept": "application/json"}})
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (body) {
          frame.contentWindow.postMessage({type: "togavsu:context.token", request_id: d.request_id, token: body.token, expires_at: body.expires_at}, "*");
        })
        .catch(function () {
          frame.contentWindow.postMessage({type: "togavsu:context.error", request_id: d.request_id, code: "session_expired"}, "*");
        });
    }
  });
})();
