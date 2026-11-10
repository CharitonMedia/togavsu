package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// NewFile describes a file an in-core producer (upload publish, the editor, Doc
// Tender, the drop folder, a module through the API) adds with its first version.
type NewFile struct {
	Name             string
	MatterID         domain.ID
	ContactID        domain.ID
	FolderID         domain.ID
	AttorneyOnly     bool
	SharedWithClient bool
	MediaType        string
	// Source is upload, client_upload, editor, email, zoom, drop_folder, importer or a module id.
	Source string
	// How is the first version's how: upload, editor, editor_run, conversion, grouping or module.
	How string
	// Origin is firm or received. Empty means received for client, email and drop sources.
	Origin string
	// FinancialClass is general, receipt or invoice_pdf. Accountants read only the last two.
	FinancialClass string
	Note           string
}

// FileView is the File contract shape after access checks.
type FileView struct {
	ID               domain.ID  `json:"id"`
	CreatedAt        string     `json:"created_at"`
	CreatedBy        domain.ID  `json:"created_by"`
	UpdatedAt        string     `json:"updated_at"`
	Name             string     `json:"name"`
	Extension        string     `json:"extension"`
	MatterID         *domain.ID `json:"matter_id"`
	ContactID        *domain.ID `json:"contact_id"`
	FolderID         *domain.ID `json:"folder_id"`
	CategoryID       *string    `json:"category_id"`
	Status           *string    `json:"status"`
	Origin           string     `json:"origin"`
	FinancialClass   *string    `json:"financial_classification"`
	AttorneyOnly     bool       `json:"attorney_only"`
	SharedWithClient bool       `json:"shared_with_client"`
	Source           string     `json:"source"`
	ArrivedAs        string     `json:"arrived_as"`
	LockedFields     []string   `json:"locked_fields"`
	CurrentVersionID *domain.ID `json:"current_version_id"`
	TrashedAt        *string    `json:"trashed_at"`
	TrashedBy        *domain.ID `json:"trashed_by"`
	Warnings         []string   `json:"warnings"`
	// MarkdownName is the Markdown copy's name: the file's base name with .md.
	MarkdownName string `json:"markdown_name,omitempty"`
}

func optID(id domain.ID) *domain.ID {
	if id == "" {
		return nil
	}
	return &id
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func view(f fileRow, warnings []string) FileView {
	if warnings == nil {
		warnings = []string{}
	}
	v := FileView{
		ID: f.ID, CreatedAt: domain.FormatUTC(f.CreatedAt), CreatedBy: f.CreatedBy, UpdatedAt: domain.FormatUTC(f.UpdatedAt),
		Name: f.Name, Extension: f.Extension, MatterID: optID(f.MatterID), ContactID: optID(f.ContactID),
		FolderID: optID(domain.ID(f.Folder)), CategoryID: optString(f.Category), Status: optString(f.Status),
		Origin: f.Origin, AttorneyOnly: f.AttorneyOnly, SharedWithClient: f.SharedWithClient, Source: f.Source,
		ArrivedAs: f.ArrivedName, LockedFields: lockedFields(f), CurrentVersionID: optID(f.CurrentVersionID),
		TrashedBy: optID(f.TrashedBy), Warnings: warnings, MarkdownName: MarkdownName(f.Name),
	}
	if f.FinancialClass != "" && f.FinancialClass != string(domain.FileGeneral) {
		v.FinancialClass = optString(f.FinancialClass)
	}
	if f.TrashedAt != zero {
		at := domain.FormatUTC(domain.FromUnixMicro(f.TrashedAt))
		v.TrashedAt = &at
	}
	return v
}

// MarkdownName is a file's Markdown copy name: the base name with .md. It is computed
// from the current name, so a rename carries the copy's name with it.
func MarkdownName(name string) string {
	ext := path.Ext(name)
	if strings.EqualFold(ext, ".md") {
		return name
	}
	return strings.TrimSuffix(name, ext) + ".md"
}

func extensionOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

func validName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return domain.Invalid("name", "a file needs a name")
	case !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00"):
		return domain.Invalid("name", "a file name cannot contain slashes or control characters")
	case len(name) > maxName:
		return domain.Invalid("name", "the file name is too long")
	}
	return nil
}

// maxName is the common file-system name limit in bytes.
const maxName = 1<<8 - 1

