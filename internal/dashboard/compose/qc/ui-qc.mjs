// UI visual QC for the ui assignment: opens every shell screen and admin tab as each
// role in headless Chrome, records status, title and console/page errors, failed
// requests and CSP violations, and saves a screenshot of each.
//
//   node ui-qc.mjs plan.json out-dir
//
// plan.json comes from TestUIVisualQC: {base, sessions: {role: [cookie...]}, pages:
// [{name, role, path, expect, width, action}]}. Exit 1 on any unexpected status or error.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const require = createRequire(process.env.QC_NODE_MODULES ? path.join(process.env.QC_NODE_MODULES, "x.js") : import.meta.url);
const puppeteer = require("puppeteer-core");

const [planFile, outDir] = process.argv.slice(2);
const plan = JSON.parse(fs.readFileSync(planFile, "utf8"));
fs.mkdirSync(outDir, { recursive: true });
const browser = await puppeteer.launch({
  executablePath: process.env.CHROME || "/usr/bin/google-chrome",
  args: ["--no-sandbox", "--disable-gpu", "--font-render-hinting=none"],
});
const results = [];
let failed = 0;
for (const pg of plan.pages) {
  const ctx = await browser.createBrowserContext();
  const page = await ctx.newPage();
  await page.setViewport({ width: pg.width || 1366, height: pg.height || 900 });
  const base = new URL(plan.base);
  const cookies = (plan.sessions[pg.role] || []).map((c) => ({ name: c.name, value: c.value, domain: base.hostname, path: "/", httpOnly: true }));
  if (cookies.length) await page.setCookie(...cookies);
  const problems = [];
  page.on("console", (m) => {
    if (m.type() !== "error" && m.type() !== "warn" && m.type() !== "warning") return;
    // Chrome logs the document's own expected 403/404 as a console error; that is the
    // status under test, checked below, not a page fault.
    const loc = m.location() || {};
    if ((pg.expect || 200) !== 200 && loc.url === plan.base + pg.path && /Failed to load resource/.test(m.text())) return;
    problems.push(`console.${m.type()}: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("requestfailed", (r) => problems.push(`requestfailed: ${r.url()} ${r.failure() && r.failure().errorText}`));
  page.on("response", (r) => {
    const u = r.url();
    if (r.status() >= 400 && u !== plan.base + pg.path) problems.push(`subresource ${r.status()}: ${u}`);
  });
  let status = 0;
  try {
    const res = await page.goto(plan.base + pg.path, { waitUntil: "networkidle0", timeout: 30000 });
    status = res ? res.status() : 0;
    if (pg.action === "search") {
      await page.click("#search-q");
      await page.keyboard.type(pg.query, { delay: 30 });
      await page.waitForSelector("#search-results a[role=option], #search-results .hint", { timeout: 5000 });
      await page.keyboard.press("ArrowDown");
      await new Promise((r) => setTimeout(r, 300));
    } else if (pg.action === "bell") {
      await page.click(".bell summary");
      await page.waitForSelector("#notify-panel .notifications, #notify-panel .hint", { timeout: 5000 });
    } else if (pg.action === "timers") {
      await page.click(".timers summary");
      await page.waitForSelector("#timer-panel .timers, #timer-panel .hint", { timeout: 5000 });
    } else if (pg.action === "ctrlk") {
      await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
      const focused = await page.evaluate(() => document.activeElement && document.activeElement.id);
      if (focused !== "search-q") problems.push("Ctrl-K did not focus the search box");
    } else if (pg.action === "editclick") {
      // In edit mode a widget link must not navigate.
      const before = page.url();
      const link = await page.$(".grid-stack a.widget-title");
      if (link) { await link.click(); await new Promise((r) => setTimeout(r, 500)); }
      if (page.url() !== before) problems.push("a widget link opened while editing: " + page.url());
    } else if (pg.action === "collapse") {
      await page.click("#sidebar-toggle");
      await new Promise((r) => setTimeout(r, 400));
    }
  } catch (e) {
    problems.push("navigation: " + e.message);
  }
  // Home widgets: rows must get the same gutter as columns, and no widget may overlap another.
  const gridProblems = await page.evaluate(() => {
    const items = [...document.querySelectorAll("#home-grid.gs-ready .grid-stack-item-content")].map((e) => e.getBoundingClientRect());
    const out = [];
    const hGaps = [], vGaps = [];
    for (let i = 0; i < items.length; i++) for (let j = 0; j < items.length; j++) {
      if (i === j) continue;
      const a = items[i], b = items[j];
      const xOverlap = Math.min(a.right, b.right) - Math.max(a.left, b.left);
      const yOverlap = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
      if (xOverlap > 0.5 && yOverlap > 0.5) out.push(`widgets ${i} and ${j} overlap`);
      if (yOverlap > 0.5 && b.left >= a.right - 0.5) hGaps.push(b.left - a.right);
      if (xOverlap > 0.5 && b.top >= a.bottom - 0.5) vGaps.push(b.top - a.bottom);
    }
    // Only the nearest neighbour counts as a gutter.
    const nearest = (gaps) => (gaps.length ? Math.min(...gaps) : null);
    const h = nearest(hGaps), v = nearest(vGaps);
    if (v !== null && v < 15) out.push(`row gutter is ${v.toFixed(1)}px`);
    if (h !== null && v !== null && Math.abs(h - v) > 1) out.push(`row gutter ${v.toFixed(1)}px does not match column gutter ${h.toFixed(1)}px`);
    return out;
  }).catch((e) => ["grid check: " + e.message]);
  problems.push(...gridProblems);
  const title = await page.title().catch(() => "");
  const file = path.join(outDir, pg.name + ".png");
  await page.screenshot({ path: file, fullPage: !!pg.full });
  const ok = status === (pg.expect || 200) && problems.length === 0;
  if (!ok) failed++;
  results.push({ name: pg.name, role: pg.role, url: pg.path, action: pg.action || undefined, width: pg.width || 1366, status, expect: pg.expect || 200, title, problems, ok });
  console.log(`${ok ? "ok  " : "FAIL"} ${pg.role.padEnd(10)} ${String(status).padEnd(4)} ${pg.path}${pg.action ? " [" + pg.action + "]" : ""}${problems.length ? "\n     " + problems.join("\n     ") : ""}`);
  await ctx.close();
}
await browser.close();
fs.writeFileSync(path.join(outDir, "qc.json"), JSON.stringify({ chrome: process.env.CHROME || "/usr/bin/google-chrome", pages: results, failed }, null, 1) + "\n");
process.exit(failed ? 1 : 0);
