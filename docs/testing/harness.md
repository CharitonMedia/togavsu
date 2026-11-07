# Harness

The product is not in this tree. The harness checks the acceptance map, the synthetic-data rules, the Doc Tender corpus, and the evidence gate.

## Provision a clean test environment

The harness needs Python 3.10 or newer, Pillow, the Tesseract executable with English recognition data, and the DejaVu Sans font. Pillow is pinned in `tests/requirements-dev.txt`; OCR and fonts are system tools. Tests fail rather than skip when they are missing. These are synthetic test dependencies, not a claim of product acceptance.

On an Ubuntu cloud VM or GitHub-hosted Linux runner:

```bash
sudo apt-get update
sudo apt-get install -y python3-venv tesseract-ocr tesseract-ocr-eng fonts-dejavu-core
python3 -m venv /tmp/togavsu-qa-venv
. /tmp/togavsu-qa-venv/bin/activate
python -m pip install -r tests/requirements-dev.txt
python -m unittest discover -s tests -p 'test_*.py'
python -m tests.harness
```

The separately reviewed integration-lead workflow `.github/workflows/qa-bootstrap.yml` reproduces these steps in a fresh environment and runs the full harness plus OCR checks. It does not install the application or accept any product criterion. On another OS, install the equivalent Tesseract tools and use the documented font locations in the corpus generator before running the same Python commands.

## Commands

From the repo root:

```bash
python3 -m unittest discover -s tests -p 'test_*.py'
python3 -m tests.harness
python3 -m tests.harness --write
```

The first command is the check. The second prints counts and exits 0 only when the map is intact and `app_complete` is false. `--write` refreshes `docs/build/acceptance-matrix.md` and `tests/acceptance/matrix.json`. `tests/test_matrix_files.py` fails if those files drift from the builder.

Result of the check on this branch:

```text
python3 -m unittest discover -s tests -p 'test_*.py'
Ran 58 tests
OK

python3 -m tests.harness
task: qa
criteria: 52
pending: 52
accepted: 0
app_complete: false
product_acceptance: pending
legacy_failures: old_doc, scanned_pdf_page_49, merged_then_split, password_pdf
integrity_errors: 0
```

Run both commands again after a change and replace this block with the new output.

## Layout

- `tests/harness/cases.py` holds the positive and failure cases.
- `tests/harness/catalog.py` joins them to `docs/build/acceptance.json` and `docs/build/tasks.json`.
- `tests/harness/evidence.py` accepts or refuses evidence.
- `tests/harness/oracles.py` runs the PRD rules that do not need the Go services yet.
- `tests/harness/corpus.py` builds the synthetic Doc Tender files in memory.
- `tests/harness/defaults.py` and `tests/harness/records.py` are the AC-029 and AC-035 inventories.
- `tests/harness/policy.py` rejects non-synthetic contacts and bytes.

## Evidence shape

A record is accepted only when it is bound to one case and its assertions repeat that case's expected lines. The harness does not run product code and does not invent these records. `python3 -m tests.harness` submits none, so accepted stays 0.

```json
{
  "case_id": "AC-007-P2",
  "kind": "command",
  "command": "go test ./internal/trust -run TestReconciliationOffBy125",
  "exit_code": 0,
  "outcome": "passed",
  "observation": "AC-007-P2 result: The stored difference is 125 cents.",
  "assertions": [
    {
      "expect": "The stored difference is 125 cents.",
      "ok": true,
      "detail": "AC-007-P2: The stored difference is 125 cents."
    }
  ],
  "provenance": {
    "command": "go test ./internal/trust -run TestReconciliationOffBy125",
    "runner": "domain:trust"
  }
}
```

`exit_code` must be a real int, not `true`, `false`, a float, or a numeric string. `command` must not be the bare `go test ./...` stub, and `observation` must not be `observed`. `assertions` must cover every expected line for that case id, with `ok` a boolean and `detail` naming the case. The same record submitted for another case is refused. `provenance.command` must equal `command`, and `runner` must name the domain run, not `scaffold` or `generic`.

A positive case needs `outcome` `passed`. A failure case needs `outcome` `rejected`. `file_hash` must not be the SHA-256 of `legacy/old.doc` or `legacy/password.pdf`. `summarize` loads that denylist itself and refuses `doubles: true` or a mock, double, or fake `backend_mode` on every case. It also refuses a matrix whose case ids, polarities, or exit codes do not match the canonical inventory.

The Docker Desktop attestation is separate and needs the same provenance rule. `exit_code` 0 must be an int. A generic suite stub cannot stand in for the install.

```json
{
  "kind": "clean_install",
  "host": "docker_desktop",
  "outcome": "passed",
  "command": "docker compose up",
  "exit_code": 0,
  "observation": "first-run form on a clean Docker Desktop install",
  "provenance": {"command": "docker compose up", "runner": "release-clean-install"}
}
```

Limitation: this gate checks the shape and the binding. It does not execute the command. A later domain runner has to actually run the command and put that run's output in `detail`. The scaffold never does that for them.

## Adding a case

Add it under the criterion in `tests/harness/cases.py`. Keep the requirement sentence in `docs/build/acceptance.json`. Set `phase` to the phase where that observation can be true. The full behavior phase has to equal the latest case phase. If that phase is before 7, list 7 as a recheck. Run the unittest command and `--write`.

Canonical validation compares the complete case definition, including expected assertions and procedure. Keeping a case ID while replacing its expectations or action refuses the submitted matrix before any evidence can accept a criterion.


Final app acceptance additionally requires every case result to carry integer `phase: 7` and the same full lowercase 40-character `tested_revision` as the clean Docker Desktop attestation. Earlier-phase or stale-revision evidence can document bounded progress but cannot complete the app. All cases are rerun on the integrated release, including the canonical phase-7 rechecks; submitted criterion definitions cannot remove that policy. The harness validates evidence shape and consistency, not cryptographic authenticity of a caller's claims. Actual domain runners and independent integrated-release tests must produce those observations.

The structural stand-in denylist is always loaded from the canonical corpus. Optional caller hashes can only add refusals, never replace the canonical hashes; unavailable canonical data fails closed.
