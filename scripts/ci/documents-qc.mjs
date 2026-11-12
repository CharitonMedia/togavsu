// Visual QC of the phase 4 screens on a running core (calendar, Files tab, Files and
// storage, edit page, New Pleading, Doc Tender review queue, tab and guide). Headless
// Chromium signs in as each role with that role's cookies, opens each page, saves a
// full-page screenshot and records the status, final path, title, console errors, page
// errors and failed requests. Run by internal/core TestDocumentsScreens.
//
// usage: node documents-qc.mjs PLAN_JSON OUT_DIR   (prints the results as JSON)
// env:   CHROME (browser binary), PUPPETEER_CORE (the puppeteer-core package folder)
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";

const [planPath, outDir] = process.argv.slice(2);
const plan = JSON.parse(fs.readFileSync(planPath, "utf8"));
const require = createRequire(path.join(process.env.PUPPETEER_CORE, "package.json"));
const puppeteer = require("puppeteer-core");

const browser = await puppeteer.launch({
  executablePath: process.env.CHROME,
  headless: true,
  args: ["--no-sandbox", "--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"],
  defaultViewport: { width: 1280, height: 900 },
});
const results = [];
try {
  for (const step of plan.pages) {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    const rec = { name: step.name, role: step.role, url: step.path, console: [], failed: [] };
    page.on("console", (m) => { if (m.type() === "error") rec.console.push(m.text()); });
    page.on("pageerror", (e) => rec.console.push(String(e)));
    page.on("response", (r) => {
      const u = new URL(r.url());
      if (r.status() >= 400 && u.pathname !== step.path) rec.failed.push(r.status() + " " + u.pathname);
    });
    const cookies = (plan.roles[step.role] || []).map((c) => ({ ...c, url: plan.base }));
    if (cookies.length) await page.setCookie(...cookies);
    const res = await page.goto(plan.base + step.path, { waitUntil: "networkidle0", timeout: 60000 });
    rec.status = res ? res.status() : 0;
    rec.landed = new URL(page.url()).pathname;
    rec.title = await page.title();
    if (step.wait) await new Promise((r) => setTimeout(r, step.wait));
    await page.screenshot({ path: path.join(outDir, step.name + ".png"), fullPage: true });
    results.push(rec);
    await context.close();
  }
} finally {
  await browser.close();
}
process.stdout.write(JSON.stringify({ pages: results }));
