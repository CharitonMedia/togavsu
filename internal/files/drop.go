package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// dropSeen is what one scan saw of a dropped file.
type dropSeen struct {
	size  int64
	mod   time.Time
	sha   string
	since time.Time
}

// DropPath is a network path backend's drop folder: drop_path, or <path>/drop.
func DropPath(set storage.Settings) string {
	if set.DropPath != "" {
		return set.DropPath
	}
	return filepath.Join(set.Path, "drop")
}

// ScanDrop runs one pass over every network path with its drop folder on. Each open
// matter gets a subfolder named by its matter number. A file whose size and SHA-256
// have held for files.drop_steady_seconds is added as a file record (to that matter,
// or to no matter at the top level, for Doc Tender to match) and then removed from the
// drop folder. A file whose record committed but which the share would not let us
// delete is never taken again, and raises one admin alert. It returns the files it took.
// ponytail: ceiling: steadiness is tracked in this process and each pass rehashes a
// file whose size and modification time held. Upgrade path: keep sightings in a table
// and hash only after the size holds. trigger: a drop folder routinely holds files over
// 1 GiB or a second core process scans the same share.
func (s *Service) ScanDrop(ctx context.Context) ([]FileView, error) {
	steady, err := s.intSetting(ctx, "files.drop_steady_seconds")
	if err != nil {
		return nil, err
	}
	rows, err := s.st.backends(ctx)
	if err != nil {
		return nil, err
	}
	matters, err := s.dir.OpenMatters(ctx)
	if err != nil {
		return nil, err
	}
	var out []FileView
	var errs []error
	for _, b := range rows {
		if !b.DropFolder || b.Kind != storage.KindNetworkPath {
			continue
		}
		set, err := s.openSettings(b.SettingsCiphertext)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		hold, err := time.ParseDuration(fmt.Sprintf("%ds", steady))
		if err != nil {
			return out, err
		}
		taken, err := s.scanOne(ctx, b, DropPath(set), matters, hold)
		out = append(out, taken...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func (s *Service) scanOne(ctx context.Context, b domain.StorageBackend, root string, matters []Matter, steady time.Duration) ([]FileView, error) {
	if err := os.MkdirAll(root, fs.ModePerm&0o750); err != nil {
		return nil, err
	}
	byNumber := map[string]domain.ID{}
	clash := map[string]bool{}
	for _, m := range matters {
		name, ok := dropFolderName(m.Number)
		if !ok {
			continue // no subfolder for a number that cannot be one safe folder name
		}
		if id, taken := byNumber[name]; taken && id != m.ID {
			clash[name] = true
		}
		byNumber[name] = m.ID
	}
	for name := range clash {
		delete(byNumber, name) // two matters map to one folder name: neither gets it
	}
	for name := range byNumber {
		if err := os.MkdirAll(filepath.Join(root, name), fs.ModePerm&0o750); err != nil {
			return nil, err
		}
	}
	type candidate struct {
		path   string
		matter domain.ID
	}
	var found []candidate
	top, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range top {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			if e.Type().IsRegular() {
				found = append(found, candidate{path: filepath.Join(root, name)})
			}
			continue
		}
		matter, ok := byNumber[name]
		if !ok {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		for _, f := range inner {
			if f.Type().IsRegular() && !strings.HasPrefix(f.Name(), ".") {
				found = append(found, candidate{path: filepath.Join(root, name, f.Name()), matter: matter})
			}
		}
	}
	now := s.now()
	who := domain.Principal{Source: domain.SourceSystem, UserID: b.CreatedBy}
	seen := map[string]bool{}
	var out []FileView
	var errs []error
	for _, c := range found {
		seen[c.path] = true
		st, err := os.Stat(c.path)
		if err != nil {
			continue
		}
		s.mu.Lock()
		prev, had := s.drops[c.path]
		s.mu.Unlock()
		cur := dropSeen{size: st.Size(), mod: st.ModTime(), since: now}
		sha, err := hashFile(c.path)
		if err != nil {
			continue
		}
		cur.sha = sha
		if had && prev.size == cur.size && prev.sha == cur.sha {
			cur.since = prev.since
		}
		s.mu.Lock()
		s.drops[c.path] = cur
		s.mu.Unlock()
		if !had || now.Sub(cur.since) < steady {
			continue
		}
		// A share copy is taken once. If an earlier pass (or an earlier process)
		// committed its record but could not delete it, only the delete is retried. The
		// check is on this copy's own take, not on records with the same name and bytes:
		// a document taken, removed and later dropped again is a new drop.
		prior, pending, err := s.st.pendingDropTake(ctx, b.ID, c.path, cur.sha)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if pending && !prior.Purged {
			s.removeTaken(ctx, b.ID, c.path, prior)
			continue
		}
		if pending {
			// The record was emptied from the trash while the share kept its copy: that
			// copy is now the only one, so close the old take and take it again.
			if err := s.st.dropTakeClosed(ctx, prior.ID, us(now)); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		view, take, err := s.ingest(ctx, who, b.ID, c.path, c.matter, cur)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.mu.Lock()
		delete(s.drops, c.path)
		s.mu.Unlock()
		out = append(out, view)
		s.removeTaken(ctx, b.ID, c.path, take)
	}
	s.mu.Lock()
	for p := range s.drops {
		if strings.HasPrefix(p, root+string(filepath.Separator)) && !seen[p] {
			delete(s.drops, p)
		}
	}
	s.mu.Unlock()
	return out, errors.Join(errs...)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := storage.NewHashingReader(f)
	if _, err := io.Copy(io.Discard, h); err != nil {
		return "", err
	}
	return h.Sum(), nil
}

// ingest stores the dropped bytes and checks they still hash to what held steady. The
// take of this share copy commits with the record; the caller removes the copy after.
func (s *Service) ingest(ctx context.Context, who domain.Principal, backend domain.ID, p string, matter domain.ID, seen dropSeen) (FileView, dropTake, error) {
	f, err := os.Open(p)
	if err != nil {
		return FileView{}, dropTake{}, err
	}
	defer f.Close()
	m, err := s.meta(who.UserID)
	if err != nil {
		return FileView{}, dropTake{}, err
	}
	take := dropTake{ID: m.ID}
	view, err := s.create(ctx, who, NewFile{
		Name: filepath.Base(p), MatterID: matter, Source: "drop_folder", How: "upload",
		MediaType: mediaTypeOf(filepath.Base(p)),
	}, f, seen.size, seen.sha, func(ctx context.Context, fr fileRow, _ domain.FileVersion) error {
		take.FileID = fr.ID
		return s.st.insertDropTake(ctx, m.ID, us(m.CreatedAt), backend, p, seen.size, seen.sha, fr.ID)
	})
	return view, take, err
}

// removeTaken deletes a drop file whose record is committed and closes its take. When
// the share refuses the delete (no delete right, a file held open), the file stays
// where it is, its take stays pending so later passes only retry the delete, and the
// admins get one storage alert for it.
func (s *Service) removeTaken(ctx context.Context, backend domain.ID, p string, take dropTake) {
	err := s.removeDrop(p)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		_ = s.st.dropTakeClosed(ctx, take.ID, us(s.now()))
		return
	}
	if take.Refused {
		return
	}
	first, merr := s.st.dropTakeRefused(ctx, take.ID)
	if merr != nil || !first {
		return
	}
	detail := fmt.Sprintf("the drop folder took %s as file %s but could not remove it from the share: %v. "+
		"It will not be taken again; delete it from the drop folder by hand or give Togavsu delete rights there.", p, take.FileID, err)
	emailed := false
	if s.mail != nil {
		emailed = s.mail.MailAdmins(ctx, "Togavsu storage alert: a dropped file could not be removed", detail) == nil
	}
	if m, err := s.meta(domain.SystemUserID); err == nil {
		_ = s.st.insertAlert(ctx, m, backend, take.FileID, take.FileID, detail, emailed)
	}
}

// dropFolderName turns a matter number into its drop subfolder name: one path
// component, with any slash or backslash in the number (a format like YYYY/NNNN)
// replaced by a hyphen. A number that would still not be one safe component (empty,
// dot-led, or holding "..") gets no subfolder, so the scanner never leaves the root.
func dropFolderName(number string) (string, bool) {
	n := strings.NewReplacer("/", "-", `\`, "-").Replace(strings.TrimSpace(number))
	if n == "" || strings.HasPrefix(n, ".") || strings.Contains(n, "..") || filepath.Base(n) != n {
		return "", false
	}
	return n, true
}
