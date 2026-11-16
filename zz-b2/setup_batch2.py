#!/usr/bin/env python3
"""Writes the batch2 integration branch setup: approved scope copy, criteria index, plan, gates.

Run from the root of a togavsu checkout on the batch2 branch:
    python3 setup_batch2.py <seed-dir> <batch2-prd-path>
"""
import copy
import hashlib
import json
import pathlib
import re
import sys

root = pathlib.Path.cwd()
seed = pathlib.Path(sys.argv[1])
prd_path = pathlib.Path(sys.argv[2])
prd = prd_path.read_text()
prd_sha = hashlib.sha256(prd_path.read_bytes()).hexdigest()


def write(rel, text):
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text)


def sub(text, old, new, count=1):
    found = text.count(old)
    if found != count:
        sys.exit(f"expected {count} of {old[:80]!r}, found {found}")
    return text.replace(old, new)


# 1. CORE-CHANGES-2.md: the copied PRD parts, headings restored, nothing added but the source line.
cc = (seed / "CORE-CHANGES-2.md").read_text()
cc = sub(cc, "### Tests Tests\n", "### 18.3 Tests\n")
cc = sub(cc, "### Review gate Review gate\n", "### 18.4 Review gate\n")
cut = cc.index("\n### 18.5 Reporter\n")
cc = cc[:cut].rstrip("\n") + "\n"
first_break = cc.index("\n\n## Fixed decisions")
cc = (cc[:first_break] + "\n\nSource: togavsu-modules-batch2-prd.md as approved, SHA-256 " + prd_sha + "."
      + cc[first_break:])
# Every copied section body must appear in the approved PRD exactly.
for block in re.split(r"\n(?=## )", cc)[1:]:
    head, _, body = block.partition("\n")
    for part in re.split(r"\n(?=### )", body.strip("\n")):
        if part.startswith("### "):
            h, _, b = part.partition("\n")
            if h not in prd:
                sys.exit("heading not in PRD: " + h)
            part = b
        for line in [x for x in part.split("\n") if x.strip()]:
            if line.strip() not in prd:
                sys.exit("text not in PRD under " + head + ": " + line[:120])
write("docs/modules/CORE-CHANGES-2.md", cc)

# 2. Core PRD 8.22: a pointer to the approved text, no new requirement words.
core_prd = (root / "togavsu-core-prd.md").read_text()
if "### 8.22 " not in core_prd:
    anchor = "\n## 9. "
    pointer = ("### 8.22 Migration import\n\nOpening invoice import. The approved requirement and its acceptance criteria "
               "(IMP-01 to IMP-08) are docs/modules/CORE-CHANGES-2.md section 6, copied unchanged from the batch 2 "
               "modules PRD Patrick approved on 2026-11-16.\n\n")
    i = core_prd.index(anchor)
    core_prd = core_prd[:i].rstrip("\n") + "\n\n" + pointer + core_prd[i + 1:]
    write("togavsu-core-prd.md", core_prd)

# 3. Migration blocks for the batch2 core lanes. The marketplace server keeps a JSON store and needs none.
ranges = (root / "internal/db/ranges.go").read_text()
ranges = sub(ranges, '\t\t{Task: "release", Min: 1200, Max: 1299},\n',
             '\t\t{Task: "release", Min: 1200, Max: 1299},\n'
             '\t\t{Task: "module-core", Min: 1300, Max: 1399},\n'
             '\t\t{Task: "core-update-2a", Min: 1400, Max: 1449},\n'
             '\t\t{Task: "core-update-2b", Min: 1450, Max: 1499},\n'
             '\t\t{Task: "core-update-2c", Min: 1500, Max: 1549},\n')
write("internal/db/ranges.go", ranges)

# 4. Push CI runs on the integration branch too.
for wf in ["product-ci.yml", "core-settings.yml", "qa-bootstrap.yml"]:
    p = ".github/workflows/" + wf
    write(p, sub((root / p).read_text(), "  push:\n    branches: [main]\n", "  push:\n    branches: [main, batch2]\n"))

