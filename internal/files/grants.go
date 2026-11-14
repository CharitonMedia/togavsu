package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
)

// codeBatchRejected is the contract's code for a bulk batch that wrote nothing; it is
// served as 422 (writeErr).
const codeBatchRejected = "batch_rejected"

// tenantID names the firm on a grant. The core is single-tenant (PRD 3: one install per
// firm), so every grant this core issues carries the same value.
const tenantID = "firm"

// GrantRequest is BackgroundFileGrantRequest (issueFilePublicationGrant).
type GrantRequest struct {
	Assignment string     `json:"assignment"`
	MatterID   *domain.ID `json:"matter_id"`
	ContactID  *domain.ID `json:"contact_id"`
	Source     string     `json:"source"`
	Queue      *string    `json:"queue"`
}

// GrantView is BackgroundFileAuthorization: what the core issued to the module. It is
// delivered to that module only, on file.background_authorized.
type GrantView struct {
	ModuleID          string      `json:"module_id"`
	Assignment        string      `json:"assignment"`
	MatterID          *domain.ID  `json:"matter_id"`
	Source            string      `json:"source"`
	AllowedOperations []string    `json:"allowed_operations"`
	ExpiresAt         string      `json:"expires_at"`
	Revoked           bool        `json:"revoked"`
	Expired           bool        `json:"expired"`
	IssuedBy          string      `json:"issued_by"`
	UploadIDs         []domain.ID `json:"upload_ids"`
	TenantID          string      `json:"tenant_id"`
	ContactID         *domain.ID  `json:"contact_id,omitempty"`
	Queue             *string     `json:"queue,omitempty"`
}

type grantRow struct {
	domain.Meta
	ModuleID   string
	Assignment string
	MatterID   domain.ID
	ContactID  domain.ID
	Queue      string
	Source     string
	ExpiresAt  int64
	RevokedAt  int64
	IdemKey    string
	Hash       string
}

// grantOperations are the calls a grant lets the module make with no viewer session.
func grantOperations(assignment string) []string {
	ops := []string{"createFileUpload", "putFileChunk", "completeFileUpload", "publishFile", "bulkFile"}
	if assignment == "unassigned" {
		ops = append(ops, "reviewUnassignedFile")
	}
	return ops
}

func (s *Service) grantView(ctx context.Context, g grantRow) (GrantView, error) {
	ids, err := s.st.grantUploads(ctx, g.ID)
	if err != nil {
		return GrantView{}, err
	}
	if ids == nil {
		ids = []domain.ID{}
	}
	v := GrantView{
		ModuleID: g.ModuleID, Assignment: g.Assignment, MatterID: optID(g.MatterID), Source: g.Source,
		AllowedOperations: grantOperations(g.Assignment), ExpiresAt: domain.FormatUTC(time.UnixMicro(g.ExpiresAt)),
		Revoked: g.RevokedAt != zero, Expired: us(s.now()) >= g.ExpiresAt, IssuedBy: "core", UploadIDs: ids, TenantID: tenantID,
	}
	if g.Assignment == "unassigned" {
		v.ContactID = optID(g.ContactID)
		if g.Queue != "" {
			q := g.Queue
			v.Queue = &q
		}
	}
	return v, nil
}

// servicePrincipal is the module's own principal with no viewer: what a grant's matter
// is checked against, so a viewer's admin role never widens it.
func servicePrincipal(p domain.Principal) domain.Principal {
	return domain.Principal{Source: domain.SourceModule, ModuleID: p.ModuleID}
}

func (s *Service) moduleEnabled(ctx context.Context, module string) (bool, error) {
	if s.moduleOn == nil || module == "" {
		return false, nil
	}
	return s.moduleOn(ctx, module)
}