func validHow(how string) bool {
	switch how {
	case "upload", "editor", "editor_run", "conversion", "grouping", "module":
		return true
	}
	return false
}

// mediaTypeOf guesses a media type from the name's extension.
func mediaTypeOf(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// receivedSource is a source whose files come from clients or other parties.
func receivedSource(src string) bool {
	switch src {
	case "client_upload", "email", "zoom":
		return true
	}
	return false
}

// storeBytes streams r to the default backend under key and returns the backend and
// the SHA-256 it hashed on the way. A size or declared hash mismatch removes the bytes.
func (s *Service) storeBytes(ctx context.Context, key domain.ID, r io.Reader, size int64, wantSHA string) (location, string, error) {
	if size < zero {
		return location{}, "", domain.Invalid("size_bytes", "size must be known")
	}
	max, err := s.intSetting(ctx, "files.max_upload_bytes")
	if err != nil {
		return location{}, "", err
	}
	if size > max {
		return location{}, "", domain.Invalid("size_bytes", fmt.Sprintf("the largest file is %d bytes", max))
	}
	h := storage.NewHashingReader(io.LimitReader(r, size))
	loc, err := s.putObject(ctx, string(key), h, size)
	if err != nil {
		return location{}, "", err
	}
	if h.N != size || longerThan(r) || (wantSHA != "" && h.Sum() != wantSHA) {
		s.dropObject(ctx, loc)
		return location{}, "", domain.Conflict("the bytes stored do not match the declared size and sha256")
	}
	return loc, h.Sum(), nil
}

// longerThan reports whether r still has bytes after its declared size was read, so
// a stream longer than declared is refused instead of saved truncated.
func longerThan(r io.Reader) bool {
	var one [1]byte
	n, _ := io.ReadFull(r, one[:])
	return n > int(zero)
}

// dropObject deletes stored bytes; used to undo a put whose record did not commit.
func (s *Service) dropObject(ctx context.Context, loc location) {
	if raw, _, err := s.adapter(ctx, loc.Backend); err == nil {
		_ = raw.Delete(context.WithoutCancel(ctx), loc.Key)
	}
}

// Create adds a file and its first version from r, for in-core producers.
func (s *Service) Create(ctx context.Context, p domain.Principal, nf NewFile, r io.Reader, size int64) (FileView, error) {
	return s.create(ctx, p, nf, r, size, "", nil)
}

func (s *Service) create(ctx context.Context, p domain.Principal, nf NewFile, r io.Reader, size int64, wantSHA string,
	extra func(ctx context.Context, f fileRow, v domain.FileVersion) error) (FileView, error) {
	if err := validName(nf.Name); err != nil {
		return FileView{}, err
	}
	if nf.How == "" {
		nf.How = "upload"
	}
	if !validHow(nf.How) {
		return FileView{}, domain.Invalid("how", "unknown version source")
	}
	if nf.Source == "" {
		nf.Source = sourceFor(p)
	}
	if nf.MediaType == "" {
		nf.MediaType = mediaTypeOf(nf.Name)
	}
	if nf.Origin == "" {
		nf.Origin = "firm"
		if receivedSource(nf.Source) {
			nf.Origin = "received"
		}
	}
	if nf.Origin != "firm" && nf.Origin != "received" {
		return FileView{}, domain.Invalid("origin", "origin is firm or received")
	}
	class := nf.FinancialClass
	if class == "" {
		class = string(domain.FileGeneral)
	}
	switch domain.FileClass(class) {
	case domain.FileGeneral, domain.FileReceipt, domain.FileInvoicePDF:
	default:
		return FileView{}, domain.Invalid("financial_classification", "unknown class")
	}
	if err := s.canWriteTo(ctx, p, nf.MatterID, nf.ContactID, nf.AttorneyOnly); err != nil {
		return FileView{}, err
	}
	if err := s.checkFolder(ctx, nf.FolderID, nf.MatterID); err != nil {
		return FileView{}, err
	}
	by := actor(p, domain.SystemUserID)
	fm, err := s.meta(by)
	if err != nil {
		return FileView{}, err
	}
	vm, err := s.meta(by)
	if err != nil {
		return FileView{}, err
	}
	loc, sha, err := s.storeBytes(ctx, vm.ID, r, size, wantSHA)
	if err != nil {
		return FileView{}, err
	}
	status := ""
	if nf.Origin == "received" {
		status = "received"
	}
	f := fileRow{File: domain.File{
		Meta: fm, Name: nf.Name, Extension: extensionOf(nf.Name), MatterID: nf.MatterID, ContactID: nf.ContactID,
		Folder: string(nf.FolderID), Status: status, Origin: nf.Origin, AttorneyOnly: nf.AttorneyOnly,
		SharedWithClient: nf.SharedWithClient, Source: nf.Source, ArrivedName: nf.Name, LockedFields: "[]",
		CurrentVersionID: vm.ID,
	}, FinancialClass: class}
	v := domain.FileVersion{
		Meta: vm, FileID: fm.ID, Number: int(zero) + 1, StorageBackendID: loc.Backend, StorageKey: string(vm.ID), SizeBytes: size,
		SHA256: sha, MediaType: nf.MediaType, SavedBy: by, SavedAt: us(vm.CreatedAt), How: nf.How, Note: nf.Note,
	}
	var warnings []string
	if nf.MatterID != "" {
		if warnings, err = s.duplicateWarnings(ctx, p, nf.MatterID, sha); err != nil {
			s.dropObject(ctx, loc)
			return FileView{}, err
		}
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.insertFile(ctx, f); err != nil {
			return err
		}
		if err := s.st.appendVersion(ctx, v); err != nil {
			return err
		}
		if err := s.st.setLocation(ctx, v.ID, "version", loc, us(vm.CreatedAt)); err != nil {
			return err
		}
		if err := s.record(ctx, p, "file", f.ID, "name", "", f.Name); err != nil {
			return err
		}
		if err := s.record(ctx, p, "file", f.ID, "version", "", versionNote(v)); err != nil {
			return err
		}
		if err := s.event(ctx, p, f, "file.version_created", "Added "+f.Name); err != nil {
			return err
		}
		if extra != nil {
			return extra(ctx, f, v)
		}
		return nil
	})
	if err != nil {
		s.dropObject(ctx, loc)
		return FileView{}, err
	}
	s.versionCreated(ctx, f, v)
	return view(f, warnings), nil
}

