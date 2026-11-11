// Togavsu shell: Ctrl-K, search keyboard navigation, sidebar persistence, idle sign-out.
// No inline script runs anywhere (CSP script-src 'self').
(function () {
  "use strict";
  var csrf = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";

  function api(method, path, body) {
    return fetch(path, {
      method: method, credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
  }

  // htmx sends the session's CSRF token on every request it makes.
  document.addEventListener("htmx:configRequest", function (e) { e.detail.headers["X-CSRF-Token"] = csrf; });

  var q = document.getElementById("search-q");
  var results = document.getElementById("search-results");
  document.addEventListener("keydown", function (e) {
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K") && q) {
      e.preventDefault();
      q.focus();
      q.select();
    }
  });
  function hits() { return results ? Array.prototype.slice.call(results.querySelectorAll("a[role=option]")) : []; }
  function move(d) {
    var list = hits();
    if (!list.length) return;
    var i = list.indexOf(document.activeElement);
    var n = i < 0 ? (d > 0 ? 0 : list.length - 1) : Math.min(list.length - 1, Math.max(0, i + d));
    list.forEach(function (a) { a.classList.remove("selected"); });
    list[n].classList.add("selected");
    list[n].focus();
  }
  if (q && results) {
    q.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      if (e.key === "Escape") { results.innerHTML = ""; }
      if (e.key === "Enter") {
        var first = results.querySelector("a.selected") || results.querySelector("a[role=option]");
        if (first) { e.preventDefault(); window.location.href = first.getAttribute("href"); }
      }
    });
    results.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      if (e.key === "ArrowUp") { e.preventDefault(); if (hits().indexOf(document.activeElement) <= 0) { q.focus(); } else { move(-1); } }
      if (e.key === "Escape") { results.innerHTML = ""; q.focus(); }
    });
    document.addEventListener("click", function (e) {
      if (!e.target.closest || !e.target.closest(".search")) { results.innerHTML = ""; }
    });
  }

  // Sidebar: the collapse button and each subgroup save with the user.
  function sidebarState() {
    var subs = [];
    document.querySelectorAll("details[data-subgroup]").forEach(function (d) {
      subs.push({ id: d.getAttribute("data-subgroup"), collapsed: !d.open });
    });
    return { collapsed: document.body.classList.contains("sidebar-collapsed"), subgroups: subs };
  }
  var toggle = document.getElementById("sidebar-toggle");
  if (toggle) {
    toggle.addEventListener("click", function (e) {
      e.preventDefault();
      document.body.classList.toggle("sidebar-collapsed");
      toggle.setAttribute("aria-pressed", document.body.classList.contains("sidebar-collapsed"));
      api("PUT", "/api/v1/me/sidebar", sidebarState());
    });
  }
  document.querySelectorAll("details[data-subgroup]").forEach(function (d) {
    d.addEventListener("toggle", function () { api("PUT", "/api/v1/me/sidebar", sidebarState()); });
  });

  // Close an open header menu when another opens or on Escape.
  document.querySelectorAll(".topbar details.menu").forEach(function (d) {
    d.addEventListener("toggle", function () {
      if (!d.open) return;
      document.querySelectorAll(".topbar details.menu").forEach(function (o) { if (o !== d) o.open = false; });
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") document.querySelectorAll("details.menu[open]").forEach(function (d) { d.open = false; });
  });

  // Appearance tab: preview the client portal header color.
  var preview = document.getElementById("client-preview");
  var clientColor = document.getElementById("client_header");
  if (preview) {
    preview.style.background = preview.getAttribute("data-color");
    if (clientColor) clientColor.addEventListener("input", function () { preview.style.background = clientColor.value; });
  }

  // Automatic sign-out after the user's idle time (My settings). The server's session
  // idle limit (security.idle_timeout) applies regardless.
  var idle = parseInt(document.body.getAttribute("data-idle-seconds") || "0", 10);
  if (idle > 0) {
    var timer;
    var reset = function () {
      clearTimeout(timer);
      timer = setTimeout(function () {
        var f = document.createElement("form");
        f.method = "post"; f.action = "/logout";
        var i = document.createElement("input");
        i.type = "hidden"; i.name = "csrf"; i.value = csrf;
        f.appendChild(i); document.body.appendChild(f); f.submit();
      }, idle * 1000);
    };
    ["mousemove", "keydown", "click", "scroll", "touchstart"].forEach(function (ev) {
      document.addEventListener(ev, reset, { passive: true });
    });
    reset();
  }
})();
