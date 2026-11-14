package files

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// zero is the integer origin for counts and offsets.
const zero = int64(0)

// Matter is what the file store needs to know about a matter.
type Matter struct {
	ID             domain.ID
	Number         string
	Status         string
	PracticeAreaID domain.ID
}

// AreaFolders is an optional Directory method: a practice area's default_folders
// (PRD 5 practice_areas, PRD 8.19). An empty list falls back to files.default_folders.
// PracticeDirectory does not implement it yet: practice_areas has no default_folders
// column (pending, owner practice; see CONTRACT.md).
type AreaFolders interface {
	AreaFolders(ctx context.Context, area domain.ID) ([]string, error)
}

// Directory reads matters and client contacts. It does no access checks; the files
// service applies walls and roles itself. Practice implements it (PracticeDirectory).
type Directory interface {
	Matter(ctx context.Context, id domain.ID) (Matter, error)
	OpenMatters(ctx context.Context) ([]Matter, error)
	// UserContact is the contact linked to a client user, or "" when there is none.
	UserContact(ctx context.Context, user domain.ID) (domain.ID, error)
}

// Mailer sends system mail to every firm admin.
type Mailer interface {
	MailAdmins(ctx context.Context, subject, body string) error
}

// Settings is the registry read the service needs.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
}

// VersionCreated is delivered after a version commits: file.version_created. Doc
// Tender queues its job from it; a scanning module subscribes to the same event.
type VersionCreated struct {
	FileID    domain.ID
	VersionID domain.ID
	MatterID  domain.ID
	Number    int
	How       string
	Source    string
}

// Config wires the service.
type Config struct {
	Pool      *sql.DB
	Dialect   string
	Audit     *audit.Service
	Settings  Settings
	Directory Directory
	// Walls resolves matter walls. practice.Service.Policy.Walls in the running core.
	Walls domain.WallReader
	Mail  Mailer
	// Key is the installer's 32-byte encryption key. Byte sealing and backend settings
	// use keys derived from it, never the key itself.
	Key []byte
	// StagingDir holds upload chunks until publish, on the core's own volume.
	StagingDir string
	HTTP       *http.Client
	Now        func() time.Time
	// OnVersion runs after every version commits. Errors are the subscriber's to keep.
	OnVersion func(ctx context.Context, ev VersionCreated)
}

// Service is the one file store. Every method checks the caller in this layer, so the
// API, MCP, UI, search, widgets and modules get the same answer.
type Service struct {
	st        *store
	audit     *audit.Service
	settings  Settings
	dir       Directory
	policy    domain.Policy
	mail      Mailer
	blobKey   []byte
	configKey []byte
	staging   string
	http      *http.Client
	now       func() time.Time
	newID     func() (domain.ID, error)
	onVersion func(ctx context.Context, ev VersionCreated)
	// moduleOn reports whether a module is installed and enabled; nil refuses every
	// module principal (SetModuleCheck).
	moduleOn func(ctx context.Context, module string) (bool, error)

	mu       sync.Mutex
	adapters map[domain.ID]cached
	running  map[domain.ID]bool
	drops    map[string]dropSeen
	// removeDrop deletes a taken drop file; a test makes it fail like a share without
	// delete permission.
	removeDrop func(string) error
	// afterMoveItem lets a test stop a move between objects.
	afterMoveItem func(done int)
	// beforeMoveCommit lets a test act between a move's copy and its record change.
	beforeMoveCommit func(object domain.ID)
}

type cached struct {
	updated time.Time
	backend storage.Backend
}