// IssueGrant is issueFilePublicationGrant. Only an installed, enabled module's token
// (the route checked files:write on it) receives a grant, for one matter its own token
// can already write to, or unassigned. A walled or missing matter is not found. A new
// grant for the same module and target replaces the one before it, which is revoked.
// The same Idempotency-Key and body replay the grant; another body is a conflict.
func (s *Service) IssueGrant(ctx context.Context, p domain.Principal, in GrantRequest, idemKey string) (GrantView, error) {
	if p.Source != domain.SourceModule || p.ModuleID == "" {
		return GrantView{}, domain.Forbidden("a background publication grant is issued to an installed module's token")
	}
	on, err := s.moduleEnabled(ctx, p.ModuleID)
	if err != nil {
		return GrantView{}, err
	}
	if !on {
		return GrantView{}, domain.Forbidden("the module is not installed and enabled")
	}
	switch in.Source {
	case "email", "zoom", "importer", "drop_folder":
	default:
		return GrantView{}, domain.Invalid("source", "source is email, zoom, importer or drop_folder")
	}
	g := grantRow{ModuleID: p.ModuleID, Assignment: in.Assignment, Source: in.Source, IdemKey: idemKey}
	switch in.Assignment {
	case "matter":
		if in.MatterID == nil || *in.MatterID == "" {
			return GrantView{}, domain.Invalid("matter_id", "a matter grant names its matter")
		}
		if in.ContactID != nil || in.Queue != nil {
			return GrantView{}, domain.Invalid("assignment", "contact_id and queue belong to an unassigned grant")
		}
		g.MatterID = *in.MatterID
		if _, err := domain.ParseID(string(g.MatterID)); err != nil {
			return GrantView{}, domain.NotFound("matter")
		}
		if err := s.canWriteTo(ctx, servicePrincipal(p), g.MatterID, "", false); err != nil {
			return GrantView{}, err
		}
		if !p.UserID.IsZero() {
			// A viewer narrows the grant, never widens it.
			if err := s.canWriteTo(ctx, p, g.MatterID, "", false); err != nil {
				return GrantView{}, err
			}
		}
	case "unassigned":
		if in.MatterID != nil {
			return GrantView{}, domain.Invalid("matter_id", "an unassigned grant has no matter")
		}
		if in.ContactID != nil && *in.ContactID != "" {
			g.ContactID = *in.ContactID
			ok, err := s.st.contactExists(ctx, g.ContactID)
			if err != nil {
				return GrantView{}, err
			}
			if !ok {
				return GrantView{}, domain.NotFound("contact")
			}
		}
		if in.Queue != nil {
			if *in.Queue == "" {
				return GrantView{}, domain.Invalid("queue", "queue must not be empty")
			}
			g.Queue = *in.Queue
		}
	default:
		return GrantView{}, domain.Invalid("assignment", "assignment is matter or unassigned")
	}
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	g.Hash = hex.EncodeToString(sum[:])
	if idemKey != "" {
		prior, found, err := s.st.grantByKey(ctx, p.ModuleID, idemKey)
		if err != nil {
			return GrantView{}, err
		}
		if found {
			if prior.Hash != g.Hash {
				return GrantView{}, &domain.APIError{Code: "idempotency_conflict", Message: "this Idempotency-Key was used with a different body"}
			}
			return s.grantView(ctx, prior)
		}
	}
	minutes, err := s.intSetting(ctx, "files.publication_grant_minutes")
	if err != nil {
		return GrantView{}, err
	}
	if g.Meta, err = s.meta(actor(p, domain.SystemUserID)); err != nil {
		return GrantView{}, err
	}
	life, err := time.ParseDuration(strconv.Itoa(int(minutes)) + "m")
	if err != nil {
		return GrantView{}, err
	}
	g.ExpiresAt = us(g.CreatedAt.Add(life))
	var out GrantView
	err = s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.revokeGrants(ctx, g, us(g.CreatedAt)); err != nil {
			return err
		}
		if err := s.st.insertGrant(ctx, g); err != nil {
			return err
		}
		if out, err = s.grantView(ctx, g); err != nil {
			return err
		}
		body, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := s.st.setGrantBody(ctx, g.ID, string(body)); err != nil {
			return err
		}
		target := "unassigned"
		if g.MatterID != "" {
			target = "matter " + string(g.MatterID)
		}
		if err := s.record(ctx, p, "file_publication_grant", g.ID, "authorization", "", fmt.Sprintf("%s %s %s until %s", g.ModuleID, g.Source, target, out.ExpiresAt)); err != nil {
			return err
		}
		m, err := s.meta(actor(p, domain.SystemUserID))
		if err != nil {
			return err
		}
		return s.st.appendEvent(ctx, domain.Event{
			Meta: m, ActorKind: actorKind(p), ActorID: actor(p, domain.SystemUserID), Action: "file.background_authorized",
			RecordType: "file_publication_grant", RecordID: g.ID, MatterID: g.MatterID,
			Summary: "Authorized " + g.ModuleID + " to publish " + g.Source + " files",
		})
	})
	if isUniqueViolation(err) && idemKey != "" {
		// A concurrent request with the same key won; answer as its replay.
		return s.IssueGrant(ctx, p, in, idemKey)
	}
	return out, err
}

