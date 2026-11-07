package supervisor

import "errors"

var (
	ErrPassphraseRequired       = errors.New("backup passphrase is not set")
	ErrDecrypt                  = errors.New("backup could not be decrypted")
	ErrChecksum                 = errors.New("backup checksum mismatch")
	ErrNotFound                 = errors.New("not found")
	ErrOutsideWindow            = errors.New("outside update window")
	ErrUpdatesPaused            = errors.New("updates are paused")
	ErrSignature                = errors.New("image signature rejected")
	ErrBusy                     = errors.New("another supervisor operation is running")
	ErrReleaseIndexUnconfigured = errors.New("release index is not configured")
	ErrMigratorUnconfigured     = errors.New("migrator is not configured")
	ErrNoCore                   = errors.New("no core container is running")
	ErrUnsafePath               = errors.New("archive path is unsafe")
	ErrBadRequest               = errors.New("bad request")
	ErrUnauthorized             = errors.New("unauthorized")
	ErrHealth                   = errors.New("health check failed")
	ErrArchive                  = errors.New("backup archive is invalid")
	ErrDestination              = errors.New("backup destination failed")
	ErrPreserveWrites           = errors.New("pre-update snapshot was kept so later commits stay in place")
	ErrRestoreCleanup           = errors.New("restored database is live and the previous database was not dropped")
	ErrProxyUnreconciled        = errors.New("caddy kept the new upstream and it could not be rolled back")
	ErrCleanupUnreconciled      = errors.New("aborted update container was not stopped and removed")
	ErrShuttingDown             = errors.New("supervisor is shutting down")
	ErrRecoveryRequired         = errors.New("a restore is waiting for recovery")
	ErrFileRestoreUnsupported   = errors.New("archived file bytes cannot be restored until a storage backend adapter exists")
)

// PublicCode maps an error to a stable API code. Unknown errors become
// "internal" so connection strings and host details stay in the log.
func PublicCode(err error) (code string, status int) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized", 401
	case errors.Is(err, ErrNotFound):
		return "not_found", 404
	case errors.Is(err, ErrPassphraseRequired):
		return "passphrase_required", 409
	case errors.Is(err, ErrOutsideWindow):
		return "outside_update_window", 409
	case errors.Is(err, ErrUpdatesPaused):
		return "updates_paused", 409
	case errors.Is(err, ErrBusy):
		return "operation_in_progress", 409
	case errors.Is(err, ErrShuttingDown):
		return "shutting_down", 409
	case errors.Is(err, ErrRecoveryRequired):
		return "recovery_required", 409
	case errors.Is(err, ErrFileRestoreUnsupported):
		return "file_restore_unsupported", 409
	case errors.Is(err, ErrBadRequest):
		return "bad_request", 400
	case errors.Is(err, ErrDecrypt):
		return "decrypt_failed", 400
	case errors.Is(err, ErrChecksum), errors.Is(err, ErrUnsafePath), errors.Is(err, ErrArchive):
		return "invalid_backup", 422
	case errors.Is(err, ErrSignature), errors.Is(err, ErrHealth):
		return "update_refused", 422
	case errors.Is(err, ErrReleaseIndexUnconfigured), errors.Is(err, ErrMigratorUnconfigured):
		return "not_configured", 503
	case errors.Is(err, ErrNoCore):
		return "no_core", 409
	case errors.Is(err, ErrDestination):
		return "destination_failed", 502
	case errors.Is(err, ErrRestoreCleanup):
		return "restore_cleanup_pending", 409
	case errors.Is(err, ErrProxyUnreconciled):
		return "proxy_unreconciled", 409
	case errors.Is(err, ErrCleanupUnreconciled):
		return "cleanup_unreconciled", 409
	default:
		return "internal", 500
	}
}