# 5. Batch 2 acceptance index, read from CORE-CHANGES-2.md in order.
rows = re.findall(r"^((?:CU2|IMP|MKT)-\d\d) (.+)$", cc, re.M)
assert [r[0] for r in rows] == ([f"CU2-0{i}" for i in range(1, 9)] + [f"IMP-0{i}" for i in range(1, 9)]
                                + [f"MKT-0{i}" for i in range(1, 8)]), [r[0] for r in rows]
write("docs/build/acceptance-batch2.json",
      json.dumps([{"id": i, "requirement": r} for i, r in rows], indent=2) + "\n")
CU2 = [f"CU2-0{i}" for i in range(1, 9)]
IMP = [f"IMP-0{i}" for i in range(1, 9)]
MKT = [f"MKT-0{i}" for i in range(1, 8)]

# 6. Assignments.
plan = json.loads((root / "docs/build/controller-plan.json").read_text())
tasks = json.loads((root / "docs/build/tasks.json").read_text())
assert plan["tasks"] == tasks["tasks"]
by_id = {t["id"]: t for t in plan["tasks"]}
CHECKS = ["Core tests", "API contract tests", "QA harness tests", "Supervisor tests", "Backup integration", "Supervisor image"]
STAGE = ('Open your PR body with a "## Stage report" section in plain words: what merged, the criteria that passed and '
         'any still pending, failures and fixes, the simplification result and the PONYTAIL-DEBT.md item count. When '
         'the PR merges, the controller posts that section on the tracking issue and emails it to Patrick. ')
BRANCH = ("You start from the batch2 branch, the integration branch for batch 2, and your PR targets batch2, not main. "
          "The release-final assignment (PR 60) is still open on main and touches only release reports, docs/release/, "
          "scripts/release/, tests/test_full_acceptance.py and tests/test_release_ci.py; never edit those. ")
SCOPE2 = ("This is a batch 2 modules task. docs/modules/CORE-CHANGES-2.md holds the core parts of the batch 2 modules "
          "PRD Patrick approved on 2026-11-16, copied unchanged; it is the approved scope for this assignment and "
          "outranks docs/modules/CORE-CHANGES.md for CORE-30 onward. Also read docs/modules/CORE-CHANGES.md, "
          "docs/modules/MODULE-KIT.md and docs/modules/CONTRIBUTOR-GAPS.md. ")
API2 = ("Every addition is part of the public module API: document it in docs/api/modules.md and the OpenAPI spec, "
        "mount it on /mcp, refuse it without its scope, and test it against real Postgres beside the code it changes, "
        "failure cases included. Nothing in the core checks a module's id or author. Keep pkg/modulekit, "
        "pkg/modulekit/coretest and cmd/togavsu-module working for the module repositories, and keep every name "
        "exactly as CORE-CHANGES-2.md gives it: nine module repositories are written against it. ")
NEVER2 = ("Never edit docs/modules/ other than CONTRIBUTOR-GAPS.md and MODULE-KIT.md, the PRDs, .github/ other than "
          "product-ci.yml, internal/db/ranges.go, controller files or review gates, and never write to a module "
          "repository. Never edit marketplace/: the marketplace-paid lane owns it. Never edit go.mod or go.sum; "
          "propose any dependency in your PR for the integration lead. Outside services are fakes and local test "
          "servers only; never create a key or secret. If the same problem survives three fix passes, stop and "
          "report it plainly instead of trying again.")


def migrations(task, lo, hi):
    return (f"Schema migrations: use versions {lo}-{hi} only, the {task} block already in internal/db/ranges.go; "
            "never edit ranges.go and renumber nothing. ")


