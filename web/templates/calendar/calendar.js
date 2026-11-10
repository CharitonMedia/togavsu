// Copy buttons for the available-dates panel and new feed links. No inline script, so the CSP holds.
document.addEventListener("click", function (ev) {
  var btn = ev.target.closest("[data-copy]");
  if (!btn) return;
  var el = document.getElementById(btn.getAttribute("data-copy"));
  if (!el) return;
  var text = el.value;
  var done = function () { btn.textContent = "Copied"; setTimeout(function () { btn.textContent = "Copy"; }, 1500); };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done, function () { el.select(); });
  } else {
    el.select();
  }
});
