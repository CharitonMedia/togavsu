package files

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// VersionView is the FileVersion contract shape.
type VersionView struct {
	ID               domain.ID `json:"id"`
	CreatedAt        string    `json:"created_at"`
	CreatedBy        domain.ID `json:"created_by"`
	FileID           domain.ID `json:"file_id"`
	Number           int       `json:"number"`
	StorageBackendID domain.ID `json:"storage_backend_id"`
	SizeBytes        int64     `json:"size_bytes"`
	SHA256           string    `json:"sha256"`
	MediaType        string    `json:"media_type"`
	SavedBy          domain.ID `json:"saved_by"`
	How              string    `json:"how"`
	Note             string    `json:"note"`
	Quarantined      bool      `json:"quarantined"`
}

func (s *Service) versionView(ctx context.Context, v domain.FileVersion) (VersionView, error) {
	q, err := s.st.quarantined(ctx, v)
	if err != nil {
		return VersionView{}, err
	}
	loc, err := s.st.location(ctx, v.ID, location{Backend: v.StorageBackendID, Key: v.StorageKey})
	if err != nil {
		return VersionView{}, err
	}
	return VersionView{
		ID: v.ID, CreatedAt: domain.FormatUTC(v.CreatedAt), CreatedBy: v.CreatedBy, FileID: v.FileID, Number: v.Number,
		StorageBackendID: loc.Backend, SizeBytes: v.SizeBytes, SHA256: v.SHA256, MediaType: v.MediaType,
		SavedBy: v.SavedBy, How: v.How, Note: v.Note, Quarantined: q,
	}, nil
}

// Versions lists every version of a visible file, oldest first.
func (s *Service) Versions(ctx context.Context, p domain.Principal, fileID domain.ID) ([]VersionView, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return nil, err
	}
	vs, err := s.st.listVersions(ctx, fileID)
	if err != nil {
		return nil, err
	}
	out := make([]VersionView, 0, len(vs))
	for _, v := range vs {
		vv, err := s.versionView(ctx, v)
		if err != nil {
			return nil, err
		}
		out = append(out, vv)
	}
	return out, nil
}

// fileVersion loads a version that belongs to a file the caller may read.
func (s *Service) fileVersion(ctx context.Context, p domain.Principal, fileID, versionID domain.ID) (fileRow, domain.FileVersion, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return fileRow{}, domain.FileVersion{}, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return fileRow{}, domain.FileVersion{}, err
	}
	if _, err := domain.ParseID(string(versionID)); err != nil {
		return fileRow{}, domain.FileVersion{}, domain.NotFound("version")
	}
	v, err := s.st.getVersion(ctx, versionID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && v.FileID != f.ID) {
		return fileRow{}, domain.FileVersion{}, domain.NotFound("version")
	}
	return f, v, err
}

// Version reads one version.
func (s *Service) Version(ctx context.Context, p domain.Principal, fileID, versionID domain.ID) (VersionView, error) {
	_, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return VersionView{}, err
	}
	return s.versionView(ctx, v)
}

// ErrQuarantined means a quarantined version needs the user's confirmation.
var ErrQuarantined = &domain.APIError{Code: "quarantine_confirmation_required", Message: "a scanner quarantined this version; confirm to download it"}

// Download is an open, verified read of one version or rendition.
type Download struct {
	Name      string
	MediaType string
	Size      int64
	SHA256    string
	Body      *storage.Verified
}

// Close releases the backend reader.
func (d *Download) Close() error { return d.Body.Close() }

// OpenVersion opens a version for download. It writes the download to the audit log
// and the case timeline; a client's download is marked client_visible for highlight.
// A quarantined version opens only with confirm.
func (s *Service) OpenVersion(ctx context.Context, p domain.Principal, fileID, versionID domain.ID, confirm bool) (*Download, error) {
	f, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return nil, err
	}
	q, err := s.st.quarantined(ctx, v)
	if err != nil {
		return nil, err
	}
	if q && !confirm {
		return nil, ErrQuarantined
	}
	body, err := s.open(ctx, versionObject(v))
	if err != nil {
		return nil, err
	}
	if err := s.logDownload(ctx, p, f, fmt.Sprintf("v%d", v.Number)); err != nil {
		_ = body.Close()
		return nil, err
	}
	name := f.Name
	if v.ID != f.CurrentVersionID {
		name = strings.TrimSuffix(f.Name, path.Ext(f.Name)) + fmt.Sprintf(" (v%d)", v.Number) + path.Ext(f.Name)
	}
	return &Download{Name: name, MediaType: v.MediaType, Size: v.SizeBytes, SHA256: v.SHA256, Body: body}, nil
}