def report(task, cases):
    lines = "; ".join(f'"{k}" covering {", ".join(v)}' for k, v in cases.items())
    return (f"Before you finish, commit docs/build/reports/{task}.json, written from the tests you actually ran: "
            f'task_id "{task}", status "pass" only if every criterion passed, tested_revision (the full SHA you '
            "tested; after it only that report may change), tests (each with name, command and exit_code), "
            "passed_acceptance_ids and pending_acceptance_ids covering exactly your criteria, and phase_cases, one "
            f"per case with id, status, command, exit_code and acceptance_ids: {lines}. Never record a failed or "
            "unrun test as passed. ")


CORE_PATHS = ["cmd/core/", "cmd/togavsu-module/", "internal/", "pkg/", "web/", "api/", "tests/", "compose.yaml",
              "deploy/", "scripts/ci/", ".github/workflows/product-ci.yml", "docs/api/",
              "docs/modules/CONTRIBUTOR-GAPS.md", "docs/modules/MODULE-KIT.md"]
CORE_EXCLUDED = ["internal/db/ranges.go", "internal/frame/frame_test.go", "tests/test_full_acceptance.py",
                 "tests/test_release_ci.py"]

mc = copy.deepcopy(by_id["module-core"])
mc["depends_on"] = []
p = mc["prompt"]
p = sub(p, "You start from the released core (the release-final assignment is merged). ",
        "You start from the batch2 branch, main after release-perf merged, and your PR targets batch2, not main. The "
        "release-final assignment (PR 60) is still open on main and touches only release reports, docs/release/, "
        "scripts/release/, tests/test_full_acceptance.py and tests/test_release_ci.py; never edit those. "
        + migrations("module-core", 1300, 1399))
p = sub(p, "Never edit docs/modules/, the PRDs,", "Never edit docs/modules/, the PRDs, internal/db/ranges.go,")
mc["prompt"] = p
mc["excluded_paths"] = CORE_EXCLUDED

a = {"id": "core-update-2a", "wave": "module-api-2a", "phase": 9, "depends_on": ["module-core"],
     "owned_paths": CORE_PATHS, "excluded_paths": CORE_EXCLUDED, "prd_sections": ["8", "9"],
     "batch2_sections": ["4", "5"], "acceptance_ids": ["CU2-01", "CU2-02", "CU2-05", "CU2-06"],
     "required_checks": CHECKS, "stage_report": True,
     "prompt": STAGE + SCOPE2 + BRANCH +
     "Build the first part of module API update 2 in CORE-CHANGES-2.md section 4, in the order of section 4.4: "
     "CORE-36 (every contributor gap fix it names, each closing its row in docs/modules/CONTRIBUTOR-GAPS.md with a "
     "test that failed before the fix, including moving invoices, payments and expenses from JSON into rows in "
     "core.invoices, core.payments and core.expenses with a backfill that reconciles every cent) and then CORE-30 "
     "(wider service calls, each with its own per-minute limit setting, default 120). Done when the CU2-01, CU2-02, "
     "CU2-05 and CU2-06 cases for CORE-30 and CORE-36 pass in automated tests and matter-clock still installs, runs "
     "and uninstalls unchanged. " + migrations("core-update-2a", 1400, 1449) + API2 + NEVER2}

b = {"id": "core-update-2b", "wave": "module-api-2b", "phase": 9, "depends_on": ["core-update-2a"],
     "owned_paths": CORE_PATHS, "excluded_paths": CORE_EXCLUDED, "prd_sections": ["8", "9"],
     "batch2_sections": ["4", "5"], "acceptance_ids": ["CU2-01", "CU2-03", "CU2-04"],
     "required_checks": CHECKS, "stage_report": True,
     "prompt": STAGE + SCOPE2 + BRANCH +
     "Build the second part of module API update 2 in CORE-CHANGES-2.md section 4: CORE-31 (module service "
     "contracts: provides and consumes manifest fields, admin-approved links on the Modules tab, the core-proxied "
     "and HMAC-signed contract call at POST /api/v1/modules/contracts/{contract}/{operation}, provider-pushed "
     "contract events, one provider per contract per firm unless the contract says many, and no module ever "
     "learning another module's address), CORE-32 (public module pages under /p/{id}/ with the rate, body size, "
     "CSRF and step-token helpers and public_operations), CORE-33 (the public forms kit, pkg/modulekit/forms, with "
     "all 16 field types, the conditions, validation, the HTMX renderer, autosave with offline queue, \"I don't "
     "know\" answers and masked-field encryption through the module secret store) and CORE-34 (the staff banner "
     "slot), with the settings, scopes and events section 4.3 names. Done when the CU2-01, CU2-03 and CU2-04 cases "
     "for these items pass in automated tests. " + migrations("core-update-2b", 1450, 1499) + API2 + NEVER2}

