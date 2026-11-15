# AC-041: the Word check

AC-041 needs a DOCX saved by the editor to open in Microsoft Word with no repair prompt. CI proves the save and the package rules (`go-lawcraft`, `core-image-cases`), but no runner has Word, so the integration lead ran this part by hand. It is AC-041's operator evidence in [acceptance-evidence.json](../acceptance-evidence.json); see "Operator evidence" in [the release README](../README.md).

## The file

- Revision: `a3f40353d2faaf7a72a04e4d1560d49b5cd40a4b` (main after #58).
- [AC-041-word.docx](AC-041-word.docx) is the version the browser editor saved in `TestBrowserEditPageE2E` (`internal/editor/e2e_pg_test.go`), run inside the core test image built from `deploy/core.Dockerfile` and `deploy/core-test.Dockerfile` at that revision, against Postgres 17:

  ```
  go test -count=1 -v -run '^TestBrowserEditPageE2E$' ./internal/editor/
  ```

  Exit code 0; the test passed, including `CheckDOCX` on the saved bytes and the check that both texts are in the saved version. The only change for the run was an uncommitted line that wrote those saved bytes out of the test.
- SHA-256 `6b682ce27a331c3089ea6323a22e6fb47a7e24a6efb5f36e94d0d6d9c187589b`, 6,585 bytes.

## The Word run

2026-11-15, on a Windows desktop with Microsoft Word, as the desktop user, with this change's `word-check.ps1` and committed DOCX laid out at their repository paths:

```
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\release\word-check.ps1 -File docs\release\evidence\AC-041-word.docx -Sha256 6b682ce27a331c3089ea6323a22e6fb47a7e24a6efb5f36e94d0d6d9c187589b
```

Output:

```
file AC-041-word.docx sha256 6b682ce27a331c3089ea6323a22e6fb47a7e24a6efb5f36e94d0d6d9c187589b bytes 6585
Word 16.0 build 16.0.20527
opened with no repair: 1 paragraphs, 8 words, compat mode 15, edit text True, original text True
PASS
```

Exit code 0. [word-check.ps1](../../scripts/release/word-check.ps1) opens the file read-only with `OpenAndRepair` off and alerts suppressed, so a damaged package raises an error instead of being repaired; compatibility mode 15 is Word 2013 and later.

The failure paths were run the same way: a copy cut to its first 4,000 bytes gave `FAIL The file appears to be corrupted.` and exit code 1, and a wrong `-Sha256` gave `FAIL sha256 is not 0000` and exit code 1.

## When to repeat it

The runner keeps AC-041 pending once anything under `internal/editor/`, `web/editor/`, `internal/domain/defaults.go` (the LawCraft pin) or `deploy/core.Dockerfile` changes after the revision above. Then save a new file the same way, run the Word check on it, and update this record and the evidence entry (file, SHA-256, revision).
