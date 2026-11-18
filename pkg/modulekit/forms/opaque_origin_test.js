#!/usr/bin/env node
// Browser-level check for CORE-33 offline queue under an opaque origin
// (CSP sandbox without allow-same-origin): localStorage throws SecurityError,
// enqueue/save must keep a memory fallback so autosave can queue offline.
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const src = fs.readFileSync(path.join(__dirname, "autosave.go"), "utf8");
const marker = "const OfflineQueueJS = `";
const start = src.indexOf(marker);
if (start < 0) {
  console.error("OfflineQueueJS not found in autosave.go");
  process.exit(1);
}
const bodyStart = start + marker.length;
const end = src.indexOf("`\n\n// RepeatingJS", bodyStart);
if (end < 0) {
  console.error("OfflineQueueJS closing backtick not found");
  process.exit(1);
}
const js = src.slice(bodyStart, end);
if (!js.includes("var mem") && !js.includes("mem =")) {
  console.error("OfflineQueueJS must keep an in-memory queue fallback");
  process.exit(1);
}
if (!js.includes('credentials: "include"')) {
  console.error("offline fetch must use credentials include for ACAO null");
  process.exit(1);
}

const storageThrows = true;
const localStorage = {
  getItem() {
    if (storageThrows) {
      const e = new Error("SecurityError");
      e.name = "SecurityError";
      throw e;
    }
    return null;
  },
  setItem() {
    if (storageThrows) {
      const e = new Error("SecurityError");
      e.name = "SecurityError";
      throw e;
    }
  },
};

const listeners = {};
const document = {
  querySelector() {
    return { value: "csrf-token" };
  },
  addEventListener(type, fn) {
    (listeners[type] = listeners[type] || []).push(fn);
  },
};
const windowObj = {
  TOGAVSU_FORMS_QUEUE_KEY: "test-queue",
  TOGAVSU_PUBLIC_CSRF: "csrf-token",
  addEventListener(type, fn) {
    (listeners[type] = listeners[type] || []).push(fn);
  },
  localStorage,
};
const navigator = { onLine: true };
const fetches = [];

async function main() {
  const fetchFn = async function (url, opts) {
    fetches.push({ url, opts });
    return { ok: true };
  };
  vm.runInNewContext(js, {
    window: windowObj,
    document,
    navigator,
    console,
    localStorage,
    fetch: fetchFn,
  });
  const api = windowObj.TogavsuFormsOffline;
  if (!api || !api.enqueue || !api.flush) {
    console.error("TogavsuFormsOffline missing");
    process.exit(1);
  }
  // enqueue must not throw when localStorage throws SecurityError
  api.enqueue("/p/intake/api/autosave", JSON.stringify({ field: "name", value: "Avery" }), {
    "Content-Type": "application/json",
    "X-CSRF-Token": "csrf-token",
  });
  await api.flush();
  if (fetches.length !== 1) {
    console.error("expected one flush fetch, got", fetches.length);
    process.exit(1);
  }
  if (!fetches[0].opts.headers["X-CSRF-Token"]) {
    console.error("flush must send CSRF");
    process.exit(1);
  }
  console.log("opaque-origin offline queue behavioural ok");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
