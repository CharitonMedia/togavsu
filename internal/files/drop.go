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
	for _, m := range matters {
		byNumber[m.Number] = m.ID
		if err := os.MkdirAll(filepath.Join(root, m.Number), fs.ModePerm&0o750); err != nil {
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
		// A file is taken once. If an earlier pass (or an earlier process) committed
		// its record but could not delete it, only the delete is retried.
		prior, taken, err := s.st.takenDrop(ctx, c.matter, filepath.Base(c.path), cur.sha)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if taken {
			s.removeTaken(ctx, b.ID, c.path, prior, cur.sha)
			continue
		}
		view, err := s.ingest(ctx, who, c.path, c.matter, cur)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.mu.Lock()
		delete(s.drops, c.path)
		s.mu.Unlock()
		out = append(out, view)
		s.removeTaken(ctx, b.ID, c.path, view.ID, cur.sha)
	}
	s.mu.Lock()
	for p := range s.drops {
		if strings.HasPrefix(p, root+string(filepath.Separator)) && !seen[p] {
			delete(s.drops, p)
		}
	}
	for p := range s.dropAlerted {
		if strings.HasPrefix(p, root+string(filepath.Separator)) && !seen[p] {
			delete(s.dropAlerted, p)
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

// ingest stores the dropped bytes and checks they still hash to what held steady.
// The caller removes the dropped file only after the record commits.
func (s *Service) ingest(ctx context.Context, who domain.Principal, p string, matter domain.ID, seen dropSeen) (FileView, error) {
	f, err := os.Open(p)
	if err != nil {
		return FileView{}, err
	}
	defer f.Close()
	return s.create(ctx, who, NewFile{
		Name: filepath.Base(p), MatterID: matter, Source: "drop_folder", How: "upload",
		MediaType: mediaTypeOf(filepath.Base(p)),
	}, f, seen.size, seen.sha, nil)
}

// removeTaken deletes a drop file whose record is committed. When the share refuses
// the delete (no delete right, a file held open), the file stays where it is, is
// never taken again (takenDrop), and the admins get one storage alert for it.
func (s *Service) removeTaken(ctx context.Context, backend domain.ID, p string, file domain.ID, sha string) {
	err := s.removeDrop(p)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		s.mu.Lock()
		delete(s.dropAlerted, p)
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	already := s.dropAlerted[p] == sha
	s.dropAlerted[p] = sha
	s.mu.Unlock()
	if already {
		return
	}
	detail := fmt.Sprintf("the drop folder took %s as file %s but could not remove it from the share: %v. "+
		"It will not be taken again; delete it from the drop folder by hand or give Togavsu delete rights there.", p, file, err)
	emailed := false
	if s.mail != nil {
		emailed = s.mail.MailAdmins(ctx, "Togavsu storage alert: a dropped file could not be removed", detail) == nil
	}
	if m, err := s.meta(domain.SystemUserID); err == nil {
		_ = s.st.insertAlert(ctx, m, backend, file, file, detail, emailed)
	}
}
