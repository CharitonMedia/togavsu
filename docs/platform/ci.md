# Product CI

`.github/workflows/product-ci.yml` runs three jobs. None of them mark AC-001, AC-016, or AC-018 passed.

- **Supervisor tests** run `scripts/ci/test-supervisor.sh`, the bootstrap secret check, the pending-doc gate, the Compose static check, UI QC wiring, and the update load-test wiring.
- **Backup integration** restores three synthetic rows through Postgres 17 with `pg_dump` and `psql`. A wrong passphrase does not apply the dump.
- **Supervisor image** validates `docker compose config`, builds the supervisor image, and builds the core image. The core process accepts bootstrap configuration and does not start HTTP or migrations.

UI QC (`scripts/ci/ui-qc.sh`) runs `tests/ui/run.sh` when QA adds it. Until then it prints a pending line. The update load script does the same for `tests/update/load.sh`.

Go tests use the repository `go.mod`. The supervisor test script still builds a temporary module with the same path when that file is absent.
