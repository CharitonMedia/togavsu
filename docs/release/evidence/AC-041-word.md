# AC-041: the Word check

AC-041 needs a DOCX saved by the editor to open in Microsoft Word with no repair prompt. CI proves the save and the package rules (`go-lawcraft`, `core-image-cases`), but no runner has Word, so the integration lead ran this part by hand.

## What was tested

- Revision: `a3f40353d2faaf7a72a04e4d1560d49b5cd40a4b` (main after #58).
- The file: [AC-041-word.docx](AC-041-word.docx), the version the browser editor saved in `TestBrowserEditPageE2E` (`internal/editor/e2e_pg_test.go`), run inside the core test image built from `deploy/core.Dockerfile` and `deploy/core-test.Dockerfile` at that revision, against Postgres 17. The test passed, including `CheckDOCX` on the saved bytes. The only change for the run was a scratch line that wrote those bytes out; it was not committed.
- SHA-256 `6b682ce27a331c3089ea6323a22e6fb47a7e24a6efb5f36e94d0d6d9c187589b`, 6,585 bytes.

## The Word run

- 2026-11-15, on a Windows desktop, Microsoft Word 16.0 (build 16.0.20527), signed in as the desktop user.
- Opened through Word automation with `Documents.Open` read-only, `OpenAndRepair` off and alerts suppressed, so a damaged package fails the open instead of being repaired silently.
- Result: the document opened with no error and no repair, 1 paragraph and 8 words, compatibility mode 15 (Word 2013 and later), with both the original text and the editor's edit present.

This replaces the AC-041 gap in [acceptance-evidence.json](../acceptance-evidence.json). A later change to how the editor writes DOCX should repeat this check.
