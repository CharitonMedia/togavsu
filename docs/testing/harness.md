# Harness

The product is not in this tree. The harness checks the acceptance map, the synthetic-data rules, the Doc Tender corpus, and the evidence gate.

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
Ran 51 tests
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

```json
{
  "kind": "command",
  "command": "go test ./internal/trust/...",
  "exit_code": 0,
  "outcome": "passed",
  "observation": "difference_cents=125"
}
```

`kind` is one of `command`, `http`, `sql`, `file_hash`, `log`, or `clean_install`. `command` and `observation` must be non-empty. `exit_code` must equal the case's expected exit code. A positive case needs `outcome` `passed`. A failure case needs `outcome` `rejected`, meaning the test observed the refusal. `file_hash` must not be the SHA-256 of `legacy/old.doc` or `legacy/password.pdf`. `summarize` loads that denylist itself and refuses `doubles: true` or a mock, double, or fake `backend_mode` on every case. It also refuses a matrix whose case ids do not match the canonical inventory.

The Docker Desktop attestation is separate:

```json
{
  "kind": "clean_install",
  "host": "docker_desktop",
  "outcome": "passed",
  "command": "docker compose up",
  "exit_code": 0,
  "observation": "first-run form"
}
```

## Adding a case

Add it under the criterion in `tests/harness/cases.py`. Keep the requirement sentence in `docs/build/acceptance.json`. Set `phase` to the phase where that observation can be true. The full behavior phase has to equal the latest case phase. If that phase is before 7, list 7 as a recheck. Run the unittest command and `--write`.