func versionNote(v domain.FileVersion) string {
	return fmt.Sprintf("v%d %s %s %d bytes", v.Number, v.How, v.SHA256, v.SizeBytes)
}

func (s *Service) versionCreated(ctx context.Context, f fileRow, v domain.FileVersion) {
	if s.onVersion != nil {
		s.onVersion(context.WithoutCancel(ctx), VersionCreated{FileID: f.ID, VersionID: v.ID, MatterID: f.MatterID, Number: v.Number, How: v.How, Source: f.Source})
	}
}

// SaveOptions describe a new version.
type SaveOptions struct {
	How       string
	MediaType string
	Note      string
	// Extension, when set, changes the file's extension (the editor saves other
	// formats as DOCX).
	Extension string
}

// SaveVersion adds the next version of a file from r. Nothing is overwritten.
func (s *Service) SaveVersion(ctx context.Context, p domain.Principal, fileID domain.ID, r io.Reader, size int64, opt SaveOptions) (VersionView, error) {
	return s.saveVersion(ctx, p, fileID, r, size, "", opt, nil)
}

func (s *Service) saveVersion(ctx context.Context, p domain.Principal, fileID domain.ID, r io.Reader, size int64, wantSHA string,
	opt SaveOptions, extra func(ctx context.Context, v domain.FileVersion) error) (VersionView, error) {
	if opt.How == "" {
		opt.How = "upload"
	}
	if !validHow(opt.How) {
		return VersionView{}, domain.Invalid("how", "unknown version source")
	}
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return VersionView{}, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return VersionView{}, err
	}
	if f.TrashedAt != zero {
		return VersionView{}, domain.Conflict("restore the file from the trash before saving to it")
	}
	by := actor(p, f.CreatedBy)
	vm, err := s.meta(by)
	if err != nil {
		return VersionView{}, err
	}
	loc, sha, err := s.storeBytes(ctx, vm.ID, r, size, wantSHA)
	if err != nil {
		return VersionView{}, err
	}
	var v domain.FileVersion
	err = s.tx(ctx, func(ctx context.Context) error {
		f, err := s.st.lockFile(ctx, fileID)
		if err != nil {
			return err
		}
		if f.TrashedAt != zero || f.PurgedAt != zero {
			return domain.Conflict("restore the file from the trash before saving to it")
		}
		prior, err := s.st.listVersions(ctx, fileID)
		if err != nil {
			return err
		}
		media := opt.MediaType
		if media == "" && len(prior) > 0 {
			media = prior[len(prior)-1].MediaType
		}
		v = domain.FileVersion{
			Meta: vm, FileID: fileID, Number: len(prior) + 1, StorageBackendID: loc.Backend, StorageKey: string(vm.ID),
			SizeBytes: size, SHA256: sha, MediaType: media, SavedBy: by, SavedAt: us(vm.CreatedAt), How: opt.How, Note: opt.Note,
		}
		if err := s.st.appendVersion(ctx, v); err != nil {
			return err
		}
		if err := s.st.setLocation(ctx, v.ID, "version", loc, us(vm.CreatedAt)); err != nil {
			return err
		}
		old := f.Name
		f.CurrentVersionID, f.UpdatedAt = v.ID, vm.CreatedAt
		if ext := strings.ToLower(strings.TrimPrefix(opt.Extension, ".")); ext != "" && ext != f.Extension {
			f.Name = strings.TrimSuffix(f.Name, path.Ext(f.Name)) + "." + ext
			f.Extension = ext
		}
		if err := s.st.updateFile(ctx, f); err != nil {
			return err
		}
		if old != f.Name {
			if err := s.record(ctx, p, "file", f.ID, "name", old, f.Name); err != nil {
				return err
			}
		}
		if err := s.record(ctx, p, "file", f.ID, "version", "", versionNote(v)); err != nil {
			return err
		}
		if err := s.event(ctx, p, f, "file.version_created", fmt.Sprintf("Saved version %d of %s", v.Number, f.Name)); err != nil {
			return err
		}
		if extra != nil {
			return extra(ctx, v)
		}
		return nil
	})
	if err != nil {
		s.dropObject(ctx, loc)
		return VersionView{}, err
	}
	f, err = s.st.getFile(ctx, fileID)
	if err == nil {
		s.versionCreated(ctx, f, v)
	}
	return s.versionView(ctx, v)
}

