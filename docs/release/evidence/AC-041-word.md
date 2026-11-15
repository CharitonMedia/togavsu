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
alerts off: opened with no repair and no prompt: 1 paragraphs, 8 words, compat mode 15, edit text True, original text True
alerts on: opened with no repair and no prompt: 1 paragraphs, 8 words, compat mode 15, edit text True, original text True
PASS
```

Exit code 0. [word-check.ps1](../../scripts/release/word-check.ps1) opens the file read-only twice, each in a Word instance of its own: once with alerts suppressed and `OpenAndRepair` off, where a damaged package raises an error; and once with alerts on, where any prompt Word raises, a repair prompt included, holds the open, so an open that has not returned within 60 seconds fails. Compatibility mode 15 is Word 2013 and later.

## Failure paths

Each was run with the same command on a file built from the artifact, with `-Sha256` set to that file's hash:

| File | How it was made | Result | Exit |
| --- | --- | --- | --- |
| first 4,000 bytes of the artifact | truncated | alerts off `FAIL ... The file appears to be corrupted.`; alerts on held at a prompt | 1 |
| the artifact, wrong `-Sha256 0000` | unchanged | `FAIL sha256 is not 0000` | 1 |
| malformed `word/document.xml` | `<w:p><w:r><w:t>broken</w:t></w:p>` (an unclosed run) added before `</w:body>`; the ZIP is otherwise intact | both opens `FAIL ... the open did not return within 60s, so Word is holding it at a prompt` | 1 |
| dangling image relationship | a `word/_rels/document.xml.rels` entry for `media/missing.png` and an inline drawing that embeds it | both opens held at a prompt, as above | 1 |

A separate probe, opening each file with alerts on and nothing else, confirmed that Word holds the open at a prompt for the last two files and opens the artifact with none. An unknown element in the main namespace (`<w:p><w:zzbogus/></w:p>`) opened with and without alerts with no prompt, so it is not a case Word offers to repair, and the check passes it.

## When to repeat it

The runner keeps AC-041 pending once anything under `internal/editor/`, `web/editor/`, `internal/domain/defaults.go` (the LawCraft pin) or `deploy/core.Dockerfile` changes after the revision above. Then save a new file the same way, run the Word check on it, and update this record and the evidence entry (file, SHA-256, revision).
