#!/usr/bin/env python3
"""Seeds the nine batch 2 module repositories (run on Render-1, scratch under /dev/shm/zz-b2-)."""
import hashlib, json, os, pathlib, re, shutil, subprocess, sys

HERE = pathlib.Path(__file__).resolve().parent
WORK = pathlib.Path("/dev/shm/zz-b2-repos")
PRD = pathlib.Path("/mnt/claude-workspace/nolan-pms/togavsu/togavsu-modules-batch2-prd.md")
UNIFIED = pathlib.Path("/mnt/claude-workspace/nolan-pms/nolan-pms-unified")
DOCFORGE = pathlib.Path("/mnt/claude-workspace/wealthcounsel-replacement")
DOCS_LICENSE = pathlib.Path("/dev/shm/zz-b2-docs/LICENSE")
REFS = json.load(open("/dev/shm/zz-b2-refs.json"))
ORG = "CharitonMedia"
AUTHOR = ["-c", "user.name=pattalkslaw-del", "-c", "user.email=pattalkslaw-del@users.noreply.github.com"]
TRAILER = "\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"

MODULES = {
    "intake": ("Intake", [7]),
    "discovery": ("Discovery", [8]),
    "phone": ("Phone and meetings", [9]),
    "phone-zoom": ("Zoom", [10, 9]),
    "phone-google": ("Google Voice and Meet", [11, 9]),
    "workflows": ("Workflows", [12]),
    "google-notebooklm": ("Google account banner and NotebookLM", [13]),
    "matter-wiki": ("Matter wiki", [14]),
    "mo-estate-planning": ("Missouri estate planning", [15, 16]),
}
SHAPED = re.compile(rb"sk-ant-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY")

# DocForge content that ships in mo-estate-planning, copied byte for byte from the committed tree.
DF_DIRS = ["engine", "templates", "clauses", "manifests", "tests", "tools", "golden-packages"]
DF_SKIP = re.compile(r"^engine/(config/firm\.yaml|server-config\.json|wc-engine\.service)$|__pycache__|\.pyc$")
DF_MASTERS = [f"audit/standard-ancillaries/extracted/yale-{n}.txt" for n in ("dpoa", "ferpa", "hcpoa", "hipaa", "living-will")]
DF_REFDOCS = ["README.md", "CLAUDE.md", "DECISION-LOG.md", "PRD-clause-library-v1.md", "PRD-output-fidelity.md",
              "MEMO-execution-requirements.md", "RESEARCH-execution-requirements.md", "PROCESS-DOCUMENTATION.md",
              "CLAUSE-INDEX.md"] + [f"audit/standard-ancillaries/{n}.md" for n in ("DPOA", "FERPA", "HCPOA", "HIPAA", "INDEX", "LIVING-WILL")]


def run(*args, cwd=None, input=None, check=True):
    r = subprocess.run(list(args), cwd=cwd, input=input, capture_output=True)
    if check and r.returncode:
        sys.exit(f"failed: {' '.join(map(str, args))}\n{r.stderr.decode(errors='replace')[-2000:]}")
    return r.stdout


def prd_sections():
    text, out, cur = PRD.read_text(), {}, None
    for line in text.splitlines(keepends=True):
        m = re.match(r"^## (\d+)\. ", line)
        if m:
            cur = int(m.group(1))
        if cur is not None:
            out[cur] = out.get(cur, "") + line
    return out


def show(repo, path):
    return run("git", "-C", str(repo), "show", f"HEAD:{path}")


def write(root, rel, data):
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data if isinstance(data, bytes) else data.encode())


def spec(mid, name, secs, S):
    parts = [f"# {name} module specification\n\nModule id `{mid}`, repository CharitonMedia/togavsu-{mid}. "
             "This is this module's part of the Togavsu modules PRD, batch 2, approved by Patrick on "
             "2026-11-16, copied unchanged. The full PRD is on this repository's `reference` branch. "
             "Where this file and any other source differ, this file wins.\n\n"]
    own = secs[0]
    parts.append(S[own])
    for extra in secs[1:]:
        if extra == 16:
            parts.append(S[16])
        else:
            parts.append(f"\n---\n\nThe section below is the module this provider feeds, for its contract. "
                         f"It is built in CharitonMedia/togavsu-phone; this repository implements only section {own}.\n\n" + S[extra])
    parts.append("\n---\n\nThe shared rules below apply to every batch 2 module.\n\n" + S[2] + S[5])
    tail = S[18]
    keep = re.search(r"### 18\.3 Tests.*?(?=### 18\.5)", tail, re.S)
    parts.append(keep.group(0) if keep else "")
    return "".join(parts)