// New validates cfg.
func New(cfg Config) (*Service, error) {
	if cfg.Pool == nil || cfg.Audit == nil || cfg.Settings == nil || cfg.Directory == nil || cfg.Walls == nil {
		return nil, errors.New("files: pool, audit, settings, directory and walls are required")
	}
	if len(cfg.Key) != sha256.Size {
		return nil, errors.New("files: the encryption key must be 32 bytes")
	}
	if cfg.StagingDir == "" {
		return nil, errors.New("files: a staging directory is required")
	}
	if cfg.Dialect == "" {
		cfg.Dialect = "postgres"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTP == nil {
		cfg.HTTP = http.DefaultClient
	}
	return &Service{
		st: &store{pool: cfg.Pool, dialect: cfg.Dialect}, audit: cfg.Audit, settings: cfg.Settings, dir: cfg.Directory,
		policy: domain.Policy{Walls: cfg.Walls}, mail: cfg.Mail,
		blobKey: derive(cfg.Key, "togavsu/storage/bytes"), configKey: derive(cfg.Key, "togavsu/storage/settings"),
		staging: cfg.StagingDir, http: cfg.HTTP, now: cfg.Now, newID: domain.NewID, onVersion: cfg.OnVersion,
		adapters: map[domain.ID]cached{}, running: map[domain.ID]bool{}, drops: map[string]dropSeen{},
		removeDrop: os.Remove,
	}, nil
}

func derive(key []byte, label string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	return m.Sum(nil)
}

func (s *Service) meta(by domain.ID) (domain.Meta, error) {
	id, err := s.newID()
	if err != nil {
		return domain.Meta{}, err
	}
	at := s.now().UTC()
	return domain.Meta{ID: id, CreatedAt: at, UpdatedAt: at, CreatedBy: by}, nil
}

// actor is the user id written to created_by and saved_by.
func actor(p domain.Principal, fallback domain.ID) domain.ID {
	if !p.UserID.IsZero() {
		return p.UserID
	}
	return fallback
}

func actorKind(p domain.Principal) string {
	switch {
	case p.Source == domain.SourceSystem:
		return "system"
	case p.Source == domain.SourceModule:
		return "module"
	case p.Source == domain.SourceAPIToken:
		return "agent"
	case clientOnly(p):
		return "client"
	default:
		return "staff"
	}
}

func clientOnly(p domain.Principal) bool {
	return p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff)
}

