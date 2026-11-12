package core

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// BackupPassphraseFile is the installer secret the supervisor reads as
// BACKUP_PASSPHRASE_FILE. Bootstrap creates it empty and owned by the core's user
// (deploy/bootstrap/bootstrap-secrets.sh); first-run setup fills it. Without it every
// backup, and so every update's pre-update backup, is refused as passphrase_required.
const BackupPassphraseFile = "/appsecrets/backup_passphrase"

// writeBackupPassphrase fills the passphrase file after setup stored the setting. The
// file is written in place: the secrets directory is not writable by the core's user.
// No secrets directory (tests, a process outside the image) is not an error.
func writeBackupPassphrase(path, passphrase string) error {
	if path == "" || passphrase == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// Bootstrap created the file with mode 0600; without O_CREATE the mode is unused.
	var unused fs.FileMode
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, unused)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(passphrase + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (a *App) persistBackupPassphrase(passphrase string) {
	if err := writeBackupPassphrase(a.passphraseFile, passphrase); err != nil {
		// The setting is stored; the supervisor reports passphrase_required until the
		// file is written. The passphrase itself is never logged.
		log.Printf("core: backup passphrase file %s was not written: %v", a.passphraseFile, err)
	}
}
