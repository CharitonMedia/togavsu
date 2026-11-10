package files

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TenderUpdate is what Doc Tender publishes for a file. Empty fields are left alone.
type TenderUpdate struct {
	// Name is the pattern name including its extension. A clash in the matter adds -2, -3.
	Name string
	// Category is a doc_categories name.
	Category string
	// Folder is the category's folder name on the file's matter.
	Folder string
}

// TenderResult says which fields Doc Tender set and which a person had locked.
type TenderResult struct {
	File    FileView
	Set     []string
	Skipped []string
}

// ApplyTender is Doc Tender's write. A name, category or folder a person set is locked
// (files.locked_fields) and is never changed here; such a field is reported in Skipped.
// It never moves a file to another matter. The caller is Doc Tender's system principal
// (or a person through the review queue); access is checked like any write.
func (s *Service) ApplyTender(ctx context.Context, p domain.Principal, id domain.ID, u TenderUpdate) (TenderResult, error) {
	var res TenderResult
	f, err := s.loadFile(ctx, id)
	if err != nil {
		return res, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return res, err
	}
	var folder domain.ID
	if u.Folder != "" && f.MatterID != "" && !isLocked(f, "grouping") {
		fv, err := s.FolderByName(ctx, p, f.MatterID, u.Folder)
		if err != nil {
			return res, err
		}
		folder = fv.ID
	}
	if u.Category != "" {
		ok, err := s.st.categoryExists(ctx, u.Category)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, domain.Invalid("category", "unknown category")
		}
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		cur, err := s.st.getFile(ctx, id)
		if err != nil {
			return err
		}
		next := cur
		type change struct{ field, old, new string }
		var changes []change
		if u.Name != "" && u.Name != cur.Name {
			if isLocked(cur, "name") {
				res.Skipped = append(res.Skipped, "name")
			} else {
				name, err := s.uniqueName(ctx, cur.MatterID, cur.ID, u.Name)
				if err != nil {
					return err
				}
				next.Name, next.Extension = name, extensionOf(name)
				changes = append(changes, change{"name", cur.Name, name})
			}
		}
		if u.Category != "" && u.Category != cur.Category {
			if isLocked(cur, "category") {
				res.Skipped = append(res.Skipped, "category")
			} else {
				next.Category = u.Category
				changes = append(changes, change{"category", cur.Category, u.Category})
			}
		}
		if folder != "" && string(folder) != cur.Folder {
			next.Folder = string(folder)
			changes = append(changes, change{"folder", cur.Folder, string(folder)})
		}
		if len(changes) == 0 {
			res.File = view(cur, nil)
			return nil
		}
		next.UpdatedAt = s.now().UTC()
		if err := s.st.updateFile(ctx, next); err != nil {
			return err
		}
		for _, c := range changes {
			res.Set = append(res.Set, c.field)
			if err := s.record(ctx, p, "file", id, c.field, c.old, c.new); err != nil {
				return err
			}
		}
		res.File = view(next, nil)
		return s.event(ctx, p, next, "file.updated", "Doc Tender filed "+next.Name)
	})
	return res, err
}

// UniqueName returns name, or name with -2, -3 and so on before the extension, so it
// does not clash with another file's name in the matter.
func (s *Service) UniqueName(ctx context.Context, matter domain.ID, name string) (string, error) {
	return s.uniqueName(ctx, matter, "", name)
}

func (s *Service) uniqueName(ctx context.Context, matter, self domain.ID, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	if matter == "" {
		return name, nil
	}
	taken, err := s.st.namesInMatter(ctx, matter)
	if err != nil {
		return "", err
	}
	if self != "" {
		if f, err := s.st.getFile(ctx, self); err == nil {
			delete(taken, strings.ToLower(f.Name))
		}
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	out := name
	// PRD 8.21: a clash in the same matter adds -2, -3 and so on.
	for n := int(zero) + 2; taken[strings.ToLower(out)]; n++ {
		out = fmt.Sprintf("%s-%d%s", base, n, ext)
	}
	return out, nil
}

// OpenForProcessing opens a version's verified bytes for an in-core processor (Doc
// Tender extraction, an editor run). It applies the same access as a write and is not
// logged as a download, because no person received the file.
func (s *Service) OpenForProcessing(ctx context.Context, p domain.Principal, fileID, versionID domain.ID) (*Download, error) {
	f, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return nil, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return nil, err
	}
	body, err := s.open(ctx, versionObject(v))
	if err != nil {
		return nil, err
	}
	return &Download{Name: f.Name, MediaType: v.MediaType, Size: v.SizeBytes, SHA256: v.SHA256, Body: body}, nil
}

// MatterFiles lists a matter's live files for a system job, such as Doc Tender
// queuing a reopened matter's files. Only the system principal may call it.
func (s *Service) MatterFiles(ctx context.Context, p domain.Principal, matter domain.ID) ([]FileView, error) {
	if p.Source != domain.SourceSystem {
		return nil, domain.Forbidden("system jobs only")
	}
	out, _, err := s.List(ctx, p, ListQuery{MatterID: matter})
	return out, err
}
