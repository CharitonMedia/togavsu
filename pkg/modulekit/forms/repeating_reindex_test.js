#!/usr/bin/env node
// RepeatingJS must reindex only the edited group's own [N] (Codex P2 on e729d02).
// Interactive click coverage runs when jsdom is installed; otherwise the source
// contract alone fails closed on the first-[N] rewrite bug.
const fs = require("fs");
const path = require("path");

const src = fs.readFileSync(path.join(__dirname, "autosave.go"), "utf8");
const marker = "const RepeatingJS = `";
const start = src.indexOf(marker);
if (start < 0) {
  console.error("RepeatingJS not found in autosave.go");
  process.exit(1);
}
const bodyStart = start + marker.length;
const end = src.indexOf("`\n\n// AutosavePatch", bodyStart);
if (end < 0) {
  console.error("RepeatingJS closing backtick not found");
  process.exit(1);
}
const js = src.slice(bodyStart, end);

function fail(msg) {
  console.error(msg);
  process.exit(1);
}

if (!js.includes('getAttribute("data-name")') && !js.includes("getAttribute('data-name')")) {
  fail("RepeatingJS must read data-name for the group name prefix");
}
// The pre-fix bug: rewrite only the first [N] in every control name.
if (/n\.replace\(\/\\\[\\d\+\\\]\/,\s*"\["\s*\+\s*i\s*\+\s*"\]"\)/.test(js)) {
  fail("RepeatingJS still rewrites only the first [N] (breaks nested groups)");
}
if (!js.includes("__present")) {
  fail("RepeatingJS must reindex __present values for the group prefix");
}

let jsdom;
try {
  jsdom = require("jsdom");
} catch (_) {
  jsdom = null;
}

if (!jsdom) {
  console.log("repeating_reindex_test.js: ok (source contract; jsdom not installed)");
  process.exit(0);
}

const {JSDOM} = jsdom;
const html = `
<div class="tv-repeating" data-field="kids" data-name="kids">
  <fieldset class="tv-repeat-row" data-index="1">
    <input type="hidden" name="kids[1].__row" value="kid-b" data-tv-row-id="1">
    <div class="tv-repeating" data-field="pets" data-name="kids[1].pets">
      <fieldset class="tv-repeat-row" data-index="0">
        <input type="hidden" name="__present" value="kids[1].pets[0].name">
        <input type="hidden" name="kids[1].pets[0].__row" value="pet-drop" data-tv-row-id="1">
        <input type="text" name="kids[1].pets[0].name" id="f-kids-1-pets-0-name" value="Drop">
        <button type="button" class="tv-repeat-remove">Remove</button>
      </fieldset>
      <fieldset class="tv-repeat-row" data-index="1">
        <input type="hidden" name="__present" value="kids[1].pets[1].name">
        <input type="hidden" name="kids[1].pets[1].__row" value="pet-keep" data-tv-row-id="1">
        <input type="text" name="kids[1].pets[1].name" id="f-kids-1-pets-1-name" value="Keep">
        <button type="button" class="tv-repeat-remove">Remove</button>
      </fieldset>
      <button type="button" class="tv-repeat-add" data-field="pets">Add</button>
    </div>
  </fieldset>
</div>
`;

const dom = new JSDOM(html, {runScripts: "outside-only"});
dom.window.eval(js);
const wrap = dom.window.document.querySelector('.tv-repeating[data-name="kids[1].pets"]');
if (!wrap) fail("missing nested pets wrap");
wrap.querySelector('fieldset.tv-repeat-row[data-index="0"] .tv-repeat-remove').click();
const rows = wrap.querySelectorAll(":scope > fieldset.tv-repeat-row");
if (rows.length !== 1) fail("expected one pet row after delete, got " + rows.length);
const nameInput = rows[0].querySelector('input[type="text"]');
if (!nameInput || nameInput.name !== "kids[1].pets[0].name") {
  fail("inner reindex rewrote outer index: name=" + (nameInput && nameInput.name));
}
const rowId = rows[0].querySelector("[data-tv-row-id]");
if (!rowId || rowId.name !== "kids[1].pets[0].__row" || rowId.value !== "pet-keep") {
  fail("row id name/value wrong: " + (rowId && rowId.name) + "=" + (rowId && rowId.value));
}
const present = Array.from(rows[0].querySelectorAll('input[name="__present"]')).map((el) => el.value);
if (present.indexOf("kids[1].pets[0].name") < 0) {
  fail("__present value not reindexed: " + JSON.stringify(present));
}
if (present.some((v) => v.startsWith("kids[0]."))) {
  fail("outer index corrupted in __present: " + JSON.stringify(present));
}
console.log("repeating_reindex_test.js: ok (jsdom)");