func (s *Service) logDownload(ctx context.Context, p domain.Principal, f fileRow, what string) error {
	return s.tx(ctx, func(ctx context.Context) error {
		if err := s.record(ctx, p, "file", f.ID, "download", "", what); err != nil {
			return err
		}
		summary := "Downloaded " + f.Name
		if clientOnly(p) {
			summary = "Client downloaded " + f.Name
		}
		return s.event(ctx, p, f, "file.downloaded", summary)
	})
}

// RestoreVersion copies an older version forward as a new version. The old one stays.
func (s *Service) RestoreVersion(ctx context.Context, p domain.Principal, fileID, versionID domain.ID) (VersionView, error) {
	f, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return VersionView{}, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return VersionView{}, err
	}
	body, err := s.open(ctx, versionObject(v))
	if err != nil {
		return VersionView{}, err
	}
	defer body.Close()
	return s.saveVersion(ctx, p, fileID, body, v.SizeBytes, v.SHA256, SaveOptions{
		How: v.How, MediaType: v.MediaType, Note: fmt.Sprintf("restored from version %d", v.Number),
	}, nil)
}

// Quarantine records a scanner's mark on a version. The version row never changes,
// and the file stays listed and downloadable after confirmation.
func (s *Service) Quarantine(ctx context.Context, p domain.Principal, fileID, versionID domain.ID, on bool) (VersionView, error) {
	f, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return VersionView{}, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return VersionView{}, err
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		m, err := s.meta(actor(p, f.CreatedBy))
		if err != nil {
			return err
		}
		source := p.ModuleID
		if source == "" {
			source = string(p.Source)
		}
		if err := s.st.appendQuarantine(ctx, m, v.ID, on, source); err != nil {
			return err
		}
		return s.record(ctx, p, "file_version", v.ID, "quarantined", "", fmt.Sprint(on))
	})
	if err != nil {
		return VersionView{}, err
	}
	return s.versionView(ctx, v)
}

// RenditionView is the FileRendition contract shape plus its display name.
type RenditionView struct {
	ID            domain.ID `json:"id"`
	CreatedAt     string    `json:"created_at"`
	CreatedBy     domain.ID `json:"created_by"`
	FileVersionID domain.ID `json:"file_version_id"`
	Kind          string    `json:"kind"`
	Number        int       `json:"number"`
	SizeBytes     int64     `json:"size_bytes"`
	SHA256        string    `json:"sha256"`
	MadeBy        domain.ID `json:"made_by"`
	Name          string    `json:"name"`
}

func renditionObject(r domain.FileRendition, file domain.ID) object {
	return object{ID: r.ID, Kind: "rendition", FileID: file, Size: r.SizeBytes, SHA: r.SHA256,
		Home: location{Backend: r.StorageBackendID, Key: r.StorageKey}}
}

func renditionName(f fileRow, r domain.FileRendition) string {
	switch r.Kind {
	case "markdown":
		return MarkdownName(f.Name)
	case "pdf_preview":
		return strings.TrimSuffix(f.Name, path.Ext(f.Name)) + ".preview.pdf"
	default:
		return strings.TrimSuffix(f.Name, path.Ext(f.Name)) + ".pdf"
	}
}

func renditionView(f fileRow, r domain.FileRendition) RenditionView {
	return RenditionView{
		ID: r.ID, CreatedAt: domain.FormatUTC(r.CreatedAt), CreatedBy: r.CreatedBy, FileVersionID: r.FileVersionID,
		Kind: r.Kind, Number: r.Number, SizeBytes: r.SizeBytes, SHA256: r.SHA256, MadeBy: r.MadeBy, Name: renditionName(f, r),
	}
}

