/* CORE-34: staff banner show/hide bridge. Module frames post {type:"togavsu:banner.show"|"togavsu:banner.hide", module_id}. */
(function () {
  function apply(moduleId, show) {
    var nodes = document.querySelectorAll('.staff-banner[data-module="' + moduleId + '"]');
    for (var i = 0; i < nodes.length; i++) {
      if (show) nodes[i].removeAttribute("hidden");
      else nodes[i].setAttribute("hidden", "");
    }
  }
  function fromBanner(ev) {
    var frames = document.querySelectorAll("iframe.staff-banner-frame");
    for (var i = 0; i < frames.length; i++) {
      if (ev.source === frames[i].contentWindow) return frames[i].getAttribute("data-module");
    }
    return "";
  }
  window.addEventListener("message", function (ev) {
    if (ev.origin !== window.location.origin) return;
    var d = ev.data;
    if (!d || typeof d !== "object") return;
    var t = d.type;
    if (t !== "togavsu:banner.show" && t !== "togavsu:banner.hide") return;
    var from = fromBanner(ev);
    if (!from) return;
    var claimed = d.module_id || d.module || "";
    if (claimed && claimed !== from) return;
    apply(from, t === "togavsu:banner.show");
  });
})();