// Get reads one file the caller may see.
func (s *Service) Get(ctx context.Context, p domain.Principal, id domain.ID) (FileView, error) {
	f, err := s.loadFile(ctx, id)
	if err != nil {
		return FileView{}, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return FileView{}, err
	}
	return view(f, nil), nil
}

// ListQuery filters a file list. Text matches the name and the newest Markdown copy.
type ListQuery struct {
	MatterID  domain.ID
	ContactID domain.ID
	FolderID  *domain.ID
	Trashed   bool
	Text      string
	Cursor    domain.ID
	Limit     int
}

// List returns the files the caller may see, in id order, with the next cursor.
// Every row passes the same wall, role and attorney-only check as a direct read.
func (s *Service) List(ctx context.Context, p domain.Principal, q ListQuery) ([]FileView, domain.ID, error) {
	if q.MatterID != "" {
		if err := s.matterVisible(ctx, p, q.MatterID); err != nil {
			return nil, "", err
		}
	}
	fq := fileQuery{MatterID: q.MatterID, ContactID: q.ContactID, Trashed: q.Trashed, Text: q.Text, After: q.Cursor}
	if q.FolderID != nil {
		folder := string(*q.FolderID)
		fq.Folder = &folder
	}
	walls := map[domain.ID]domain.WallView{}
	var out []FileView
	for {
		if q.Limit > int(zero) {
			fq.Limit = q.Limit + 1
		}
		rows, err := s.st.listFiles(ctx, fq)
		if err != nil {
			return nil, "", err
		}
		for _, f := range rows {
			fq.After = f.ID
			d, err := s.decide(ctx, p, f, domain.ActionRead, walls)
			if err != nil {
				return nil, "", err
			}
			if !d.Allow {
				continue
			}
			if q.Limit > int(zero) && len(out) == q.Limit {
				return out, out[len(out)-1].ID, nil
			}
			out = append(out, view(f, nil))
		}
		if q.Limit <= int(zero) || len(rows) < fq.Limit {
			return out, "", nil
		}
	}
}

// Search is Files search: the name and the Markdown copy, inside the caller's access.
func (s *Service) Search(ctx context.Context, p domain.Principal, text string, limit int) ([]FileView, error) {
	if strings.TrimSpace(text) == "" {
		return []FileView{}, nil
	}
	out, _, err := s.List(ctx, p, ListQuery{Text: text, Limit: limit})
	return out, err
}