// AddRendition stores a Markdown copy, PDF preview or grouped PDF of a version on the
// file's own backend. Each one is a new row with the next number for its kind, so a
// reprocess adds a Markdown version and keeps the old one. The newest Markdown copy is
// what Files search matches.
func (s *Service) AddRendition(ctx context.Context, p domain.Principal, fileID, versionID domain.ID, kind string, r io.Reader, size int64) (RenditionView, error) {
	switch kind {
	case "markdown", "pdf_preview", "grouped_pdf":
	default:
		return RenditionView{}, domain.Invalid("kind", "kind is markdown, pdf_preview or grouped_pdf")
	}
	f, v, err := s.fileVersion(ctx, p, fileID, versionID)
	if err != nil {
		return RenditionView{}, err
	}
	if err := s.writable(ctx, p, f); err != nil {
		return RenditionView{}, err
	}
	var text []byte
	if kind == "markdown" {
		if text, err = io.ReadAll(io.LimitReader(r, size+1)); err != nil {
			return RenditionView{}, err
		}
		if int64(len(text)) != size {
			return RenditionView{}, domain.Invalid("size_bytes", "the rendition is not the size it declares")
		}
		r = bytes.NewReader(text)
	}
	by := actor(p, v.SavedBy)
	m, err := s.meta(by)
	if err != nil {
		return RenditionView{}, err
	}
	// The copy goes on the backend the original's bytes are on now.
	home, err := s.st.location(ctx, v.ID, location{Backend: v.StorageBackendID, Key: v.StorageKey})
	if err != nil {
		return RenditionView{}, err
	}
	h := storage.NewHashingReader(io.LimitReader(r, size))
	loc, err := s.putOn(ctx, home.Backend, string(m.ID), h, size)
	if err != nil {
		return RenditionView{}, err
	}
	if h.N != size || longerThan(r) {
		s.dropObject(ctx, loc)
		return RenditionView{}, domain.Invalid("size_bytes", "the rendition is not the size it declares")
	}
	var row domain.FileRendition
	err = s.tx(ctx, func(ctx context.Context) error {
		n, err := s.st.nextRenditionNumber(ctx, f.ID, kind)
		if err != nil {
			return err
		}
		row = domain.FileRendition{
			Meta: m, FileVersionID: v.ID, Kind: kind, Number: n, StorageBackendID: loc.Backend, StorageKey: string(m.ID),
			SizeBytes: size, SHA256: h.Sum(), MadeBy: by, MadeAt: us(m.CreatedAt),
		}
		if err := s.st.appendRendition(ctx, row); err != nil {
			return err
		}
		if err := s.st.setLocation(ctx, row.ID, "rendition", loc, us(m.CreatedAt)); err != nil {
			return err
		}
		if kind == "markdown" {
			if err := s.st.setSearchText(ctx, f.ID, row.ID, string(text)); err != nil {
				return err
			}
		}
		return s.record(ctx, p, "file", f.ID, "rendition", "", fmt.Sprintf("%s %d of v%d %s", kind, n, v.Number, row.SHA256))
	})
	if err != nil {
		s.dropObject(ctx, loc)
		return RenditionView{}, err
	}
	return renditionView(f, row), nil
}

// Renditions lists a visible file's renditions, oldest first.
// CORE-30: a module service principal sees only the markdown kind.
func (s *Service) Renditions(ctx context.Context, p domain.Principal, fileID domain.ID) ([]RenditionView, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return nil, err
	}
	rs, err := s.st.renditionsOf(ctx, f.ID, "")
	if err != nil {
		return nil, err
	}
	out := make([]RenditionView, 0, len(rs))
	for _, r := range rs {
		if moduleOnly(p) && r.Kind != "markdown" {
			continue
		}
		out = append(out, renditionView(f, r))
	}
	return out, nil
}