c_cases = {"api-mcp-scopes": ["CU2-01"], "service-call-walls": ["CU2-02"], "contracts": ["CU2-03"],
           "public-pages": ["CU2-04"], "gap-rows-closed": ["CU2-05"], "billing-rows": ["CU2-06"],
           "batch1-unchanged": ["CU2-07"], "update-and-fresh-install": ["CU2-08"], "invoice-import": IMP,
           "license-intake": ["MKT-03"]}
c = {"id": "core-update-2c", "wave": "module-api-2c", "phase": 9, "depends_on": ["core-update-2b", "marketplace-paid"],
     "owned_paths": CORE_PATHS + ["docs/build/reports/core-update-2c.json"], "excluded_paths": CORE_EXCLUDED,
     "prd_sections": ["8", "9"], "batch2_sections": ["4", "5", "6", "17"], "acceptance_ids": CU2 + IMP + ["MKT-03"],
     "required_checks": CHECKS, "stage_report": True, "report_path": "docs/build/reports/core-update-2c.json",
     "phase_acceptance": c_cases, "full_acceptance": True,
     "prompt": STAGE + SCOPE2 + BRANCH +
     "Finish module API update 2 in CORE-CHANGES-2.md: CORE-35 (the manifest's structured price, shown and stored "
     "by the core, never charged, checked or enforced) and CORE-37, the opening invoice import of section 6 (core "
     "PRD section 8.22): the admin page's Migration import tab with the single form and the CSV upload with its "
     "full preview and one-transaction write, the three new InvoiceImport fields on the existing bulkInvoice "
     "endpoint, the Imported marker and document of record on the matter billing tab, the portal and statements "
     "showing the document of record, and the invoice.imported event. Also build the install side of section 17.3: "
     "the license key the marketplace sends reaches the install, where the admin approves it into the module's "
     "secret settings, tested end to end with the marketplace-paid lane's marketplace and its fake Stripe for "
     "MKT-03. Then close the docs rows section 17 names: remove the jq step from docs/modules/MODULE-KIT.md now that "
     "upload takes the signed file, and close in docs/modules/CONTRIBUTOR-GAPS.md the rows the marketplace-paid PR "
     "listed for the integration lead. Done when IMP-01 to IMP-08 and MKT-03 pass in automated tests and every CU2 "
     "case in section 4.4 passes for the whole update, CU2-07 (matter-clock and the five batch 1 module packages "
     "install, run and uninstall unchanged; build them from their public repositories at their main branch in the "
     "test, read-only) and CU2-08 (the update applies through the supervisor with no failed requests under the "
     "update load test, and a fresh install reaches first-run setup with no module installed) included. "
     + report("core-update-2c", c_cases) + migrations("core-update-2c", 1500, 1549) + API2 + NEVER2}

m_cases = {"structured-price": ["MKT-01"], "signed-upload": ["MKT-02"], "subscribe-license": ["MKT-03"],
           "renewal": ["MKT-04"], "cancel": ["MKT-05"], "no-card-or-secret": ["MKT-06"], "stripe-faked": ["MKT-07"]}