func (s *Service) intSetting(ctx context.Context, key string) (int64, error) {
	raw, err := s.settings.Get(ctx, key)
	if err != nil {
		return zero, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

// tx runs fn in one transaction with its audit rows and timeline events.
func (s *Service) tx(ctx context.Context, fn func(ctx context.Context) error) error {
	return audit.WithTx(ctx, s.st.pool, fn)
}

func (s *Service) record(ctx context.Context, p domain.Principal, entity string, id domain.ID, field, old, new string) error {
	if p.Source == domain.SourceSystem && p.UserID.IsZero() {
		p.UserID = domain.SystemUserID
	}
	_, err := s.audit.Record(ctx, p, audit.Change{Entity: entity, EntityID: id, Field: field, Old: old, New: new})
	return err
}

// event appends a timeline row for f. A client sees it only when the client can see the
// file (shared and not attorney-only) or when the client did it: an attorney-only file's
// name and activity never reach the client timeline, whatever its share flag says.
func (s *Service) event(ctx context.Context, p domain.Principal, f fileRow, action, summary string) error {
	m, err := s.meta(actor(p, f.CreatedBy))
	if err != nil {
		return err
	}
	return s.st.appendEvent(ctx, domain.Event{
		Meta: m, ActorKind: actorKind(p), ActorID: actor(p, domain.SystemUserID), Action: action,
		RecordType: "file", RecordID: f.ID, MatterID: f.MatterID, Summary: summary,
		ClientVisible: clientSees(p, f),
	})
}

// clientSees is whether a timeline row about f is client-visible.
func clientSees(p domain.Principal, f fileRow) bool {
	return clientOnly(p) || (f.SharedWithClient && !f.AttorneyOnly)
}

// decide applies walls and roles to one file. A file with a matter follows the
// matter's wall; a file with only a contact follows that contact (a client sees it
// only when it is their own contact and shared); a file with neither is firm-only.
// SetModuleCheck gives the service the module runtime's installed-and-enabled check.
// The core sets it once at start, before serving; until then a module principal is
// refused.
func (s *Service) SetModuleCheck(fn func(ctx context.Context, module string) (bool, error)) {
	s.moduleOn = fn
}

// moduleOnly is a module's own token with no viewer (PRD 9.3): no user and no roles, only
// the approved scopes the core checked on the route.
func moduleOnly(p domain.Principal) bool {
	return p.Source == domain.SourceModule && p.ModuleID != "" && p.UserID.IsZero()
}

// clientViaModule is a client user acting through a module's frame (a context token):
// the client portal module (PRD 8.19).
func clientViaModule(p domain.Principal) bool {
	return p.Source == domain.SourceModule && p.ModuleID != "" && !p.UserID.IsZero() && clientOnly(p)
}

// moduleDecision applies the file rule for a module's own token and for a client's
// upload through a module, after the matter wall view is known; ok false leaves the
// decision to domain.Decide. A module that is not installed and enabled is refused.
// A module's own token reads and writes general files on a matter, never one behind a
// wall or an attorney-only file. A client through a module writes only onto a matter
// they are a client on; reads keep the ordinary client rule.
func (s *Service) moduleDecision(ctx context.Context, p domain.Principal, in domain.AccessInput) (domain.Decision, bool, error) {
	only, client := moduleOnly(p), clientViaModule(p)
	if !only && !(client && in.Action == domain.ActionWrite) {
		return domain.Decision{}, false, nil
	}
	on := false
	if s.moduleOn != nil {
		var err error
		if on, err = s.moduleOn(ctx, p.ModuleID); err != nil {
			return domain.Decision{}, true, err
		}
	}
	switch {
	case !on:
		return domain.Decision{Allow: false, Reason: domain.ReasonRole}, true, nil
	case in.MatterID == "":
		return domain.Decision{Allow: false, Reason: domain.ReasonRole}, true, nil
	case in.Walled && !in.WallAllows:
		return domain.Decision{Allow: false, Reason: domain.ReasonMatterWall}, true, nil
	case in.AttorneyOnly:
		return domain.Decision{Allow: false, Reason: domain.ReasonAttorneyOnly}, true, nil
	case in.FileClass != domain.FileGeneral:
		return domain.Decision{Allow: false, Reason: domain.ReasonRole}, true, nil
	case client && !in.ClientMatter:
		return domain.Decision{Allow: false, Reason: domain.ReasonClientScope}, true, nil
	case only && in.Action != domain.ActionRead && in.Action != domain.ActionWrite:
		return domain.Decision{Allow: false, Reason: domain.ReasonRole}, true, nil
	}
	return domain.Decision{Allow: true}, true, nil
}

func (s *Service) decide(ctx context.Context, p domain.Principal, f fileRow, action domain.Action, walls map[domain.ID]domain.WallView) (domain.Decision, error) {
	in := domain.AccessInput{
		Resource: domain.ResFile, Action: action, MatterID: f.MatterID, AttorneyOnly: f.AttorneyOnly,
		FileClass: domain.FileGeneral, SharedWithClient: f.SharedWithClient,
	}
	if f.FinancialClass == string(domain.FileReceipt) || f.FinancialClass == string(domain.FileInvoicePDF) {
		in.FileClass = domain.FileClass(f.FinancialClass)
	}
	switch {
	case f.MatterID != "":
		view, ok := walls[f.MatterID]
		if !ok {
			var err error
			view, err = s.policy.Walls.View(ctx, p, f.MatterID)
			if err != nil {
				return domain.Decision{}, err
			}
			if walls != nil {
				walls[f.MatterID] = view
			}
		}
		in.Walled, in.WallAllows, in.ClientMatter = view.Walled, view.Allows, view.ClientMatter
	case f.ContactID != "" && p.HasRole(domain.RoleClient) && !p.UserID.IsZero():
		own, err := s.dir.UserContact(ctx, p.UserID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return domain.Decision{}, err
		}
		in.ClientMatter = own != "" && own == f.ContactID
	}
	if own, err := s.ownUnassigned(ctx, p, f, action); err != nil || own {
		return domain.Decision{Allow: own}, err
	}
	if d, ok, err := s.moduleDecision(ctx, p, in); ok || err != nil {
		return d, err
	}
	return domain.Decide(p, in), nil
}

// visible returns not found unless the caller may read f. Trashed files stay
// readable; a purged file is gone for everyone.
func (s *Service) visible(ctx context.Context, p domain.Principal, f fileRow) error {
	if f.PurgedAt != zero {
		return domain.NotFound("file")
	}
	d, err := s.decide(ctx, p, f, domain.ActionRead, nil)
	if err != nil {
		return err
	}
	if !d.Allow {
		return domain.NotFound("file")
	}
	return nil
}

// writable needs read first, so a hidden file answers not found, then write.
func (s *Service) writable(ctx context.Context, p domain.Principal, f fileRow) error {
	if err := s.visible(ctx, p, f); err != nil {
		return err
	}
	d, err := s.decide(ctx, p, f, domain.ActionWrite, nil)
	if err != nil {
		return err
	}
	if !d.Allow {
		return domain.Forbidden("this role cannot change files")
	}
	return nil
}

// canWriteTo checks a new file's placement before any byte is accepted.
func (s *Service) canWriteTo(ctx context.Context, p domain.Principal, matter, contact domain.ID, attorneyOnly bool) error {
	probe := fileRow{File: domain.File{MatterID: matter, ContactID: contact, AttorneyOnly: attorneyOnly}}
	if matter != "" {
		if _, err := s.dir.Matter(ctx, matter); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return domain.NotFound("matter")
			}
			return err
		}
	}
	d, err := s.decide(ctx, p, probe, domain.ActionWrite, nil)
	if err != nil {
		return err
	}
	if !d.Allow {
		if d.Reason == domain.ReasonMatterWall || d.Reason == domain.ReasonClientScope {
			return domain.NotFound("matter")
		}
		return domain.Forbidden("this role cannot add files here")
	}
	return nil
}

