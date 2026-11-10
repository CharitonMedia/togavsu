package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// UploadRequest starts a resumable upload (createFileUpload).
type UploadRequest struct {
	FileName  string    `json:"file_name"`
	MatterID  domain.ID `json:"matter_id"`
	ContactID domain.ID `json:"contact_id"`
	MediaType string    `json:"media_type"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
}

// Upload is the FileUpload contract shape.
type Upload struct {
	ID         domain.ID      `json:"id"`
	Owner      map[string]any `json:"owner"`
	ChunkBytes int64          `json:"chunk_size_bytes"`
	NextOffset int64          `json:"next_offset"`
	SizeBytes  int64          `json:"size_bytes"`
	ExpiresAt  string         `json:"expires_at"`
	Status     string         `json:"status"`
	SHA256     string         `json:"sha256,omitempty"`
	FileID     domain.ID      `json:"file_id,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// noExpiry is reported as expires_at. Staged uploads are kept until published.
// ponytail: ceiling: an abandoned upload's staged bytes stay on the core volume.
// Upgrade path: a files.upload_expiry setting and a sweep that removes expired staging.
// trigger: the staging directory passes 10% of the core volume or the setting is added.
var noExpiry = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func uploadView(u uploadRow, warnings []string) Upload {
	owner := map[string]any{"owner_kind": "user", "owner_user_id": u.OwnerUserID}
	if u.OwnerModuleID != "" {
		owner = map[string]any{"owner_kind": "module", "owner_module_id": u.OwnerModuleID}
	}
	return Upload{
		ID: u.ID, Owner: owner, ChunkBytes: u.ChunkBytes, NextOffset: u.ReceivedBytes, SizeBytes: u.SizeBytes,
		ExpiresAt: domain.FormatUTC(noExpiry), Status: u.Status, SHA256: u.SHA256, FileID: u.FileID, Warnings: warnings,
	}
}

func (s *Service) stagingPath(id domain.ID) string {
	return filepath.Join(s.staging, string(id)+".part")
}

// CreateUpload checks the placement and size before any byte arrives.
func (s *Service) CreateUpload(ctx context.Context, p domain.Principal, in UploadRequest) (Upload, error) {
	if err := validName(in.FileName); err != nil {
		return Upload{}, err
	}
	if in.SizeBytes < zero {
		return Upload{}, domain.Invalid("size_bytes", "size must not be negative")
	}
	if in.SHA256 != "" && !shaPattern.MatchString(in.SHA256) {
		return Upload{}, domain.Invalid("sha256", "sha256 must be 64 lowercase hex characters")
	}
	max, err := s.intSetting(ctx, "files.max_upload_bytes")
	if err != nil {
		return Upload{}, err
	}
	if in.SizeBytes > max {
		return Upload{}, domain.Invalid("size_bytes", fmt.Sprintf("the largest upload is %d bytes", max))
	}
	chunk, err := s.intSetting(ctx, "files.upload_chunk_bytes")
	if err != nil {
		return Upload{}, err
	}
	if err := s.canWriteTo(ctx, p, in.MatterID, in.ContactID, false); err != nil {
		return Upload{}, err
	}
	m, err := s.meta(actor(p, domain.SystemUserID))
	if err != nil {
		return Upload{}, err
	}
	u := uploadRow{
		Meta: m, OwnerUserID: p.UserID, OwnerModuleID: p.ModuleID, FileName: in.FileName, MatterID: in.MatterID,
		ContactID: in.ContactID, MediaType: in.MediaType, SizeBytes: in.SizeBytes, DeclaredSHA256: in.SHA256,
		ChunkBytes: chunk, Status: "receiving", ExpiresAt: us(noExpiry),
	}
	if err := os.MkdirAll(s.staging, fs.ModePerm&0o700); err != nil {
		return Upload{}, err
	}
	f, err := os.OpenFile(s.stagingPath(u.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.ModePerm&0o600)
	if err != nil {
		return Upload{}, err
	}
	if err := f.Close(); err != nil {
		return Upload{}, err
	}
	if err := s.st.insertUpload(ctx, u); err != nil {
		_ = os.Remove(s.stagingPath(u.ID))
		return Upload{}, err
	}
	var warnings []string
	if in.SHA256 != "" && in.MatterID != "" {
		if warnings, err = s.duplicateWarnings(ctx, p, in.MatterID, in.SHA256); err != nil {
			return Upload{}, err
		}
	}
	return uploadView(u, warnings), nil
}

// ownUpload loads an upload only for the principal that started it.
func (s *Service) ownUpload(ctx context.Context, p domain.Principal, id domain.ID, lock bool) (uploadRow, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return uploadRow{}, domain.NotFound("upload")
	}
	u, err := s.st.getUpload(ctx, id, lock)
	if errors.Is(err, repository.ErrNotFound) {
		return uploadRow{}, domain.NotFound("upload")
	}
	if err != nil {
		return uploadRow{}, err
	}
	if u.OwnerUserID != p.UserID || u.OwnerModuleID != p.ModuleID || (p.UserID.IsZero() && p.ModuleID == "") {
		return uploadRow{}, domain.NotFound("upload")
	}
	return u, nil
}

// GetUpload reports where a dropped upload resumes.
func (s *Service) GetUpload(ctx context.Context, p domain.Principal, id domain.ID) (Upload, error) {
	u, err := s.ownUpload(ctx, p, id, false)
	if err != nil {
		return Upload{}, err
	}
	return uploadView(u, nil), nil
}

var contentRange = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+)$`)

// ParseContentRange reads "bytes start-end/total".
func ParseContentRange(h string) (start, end, total int64, err error) {
	if !contentRange.MatchString(h) {
		return zero, zero, zero, domain.Invalid("Content-Range", "Content-Range must be bytes start-end/total")
	}
	if _, err := fmt.Sscanf(h, "bytes %d-%d/%d", &start, &end, &total); err != nil {
		return zero, zero, zero, domain.Invalid("Content-Range", "Content-Range numbers are too large")
	}
	if end < start {
		return zero, zero, zero, domain.Invalid("Content-Range", "range end is before its start")
	}
	return start, end, total, nil
}

// PutChunk writes one chunk at the upload's next offset. The offset advances only
// after the whole chunk is on disk, so a dropped connection resumes at that chunk.
// ponytail: ceiling: each chunk in flight holds one Postgres connection (the upload row
// is locked FOR UPDATE while its body arrives). Upgrade path: write the chunk under a
// per-upload file lock and advance the offset in a short transaction. trigger:
// concurrent uploads wait on the connection pool or a chunk takes over 30 seconds.
func (s *Service) PutChunk(ctx context.Context, p domain.Principal, id domain.ID, start, end, total int64, body io.Reader) (Upload, error) {
	var out Upload
	err := s.tx(ctx, func(ctx context.Context) error {
		u, err := s.ownUpload(ctx, p, id, true)
		if err != nil {
			return err
		}
		if u.Status != "receiving" {
			return domain.Conflict("this upload is " + u.Status)
		}
		n := end - start + 1
		switch {
		case total != u.SizeBytes:
			return domain.Invalid("Content-Range", fmt.Sprintf("total must be %d", u.SizeBytes))
		case start != u.ReceivedBytes:
			return &domain.APIError{Code: domain.CodeConflict, Field: "Content-Range",
				Message: fmt.Sprintf("the next chunk starts at byte %d", u.ReceivedBytes)}
		case n > u.ChunkBytes:
			return domain.Invalid("Content-Range", fmt.Sprintf("a chunk is at most %d bytes", u.ChunkBytes))
		case end >= u.SizeBytes:
			return domain.Invalid("Content-Range", "range runs past the declared size")
		}
		f, err := os.OpenFile(s.stagingPath(u.ID), os.O_WRONLY, fs.ModePerm&0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return err
		}
		got, err := io.Copy(f, io.LimitReader(body, n+1))
		if err != nil {
			return domain.Invalid("body", fmt.Sprintf("the chunk did not arrive whole; send it again from byte %d", start))
		}
		if got != n {
			return domain.Invalid("body", fmt.Sprintf("Content-Range says %d bytes, the body has %d", n, got))
		}
		if err := f.Sync(); err != nil {
			return err
		}
		u.ReceivedBytes = end + 1
		u.UpdatedAt = s.now().UTC()
		if err := s.st.updateUpload(ctx, u); err != nil {
			return err
		}
		out = uploadView(u, nil)
		return nil
	})
	return out, err
}

// CompleteUpload hashes the staged bytes. A declared SHA-256 that does not match
// discards the bytes and the upload starts again at byte 0.
func (s *Service) CompleteUpload(ctx context.Context, p domain.Principal, id domain.ID) (Upload, error) {
	var out Upload
	var mismatch error
	err := s.tx(ctx, func(ctx context.Context) error {
		u, err := s.ownUpload(ctx, p, id, true)
		if err != nil {
			return err
		}
		if u.Status == "staged" || u.Status == "published" {
			out = uploadView(u, nil)
			return nil
		}
		if u.ReceivedBytes != u.SizeBytes {
			return domain.Conflict(fmt.Sprintf("received %d of %d bytes", u.ReceivedBytes, u.SizeBytes))
		}
		f, err := os.Open(s.stagingPath(u.ID))
		if err != nil {
			return err
		}
		h := storage.NewHashingReader(io.LimitReader(f, u.SizeBytes))
		_, err = io.Copy(io.Discard, h)
		_ = f.Close()
		if err != nil {
			return err
		}
		u.UpdatedAt = s.now().UTC()
		if u.DeclaredSHA256 != "" && h.Sum() != u.DeclaredSHA256 {
			u.ReceivedBytes = zero
			if err := os.Truncate(s.stagingPath(u.ID), zero); err != nil {
				return err
			}
			mismatch = domain.Conflict("the bytes received hash to " + h.Sum() + ", not the declared sha256; send them again from byte 0")
		} else {
			u.SHA256, u.Status = h.Sum(), "staged"
		}
		if err := s.st.updateUpload(ctx, u); err != nil {
			return err
		}
		out = uploadView(u, nil)
		return nil
	})
	if err == nil && mismatch != nil {
		return out, mismatch
	}
	return out, err
}

// Publish is publishFile: it creates the file and its first version from a staged
// upload. A repeated publish of the same upload replays the file it made.
type Publish struct {
	UploadID         domain.ID  `json:"upload_id"`
	MatterID         *domain.ID `json:"matter_id"`
	ContactID        *domain.ID `json:"contact_id"`
	Name             string     `json:"name"`
	AttorneyOnly     bool       `json:"attorney_only"`
	SharedWithClient bool       `json:"shared_with_client"`
	FolderID         domain.ID  `json:"folder_id"`
}

// PublishUpload creates a file from a staged upload.
func (s *Service) PublishUpload(ctx context.Context, p domain.Principal, in Publish) (FileView, error) {
	u, err := s.ownUpload(ctx, p, in.UploadID, false)
	if err != nil {
		return FileView{}, err
	}
	if u.Status == "published" && u.FileID != "" {
		return s.Get(ctx, p, u.FileID)
	}
	if u.Status != "staged" {
		return FileView{}, domain.Conflict("complete the upload before publishing it")
	}
	matter, contact, name := u.MatterID, u.ContactID, u.FileName
	if in.MatterID != nil {
		matter = *in.MatterID
	}
	if in.ContactID != nil {
		contact = *in.ContactID
	}
	if in.Name != "" {
		name = in.Name
	}
	nf := NewFile{
		Name: name, MatterID: matter, ContactID: contact, FolderID: in.FolderID, AttorneyOnly: in.AttorneyOnly,
		SharedWithClient: in.SharedWithClient, MediaType: u.MediaType, Source: sourceFor(p), How: "upload",
	}
	staged, err := os.Open(s.stagingPath(u.ID))
	if errors.Is(err, os.ErrNotExist) {
		// A concurrent publish finished and removed the staged bytes.
		return s.replayPublish(ctx, p, in.UploadID, err)
	}
	if err != nil {
		return FileView{}, err
	}
	defer staged.Close()
	view, err := s.create(ctx, p, nf, staged, u.SizeBytes, u.SHA256, func(ctx context.Context, f fileRow, v domain.FileVersion) error {
		u.FileID, u.VersionID, u.UpdatedAt = f.ID, v.ID, s.now().UTC()
		return s.claimUpload(ctx, u)
	})
	if errors.Is(err, errUploadTaken) {
		// create rolled back this file and dropped its stored copy.
		return s.replayPublish(ctx, p, in.UploadID, err)
	}
	if err != nil {
		return FileView{}, err
	}
	_ = os.Remove(s.stagingPath(u.ID))
	return view, nil
}

// AddUploadedVersion is addFileVersion: a staged upload becomes the next version.
func (s *Service) AddUploadedVersion(ctx context.Context, p domain.Principal, fileID, uploadID domain.ID) (VersionView, error) {
	u, err := s.ownUpload(ctx, p, uploadID, false)
	if err != nil {
		return VersionView{}, err
	}
	if u.Status == "published" && u.VersionID != "" {
		return s.Version(ctx, p, fileID, u.VersionID)
	}
	if u.Status != "staged" {
		return VersionView{}, domain.Conflict("complete the upload before adding it as a version")
	}
	staged, err := os.Open(s.stagingPath(u.ID))
	if errors.Is(err, os.ErrNotExist) {
		return s.replayVersion(ctx, p, fileID, uploadID, err)
	}
	if err != nil {
		return VersionView{}, err
	}
	defer staged.Close()
	v, err := s.saveVersion(ctx, p, fileID, staged, u.SizeBytes, u.SHA256, SaveOptions{How: "upload", MediaType: u.MediaType},
		func(ctx context.Context, v domain.FileVersion) error {
			u.FileID, u.VersionID, u.UpdatedAt = fileID, v.ID, s.now().UTC()
			return s.claimUpload(ctx, u)
		})
	if errors.Is(err, errUploadTaken) {
		return s.replayVersion(ctx, p, fileID, uploadID, err)
	}
	if err != nil {
		return VersionView{}, err
	}
	_ = os.Remove(s.stagingPath(u.ID))
	return v, nil
}

// errUploadTaken aborts a publish whose upload another publish already took.
var errUploadTaken = errors.New("files: upload already published")

// claimUpload marks the upload published inside the file's transaction, or aborts it
// when a concurrent publish won, so one upload never makes two files or versions.
func (s *Service) claimUpload(ctx context.Context, u uploadRow) error {
	ok, err := s.st.publishUpload(ctx, u)
	if err == nil && !ok {
		err = errUploadTaken
	}
	return err
}

// replayPublish answers a publish that lost the race with the file the winner made.
func (s *Service) replayPublish(ctx context.Context, p domain.Principal, id domain.ID, cause error) (FileView, error) {
	u, err := s.ownUpload(ctx, p, id, false)
	if err != nil {
		return FileView{}, err
	}
	if u.Status != "published" || u.FileID == "" {
		return FileView{}, cause
	}
	return s.Get(ctx, p, u.FileID)
}

func (s *Service) replayVersion(ctx context.Context, p domain.Principal, fileID, id domain.ID, cause error) (VersionView, error) {
	u, err := s.ownUpload(ctx, p, id, false)
	if err != nil {
		return VersionView{}, err
	}
	if u.Status != "published" || u.VersionID == "" {
		return VersionView{}, cause
	}
	return s.Version(ctx, p, fileID, u.VersionID)
}

func sourceFor(p domain.Principal) string {
	switch {
	case p.Source == domain.SourceModule && p.ModuleID != "":
		return p.ModuleID
	case clientOnly(p):
		return "client_upload"
	default:
		return "upload"
	}
}

func (s *Service) duplicateWarnings(ctx context.Context, p domain.Principal, matter domain.ID, sha string) ([]string, error) {
	ids, err := s.st.sameSHA(ctx, matter, sha)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		f, err := s.st.getFile(ctx, id)
		if err != nil {
			return nil, err
		}
		if s.visible(ctx, p, f) == nil {
			out = append(out, "duplicate_sha256:"+string(id))
		}
	}
	return out, nil
}