def agents(mid, name):
    schema = "mod_" + mid.replace("-", "_")
    extra = ""
    if mid == "intake":
        extra = ("\n## Intake specifics\n\n- Ship one general firm intake questionnaire, written fresh for any small law firm. "
                 "Never include veterans evaluations, and never include or copy any of Patrick's own intake questionnaires.\n")
    if mid == "mo-estate-planning":
        extra = ("\n## Estate planning specifics\n\n"
                 "- **Never write, rewrite or edit the legal text of any template, clause or master.** docforge/templates, "
                 "docforge/clauses and docforge/masters hold Patrick Nolan's own work, copied byte for byte from DocForge. "
                 "You may only add or change the front-matter keys provenance, authored_by and version inside a file's leading "
                 "`{# --- ... --- #}` block. The Template text check in CI enforces this against docforge/SOURCE.sha256 and "
                 "fails on any other change.\n"
                 "- Wiring a master in docforge/masters into a template changes only its merge-field tokens to the engine's "
                 "names. Write the new template so every other character matches the master, and list each token change in "
                 "the PR body.\n"
                 "- The engine is Python (section 16). It runs as a subprocess of the Go server with stdin and stdout, no network, "
                 "and reads only its template set. Pin Python dependencies with hashes. Put Python tests and the provenance gate "
                 "in scripts/ci/extra.sh so CI runs them.\n"
                 "- engine/config/firm.yaml is not copied: firm and notary details are module settings. Engine samples and golden "
                 "packages are synthetic test data; never add a real client.\n"
                 "- The package ships only the families listed in docs/SPEC.md 15.3; the business, veterans and guardianship "
                 "families stay out of the package.\n"
                 "- This repository is private and its license is Chariton Media's commercial license. Don't write license text.\n")
    return f"""# Build rules for this repository

This repository is one Togavsu marketplace module, {name} (`{mid}`). Cursor lane agents build it; Claude Code
checks every pull request before it merges. docs/SPEC.md is this module's part of the approved batch 2 PRD and wins
over every other source on what it states.

## Ground rules

- **Public kit only.** Build only from github.com/CharitonMedia/togavsu's public kit (pkg/modulekit,
  cmd/togavsu-module, pkg/modulekit/coretest), its public API and its docs (docs/modules/MODULE-KIT.md,
  CORE-CHANGES.md, CORE-CHANGES-2.md), at the version go.mod pins. Never import the core's internal packages
  and never copy core code.
- **Gaps.** When the kit or the API lacks something you need, list it in the PR body under "Contributor gaps"
  with what you needed and why, and hold that part back in package/pending.yaml with the CORE item it waits on.
  Never work around a gap inside the module.
- **This repository only.** Never push, open pull requests or write to any other repository.
- **Self-contained.** Own schema `{schema}`, links to core records by id with ON DELETE RESTRICT, no copied core
  records, every file a core file record, other modules only through core-brokered contracts.
- **Agent endpoints.** Every screen action is also a JSON endpoint under /m/{mid}/api/, listed in
  /m/{mid}/api/openapi.json and the help guide.
- **Settings.** Every value a firm might change is a module setting checked by settings.CheckModule.
- **Credentials** come only at runtime from settings or the core's secret store, never from code, tests,
  fixtures, logs or commits. Tests build fake tokens at runtime.
- **Outside services** (Zoom, Google, NotebookLM, Stripe, AI providers) are fakes or local test servers in every
  test, and test helpers refuse any other host. AI goes through the core's createAiCall.
- **Time.** Stored times are timestamptz, "today" is the firm's time zone, date-only values stay dates.
- **Synthetic data only:** batch 1's fixture firm (Example Law LLC, attorney Jordan Testwell, matter 2026-0001
  Sample v. Placeholder, client Avery Sample, opposing party Riley Placeholder, county Testcounty). Never a real
  client name or document.
- **Reference material** (the full PRD and the working code this module is rebuilt from) is read-only background.
  Build fresh and clean, keep every working idea, and don't copy reference files into this repository.
{extra}
## Layout and CI

Use the batch 1 module layout (see CharitonMedia/togavsu-documents): go.mod, cmd/{mid}/ with serve and package
subcommands, package/togavsu-module.yaml, package/pending.yaml, package/migrations/, pages/, an acceptance/ suite
on coretest, and a Dockerfile on a distroless runtime. Author Chariton Media, version 1.0.0. CI runs gofmt, go vet,
go test -race -timeout 0 against Postgres 17, togavsu-module check -installable -strict package and a docker build.
Extra CI steps go in scripts/ci/extra.sh, which CI runs when it exists. You can't change .github/, AGENTS.md or
docs/SPEC.md; propose a change in the PR body under "Proposed change".

## Done

Done means every acceptance criterion in docs/SPEC.md passes in the acceptance suite, failure paths included, or
is listed as pending with the CORE item it waits on. Never skip, disable or weaken a test to get green, and set no
time limit or timeout that docs/SPEC.md doesn't state.
"""


