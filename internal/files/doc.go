// Package files is Togavsu's one file store (PRD 8.19): file records, immutable
// versions and renditions, typed links, folders, resumable uploads, downloads,
// trash, storage backends, moves between them, the drop folder, and the file-bytes
// half of backups.
//
// Every method takes the caller's domain.Principal and decides access here, in the
// service: a file on a matter follows that matter's wall, a file with only a contact
// follows the contact (a client sees it only when it is their own contact and shared),
// attorney-only files are hidden from roles without attorney access, and accountants
// see only receipts and invoice PDFs. Lists, search, previews, downloads, renditions,
// history, archives, the HTTP API and the Files tab all go through the same check.
//
// Bytes live on a storage.Backend under opaque keys (the version or rendition id),
// sealed with AES-256-GCM when the backend encrypts, and every read is checked
// against the version's SHA-256. A mismatch fails the read, stores a storage alert
// and mails the admins. Version and rendition rows are only ever inserted; where an
// object's bytes are now, and scanner quarantine marks, are kept beside them.
//
// CONTRACT.md is the interface the editor and Doc Tender build on.
package files
