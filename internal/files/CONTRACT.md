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
  matter becomes `-2`, `-3`. `Folder` is the category's folder name; the matter folder
  is created when missing.
- A person's `Rename` locks `name`; `UpdateMetadata` locks `category` and `matter`;
  `MoveToFolder` locks `folder`.
- `UniqueName(ctx, matter, name)` previews the clash rule.
- `MatterFiles(ctx, systemPrincipal, matter)` lists a reopened matter's files to queue.

## Editor locks

`edit_locks` (migration 0001) and the lock endpoints (`acquireEditLock`,
`renewEditLock`, `releaseEditLock`) belong to the editor task. Its writes go through
`SaveVersion` above, so walls and roles apply exactly as they do to the file.

## Backups

`ExportBytes(ctx, admin, w, skip)` writes a tar (manifest, then each object's verified
plaintext) of every version and rendition, Markdown copies included, on every backend
the backup covers (`backed_up` and `files.backup_scope`). `skip` lets the nightly backup
copy each immutable object once. `RestoreBytes(ctx, admin, r)` puts each object back
where the restored database says it lives and checks its SHA-256 there. The supervisor
encrypts the archive and restores the database; wiring it to these two calls is
pending (PONYTAIL-DEBT platform ceiling 13).