m = {"id": "marketplace-paid", "wave": "marketplace-2", "phase": 9, "depends_on": ["module-core"],
     "owned_paths": ["marketplace/", "docs/build/reports/marketplace-paid.json"], "prd_sections": ["10"],
     "batch2_sections": ["17"], "acceptance_ids": MKT, "required_checks": CHECKS, "stage_report": True,
     "report_path": "docs/build/reports/marketplace-paid.json", "phase_acceptance": m_cases, "full_acceptance": True,
     "prompt": STAGE + SCOPE2 + BRANCH +
     "Build the marketplace paid listings in CORE-CHANGES-2.md section 17, in marketplace/ only: the listing price "
     "taken from the manifest's structured price {amount_cents, currency, interval, note} (CORE-35's format; read it "
     "from the uploaded manifest in the marketplace, because the core lanes own the core's manifest code) with the "
     "old string still accepted, author price changes with the notice period setting (30 days) and the notice "
     "email, POST /v1/authors/me/packages taking the kit's signed package file unchanged and refusing a tampered "
     "one, Stripe Connect for authors, Stripe Checkout subscriptions naming the install id, license keys bound to "
     "the install id with the daily renewal endpoint, cancel running to the paid-through date, the platform fee "
     "operator setting (default 0 percent), and no card number or Stripe secret anywhere in state, logs or "
     "responses. The marketplace keeps its records in its own state store (marketplace/store.go); add no core "
     "migration. Stripe is a fake or local test server in every test; test helpers refuse any other host. Every "
     "listing stays private and no public marketplace is deployed. The install side of MKT-03 (the core taking the "
     "key into the module's secret settings) belongs to core-update-2c; you prove MKT-03 on the marketplace side. "
     "List in your PR body, for the integration lead, the CONTRIBUTOR-GAPS rows and the MODULE-KIT.md jq step that "
     "section 17 closes, because docs/modules/ is not yours. Done when MKT-01 to MKT-07 pass in automated tests. "
     + report("marketplace-paid", m_cases) +
     "Never edit anything outside marketplace/ and that report: not internal/, go.mod, go.sum, controller files, "
     "review gates, the PRDs or docs/modules/. If the same problem survives three fix passes, stop and report it "
     "plainly instead of trying again."}

rel = copy.deepcopy(by_id["release-module-api"])
rel["depends_on"] = ["core-update-2c"]

batch2 = [mc, a, b, c, m, rel]
ids2 = {t["id"] for t in batch2}
merged = []
for t in plan["tasks"]:
    if t["id"] == "module-core":
        merged.append(mc)
    elif t["id"] == "release-module-api":
        merged += [rel, a, b, c, m]
    else:
        merged.append(t)
assert {t["id"] for t in merged} >= ids2 and len(merged) == len({t["id"] for t in merged})
plan["tasks"] = merged
tasks["tasks"] = merged
write("docs/build/controller-plan.json", json.dumps(plan, indent=2) + "\n")
write("docs/build/tasks.json", json.dumps(tasks, indent=2) + "\n")

instance = {k: v for k, v in plan.items() if k != "tasks"}
instance["base_branch"] = "batch2"
instance["held_tasks"] = ["release-module-api"]
instance["tasks"] = batch2
write("docs/build/batch2-plan.json", json.dumps(instance, indent=2) + "\n")

# 7. Setup checks know the batch 2 criteria and the batch2 plan.
cs = (root / "scripts/check_setup.py").read_text()
cs = sub(cs, 'assert rows == [x["requirement"] for x in acceptance], "Acceptance index drift"\n',
         'assert rows == [x["requirement"] for x in acceptance], "Acceptance index drift"\n'
         '# Batch 2 criteria (CU2, IMP, MKT) come from the approved copy in docs/modules/CORE-CHANGES-2.md.\n'
         'import re\n'
         'changes2 = (root / "docs/modules/CORE-CHANGES-2.md").read_text()\n'
         'acceptance2 = json.loads((root / "docs/build/acceptance-batch2.json").read_text())\n'
         'rows2 = [{"id": i, "requirement": r} for i, r in re.findall(r"^((?:CU2|IMP|MKT)-\\d\\d) (.+)$", changes2, re.M)]\n'
         'assert rows2 == acceptance2 and len(rows2) == 23, "Batch 2 acceptance index drift"\n'
         'ids = ids | {x["id"] for x in acceptance2}\n')
