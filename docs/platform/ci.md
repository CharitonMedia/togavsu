# Product CI

`.github/workflows/product-ci.yml` runs three jobs. None of them mark AC-001, AC-016, or AC-018 passed.

- **Supervisor tests** run `scripts/ci/test-supervisor.sh`, the bootstrap secret check, the pending-doc gate, the Compose static check, UI QC wiring, and the update load-test wiring.
- **Backup integration** restores three synthetic rows through Postgres 17 with `pg_dump` and `psql`. A wrong passphrase does not apply the dump.
- **Supervisor image** validates `docker compose config` and builds the supervisor image. The core image build is expected to fail closed until `cmd/core` exists.

UI QC (`scripts/ci/ui-qc.sh`) runs `tests/ui/run.sh` when QA adds it. Until then it prints a pending line. The update load script does the same for `tests/update/load.sh`.

Go tests use the repository `go.mod` when it exists. Before that, the script builds a temporary module named `github.com/CharitonMedia/togavsu` so this assignment does not edit `go.mod`.
