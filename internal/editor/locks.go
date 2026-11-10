package editor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// EditLock is the API's view of a file's edit lock.
type EditLock struct {
	FileID    domain.ID `json:"file_id"`
	UserID    domain.ID `json:"user_id"`
	Since     string    `json:"since"`
	RenewedAt string    `json:"renewed_at"`
}

// CodeEditLockHeld is the API error when someone else holds the lock.
const CodeEditLockHeld = "edit_lock_held"

// LockHeld is the edit_lock_held error: who is editing and since when (PRD 8.20).
type LockHeld struct {
	Holder     domain.ID
	HolderName string
	Since      time.Time
	RenewedAt  time.Time
}

func (e *LockHeld) Error() string {
	who := e.HolderName
	if who == "" {
		who = "Another user"
	}
	return fmt.Sprintf("%s has been editing this file since %s", who, e.Since.UTC().Format("Jan 2, 2006 15:04 UTC"))
}

// lockRow is one edit_locks row.
type lockRow struct {
	ID        domain.ID
	FileID    domain.ID
	UserID    domain.ID
	Since     time.Time
	RenewedAt time.Time
}

func (l lockRow) view() EditLock {
	return EditLock{FileID: l.FileID, UserID: l.UserID, Since: stamp(l.Since), RenewedAt: stamp(l.RenewedAt)}
}

// lockTimeout is the Document editor tab's lock timeout (default 5 minutes).
func (s *Service) lockTimeout(ctx context.Context) (time.Duration, error) {
	return s.durationSetting(ctx, "editor.lock_timeout")
}

func (s *Service) live(ctx context.Context, l lockRow) (bool, error) {
	timeout, err := s.lockTimeout(ctx)
	if err != nil {
		return false, err
	}
	return s.now().Before(l.RenewedAt.Add(timeout)), nil
}

func (s *Service) readLock(ctx context.Context, fileID domain.ID, forUpdate bool) (lockRow, bool, error) {
	return readLockOn(ctx, audit.Conn(ctx, s.pool), fileID, forUpdate)
}

