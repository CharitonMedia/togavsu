# Files contract for the editor and Doc Tender

Task `files`, PRD 5 and 8.19. This is the interface the `editor` (PRD 8.20) and
`doc-tender` (PRD 8.21) tasks build on. Neither keeps bytes, a file table or an editor
store of its own. Everything below is in `internal/files` and checks access in the
service, so a caller passes the real principal: a person, a module token, or the
system principal (`domain.Principal{Source: domain.SourceSystem, UserID: <a real user>}`;
the user id is written to `saved_by`/`made_by`, which reference `users`).

## Events

- `Config.OnVersion func(ctx, VersionCreated)` runs after every version commits:
  upload publish, new version, editor save, editor run, conversion, grouping, restore,
  drop folder, module write. It carries file, version, matter, number, `how` and
  `source`. This is `file.version_created`. Doc Tender queues its job from it; the
  integration lead also fans it out to module webhooks.
- Timeline rows go to `core.events` in the same transaction as the write:
  `file.version_created`, `file.updated`, `file.downloaded` (client downloads have
  `actor_kind = client` and `client_visible = 1` for the highlight), `file.purged`.
  `client_visible` is 1 for a client's own action, or for a file that is shared with the
  client and not attorney-only; an attorney-only file's events are never client-visible.

## Reading bytes

- `OpenForProcessing(ctx, p, fileID, versionID)`: verified bytes for extraction or an
  editor run. Needs write access; not logged as a download.
- `OpenVersion(ctx, p, fileID, versionID, confirm)`: a person's download. Logged to the
  audit log and timeline. A quarantined version returns `ErrQuarantined` until `confirm`.
- Every read is a `*storage.Verified`: read it to EOF. `storage.ErrIntegrity` means the
  bytes changed on the backend; the alert and admin mail are already done.

## Writing

- `Create(ctx, p, NewFile{...}, r, size)`: a new file and version 1. `How` is one of
  `upload, editor, editor_run, conversion, grouping, module`; `Source` is `upload,
  client_upload, editor, email, zoom, drop_folder, importer` or a module id. Client,
  email and Zoom sources arrive with origin and status `received`.
- `SaveVersion(ctx, p, fileID, r, size, SaveOptions{How, MediaType, Note, Extension})`:
  the next version. The editor saves a non-DOCX original as DOCX with
  `Extension: "docx"`; the name's extension changes and the rename is audited.
- `AddRendition(ctx, p, fileID, versionID, kind, r, size)`: `markdown`, `pdf_preview`
  or `grouped_pdf`. Each call adds the next number for that kind on the original's
  current backend; nothing is overwritten. The newest Markdown copy becomes the Files
  search text. `MarkdownName(name)` is the copy's name (base name + `.md`); renditions
  report it from the current file name, so a rename carries it.
- `Link(ctx, p, from, to, relationship)`: `conversion_of` (conversion to its source),
  `grouped_photo_source` (grouped PDF to each photo), `attachment` (email to its
  attachments). Both files must share a matter.

## Doc Tender's naming and filing

- `ApplyTender(ctx, p, fileID, TenderUpdate{Name, Category, Folder})` sets only fields a
  person has not locked (`locked_fields`: `name`, `category`, `matter`, `folder`, `grouping`) and
  reports `Set` and `Skipped`. The folder follows the category, so `Folder` is skipped
  when either `folder` or `category` is locked. It never changes a file's matter. A name clash in the
  matter becomes `-2`, `-3`; clash resolution holds a per-matter lock, so files filed at once never share a name. `Folder` is the category's folder name; the matter folder
  is created when missing.
- A person's `Rename` locks `name`; `UpdateMetadata` locks `category` and `matter`;
  `MoveToFolder` locks `folder`.
- `ApplyTender`, `Rename`, `MoveToFolder`, `MarkStatus`, `Trash`, `Restore`,
  `UpdateMetadata` and `Purge` read the file row `FOR UPDATE` inside their transaction,
  the same lock `SaveVersion` takes. Handling `file.version_created` for version N while
  the editor saves N+1 therefore never makes N current again, and never writes back a
  name, folder or `locked_fields` that a person committed while Doc Tender waited.
- `UniqueName(ctx, matter, name)` previews the clash rule.
- `MatterFiles(ctx, systemPrincipal, matter)` lists a reopened matter's files to queue.

## Folders and default folders

- `Folders`, `GetFolder`, the folder zip and the matter Files tab use the full matter read
  check (wall, then role and client scope, as practice applies to the matter record).
  A client sees only a matter they are a party to; an accountant sees no folders. Each
  refusal is not found. Only a caller who may write files on the matter creates its
  default folders on first open.
- Default folders come from the matter's practice area when the `Directory` implements
  `AreaFolders(ctx, area) ([]string, error)` and returns a non-empty list, otherwise from
  the global `files.default_folders` setting.
- **Pending, owner practice:** `practice_areas` has no `default_folders` column yet (PRD 5,
  API contract `PracticeArea.default_folders`), so `PracticeDirectory` does not implement
  `AreaFolders`, and every matter in the running product gets the global list. Once
  practice stores the column, `PracticeDirectory.AreaFolders` should return it. The files
  side is implemented and tested with a directory that supplies area folders.

## Drop folder

A dropped share copy is taken once. Its `drop_takes` row (backend, path, size, SHA-256,
file id) commits in the same transaction as the file record and is closed once the share
copy is gone. If the share refuses the delete (no delete right, a file held open), the
copy stays in the drop folder and its take stays open: later passes, including after a
restart and after Doc Tender files a top-level drop to a matter, only retry the delete.
The admins get one storage alert per refused take. Only an open take stops a new record;
a document that was taken, removed and dropped again with the same name and bytes is a
new drop, and so is a copy whose record was emptied from the trash while the share still
held it. A file whose bytes change under the same name is a new drop.

## Editor locks

`edit_locks` (migration 0001) and the lock endpoints (`acquireEditLock`,
`renewEditLock`, `releaseEditLock`) belong to the editor task. Its writes go through
`SaveVersion` above, so walls and roles apply exactly as they do to the file.

## Backups

`ExportBytes(ctx, admin, w, skip)` writes a tar (manifest, then each object's verified
plaintext) of every version and rendition, Markdown copies included, on every backend
the backup covers (`backed_up` and `files.backup_scope`). `skip` lets the nightly backup
copy each immutable object once. `RestoreBytes(ctx, admin, r)` puts each object back
where the restored database says it lives and checks its SHA-256 there. An archive
missing a manifest object, or repeating one, fails the restore; an object whose file
was emptied from the trash after the archive was taken is skipped (`purged`), so a
purge stays a purge. A storage move skips purged files the same way. A move that fails
stays failed and keeps every old copy; starting the same move again (same source, target
and scope) resumes it, and old copies go only when it checks out and is confirmed.
Confirming never deletes a copy that is an object's current location. The supervisor
encrypts the archive and restores the database; wiring it to these two calls is
pending (PONYTAIL-DEBT platform ceiling 13).
