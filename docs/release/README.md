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
| signed-release | `scripts/release/verify-release.sh` | HEAD carries a release tag, `cosign verify --key deploy/release/cosign.pub --insecure-ignore-tlog=true` accepts `ghcr.io/charitonmedia/togavsu-core:<tag>` and `togavsu-supervisor:<tag>`, and the supervisor image ships that same key at `/usr/share/togavsu/cosign.pub`. |
| docker-desktop | `scripts/release/desktop-install.sh` | It runs on Docker Desktop (Windows or macOS) and a clean install of the tested revision passes the first-run checks. It refuses any other engine. |
| product-suites | the Core, API contract, QA harness, Supervisor, Backup integration, UI QC and Compose first-run scripts | Every product CI suite passes at the tested revision, not only the checks a criterion names. |

The signed-release and docker-desktop gates can't pass from a lane runner: the release private key is kept off GitHub by design, nothing here can push images or tags, and GitHub's Linux runners have Docker Engine, not Docker Desktop.

## Release signing

Patrick decided on 2026-11-12:

- The release private key is kept off GitHub, on the integration lead's machine. No repository secret holds it, ever, and no workflow signs.
- The release public key is committed at [deploy/release/cosign.pub](../../deploy/release/cosign.pub). The supervisor image copies it to `/usr/share/togavsu/cosign.pub`, and compose.yaml points `COSIGN_KEY` there, so no install creates or edits a key file.
- Release signatures are not written to the public transparency log. compose.yaml therefore defaults `COSIGN_VERIFY_FLAGS` to `--insecure-ignore-tlog=true`. The supervisor still runs `cosign verify --key` against the release key before every update; nothing skips the signature check (`TestUpdate*` in internal/supervisor, `tests/update/load.sh`).
- The release workflow, [proposed-ci/release.yml](proposed-ci/release.yml), passes actionlint 1.7.7. On a `v*` tag, or a manual run with `operation=publish`, it builds `deploy/core.Dockerfile` and `Dockerfile` at the tag, pushes `ghcr.io/charitonmedia/togavsu-core:<tag>` and `togavsu-supervisor:<tag>`, and records both digests in the job summary and in a `digests.json` asset on a draft release for the tag. It never signs and holds no private key.
- Its second job runs only when dispatched by hand with `operation=attach-signature`. It takes one image digest plus a base64 signature and a base64 payload made off GitHub. It runs [scripts/release/attach-signature.sh](../../scripts/release/attach-signature.sh), which refuses unless the digest is one of that tag's recorded images, the payload names that digest, and the signature verifies over the payload with `deploy/release/cosign.pub`. Only then does it run `cosign attach signature` with `GITHUB_TOKEN` (`packages: write`) and verify the image. `scripts/ci/test-release-attach.sh` checks every refusal and the good path against a local registry with throwaway keys; `scripts/ci/update-load.sh` runs it.

## Cutting the release

These steps need the integration lead and credentials no lane has:

1. Land `docs/release/proposed-ci/release.yml` as `.github/workflows/release.yml` through a reviewed control PR.
2. Tag the revision on which `sh scripts/ci/full-acceptance.sh` exits 0, except for the two gates below, and push the tag. The workflow publishes both images and `digests.json`.
3. Off GitHub, for each recorded digest: `cosign generate ghcr.io/charitonmedia/<image>@<digest> > payload.json`, then `cosign sign-blob --key <release key> --tlog-upload=false payload.json`. Dispatch the workflow with `operation=attach-signature`, the tag, the digest, the base64 signature and `base64 -w0 payload.json`.
4. Run `sh scripts/release/verify-release.sh` at the tag; it passes only when both images verify and the supervisor image ships the committed key.
5. Run `sh scripts/release/desktop-install.sh` on a clean Windows machine and a clean Mac with Docker Desktop, and attach the output to the release.
6. Publish the draft release.

## Proposed CI change

The Full acceptance job is in `.github/workflows/product-ci.yml` (control PR #46). The release workflow is the one CI change still proposed: copy [proposed-ci/release.yml](proposed-ci/release.yml) to `.github/workflows/release.yml` unchanged. `docs/release/proposed-ci/product-ci-full-acceptance.patch` is the earlier Full acceptance proposal, kept for the record.