// matterVisible hides a walled matter from everyone outside its wall.
func (s *Service) matterVisible(ctx context.Context, p domain.Principal, matter domain.ID) error {
	view, err := s.policy.Walls.View(ctx, p, matter)
	if err != nil {
		return err
	}
	if view.Walled && !view.Allows && p.Source != domain.SourceSystem {
		return domain.NotFound("matter")
	}
	return nil
}

// matterReadable is the full matter read check for what describes a matter itself:
// its folders and its Files tab (matter number and status). On top of the wall it
// applies the role and client rules practice uses for the matter record: a client
// reads only a matter they are a party to, and an accountant, who cannot read
// matters, sees none. Every refusal is not found, so a matter's existence leaks to no
// one who couldn't open it. (List keeps matterVisible: an accountant may still list a
// matter's receipts, and each row there passes the file decision anyway.)
func (s *Service) matterReadable(ctx context.Context, p domain.Principal, matter domain.ID) error {
	if _, err := domain.ParseID(string(matter)); err != nil {
		return domain.NotFound("matter")
	}
	view, err := s.policy.Walls.View(ctx, p, matter)
	if err != nil {
		return err
	}
	d := domain.Decide(p, domain.AccessInput{
		Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: matter,
		Walled: view.Walled, WallAllows: view.Allows, ClientMatter: view.ClientMatter, SharedWithClient: view.ClientMatter,
	})
	if !d.Allow {
		return domain.NotFound("matter")
	}
	return nil
}

func (s *Service) checkFolder(ctx context.Context, folder, matter domain.ID) error {
	if folder == "" {
		return nil
	}
	f, err := s.st.getFolder(ctx, folder)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && f.MatterID != matter) {
		return domain.Invalid("folder_id", "the folder is not one of this file's matter folders")
	}
	return err
}

// change applies one field edit with its audit row and timeline event. The row is
// read FOR UPDATE, so the whole-row write never puts back a version, name, folder or
// lock that a concurrent save or edit committed first.
func (s *Service) change(ctx context.Context, p domain.Principal, id domain.ID, field string, apply func(f *fileRow) (old, new string, err error)) (FileView, error) {
	var out fileRow
	err := s.tx(ctx, func(ctx context.Context) error {
		f, err := s.lockedFile(ctx, id)
		if err != nil {
			return err
		}
		if err := s.writable(ctx, p, f); err != nil {
			return err
		}
		old, nv, err := apply(&f)
		if err != nil {
			return err
		}
		if old == nv {
			out = f
			return nil
		}
		f.UpdatedAt = s.now().UTC()
		if err := s.st.updateFile(ctx, f); err != nil {
			return err
		}
		if err := s.record(ctx, p, "file", f.ID, field, old, nv); err != nil {
			return err
		}
		if err := s.event(ctx, p, f, "file.updated", fmt.Sprintf("Changed %s of %s", field, f.Name)); err != nil {
			return err
		}
		out = f
		return nil
	})
	if err != nil {
		return FileView{}, err
	}
	return view(out, nil), nil
}

// Rename sets the file's one name. The extension stays; a person's rename locks the
// name so Doc Tender never changes it.
func (s *Service) Rename(ctx context.Context, p domain.Principal, id domain.ID, name string) (FileView, error) {
	name = strings.TrimSpace(name)
	return s.change(ctx, p, id, "name", func(f *fileRow) (string, string, error) {
		next := name
		if f.Extension != "" {
			next = strings.TrimSuffix(name, path.Ext(name))
			if !strings.EqualFold(path.Ext(name), "."+f.Extension) {
				next = name
			}
			next += "." + f.Extension
		}
		if err := validName(next); err != nil {
			return "", "", err
		}
		old := f.Name
		f.Name = next
		lock(f, "name")
		return old, next, nil
	})
}

// MoveToFolder moves a file between its matter's folders. Bytes never move. A
// person's move locks the folder so Doc Tender never refiles it.
func (s *Service) MoveToFolder(ctx context.Context, p domain.Principal, id domain.ID, folder domain.ID) (FileView, error) {
	return s.change(ctx, p, id, "folder", func(f *fileRow) (string, string, error) {
		if err := s.checkFolder(ctx, folder, f.MatterID); err != nil {
			return "", "", err
		}
		old := f.Folder
		f.Folder = string(folder)
		lock(f, "folder")
		return old, f.Folder, nil
	})
}