// OpenRendition opens a rendition under the same access as its file. The quarantine
// rule comes from the version it was made from.
// CORE-30: a module service principal may download the markdown kind only.
func (s *Service) OpenRendition(ctx context.Context, p domain.Principal, fileID, renditionID domain.ID, confirm bool) (*Download, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return nil, err
	}
	if _, err := domain.ParseID(string(renditionID)); err != nil {
		return nil, domain.NotFound("rendition")
	}
	r, err := s.st.getRendition(ctx, renditionID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.NotFound("rendition")
	}
	if err != nil {
		return nil, err
	}
	v, err := s.st.getVersion(ctx, r.FileVersionID)
	if err != nil || v.FileID != f.ID {
		return nil, domain.NotFound("rendition")
	}
	if moduleOnly(p) && r.Kind != "markdown" {
		return nil, &domain.APIError{Code: "scope_denied", Message: "downloadFileRendition for a service call allows only the markdown kind"}
	}
	q, err := s.st.quarantined(ctx, v)
	if err != nil {
		return nil, err
	}
	if q && !confirm {
		return nil, ErrQuarantined
	}
	body, err := s.open(ctx, renditionObject(r, f.ID))
	if err != nil {
		return nil, err
	}
	if err := s.logDownload(ctx, p, f, r.Kind+fmt.Sprintf(" %d", r.Number)); err != nil {
		_ = body.Close()
		return nil, err
	}
	media := "application/pdf"
	if r.Kind == "markdown" {
		media = "text/markdown; charset=utf-8"
	}
	return &Download{Name: renditionName(f, r), MediaType: media, Size: r.SizeBytes, SHA256: r.SHA256, Body: body}, nil
}

// MarkdownCopy returns the newest Markdown copy's text, for search, AI features and
// modules reading through the API. It follows the file's walls like any read.
func (s *Service) MarkdownCopy(ctx context.Context, p domain.Principal, fileID domain.ID) (RenditionView, []byte, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return RenditionView{}, nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return RenditionView{}, nil, err
	}
	rs, err := s.st.renditionsOf(ctx, f.ID, "")
	if err != nil {
		return RenditionView{}, nil, err
	}
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Kind != "markdown" {
			continue
		}
		body, err := s.readAll(ctx, renditionObject(rs[i], f.ID))
		if err != nil {
			return RenditionView{}, nil, err
		}
		return renditionView(f, rs[i]), body, nil
	}
	return RenditionView{}, nil, domain.NotFound("markdown copy")
}

// Link kinds in the API and in file_links.
var linkKinds = map[string]string{"conversion_of": "conversion_of", "grouped_photo_source": "photos_of", "attachment": "email_attachment"}

func apiLinkKind(stored string) string {
	for api, db := range linkKinds {
		if db == stored {
			return api
		}
	}
	return stored
}

// LinkView is the FileLink contract shape.
type LinkView struct {
	ID           domain.ID `json:"id"`
	CreatedAt    string    `json:"created_at"`
	CreatedBy    domain.ID `json:"created_by"`
	FromFileID   domain.ID `json:"from_file_id"`
	ToFileID     domain.ID `json:"to_file_id"`
	Relationship string    `json:"relationship"`
}

// Link records a typed relationship between two files. The caller must be able to
// change the first and read the second, and both must be on the same matter.
func (s *Service) Link(ctx context.Context, p domain.Principal, from, to domain.ID, relationship string) (LinkView, error) {
	kind, ok := linkKinds[relationship]
	if !ok {
		return LinkView{}, domain.Invalid("relationship", "relationship is conversion_of, grouped_photo_source or attachment")
	}
	if from == to {
		return LinkView{}, domain.Invalid("to_file_id", "a file cannot link to itself")
	}
	a, err := s.loadFile(ctx, from)
	if err != nil {
		return LinkView{}, err
	}
	if err := s.writable(ctx, p, a); err != nil {
		return LinkView{}, err
	}
	b, err := s.loadFile(ctx, to)
	if err != nil {
		return LinkView{}, err
	}
	if err := s.visible(ctx, p, b); err != nil {
		return LinkView{}, err
	}
	if a.MatterID != b.MatterID {
		return LinkView{}, domain.Invalid("to_file_id", "linked files must share a matter")
	}
	m, err := s.meta(actor(p, a.CreatedBy))
	if err != nil {
		return LinkView{}, err
	}
	l := domain.FileLink{Meta: m, FromFileID: from, ToFileID: to, Kind: kind}
	err = s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.insertLink(ctx, l); err != nil {
			return err
		}
		return s.record(ctx, p, "file", from, "link", "", relationship+" "+string(to))
	})
	if err != nil {
		return LinkView{}, err
	}
	return LinkView{ID: l.ID, CreatedAt: domain.FormatUTC(l.CreatedAt), CreatedBy: l.CreatedBy, FromFileID: from, ToFileID: to, Relationship: relationship}, nil
}

