// Package editor is the core's document editor (PRD 8.20): LawCraft at the pinned
// commit, the edit page that frames the browser build, one edit lock per file, saves
// as new file versions, headless editor runs and conversions through wordcraft-cli in
// a sandbox, the firm's court profiles, and New Pleading filled from the matter.
//
// It keeps no bytes and no file table of its own. Every read and write goes through
// internal/files (CONTRACT.md), so walls, roles and attorney-only apply exactly as
// they do to the file, in the UI, the API, /mcp and module calls alike.
package editor