func readLockOn(ctx context.Context, db audit.Querier, fileID domain.ID, forUpdate bool) (lockRow, bool, error) {
	q := `SELECT id, file_id, user_id, since_at, renewed_at FROM edit_locks WHERE file_id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	var l lockRow
	var since, renewed int64
	err := db.QueryRowContext(ctx, q, fileID).Scan(&l.ID, &l.FileID, &l.UserID, &since, &renewed)
	if errors.Is(err, sql.ErrNoRows) {
		return lockRow{}, false, nil
	}
	if err != nil {
		return lockRow{}, false, err
	}
	l.Since, l.RenewedAt = fromUS(since), fromUS(renewed)
	return l, true, nil
}

func (s *Service) held(ctx context.Context, l lockRow) error {
	return &LockHeld{Holder: l.UserID, HolderName: s.userName(ctx, l.UserID), Since: l.Since, RenewedAt: l.RenewedAt}
}

// checkEditable is the access an edit lock needs: an editor role, a file the caller can
// write, in a format the editor opens.
func (s *Service) checkEditable(ctx context.Context, p domain.Principal, fileID domain.ID) error {
	c, err := s.loadCurrent(ctx, p, fileID)
	if err != nil {
		return err
	}
	if !Editable(c.File.Extension) {
		return domain.Invalid("file", "the editor opens DOCX, DOC, ODT, RTF, HTML, Markdown and text files; preview or download this one")
	}
	return nil
}

// AcquireLock takes the file's edit lock for p (PRD 8.20: one editor at a time). A live
// lock held by someone else is a *LockHeld; p's own lock is renewed; a lapsed lock is
// taken over.
func (s *Service) AcquireLock(ctx context.Context, p domain.Principal, fileID domain.ID) (EditLock, error) {
	if err := s.checkEditable(ctx, p, fileID); err != nil {
		return EditLock{}, err
	}
	var out lockRow
	err := audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		db := audit.Conn(ctx, s.pool)
		if err := serialize(ctx, db, fileID); err != nil {
			return err
		}
		now := s.now().UTC()
		id, err := newID()
		if err != nil {
			return err
		}
		// A first lock inserts; the unique index on file_id makes a second insert a no-op,
		// and the row is then read under FOR UPDATE.
		if _, err := db.ExecContext(ctx, `INSERT INTO edit_locks (id, created_at, updated_at, created_by, file_id, user_id, since_at, renewed_at)
VALUES ($1, $2, $2, $3, $4, $3, $2, $2) ON CONFLICT (file_id) DO NOTHING`, id, us(now), p.UserID, fileID); err != nil {
			return err
		}
		l, ok, err := s.readLock(ctx, fileID, true)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("editor: edit lock vanished")
		}
		if l.ID == id {
			out = l
			return nil
		}
		alive, err := s.live(ctx, l)
		if err != nil {
			return err
		}
		switch {
		case l.UserID == p.UserID && alive:
			l.RenewedAt = now
		case alive:
			return s.held(ctx, l)
		default:
			// The holder stopped renewing: the lock lapsed and p takes it.
			l.UserID, l.Since, l.RenewedAt = p.UserID, now, now
		}
		if _, err := db.ExecContext(ctx, `UPDATE edit_locks SET user_id = $1, since_at = $2, renewed_at = $3, updated_at = $3 WHERE id = $4`,
			l.UserID, us(l.Since), us(l.RenewedAt), l.ID); err != nil {
			return err
		}
		out = l
		return nil
	})
	if err != nil {
		return EditLock{}, err
	}
	return out.view(), nil
}

// RenewLock keeps p's lock while the editor is open. A lock that lapsed, or that someone
// else took, is not renewed: the page must take it again.
func (s *Service) RenewLock(ctx context.Context, p domain.Principal, fileID domain.ID) (EditLock, error) {
	if err := requireEditor(p); err != nil {
		return EditLock{}, err
	}
	if _, err := s.files.Get(ctx, p, fileID); err != nil {
		return EditLock{}, err
	}
	var out lockRow
	err := audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		if err := serialize(ctx, audit.Conn(ctx, s.pool), fileID); err != nil {
			return err
		}
		l, ok, err := s.readLock(ctx, fileID, true)
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("you do not hold the edit lock; open the file to edit it again")
		}
		alive, err := s.live(ctx, l)
		if err != nil {
			return err
		}
		if l.UserID != p.UserID {
			if alive {
				return s.held(ctx, l)
			}
			return domain.Conflict("you do not hold the edit lock; open the file to edit it again")
		}
		if !alive {
			return domain.Conflict("the edit lock lapsed; open the file to edit it again")
		}
		l.RenewedAt = s.now().UTC()
		if _, err := audit.Conn(ctx, s.pool).ExecContext(ctx, `UPDATE edit_locks SET renewed_at = $1, updated_at = $1 WHERE id = $2`, us(l.RenewedAt), l.ID); err != nil {
			return err
		}
		out = l
		return nil
	})
	if err != nil {
		return EditLock{}, err
	}
	return out.view(), nil
}

// ReleaseLock frees the lock. The holder may release it; an admin may release anyone's,
// and that release goes to the audit log (PRD 8.20).
func (s *Service) ReleaseLock(ctx context.Context, p domain.Principal, fileID domain.ID) error {
	if err := requireEditor(p); err != nil {
		return err
	}
	if _, err := s.files.Get(ctx, p, fileID); err != nil {
		return err
	}
	return audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		if err := serialize(ctx, audit.Conn(ctx, s.pool), fileID); err != nil {
			return err
		}
		l, ok, err := s.readLock(ctx, fileID, true)
		if err != nil {
			return err
		}
		if !ok {
			return domain.NotFound("edit lock")
		}
		alive, err := s.live(ctx, l)
		if err != nil {
			return err
		}
		if l.UserID != p.UserID && alive {
			if !p.HasRole(domain.RoleAdmin) {
				return domain.Forbidden("only the person editing or an admin can release the edit lock")
			}
			old := fmt.Sprintf("held by %s (%s) since %s", s.userName(ctx, l.UserID), l.UserID, stamp(l.Since))
			if err := s.record(ctx, p, "file", fileID, "edit_lock", old, "released by an admin"); err != nil {
				return err
			}
		}
		_, err = audit.Conn(ctx, s.pool).ExecContext(ctx, `DELETE FROM edit_locks WHERE id = $1`, l.ID)
		return err
	})
}

// holdsLock says p holds the file's live lock.
func (s *Service) holdsLock(ctx context.Context, p domain.Principal, fileID domain.ID) error {
	l, ok, err := s.readLock(ctx, fileID, false)
	if err != nil {
		return err
	}
	return s.checkHolder(ctx, p, l, ok)
}

func (s *Service) checkHolder(ctx context.Context, p domain.Principal, l lockRow, ok bool) error {
	if !ok {
		return domain.Conflict("open the file in the editor first: you do not hold its edit lock")
	}
	alive, err := s.live(ctx, l)
	if err != nil {
		return err
	}
	if l.UserID != p.UserID {
		if alive {
			return s.held(ctx, l)
		}
		return domain.Conflict("you do not hold the edit lock")
	}
	if !alive {
		return domain.Conflict("the edit lock lapsed; reopen the file before saving")
	}
	return nil
}

// otherHolds reports a live lock someone other than p holds.
func (s *Service) otherHolds(ctx context.Context, p domain.Principal, fileID domain.ID) error {
	l, ok, err := s.readLock(ctx, fileID, false)
	if err != nil {
		return err
	}
	return s.checkOther(ctx, p, l, ok)
}

func (s *Service) checkOther(ctx context.Context, p domain.Principal, l lockRow, ok bool) error {
	if !ok || l.UserID == p.UserID {
		return nil
	}
	alive, err := s.live(ctx, l)
	if err != nil || !alive {
		return err
	}
	return s.held(ctx, l)
}

// serialize takes the file's edit-lock advisory lock for the rest of the transaction on
// db. Every change to a file's edit lock (AcquireLock, RenewLock, ReleaseLock) and every
// locked write (writeUnderLock) takes it first, so they run one at a time per file. It is
// an advisory lock rather than the edit_locks row lock because the write runs on another
// connection: a waiting AcquireLock would otherwise hold a foreign-key share lock on the
// files row that files.SaveVersion needs, and the two would wait on each other forever.
func serialize(ctx context.Context, db audit.Querier, fileID domain.ID) error {
	_, err := db.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "togavsu.editor.edit_lock/"+string(fileID))
	return err
}

// writeUnderLock runs write, a new version of the file, inside the file's edit-lock
// serialization (serialize) and after the lock check passes there: holder (Save) means p
// must hold the live lock, otherwise (a run saved as a version) nobody else may. No one
// can take, renew or release the lock between the check and the write, so the lock
// cannot lapse into someone else's hands with this version still to come, and a new
// holder opens the file only after it is in. A save by the holder renews the lock in the
// same transaction.
//
// write runs on its own connection (files.SaveVersion opens its own transaction), so
// ctx must not carry a transaction on s.pool.
func (s *Service) writeUnderLock(ctx context.Context, p domain.Principal, fileID domain.ID, holder bool, write func() error) error {
	if audit.Tx(ctx, s.pool) != nil {
		return errors.New("editor: a locked write cannot run inside a transaction")
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := serialize(ctx, tx, fileID); err != nil {
		return err
	}
	l, ok, err := readLockOn(ctx, tx, fileID, false)
	if err != nil {
		return err
	}
	if holder {
		err = s.checkHolder(ctx, p, l, ok)
	} else {
		err = s.checkOther(ctx, p, l, ok)
	}
	if err != nil {
		return err
	}
	if s.beforeLockedWrite != nil {
		s.beforeLockedWrite()
	}
	if err := write(); err != nil {
		return err
	}
	if holder {
		// A save is activity: it renews the lock too. The version is in either way: a
		// lost renewal is made up by the page's own, so it does not fail the save.
		at := us(s.now().UTC())
		if _, err := tx.ExecContext(ctx, `UPDATE edit_locks SET renewed_at = $1, updated_at = $1 WHERE id = $2`, at, l.ID); err == nil {
			_ = tx.Commit()
		}
	}
	return nil
}