func (s *Service) loadFile(ctx context.Context, id domain.ID) (fileRow, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return fileRow{}, domain.NotFound("file")
	}
	f, err := s.st.getFile(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return fileRow{}, domain.NotFound("file")
	}
	return f, err
}

// lockedFile is loadFile inside a transaction with the row held FOR UPDATE until the
// transaction ends. Every read-modify-write of a files row goes through it.
func (s *Service) lockedFile(ctx context.Context, id domain.ID) (fileRow, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return fileRow{}, domain.NotFound("file")
	}
	f, err := s.st.lockFile(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return fileRow{}, domain.NotFound("file")
	}
	return f, err
}

// adapter returns the raw adapter for a storage_backends row, rebuilt when the row
// changes.
func (s *Service) adapter(ctx context.Context, id domain.ID) (storage.Backend, domain.StorageBackend, error) {
	row, err := s.st.getBackend(ctx, id)
	if err != nil {
		return nil, domain.StorageBackend{}, err
	}
	s.mu.Lock()
	c, ok := s.adapters[id]
	s.mu.Unlock()
	if ok && c.updated.Equal(row.UpdatedAt) {
		return c.backend, row, nil
	}
	set, err := s.openSettings(row.SettingsCiphertext)
	if err != nil {
		return nil, row, err
	}
	b, err := storage.Open(row.Kind, set, storage.Options{HTTP: s.http})
	if err != nil {
		return nil, row, err
	}
	s.mu.Lock()
	s.adapters[id] = cached{updated: row.UpdatedAt, backend: b}
	s.mu.Unlock()
	return b, row, nil
}

// sealsNew reports whether new bytes on this backend are encrypted.
func sealsNew(row domain.StorageBackend) bool {
	return row.Encryption || storage.AlwaysEncrypted(row.Kind)
}

// at returns the backend view for one object's bytes: sealed or plain as written.
func (s *Service) at(ctx context.Context, loc location) (storage.Backend, error) {
	raw, row, err := s.adapter(ctx, loc.Backend)
	if err != nil {
		return nil, err
	}
	sealed := loc.Sealed
	if !loc.known {
		sealed = sealsNew(row)
	}
	if !sealed {
		return raw, nil
	}
	return storage.NewSealed(raw, s.blobKey)
}

// defaultBackend is the row marked default. files.default_backend names the kind a
// fresh install starts on; EnsureLocalDisk creates that row.
func (s *Service) defaultBackend(ctx context.Context) (domain.StorageBackend, error) {
	rows, err := s.st.backends(ctx)
	if err != nil {
		return domain.StorageBackend{}, err
	}
	for _, b := range rows {
		if b.IsDefault {
			return b, nil
		}
	}
	return domain.StorageBackend{}, domain.Conflict("no default storage backend is set")
}

// putOn stores r under key on one backend, sealed when that backend encrypts.
func (s *Service) putOn(ctx context.Context, backend domain.ID, key string, r io.Reader, size int64) (location, error) {
	_, row, err := s.adapter(ctx, backend)
	if err != nil {
		return location{}, err
	}
	loc := location{Backend: backend, Key: key, Sealed: sealsNew(row), known: true}
	b, err := s.at(ctx, loc)
	if err != nil {
		return location{}, err
	}
	if err := b.Put(ctx, key, r, size); err != nil {
		return location{}, fmt.Errorf("files: store bytes on %s: %w", row.Name, err)
	}
	return loc, nil
}

