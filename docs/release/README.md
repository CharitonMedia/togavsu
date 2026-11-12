# Release (phase 7)

This folder is the release assignment's working record: how full acceptance is decided, what still blocks a tagged release, and how the release is cut once nothing does.

- [acceptance-evidence.json](acceptance-evidence.json): each of the 52 PRD 12 criteria, the executable checks that prove it, and any gap no check proves yet. Two release gates sit beside them.
- [INSTALL.md](INSTALL.md): the firm's install and operations guide (Docker Desktop install, first-run, updates, backups, restore, removal).
- [simplification-audit.md](simplification-audit.md): the whole-repository simplification review before the release tag.
- [license/](license/): the LICENSE and LICENSE-SUMMARY.md drafts from PRD 3, for the integration lead to place at the repository root.
- The report is [docs/build/reports/release.json](../build/reports/release.json).

## How acceptance is decided

`sh scripts/ci/full-acceptance.sh` runs `scripts/release/full_acceptance.py`. It runs every check in the evidence map once and decides each criterion:

- A criterion passes only when it has at least one check, every check exits 0, and it lists no gap.
- A `go test` check passes only when every test its `-run` pattern names printed `--- PASS`, and none was skipped. A pattern that matches nothing fails.
- A check that prints `PENDING`, or whose prerequisites (Docker, the two Postgres databases, the Postgres 17 client, the core image's wordcraft-cli) are missing, is not a pass.
- The release passes only when all 52 criteria pass, every gate passes, and the working tree is clean. Otherwise the script exits 1.

`--phase desktop-install`, `--phase signed-images-updater-load` and `--phase backup-restore` run one phase case's checks and decide only its criteria. `--check ID` runs single checks; `--list` prints them.

## Release gates

The first two can't pass from a lane runner (below); the third is the product CI suites.


| Gate | Check | Passes when |
| --- | --- | --- |
| signed-release | `scripts/release/verify-release.sh` | `deploy/release/cosign.pub` (the Togavsu release public key) is in the repository, HEAD carries a release tag, and `cosign verify` accepts `ghcr.io/charitonmedia/togavsu-core:<tag>` and `togavsu-supervisor:<tag>` with that key. |
| docker-desktop | `scripts/release/desktop-install.sh` | It runs on Docker Desktop (Windows or macOS) and a clean install of the tested revision passes the first-run checks. It refuses any other engine. |
| product-suites | the Core, API contract, QA harness, Supervisor, Backup integration, UI QC and Compose first-run scripts | Every product CI suite passes at the tested revision, not only the checks a criterion names. |

The signed-release and docker-desktop gates can't pass from a lane runner: the release private key is not provisioned, nothing here can push images or tags, and GitHub's Linux runners have Docker Engine, not Docker Desktop.

## Cutting the release

These steps need Patrick or the integration lead and credentials no lane has:

1. Generate the Togavsu release key pair with `cosign generate-key-pair`. Store the private key and its password as repository secrets. Commit the public key as `deploy/release/cosign.pub` and copy it into the supervisor image (`COSIGN_KEY` in compose.yaml then points at the image's copy instead of `/appsecrets/cosign.pub`, which nothing creates today).
2. Land the "Full acceptance" job (below) and a release workflow that, on a `v*` tag, builds `deploy/core.Dockerfile` and `Dockerfile` with `TOGAVSU_VERSION` set to the tag, pushes both to ghcr.io/charitonmedia, signs each digest with the release key, publishes the release index the supervisor reads, and attaches a `compose.yaml` that names the images by digest instead of building them.
3. Run `sh scripts/release/desktop-install.sh` on a clean Windows machine and a clean Mac with Docker Desktop, and attach the output to the release.
4. Tag only when `sh scripts/ci/full-acceptance.sh` exits 0 on the tagged revision.

## Proposed CI change

The release assignment owns the "Full acceptance" required check, but lanes can't change `.github/`. The integration lead lands this job in `.github/workflows/product-ci.yml` through a reviewed control PR. It runs every criterion's checks and fails while any criterion or gate is pending, so it can't turn unexecuted acceptance into a pass:

```yaml
  full-acceptance:
    name: Full acceptance
    runs-on: ubuntu-latest
    timeout-minutes: 300
    services:
      postgres:
        image: postgres:17
        env:
          POSTGRES_USER: togavsu
          POSTGRES_PASSWORD: synthetic-postgres-password
          POSTGRES_DB: togavsu
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U togavsu -d togavsu"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 12
      postgres_b:
        image: postgres:17
        env:
          POSTGRES_USER: togavsu
          POSTGRES_PASSWORD: synthetic-postgres-password
          POSTGRES_DB: togavsu
        ports:
          - 5433:5432
        options: >-
          --health-cmd "pg_isready -U togavsu -d togavsu"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 12
    env:
      TOGAVSU_TEST_DATABASE_URL: postgres://togavsu:synthetic-postgres-password@127.0.0.1:5432/togavsu?sslmode=disable
      TOGAVSU_TEST_DATABASE_URL_B: postgres://togavsu:synthetic-postgres-password@127.0.0.1:5433/togavsu?sslmode=disable
      TOGAVSU_PG_DUMP: /usr/lib/postgresql/17/bin/pg_dump
      TOGAVSU_PSQL: /usr/lib/postgresql/17/bin/psql
      TOGAVSU_ACCEPTANCE_OUT: ${{ runner.temp }}/acceptance
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262
        with:
          persist-credentials: false
      - uses: actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5
        with:
          go-version: "1.22"
      - uses: actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065
        with:
          python-version: "3.12"
      - name: Install Postgres 17 client
        run: |
          sudo install -d /usr/share/postgresql-common/pgdg
          sudo curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc
          . /etc/os-release
          echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt ${VERSION_CODENAME}-pgdg main" | sudo tee /etc/apt/sources.list.d/pgdg.list
          sudo apt-get update
          sudo apt-get install -y postgresql-client-17
      - name: Full acceptance (all 52 criteria and every release gate)
        run: sh scripts/ci/full-acceptance.sh
      - name: Acceptance results
        if: always()
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # the pin the other workflows use
        with:
          name: full-acceptance
          path: ${{ runner.temp }}/acceptance
```

The existing Supervisor tests job already runs `scripts/ci/update-load.sh`, which now builds the core image twice and runs a real Compose stack. It needs `timeout-minutes: 90` or a shared image cache.