cs = sub(cs, 'import re\nmprd =', 'mprd =')
cs = sub(cs, '        assert s in headings, (t["id"], "unknown modules PRD section", s)\n',
         '        assert s in headings, (t["id"], "unknown modules PRD section", s)\n'
         'headings2 = set(re.findall(r"^#{2,3} (\\d+(?:\\.\\d+)?)\\.? ", changes2, re.M))\n'
         'for t in tasks.values():\n'
         '    for s in t.get("batch2_sections", []):\n'
         '        assert s in headings2, (t["id"], "unknown CORE-CHANGES-2.md section", s)\n'
         '# The batch2 controller instance drives a subset of these assignments on the batch2 branch.\n'
         'batch2 = json.loads((root / "docs/build/batch2-plan.json").read_text())\n'
         'assert batch2["base_branch"] == "batch2"\n'
         'assert {k: v for k, v in batch2.items() if k not in ("tasks", "base_branch", "held_tasks")} == '
         '{k: v for k, v in controller.items() if k not in ("tasks", "held_tasks")}, "batch2 plan settings differ"\n'
         'b2 = {t["id"]: t for t in batch2["tasks"]}\n'
         'assert b2 and all(tasks.get(k) == t for k, t in b2.items()), "batch2 plan assignments differ from tasks.json"\n'
         'assert all(set(t["depends_on"]) <= b2.keys() for t in b2.values()), "batch2 plan depends outside itself"\n'
         'assert set(batch2["held_tasks"]) <= b2.keys()\n')
cs = sub(cs, '"docs/modules/CORE-CHANGES.md"]', '"docs/modules/CORE-CHANGES.md", "docs/modules/CORE-CHANGES-2.md",\n'
         '          "docs/build/acceptance-batch2.json", "docs/build/batch2-plan.json"]')
cs = sub(cs, 'print("PASS: 52 criteria indexed,', 'print("PASS: 52 core and 23 batch 2 criteria indexed, batch2 plan matches,')
write("scripts/check_setup.py", cs)

# 8. A second controller instance drives the batch2 plan, with its own pinned copy, state and timer.
svc = (root / "deploy/controller/togavsu-build-controller.service").read_text()
svc = sub(svc, "Description=Togavsu cloud build integration controller",
          "Description=Togavsu cloud build integration controller for the batch2 branch")
svc = sub(svc, "/home/patrick/.local/share/togavsu-controller", "/home/patrick/.local/share/togavsu-controller-batch2", 3)
svc = sub(svc, "--state /mnt/claude-workspace/nolan-pms/togavsu/.build-state",
          "--state /mnt/claude-workspace/nolan-pms/togavsu/.build-state-batch2")
write("deploy/controller/togavsu-build-controller-batch2.service", svc)
tmr = (root / "deploy/controller/togavsu-build-controller.timer").read_text()
tmr = sub(tmr, "Description=Continue Togavsu build without an open chat",
          "Continue the Togavsu batch2 build without an open chat".join(["Description=", ""]))
tmr = sub(tmr, "Unit=togavsu-build-controller.service", "Unit=togavsu-build-controller-batch2.service")
write("deploy/controller/togavsu-build-controller-batch2.timer", tmr)

# 9. The reviewer reads the batch 2 scope for batch 2 assignments.
rv = (root / "docs/build/prompts/codex-review.md").read_text()
rv = sub(rv, "and the trusted docs/modules files it names. ",
         "and the trusted docs/modules files it names. For an assignment with batch2_sections, also read trusted root "
         "docs/modules/CORE-CHANGES-2.md, the approved batch 2 scope, and docs/build/acceptance-batch2.json. ")
write("docs/build/prompts/codex-review.md", rv)
print("written", len(batch2), "batch2 assignments; PRD sha", prd_sha)