// Links lists a file's links whose other end the caller may also see.
func (s *Service) Links(ctx context.Context, p domain.Principal, fileID domain.ID) ([]LinkView, error) {
	f, err := s.loadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, p, f); err != nil {
		return nil, err
	}
	ls, err := s.st.links(ctx, fileID)
	if err != nil {
		return nil, err
	}
	out := []LinkView{}
	for _, l := range ls {
		other := l.ToFileID
		if other == fileID {
			other = l.FromFileID
		}
		o, err := s.st.getFile(ctx, other)
		if err != nil || s.visible(ctx, p, o) != nil {
			continue
		}
		out = append(out, LinkView{ID: l.ID, CreatedAt: domain.FormatUTC(l.CreatedAt), CreatedBy: l.CreatedBy, FromFileID: l.FromFileID, ToFileID: l.ToFileID, Relationship: apiLinkKind(l.Kind)})
	}
	return out, nil
}

// ArchiveRequest selects files for createFileArchive: exactly one of FileIDs, FolderID
// or Entries (CORE-21).
type ArchiveRequest struct {
	FileIDs           []domain.ID
	FolderID          domain.ID
	Entries           []ArchiveEntry
	ConfirmQuarantine bool
}

// ArchiveEntry names one version or rendition and the relative zip path it writes to.
type ArchiveEntry struct {
	FileID      domain.ID  `json:"file_id"`
	VersionID   *domain.ID `json:"version_id"`
	RenditionID *domain.ID `json:"rendition_id"`
	Path        string     `json:"path"`
}

// archiveMember is one zip member resolved before any byte is written.
type archiveMember struct {
	file fileRow
	name string
	obj  object
	what string
}

// Archive writes selected files into one zip. A folder archive holds only the files the
// caller may see, the way List shows the folder; a hidden file the caller named denies
// the whole archive. Entries (CORE-21) use caller paths after the same visibility and
// quarantine checks, all before the first byte. Each member is verified as it is written
// and each download is logged.
func (s *Service) Archive(ctx context.Context, p domain.Principal, req ArchiveRequest, w io.Writer) error {
	members, err := s.resolveArchive(ctx, p, req)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, m := range members {
		body, err := s.open(ctx, m.obj)
		if err != nil {
			return err
		}
		zf, err := zw.Create(m.name)
		if err == nil {
			_, err = io.Copy(zf, body)
		}
		_ = body.Close()
		if err != nil {
			return err
		}
		if err := s.logDownload(ctx, p, m.file, m.what); err != nil {
			return err
		}
	}
	return zw.Close()
}