// MarkStatus is Mark draft, final, executed or filed. A model never calls it.
func (s *Service) MarkStatus(ctx context.Context, p domain.Principal, id domain.ID, status string) (FileView, error) {
	switch status {
	case "draft", "final", "executed", "filed", "received":
	default:
		return FileView{}, domain.Invalid("status", "status is draft, final, executed, filed or received")
	}
	return s.change(ctx, p, id, "status", func(f *fileRow) (string, string, error) {
		old := f.Status
		f.Status = status
		return old, status, nil
	})
}

// Metadata is updateFileMetadata. Nil fields stay as they are.
type Metadata struct {
	AttorneyOnly     *bool      `json:"attorney_only"`
	SharedWithClient *bool      `json:"shared_with_client"`
	CategoryID       *string    `json:"category_id"`
	MatterID         *domain.ID `json:"matter_id"`
	ContactID        *domain.ID `json:"contact_id"`
}

// UpdateMetadata changes flags, category and placement. A person's category and
// matter lock those fields. Making a file attorney-only needs attorney access.
func (s *Service) UpdateMetadata(ctx context.Context, p domain.Principal, id domain.ID, m Metadata) (FileView, error) {
	var out FileView
	err := s.tx(ctx, func(ctx context.Context) error {
		f, err := s.lockedFile(ctx, id)
		if err != nil {
			return err
		}
		if err := s.writable(ctx, p, f); err != nil {
			return err
		}
		next := f
		if m.AttorneyOnly != nil {
			next.AttorneyOnly = *m.AttorneyOnly
		}
		if m.SharedWithClient != nil {
			next.SharedWithClient = *m.SharedWithClient
		}
		if m.CategoryID != nil && *m.CategoryID != f.Category {
			if *m.CategoryID != "" {
				ok, err := s.st.categoryExists(ctx, *m.CategoryID)
				if err != nil {
					return err
				}
				if !ok {
					return domain.Invalid("category_id", "unknown category")
				}
			}
			next.Category = *m.CategoryID
			lock(&next, "category")
		}
		if m.MatterID != nil && *m.MatterID != f.MatterID {
			next.MatterID = *m.MatterID
			next.Folder = ""
			lock(&next, "matter")
		}
		if m.ContactID != nil {
			next.ContactID = *m.ContactID
		}
		if next.MatterID != f.MatterID || next.ContactID != f.ContactID || next.AttorneyOnly != f.AttorneyOnly {
			if err := s.canWriteTo(ctx, p, next.MatterID, next.ContactID, next.AttorneyOnly); err != nil {
				return err
			}
		}
		next.UpdatedAt = s.now().UTC()
		if err := s.st.updateFile(ctx, next); err != nil {
			return err
		}
		for _, c := range []struct{ field, old, new string }{
			{"attorney_only", fmt.Sprint(f.AttorneyOnly), fmt.Sprint(next.AttorneyOnly)},
			{"shared_with_client", fmt.Sprint(f.SharedWithClient), fmt.Sprint(next.SharedWithClient)},
			{"category", f.Category, next.Category},
			{"matter_id", string(f.MatterID), string(next.MatterID)},
			{"contact_id", string(f.ContactID), string(next.ContactID)},
		} {
			if c.old == c.new {
				continue
			}
			if err := s.record(ctx, p, "file", f.ID, c.field, c.old, c.new); err != nil {
				return err
			}
		}
		if err := s.event(ctx, p, next, "file.updated", "Changed "+next.Name); err != nil {
			return err
		}
		out = view(next, nil)
		return nil
	})
	return out, err
}

// Trash moves a file to the trash, where it stays listed and restorable.
func (s *Service) Trash(ctx context.Context, p domain.Principal, id domain.ID) (FileView, error) {
	return s.change(ctx, p, id, "trashed_at", func(f *fileRow) (string, string, error) {
		if f.TrashedAt != zero {
			return "x", "x", nil
		}
		f.TrashedAt, f.TrashedBy = us(s.now()), actor(p, domain.SystemUserID)
		return "", domain.FormatUTC(domain.FromUnixMicro(f.TrashedAt)), nil
	})
}

