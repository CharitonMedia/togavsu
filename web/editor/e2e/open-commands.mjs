// Unit check of edit.js's open path without a browser: with stand-ins for the DOM,
// fetch and the editor frame, wordcraft:ready makes the page send the current version,
// then each command from /edit/context as its own togavsu:run job, so one command the
// editor refuses cannot drop the ones after it. Prints the posted messages as JSON.
// Run by internal/editor TestEditPageSendsEachOpenCommandAsItsOwnJob.
//
// usage: node open-commands.mjs EDIT_JS CONTEXT_JSON
import fs from "node:fs";
import vm from "node:vm";

const [script, contextFile] = process.argv.slice(2);
const context = JSON.parse(fs.readFileSync(contextFile, "utf8"));
const posted = [];
const listeners = {};
const el = (extra) => Object.assign({ hidden: false, textContent: "", className: "", dataset: {} }, extra);
const frameWindow = { postMessage: (msg) => posted.push(msg) };
const elements = {
  editor: el({ dataset: { mode: "edit", fileId: "f1", fileName: "Letter.docx" } }),
  frame: el({ contentWindow: frameWindow }),
  state: el(),
  message: el(),
};
const respond = (body, headers) => ({
  ok: true,
  headers: { get: (k) => (headers || {})[k] || null },
  arrayBuffer: async () => new ArrayBuffer(1),
  json: async () => body,
});
const sandbox = {
  document: { getElementById: (id) => elements[id] || null, querySelector: () => null },
  window: { addEventListener: (type, fn) => { listeners[type] = fn; } },
  location: { origin: "https://firm.example" },
  fetch: async (url) => (String(url).endsWith("/edit/context") ? respond(context) : respond({}, { "X-File-Name": "Letter.docx" })),
  setInterval: () => 0,
  setTimeout: () => 0,
  console,
};
vm.runInNewContext(fs.readFileSync(script, "utf8"), sandbox);
listeners.message({ origin: "https://firm.example", source: frameWindow, data: { type: "wordcraft:ready" } });
await new Promise((r) => setImmediate(r));
await new Promise((r) => setTimeout(r, 50));
listeners.message({ origin: "https://firm.example", source: frameWindow, data: { type: "togavsu:result", id: 2, error: "law.profiles.create: refused" } });
console.log(JSON.stringify({ posted: posted.map((m) => ({ type: m.type, id: m.id, commands: m.commands })), message: elements.message.textContent }));