// activeGrant is the module's current grant for matter ("" is unassigned), if any.
func (s *Service) activeGrant(ctx context.Context, module string, matter domain.ID) (grantRow, bool, error) {
	return s.st.activeGrant(ctx, module, matter, us(s.now()))
}

// checkGrant refuses a grant that is revoked, expired, or whose module is off.
func (s *Service) checkGrant(ctx context.Context, id domain.ID) (grantRow, error) {
	g, err := s.st.getGrant(ctx, id)
	if err != nil {
		return grantRow{}, err
	}
	on, err := s.moduleEnabled(ctx, g.ModuleID)
	switch {
	case err != nil:
		return grantRow{}, err
	case !on:
		return grantRow{}, domain.Forbidden("the module is not installed and enabled")
	case g.RevokedAt != zero:
		return grantRow{}, domain.Forbidden("the background publication grant was revoked")
	case us(s.now()) >= g.ExpiresAt:
		return grantRow{}, domain.Forbidden("the background publication grant has expired")
	}
	return g, nil
}

// grantPlacement applies an upload's grant to its publication: the matter and
// contact stay the grant's, and the module cannot supply another.
func grantPlacement(g grantRow, in *Publish) error {
	if in.MatterID != nil && *in.MatterID != g.MatterID {
		if g.Assignment == "unassigned" {
			return domain.Forbidden("an unassigned upload cannot be given a matter; Doc Tender's review assigns it")
		}
		return &domain.APIError{Code: domain.CodeForbidden, Field: "matter_id", Message: "the grant names another matter"}
	}
	if in.ContactID != nil && *in.ContactID != g.ContactID && g.Assignment == "unassigned" {
		return &domain.APIError{Code: domain.CodeForbidden, Field: "contact_id", Message: "the grant names another contact"}
	}
	return nil
}

// ReviewUnassigned is reviewUnassignedFile: the file the calling module published from
// its own upload with no matter. Another module's upload, a human caller, or a file
// that has since been given a matter is not found. It assigns nothing.
func (s *Service) ReviewUnassigned(ctx context.Context, p domain.Principal, upload domain.ID) (FileView, error) {
	if p.Source != domain.SourceModule || p.ModuleID == "" {
		return FileView{}, domain.Forbidden("unassigned review belongs to the module that published the file")
	}
	u, err := s.ownUpload(ctx, servicePrincipal(p), upload, false)
	if err != nil {
		return FileView{}, err
	}
	if u.Status != "published" || u.FileID == "" {
		return FileView{}, domain.NotFound("file")
	}
	f, err := s.loadFile(ctx, u.FileID)
	if err != nil {
		return FileView{}, err
	}
	if f.MatterID != "" {
		return FileView{}, domain.NotFound("file")
	}
	if err := s.visible(ctx, servicePrincipal(p), f); err != nil {
		return FileView{}, err
	}
	return view(f, nil), nil
}