// Restore takes a file out of the trash.
func (s *Service) Restore(ctx context.Context, p domain.Principal, id domain.ID) (FileView, error) {
	return s.change(ctx, p, id, "trashed_at", func(f *fileRow) (string, string, error) {
		if f.TrashedAt == zero {
			return "", "", nil
		}
		old := domain.FormatUTC(domain.FromUnixMicro(f.TrashedAt))
		f.TrashedAt, f.TrashedBy = zero, ""
		return old, "", nil
	})
}

// Purge empties a trashed file: an admin types its name, every stored copy of its
// versions and renditions is deleted with its search text, and the record stays for
// the audit log.
func (s *Service) Purge(ctx context.Context, p domain.Principal, id domain.ID, confirmName string) error {
	if err := admin(p); err != nil {
		return err
	}
	f, err := s.loadFile(ctx, id)
	if err != nil {
		return err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return err
	}
	if f.TrashedAt == zero {
		return domain.Conflict("only a file in the trash can be emptied")
	}
	if confirmName != f.Name {
		return domain.Invalid("confirm_name", "type the file's name to empty it from the trash")
	}
	var objs []object
	err = s.tx(ctx, func(ctx context.Context) error {
		// Recheck under the row lock: a concurrent Restore or rename must not be purged
		// over, and the object list is read after any save in flight has committed.
		f, err := s.lockedFile(ctx, id)
		if err != nil {
			return err
		}
		if f.TrashedAt == zero || f.PurgedAt != zero {
			return domain.Conflict("only a file in the trash can be emptied")
		}
		if confirmName != f.Name {
			return domain.Invalid("confirm_name", "type the file's name to empty it from the trash")
		}
		if objs, err = s.objectsOf(ctx, f.ID); err != nil {
			return err
		}
		// The search copy of the Markdown text goes with the file's bytes.
		if err := s.st.deleteSearchText(ctx, f.ID); err != nil {
			return err
		}
		f.PurgedAt, f.PurgedBy, f.UpdatedAt = us(s.now()), p.UserID, s.now().UTC()
		if err := s.st.updateFile(ctx, f); err != nil {
			return err
		}
		if err := s.record(ctx, p, "file", f.ID, "purged_at", "", domain.FormatUTC(domain.FromUnixMicro(f.PurgedAt))); err != nil {
			return err
		}
		return s.event(ctx, p, f, "file.purged", "Emptied "+f.Name+" from the trash")
	})
	if err != nil {
		return err
	}
	for _, o := range objs {
		loc, err := s.st.location(ctx, o.ID, o.Home)
		if err == nil {
			s.dropObject(ctx, loc)
		}
	}
	return nil
}

// objectsOf lists a file's stored objects: every version and rendition.
func (s *Service) objectsOf(ctx context.Context, file domain.ID) ([]object, error) {
	vs, err := s.st.listVersions(ctx, file)
	if err != nil {
		return nil, err
	}
	var out []object
	for _, v := range vs {
		out = append(out, versionObject(v))
	}
	rs, err := s.st.renditionsOf(ctx, file, "")
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		out = append(out, renditionObject(r, file))
	}
	return out, nil
}

// HistoryEntry is the FileHistoryEntry contract shape.
type HistoryEntry struct {
	At        string     `json:"at"`
	ActorID   *domain.ID `json:"actor_id"`
	ActorKind string     `json:"actor_kind"`
	Kind      string     `json:"kind"`
	Field     string     `json:"field"`
	OldValue  string     `json:"old_value"`
	NewValue  string     `json:"new_value"`
}

// History is the file's history card: field edits and version saves together, from
// the audit log, oldest first.
func (s *Service) History(ctx context.Context, p domain.Principal, id domain.ID) ([]HistoryEntry, error) {
	f, err := s.loadFile(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return nil, err
	}
	rows, err := s.audit.ForEntity(ctx, "file", id)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(rows))
	for _, a := range rows {
		kind := "field_edit"
		if a.Field == "version" {
			kind = "version_save"
		}
		actorKind := map[string]string{"api_token": "agent", "module": "module", "system": "system"}[a.Source]
		if actorKind == "" {
			actorKind = "staff"
			if a.Role == string(domain.RoleClient) {
				actorKind = "client"
			}
		}
		out = append(out, HistoryEntry{At: domain.FormatUTC(a.CreatedAt), ActorID: optID(a.ActorID), ActorKind: actorKind,
			Kind: kind, Field: a.Field, OldValue: a.OldValue, NewValue: a.NewValue})
	}
	return out, nil
}
