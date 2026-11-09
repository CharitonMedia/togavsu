// Package identity authenticates users. Passwords are Argon2id. Admin, attorney,
// staff, and accountant must enroll TOTP before a session exists; client users
// enroll only when the firm requires it. Recovery codes are single-use.
// Sessions and API tokens are stored as hashes. The service layer records writes
// in the audit log and never returns a system principal from a request.
//
// HTTP cookies, Compose, and the backup half of AC-018 are outside this package.
// Pending auth tokens expire after security.lockout_length until the registry
// grows a challenge lifetime. Email lookup scans users until the repository
// grows GetByEmail. API rate counts live in this process.
package identity