func (s *Service) resolveArchive(ctx context.Context, p domain.Principal, req ArchiveRequest) ([]archiveMember, error) {
	hasFiles := len(req.FileIDs) > 0
	hasFolder := req.FolderID != ""
	hasEntries := len(req.Entries) > 0
	n := 0
	if hasFiles {
		n++
	}
	if hasFolder {
		n++
	}
	if hasEntries {
		n++
	}
	if n != 1 {
		return nil, domain.Invalid("body", "set exactly one of file_ids, folder_id or entries")
	}
	if hasEntries {
		return s.resolveArchiveEntries(ctx, p, req.Entries, req.ConfirmQuarantine)
	}
	var rows []fileRow
	if hasFolder {
		fd, err := s.st.getFolder(ctx, req.FolderID)
		if errors.Is(err, repository.ErrNotFound) {
			return nil, domain.NotFound("folder")
		}
		if err != nil {
			return nil, err
		}
		// A folder is matter information: the same check as Folders and GetFolder.
		if err := s.matterReadable(ctx, p, fd.MatterID); err != nil {
			return nil, domain.NotFound("folder")
		}
		f := string(req.FolderID)
		listed, err := s.st.listFiles(ctx, fileQuery{MatterID: fd.MatterID, Folder: &f})
		if err != nil {
			return nil, err
		}
		walls := map[domain.ID]domain.WallView{}
		for _, f := range listed {
			if f.PurgedAt != zero {
				continue
			}
			d, err := s.decide(ctx, p, f, domain.ActionRead, walls)
			if err != nil {
				return nil, err
			}
			if d.Allow {
				rows = append(rows, f)
			}
		}
	}
	for _, id := range req.FileIDs {
		f, err := s.loadFile(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.visible(ctx, p, f); err != nil {
			return nil, err
		}
		rows = append(rows, f)
	}
	if len(rows) == 0 {
		return nil, domain.Invalid("file_ids", "select at least one file or a folder")
	}
	used := map[string]int{}
	out := make([]archiveMember, 0, len(rows))
	for _, f := range rows {
		v, err := s.st.getVersion(ctx, f.CurrentVersionID)
		if err != nil {
			return nil, err
		}
		q, err := s.st.quarantined(ctx, v)
		if err != nil {
			return nil, err
		}
		if q && !req.ConfirmQuarantine {
			return nil, ErrQuarantined
		}
		name := f.Name
		if n := used[strings.ToLower(name)]; n > int(zero) {
			name = strings.TrimSuffix(f.Name, path.Ext(f.Name)) + fmt.Sprintf("-%d", n+1) + path.Ext(f.Name)
		}
		used[strings.ToLower(f.Name)]++
		out = append(out, archiveMember{file: f, name: name, obj: versionObject(v), what: "archive"})
	}
	return out, nil
}

func (s *Service) resolveArchiveEntries(ctx context.Context, p domain.Principal, entries []ArchiveEntry, confirmQuarantine bool) ([]archiveMember, error) {
	seen := map[string]struct{}{}
	out := make([]archiveMember, 0, len(entries))
	for i, e := range entries {
		memberPath, err := archiveZipPath(e.Path)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(memberPath)
		if _, dup := seen[key]; dup {
			return nil, domain.Invalid("entries", "entry paths must be unique without regard to case")
		}
		seen[key] = struct{}{}
		versionID, hasVersion := optionalArchiveID(e.VersionID)
		renditionID, hasRendition := optionalArchiveID(e.RenditionID)
		if hasVersion == hasRendition {
			return nil, domain.Invalid(fmt.Sprintf("entries[%d]", i), "set exactly one of version_id or rendition_id")
		}
		if e.FileID == "" {
			return nil, domain.Invalid(fmt.Sprintf("entries[%d].file_id", i), "file_id is required")
		}
		var (
			f    fileRow
			obj  object
			what string
			ver  domain.FileVersion
		)
		if hasVersion {
			f, ver, err = s.fileVersion(ctx, p, e.FileID, versionID)
			if err != nil {
				return nil, err
			}
			obj = versionObject(ver)
			what = fmt.Sprintf("archive v%d", ver.Number)
		} else {
			f, err = s.loadFile(ctx, e.FileID)
			if err != nil {
				return nil, err
			}
			if err := s.visible(ctx, p, f); err != nil {
				return nil, err
			}
			if _, err := domain.ParseID(string(renditionID)); err != nil {
				return nil, domain.NotFound("rendition")
			}
			r, err := s.st.getRendition(ctx, renditionID)
			if errors.Is(err, repository.ErrNotFound) {
				return nil, domain.NotFound("rendition")
			}
			if err != nil {
				return nil, err
			}
			ver, err = s.st.getVersion(ctx, r.FileVersionID)
			if err != nil || ver.FileID != f.ID {
				return nil, domain.NotFound("rendition")
			}
			obj = renditionObject(r, f.ID)
			what = fmt.Sprintf("archive %s %d", r.Kind, r.Number)
		}
		q, err := s.st.quarantined(ctx, ver)
		if err != nil {
			return nil, err
		}
		if q && !confirmQuarantine {
			return nil, ErrQuarantined
		}
		out = append(out, archiveMember{file: f, name: memberPath, obj: obj, what: what})
	}
	return out, nil
}

func optionalArchiveID(id *domain.ID) (domain.ID, bool) {
	if id == nil || *id == "" {
		return "", false
	}
	return *id, true
}

// archiveZipPath accepts a relative zip member path for CORE-21 entries.
func archiveZipPath(raw string) (string, error) {
	if raw == "" || len(raw) > domain.ArchiveZipPathMaxBytes {
		return "", domain.Invalid("path", "path must be 1 to 255 bytes")
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, `\`) {
		return "", domain.Invalid("path", "path must be relative with / separators")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", domain.Invalid("path", "path must not contain control characters")
		}
	}
	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", domain.Invalid("path", "path segments must not be empty, . or ..")
		}
	}
	return raw, nil
}