// ownUnassigned reports a module's own token reading a file it published with no
// matter (reviewUnassignedFile and the replay of its publish).
func (s *Service) ownUnassigned(ctx context.Context, p domain.Principal, f fileRow, action domain.Action) (bool, error) {
	if !moduleOnly(p) || action != domain.ActionRead || f.ID == "" || f.MatterID != "" || f.Source != p.ModuleID || f.AttorneyOnly {
		return false, nil
	}
	return s.moduleEnabled(ctx, p.ModuleID)
}

// BulkItem is FileBulkItem.
type BulkItem struct {
	UploadID         domain.ID  `json:"upload_id"`
	MatterID         *domain.ID `json:"matter_id"`
	ContactID        *domain.ID `json:"contact_id"`
	Name             string     `json:"name"`
	AttorneyOnly     bool       `json:"attorney_only"`
	SharedWithClient bool       `json:"shared_with_client"`
}

// batch defers what a single publish does after its own commit until the bulk
// transaction commits: Doc Tender's version callback and removing the staged bytes.
// rollback drops the objects already stored when the batch fails.
type batch struct {
	commit, rollback []func()
}

type batchKey struct{}

func batchOf(ctx context.Context) *batch {
	b, _ := ctx.Value(batchKey{}).(*batch)
	return b
}

// PublishBatch is bulkFile: every item publishes its staged upload in one transaction,
// or none does. Each upload is locked first, so a concurrent publish cannot take one
// mid-batch. An upload that already has a file replays it and adds nothing. A failed
// item leaves every upload staged, stores nothing and emits nothing; a hidden matter or
// upload is not_found, a denied role forbidden, anything else batch_rejected.
func (s *Service) PublishBatch(ctx context.Context, p domain.Principal, items []BulkItem) ([]FileView, error) {
	max, err := s.intSetting(ctx, "api.bulk_batch_max")
	if err != nil {
		return nil, err
	}
	if len(items) == int(zero) || int64(len(items)) > max {
		return nil, &domain.APIError{Code: codeBatchRejected, Field: "items", Message: fmt.Sprintf("a batch holds 1 to %d items", max)}
	}
	seen := map[domain.ID]bool{}
	for i, it := range items {
		if seen[it.UploadID] {
			return nil, &domain.APIError{Code: codeBatchRejected, Field: fmt.Sprintf("items[%d].upload_id", i), Message: "upload_id appears twice in the batch"}
		}
		seen[it.UploadID] = true
	}
	b := &batch{}
	ctx = context.WithValue(ctx, batchKey{}, b)
	out := make([]FileView, 0, len(items))
	err = s.tx(ctx, func(ctx context.Context) error {
		for i, it := range items {
			if _, err := s.ownUpload(ctx, p, it.UploadID, true); err != nil {
				return itemError(i, err)
			}
		}
		for i, it := range items {
			v, err := s.PublishUpload(ctx, p, Publish{UploadID: it.UploadID, MatterID: it.MatterID, ContactID: it.ContactID,
				Name: it.Name, AttorneyOnly: it.AttorneyOnly, SharedWithClient: it.SharedWithClient})
			if err != nil {
				return itemError(i, err)
			}
			out = append(out, v)
		}
		return nil
	})
	if err != nil {
		for _, f := range b.rollback {
			f()
		}
		return nil, err
	}
	for _, f := range b.commit {
		f()
	}
	return out, nil
}

// itemError names the failing item. not_found and forbidden keep their codes; every
// other failure rejects the batch.
func itemError(i int, err error) error {
	var api *domain.APIError
	if !errors.As(err, &api) {
		return err
	}
	out := *api
	prefix := fmt.Sprintf("items[%d]", i)
	out.Message = prefix + ": " + out.Message
	if out.Field != "" {
		out.Field = prefix + "." + out.Field
	} else {
		out.Field = prefix
	}
	switch out.Code {
	case domain.CodeNotFound, domain.CodeForbidden, domain.CodeUnauthenticated:
	default:
		out.Code = codeBatchRejected
	}
	return &out
}

