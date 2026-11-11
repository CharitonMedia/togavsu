// Home screen grid (PRD 8.6): gridstack.js on the server-rendered widgets. Edit layout
// turns on drag and resize; every change saves the working layout through
// PUT /api/v1/me/layout. Links don't open while the layout is being edited.
(function () {
  "use strict";
  var el = document.getElementById("home-grid");
  if (!el || typeof GridStack === "undefined") return;
  var csrf = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";
  var editing = el.getAttribute("data-editing") === "true";
  var grid = GridStack.init({ column: 12, cellHeight: 70, margin: 8, float: false, staticGrid: !editing, disableOneColumnMode: false, columnOpts: { breakpoints: [{ w: 700, c: 1 }] } }, el);
  el.classList.add("gs-ready");

  function layout() {
    var out = [];
    grid.getGridItems().forEach(function (item) {
      var n = item.gridstackNode;
      var w = { widget_id: item.getAttribute("data-widget-id"), x: n.x, y: n.y, w: n.w, h: n.h };
      var f = item.getAttribute("data-filters");
      if (f && f !== "null") { w.filters = JSON.parse(f); }
      out.push(w);
    });
    out.sort(function (a, b) { return a.y - b.y || a.x - b.x; });
    return out;
  }
  function save() {
    return fetch("/api/v1/me/layout", {
      method: "PUT", credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify({ widgets: layout() })
    });
  }
  if (editing) {
    grid.on("change", function () { save(); });
    el.addEventListener("click", function (e) {
      var a = e.target.closest && e.target.closest("a");
      if (a && el.contains(a)) { e.preventDefault(); }
    }, true);
  }
  // Save as stores the layout on screen, positions included.
  var saveAs = document.getElementById("save-as-form");
  if (saveAs) {
    saveAs.addEventListener("submit", function () {
      document.getElementById("save-as-widgets").value = JSON.stringify(layout());
    });
  }
  var done = document.getElementById("edit-done");
  if (done) {
    done.addEventListener("click", function (e) {
      e.preventDefault();
      save().then(function () { window.location.href = "/"; });
    });
  }
})();
