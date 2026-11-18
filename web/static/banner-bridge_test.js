#!/usr/bin/env node
// Behavioural check: opaque-origin banner postMessage (origin "null") is accepted
// when ev.source matches the banner iframe; foreign sources are ignored.
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const src = fs.readFileSync(path.join(__dirname, "banner-bridge.js"), "utf8");
if (src.includes("ev.origin")) {
  console.error("banner-bridge.js must not filter on ev.origin");
  process.exit(1);
}

const banner = {
  attrs: { hidden: "" },
  removeAttribute(name) { delete this.attrs[name]; },
  setAttribute(name, val) { this.attrs[name] = val; },
};
const fakeFrame = {
  contentWindow: { id: "banner-win" },
  getAttribute(name) { return name === "data-module" ? "banner-mod" : ""; },
};
const listeners = [];
const document = {
  querySelectorAll(sel) {
    if (sel.indexOf(".staff-banner") === 0) return [banner];
    if (sel === "iframe.staff-banner-frame") return [fakeFrame];
    return [];
  },
};
const window = {
  location: { origin: "https://core.example" },
  addEventListener(type, fn) { if (type === "message") listeners.push(fn); },
};
vm.runInNewContext(src, { window, document, console });
if (listeners.length !== 1) {
  console.error("expected one message listener");
  process.exit(1);
}

listeners[0]({ origin: "null", source: fakeFrame.contentWindow, data: { type: "togavsu:banner.show", module_id: "banner-mod" } });
if (Object.prototype.hasOwnProperty.call(banner.attrs, "hidden")) {
  console.error("show from opaque origin / matching source should clear hidden");
  process.exit(1);
}
banner.setAttribute("hidden", "");
listeners[0]({ origin: "null", source: { id: "other" }, data: { type: "togavsu:banner.show", module_id: "banner-mod" } });
if (!Object.prototype.hasOwnProperty.call(banner.attrs, "hidden")) {
  console.error("foreign source must not show banner");
  process.exit(1);
}
console.log("banner-bridge behavioural ok");