// removeStaged deletes an upload's staged bytes once its publication commits.
func (s *Service) removeStaged(ctx context.Context, id domain.ID) {
	if b := batchOf(ctx); b != nil {
		b.commit = append(b.commit, func() { _ = os.Remove(s.stagingPath(id)) })
		return
	}
	_ = os.Remove(s.stagingPath(id))
}

func (s *store) insertGrant(ctx context.Context, g grantRow) error {
	return s.exec(ctx, `INSERT INTO file_publication_grants (id, created_at, updated_at, created_by, module_id, assignment, matter_id, contact_id,
queue, source, expires_at, revoked_at, idempotency_key, request_hash, authorization_body) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'')`,
		g.ID, us(g.CreatedAt), us(g.UpdatedAt), g.CreatedBy, g.ModuleID, g.Assignment, nullID(g.MatterID), nullID(g.ContactID),
		g.Queue, g.Source, g.ExpiresAt, g.RevokedAt, g.IdemKey, g.Hash)
}

func (s *store) setGrantBody(ctx context.Context, id domain.ID, body string) error {
	return s.exec(ctx, `UPDATE file_publication_grants SET authorization_body = ? WHERE id = ?`, body, id)
}

// revokeGrants revokes the module's earlier active grants for g's target.
func (s *store) revokeGrants(ctx context.Context, g grantRow, at int64) error {
	return s.exec(ctx, `UPDATE file_publication_grants SET revoked_at = ?, updated_at = ? WHERE module_id = ? AND assignment = ?
AND COALESCE(matter_id, '') = ? AND revoked_at = 0`, at, at, g.ModuleID, g.Assignment, string(g.MatterID))
}

const grantCols = `id, created_at, updated_at, created_by, module_id, assignment, matter_id, contact_id, queue, source, expires_at, revoked_at, idempotency_key, request_hash`

func scanGrant(sc interface{ Scan(...any) error }) (grantRow, error) {
	var g grantRow
	var matter, contact nullString
	err := sc.Scan(&g.ID, ts(&g.CreatedAt), ts(&g.UpdatedAt), &g.CreatedBy, &g.ModuleID, &g.Assignment, &matter, &contact,
		&g.Queue, &g.Source, &g.ExpiresAt, &g.RevokedAt, &g.IdemKey, &g.Hash)
	g.MatterID, g.ContactID = matter.id(), contact.id()
	return g, notFound(err)
}

func (s *store) getGrant(ctx context.Context, id domain.ID) (grantRow, error) {
	return scanGrant(s.row(ctx, `SELECT `+grantCols+` FROM file_publication_grants WHERE id = ?`, id))
}

func (s *store) grantByKey(ctx context.Context, module, key string) (grantRow, bool, error) {
	g, err := scanGrant(s.row(ctx, `SELECT `+grantCols+` FROM file_publication_grants WHERE module_id = ? AND idempotency_key = ?`, module, key))
	if errors.Is(err, repository.ErrNotFound) {
		return grantRow{}, false, nil
	}
	return g, err == nil, err
}

func (s *store) activeGrant(ctx context.Context, module string, matter domain.ID, now int64) (grantRow, bool, error) {
	assignment := "unassigned"
	if matter != "" {
		assignment = "matter"
	}
	g, err := scanGrant(s.row(ctx, `SELECT `+grantCols+` FROM file_publication_grants WHERE module_id = ? AND assignment = ?
AND COALESCE(matter_id, '') = ? AND revoked_at = 0 AND expires_at > ? ORDER BY created_at DESC, id DESC LIMIT 1`, module, assignment, string(matter), now))
	if errors.Is(err, repository.ErrNotFound) {
		return grantRow{}, false, nil
	}
	return g, err == nil, err
}

func (s *store) grantUploads(ctx context.Context, grant domain.ID) ([]domain.ID, error) {
	rows, err := s.query(ctx, `SELECT id FROM file_uploads WHERE grant_id = ? ORDER BY created_at, id`, grant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ID
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *store) contactExists(ctx context.Context, id domain.ID) (bool, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return false, nil
	}
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM contacts WHERE id = ?`, id).Scan(&n)
	return n != int(zero), err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
