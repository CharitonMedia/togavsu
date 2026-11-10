// End-to-end check of the edit page with the real browser editor (PRD 8.20, AC-041):
// headless Chromium (SwiftShader WebGL2) loads /files/{id}/edit, the LawCraft wrapper
// reports wordcraft:ready, the page sends the current version in and the firm's
// details (togavsu:run), a person types and presses Ctrl+S, and the page stores the
// save as a new version. Run by internal/editor TestBrowserEditPageE2E.
//
// usage: node edit-page.mjs BASE_URL FILE_ID USER SHOT_DIR
// env:   CHROME (browser binary), PUPPETEER_CORE (the puppeteer-core package folder)
import { createRequire } from "node:module";
import path from "node:path";

const [base, fileId, user, shots] = process.argv.slice(2);
const require = createRequire(path.join(process.env.PUPPETEER_CORE, "package.json"));
const puppeteer = require("puppeteer-core");

const out = { messages: [], console: [], violations: [], state: "", saved: false };
const browser = await puppeteer.launch({
  executablePath: process.env.CHROME,
  headless: true,
  args: ["--no-sandbox", "--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist", "--window-size=1400,1000"],
  defaultViewport: { width: 1400, height: 1000 },
});
try {
  const page = await browser.newPage();
  await page.setExtraHTTPHeaders({ "X-Test-User": user });
  page.on("console", (m) => { if (m.type() === "error") out.console.push(m.text()); });
  page.on("pageerror", (e) => out.console.push(String(e)));
  await page.evaluateOnNewDocument(() => {
    window.__seen = [];
    window.addEventListener("message", (e) => {
      const d = e.data || {};
      window.__seen.push({ type: d.type, error: d.error || null, name: d.name || null });
    });
    document.addEventListener("securitypolicyviolation", (e) => {
      (window.__csp = window.__csp || []).push(e.violatedDirective + " " + e.blockedURI);
    });
  });
  await page.goto(`${base}/files/${fileId}/edit`, { waitUntil: "domcontentloaded" });
  const seen = (type) => page.evaluate((t) => window.__seen.some((m) => m.type === t), type);
  const until = async (fn, ms, what) => {
    const end = Date.now() + ms;
    while (Date.now() < end) { if (await fn()) return; await new Promise((r) => setTimeout(r, 250)); }
    throw new Error("timed out waiting for " + what);
  };
  await until(async () => (await seen("wordcraft:ready")) || (await seen("wordcraft:failed")), 120000, "wordcraft:ready");
  if (await seen("wordcraft:failed")) throw new Error("the editor reported wordcraft:failed");
  await until(() => seen("togavsu:result"), 30000, "togavsu:result");
  await new Promise((r) => setTimeout(r, 3000));
  if (shots) await page.screenshot({ path: path.join(shots, "edit-open.png") });
  const frame = await page.$("#frame");
  const box = await frame.boundingBox();
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 3);
  await page.keyboard.press("End");
  await page.keyboard.type(" Typed in the browser editor.");
  await until(async () => (await page.$eval("#state", (e) => e.textContent)) === "Unsaved changes", 15000, "the unsaved-changes header");
  if (shots) await page.screenshot({ path: path.join(shots, "edit-dirty.png") });
  await page.keyboard.down("Control");
  await page.keyboard.press("KeyS");
  await page.keyboard.up("Control");
  await until(async () => /^Saved as version/.test(await page.$eval("#state", (e) => e.textContent)), 30000, "the save");
  out.state = await page.$eval("#state", (e) => e.textContent);
  out.saved = true;
  if (shots) await page.screenshot({ path: path.join(shots, "edit-saved.png") });
  out.messages = await page.evaluate(() => window.__seen);
  out.violations = await page.evaluate(() => window.__csp || []);
} catch (e) {
  out.error = String(e && e.message || e);
} finally {
  await browser.close();
}
console.log(JSON.stringify(out));
process.exit(out.error ? 1 : 0);