def ci(mid):
    docforge = ""
    if mid == "mo-estate-planning":
        docforge = ("      - name: Template text check\n"
                    "        # Patrick's template, clause and master text ships exactly as he wrote it.\n"
                    "        run: python3 .github/scripts/check_docforge_text.py .\n")
    return f"""name: ci
on:
  push:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:17
        env:
          POSTGRES_PASSWORD: pg
        ports:
          - 5432:5432
    env:
      # A superuser URL: coretest creates and drops its own databases and roles.
      TOGAVSU_TEST_DATABASE_URL: postgres://postgres:pg@127.0.0.1:5432/postgres?sslmode=disable
      GOPRIVATE: github.com/CharitonMedia
    steps:
      - uses: actions/checkout@v4
{docforge}      - name: Find the Go module
        id: mod
        run: |
          [ -f go.mod ] && echo "go=true" >> "$GITHUB_OUTPUT" || echo "go=false" >> "$GITHUB_OUTPUT"
      - uses: actions/setup-go@v5
        if: steps.mod.outputs.go == 'true'
        with:
          go-version-file: go.mod
      - name: gofmt
        if: steps.mod.outputs.go == 'true'
        run: test -z "$(gofmt -l .)" || {{ gofmt -l .; exit 1; }}
      - if: steps.mod.outputs.go == 'true'
        run: go vet ./...
      - name: Wait for Postgres
        run: until pg_isready -h 127.0.0.1 -p 5432 -U postgres; do sleep 1; done
      - if: steps.mod.outputs.go == 'true'
        run: go test -race -timeout 0 ./...
      - name: Extra checks
        if: hashFiles('scripts/ci/extra.sh') != ''
        run: bash scripts/ci/extra.sh
      # The kit's own check of the package as the pinned core installs it and the
      # marketplace checks it: the parts package/pending.yaml lists are held back while
      # the core refuses them, and (-strict) an entry that holds nothing back fails.
      - name: Package check
        if: hashFiles('package/togavsu-module.yaml') != ''
        run: go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable -strict package
      - name: Docker build
        if: hashFiles('Dockerfile') != ''
        run: docker build -t togavsu-{mid} .
"""


def readme(mid, name, S, sec):
    purpose = re.search(r"### \d+\.1 Purpose\n\n(.*?)\n\n", S[sec], re.S)
    lic = ("Chariton Media commercial license; the repository is private." if mid == "mo-estate-planning"
           else "Same as Togavsu; see LICENSE.")
    return (f"# Togavsu {name}\n\nA Togavsu marketplace module (`{mid}`). {purpose.group(1) if purpose else ''}\n\n"
            f"The spec is [docs/SPEC.md](docs/SPEC.md), from section {sec} of the Togavsu modules PRD, batch 2. "
            f"Build rules are in [AGENTS.md](AGENTS.md).\n\nLicense: {lic}\n")


def scan(files):
    bad = [rel for rel, data in files.items() if SHAPED.search(data)]
    if bad:
        sys.exit("token-shaped text in: " + ", ".join(bad))