// putObject stores r under key on the default backend.
func (s *Service) putObject(ctx context.Context, key string, r io.Reader, size int64) (location, error) {
	row, err := s.defaultBackend(ctx)
	if err != nil {
		return location{}, err
	}
	return s.putOn(ctx, row.ID, key, r, size)
}

// object identifies stored bytes and what they must hash to.
type object struct {
	ID     domain.ID
	Kind   string
	FileID domain.ID
	Size   int64
	SHA    string
	Home   location
}

func versionObject(v domain.FileVersion) object {
	return object{ID: v.ID, Kind: "version", FileID: v.FileID, Size: v.SizeBytes, SHA: v.SHA256,
		Home: location{Backend: v.StorageBackendID, Key: v.StorageKey}}
}

// open reads an object from wherever it lives now and checks its SHA-256 as it
// streams. A mismatch fails the read, records a storage alert and mails the admins.
func (s *Service) open(ctx context.Context, o object) (*storage.Verified, error) {
	loc, err := s.st.location(ctx, o.ID, o.Home)
	if err != nil {
		return nil, err
	}
	b, err := s.at(ctx, loc)
	if err != nil {
		return nil, err
	}
	v, err := storage.OpenVerified(ctx, b, loc.Key, o.Size, o.SHA)
	if err != nil {
		if errors.Is(err, storage.ErrIntegrity) || errors.Is(err, storage.ErrNotFound) {
			s.alert(context.WithoutCancel(ctx), loc.Backend, o, err)
		}
		return nil, err
	}
	v.OnMismatch = func(err error) { s.alert(context.WithoutCancel(ctx), loc.Backend, o, err) }
	return v, nil
}

// readAll reads a small object, such as a Markdown copy, fully verified.
func (s *Service) readAll(ctx context.Context, o object) ([]byte, error) {
	v, err := s.open(ctx, o)
	if err != nil {
		return nil, err
	}
	defer v.Close()
	return io.ReadAll(v)
}

// alert stores the storage alert and emails the admins. It never hides the file.
func (s *Service) alert(ctx context.Context, backend domain.ID, o object, cause error) {
	detail := fmt.Sprintf("%s %s of file %s failed its check on backend %s: %v", o.Kind, o.ID, o.FileID, backend, cause)
	emailed := false
	if s.mail != nil {
		emailed = s.mail.MailAdmins(ctx, "Togavsu storage alert: a file failed its integrity check", detail+
			"\n\nThe file stays listed. Restore the bytes from a backup or another backend copy.") == nil
	}
	m, err := s.meta(domain.SystemUserID)
	if err == nil {
		_ = s.st.insertAlert(ctx, m, backend, o.ID, o.FileID, detail, emailed)
	}
}

// Alerts lists storage alerts. Admin only.
func (s *Service) Alerts(ctx context.Context, p domain.Principal) ([]Alert, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	return s.st.alerts(ctx)
}

func admin(p domain.Principal) error {
	if p.Source == domain.SourceSystem || p.HasRole(domain.RoleAdmin) {
		return nil
	}
	if p.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	return domain.Forbidden("admin only")
}

// lockedFields parses files.locked_fields, a JSON array of the fields a person set.
func lockedFields(f fileRow) []string {
	raw := strings.Trim(f.LockedFields, "[] ")
	if raw == "" {
		return []string{}
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v, err := strconv.Unquote(strings.TrimSpace(part)); err == nil && v != "" {
			out = append(out, v)
		}
	}
	return out
}

func lock(f *fileRow, field string) {
	fields := lockedFields(*f)
	for _, x := range fields {
		if x == field {
			return
		}
	}
	fields = append(fields, field)
	quoted := make([]string, len(fields))
	for i, x := range fields {
		quoted[i] = strconv.Quote(x)
	}
	f.LockedFields = "[" + strings.Join(quoted, ",") + "]"
}

func isLocked(f fileRow, field string) bool {
	for _, x := range lockedFields(f) {
		if x == field {
			return true
		}
	}
	return false
}