def seed(mid, S):
    name, secs = MODULES[mid]
    root = WORK / mid
    if root.exists():
        shutil.rmtree(root)
    root.mkdir(parents=True)
    main = {
        "README.md": readme(mid, name, S, secs[0]).encode(),
        "AGENTS.md": agents(mid, name).encode(),
        "docs/SPEC.md": spec(mid, name, secs, S).encode(),
        ".github/workflows/ci.yml": ci(mid).encode(),
        ".github/workflows/lane.yml": (HERE / "lane.yml").read_bytes(),
        ".gitignore": b"/bin/\n/dist/\n*.test\n__pycache__/\n*.pyc\n.env\n",
    }
    if mid != "mo-estate-planning":
        lic = DOCS_LICENSE.read_text().splitlines(keepends=True)
        lic[0] = f"Togavsu {name} module\n"
        main["LICENSE"] = "".join(lic).encode()
    else:
        main[".github/scripts/check_docforge_text.py"] = (HERE / "check_docforge_text.py").read_bytes()
        sys.path.insert(0, str(HERE))
        from check_docforge_text import strip_meta
        df_files = run("git", "-C", str(DOCFORGE), "ls-files", "-z", *DF_DIRS).decode().split("\0")
        record = ["# DocForge source " + run("git", "-C", str(DOCFORGE), "rev-parse", "HEAD").decode().strip()
                  + ": sha256 as copied, sha256 without front-matter metadata keys, path"]
        for rel in [f for f in df_files if f and not DF_SKIP.search(f)] + DF_MASTERS:
            data = show(DOCFORGE, rel)
            dest = "docforge/masters/" + rel.rsplit("/", 1)[1] if rel in DF_MASTERS else "docforge/" + rel
            main[dest] = data
            if dest.startswith(("docforge/templates/", "docforge/clauses/", "docforge/masters/")):
                record.append(f"{hashlib.sha256(data).hexdigest()} {hashlib.sha256(strip_meta(data)).hexdigest()} {dest}")
        main["docforge/SOURCE.sha256"] = ("\n".join(record) + "\n").encode()
    ref = {"docs/reference/togavsu-modules-batch2-prd.md": PRD.read_bytes()}
    for rel in REFS.get(mid, []):
        ref["unified/" + rel] = show(UNIFIED, rel)
    if mid == "mo-estate-planning":
        for rel in DF_REFDOCS:
            ref["docforge-docs/" + rel] = show(DOCFORGE, rel)
    scan(main)
    scan(ref)
    for rel, data in main.items():
        write(root, rel, data)
    run("git", "init", "-q", "-b", "main", cwd=root)
    run("git", "add", "-A", cwd=root)
    run("git", *AUTHOR, "commit", "-q", "-m", f"Seed the {name} module repository" + TRAILER, cwd=root)
    run("git", "switch", "-q", "--orphan", "reference", cwd=root)
    run("git", "rm", "-rq", "--cached", "--ignore-unmatch", ".", cwd=root, check=False)
    for p in root.iterdir():
        if p.name != ".git":
            shutil.rmtree(p) if p.is_dir() else p.unlink()
    readme_ref = (f"# Reference material for {mid}\n\nRead-only background for the build lanes: the full batch 2 PRD and "
                  "the working code this module is rebuilt from. Never merged into main and never shipped.\n")
    ref["README.md"] = readme_ref.encode()
    for rel, data in ref.items():
        write(root, rel, data)
    run("git", "add", "-A", cwd=root)
    run("git", *AUTHOR, "commit", "-q", "-m", f"Reference material for {mid}" + TRAILER, cwd=root)
    run("git", "switch", "-q", "main", cwd=root)
    return len(main), len(ref)


def publish(mid):
    name, _ = MODULES[mid]
    full = f"{ORG}/togavsu-{mid}"
    root = WORK / mid
    exists = subprocess.run(["gh", "repo", "view", full], capture_output=True).returncode == 0
    if not exists:
        run("gh", "repo", "create", full, "--private", "--description", f"Togavsu {name} module")
    run("git", "remote", "add", "origin", f"git@github.com:{full}.git", cwd=root, check=False)
    run("git", "push", "-q", "-u", "origin", "main", "reference", cwd=root)
    run("gh", "repo", "edit", full, "--default-branch", "main", "--delete-branch-on-merge", "--enable-wiki=false")


if __name__ == "__main__":
    S = prd_sections()
    targets = sys.argv[2:] or list(MODULES)
    for mid in targets:
        n = seed(mid, S)
        print(mid, "main files", n[0], "reference files", n[1])
        if sys.argv[1] == "publish":
            publish(mid)
            print(mid, "published")
