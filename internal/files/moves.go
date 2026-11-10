package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// MoveView is the StorageMove contract shape.
type MoveView struct {
	ID            domain.ID  `json:"id"`
	CreatedAt     string     `json:"created_at"`
	CreatedBy     domain.ID  `json:"created_by"`
	UpdatedAt     string     `json:"updated_at"`
	FromBackendID domain.ID  `json:"from_backend_id"`
	ToBackendID   domain.ID  `json:"to_backend_id"`
	Scope         string     `json:"scope"`
	MatterID      *domain.ID `json:"matter_id"`
	Status        string     `json:"status"`
	ProgressDone  int        `json:"progress_done"`
	ProgressTotal int        `json:"progress_total"`
	ConfirmedBy   *domain.ID `json:"confirmed_by"`
}

func (s *Service) moveView(m moveRow) MoveView {
	status := map[string]string{
		"pending": "running", "running": "running", "checked": "awaiting_confirmation", "confirmed": "completed", "failed": "failed",
	}[m.Status]
	s.mu.Lock()
	active := s.running[m.ID]
	s.mu.Unlock()
	if status == "running" && !active {
		status = "stopped"
	}
	return MoveView{
		ID: m.ID, CreatedAt: domain.FormatUTC(m.CreatedAt), CreatedBy: m.CreatedBy, UpdatedAt: domain.FormatUTC(m.UpdatedAt),
		FromBackendID: m.FromBackendID, ToBackendID: m.ToBackendID, Scope: m.Scope, MatterID: optID(m.MatterID),
		Status: status, ProgressDone: m.Progress, ProgressTotal: m.Total, ConfirmedBy: optID(m.ConfirmedBy),
	}
}

// MoveRequest is StorageMoveWrite.
type MoveRequest struct {
	FromBackendID domain.ID `json:"from_backend_id"`
	ToBackendID   domain.ID `json:"to_backend_id"`
	Scope         string    `json:"scope"`
	MatterID      domain.ID `json:"matter_id"`
}

// StartMove records a move of every version and rendition now on the source
// backend, for all files or one matter. Run copies them; nothing moves until then.
func (s *Service) StartMove(ctx context.Context, p domain.Principal, in MoveRequest) (MoveView, error) {
	if err := admin(p); err != nil {
		return MoveView{}, err
	}
	if in.FromBackendID == in.ToBackendID {
		return MoveView{}, domain.Invalid("to_backend_id", "pick a different backend")
	}
	for _, id := range []domain.ID{in.FromBackendID, in.ToBackendID} {
		if _, err := s.loadBackend(ctx, id); err != nil {
			return MoveView{}, err
		}
	}
	switch in.Scope {
	case "all":
		in.MatterID = ""
	case "matter":
		if _, err := s.dir.Matter(ctx, in.MatterID); err != nil {
			return MoveView{}, domain.Invalid("matter_id", "a matter move needs an existing matter")
		}
	default:
		return MoveView{}, domain.Invalid("scope", "scope is all or matter")
	}
	objs, err := s.objectsOn(ctx, in.FromBackendID, in.MatterID)
	if err != nil {
		return MoveView{}, err
	}
	meta, err := s.meta(actor(p, domain.SystemUserID))
	if err != nil {
		return MoveView{}, err
	}
	m := moveRow{StorageMove: domain.StorageMove{
		Meta: meta, FromBackendID: in.FromBackendID, ToBackendID: in.ToBackendID, Scope: in.Scope, MatterID: in.MatterID, Status: "running",
	}, Total: len(objs)}
	err = s.tx(ctx, func(ctx context.Context) error {
		existing, err := s.st.moves(ctx)
		if err != nil {
			return err
		}
		for _, mv := range existing {
			if (mv.Status == "running" || mv.Status == "checked") && (mv.FromBackendID == in.FromBackendID || mv.ToBackendID == in.FromBackendID) {
				return domain.Conflict("finish or confirm the move already running on this backend first")
			}
		}
		if err := s.st.insertMove(ctx, m); err != nil {
			return err
		}
		for _, o := range objs {
			it := moveItem{MoveID: m.ID, ObjectID: o.obj.ID, ObjectKind: o.obj.Kind, FromBackendID: o.loc.Backend, FromKey: o.loc.Key,
				SizeBytes: o.obj.Size, SHA256: o.obj.SHA, Status: "pending"}
			if err := s.st.insertMoveItem(ctx, it); err != nil {
				return err
			}
		}
		return s.record(ctx, p, "storage_move", m.ID, "status", "", fmt.Sprintf("running %d objects", len(objs)))
	})
	if err != nil {
		return MoveView{}, err
	}
	return s.moveView(m), nil
}

type placed struct {
	obj object
	loc location
}

// objectsOn lists objects whose current bytes are on backend.
// ponytail: ceiling: one location lookup per object. Upgrade path: join
// file_object_locations in one query. trigger: a move or backup lists over 100k objects.
func (s *Service) objectsOn(ctx context.Context, backend, matter domain.ID) ([]placed, error) {
	vs, err := s.st.allVersions(ctx, matter)
	if err != nil {
		return nil, err
	}
	var objs []object
	for _, v := range vs {
		objs = append(objs, versionObject(v))
	}
	rs, err := s.st.renditionsOf(ctx, "", matter)
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		v, err := s.st.getVersion(ctx, r.FileVersionID)
		if err != nil {
			return nil, err
		}
		objs = append(objs, renditionObject(r, v.FileID))
	}
	var out []placed
	for _, o := range objs {
		loc, err := s.st.location(ctx, o.ID, o.Home)
		if err != nil {
			return nil, err
		}
		if loc.Backend == backend {
			out = append(out, placed{obj: o, loc: loc})
		}
	}
	return out, nil
}

// RunMove copies each pending object to the target backend, checks its SHA-256 there,
// and only then points the record at the new copy. Files stay readable from the old
// copy until that point and from the new one after it. A stopped run resumes at the
// first object not yet checked. Old copies stay until ConfirmMove.
// ponytail: ceiling: the run is a goroutine the core resumes at start (ResumeMoves),
// not a Postgres job-queue job. Upgrade path: enqueue moves on the core job queue.
// trigger: the job queue package lands or a second core process serves the database.
func (s *Service) RunMove(ctx context.Context, id domain.ID) error {
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return domain.Conflict("this move is already running")
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()
	m, err := s.st.getMove(ctx, id)
	if err != nil {
		return err
	}
	if m.Status != "running" && m.Status != "pending" {
		return nil
	}
	items, err := s.st.moveItems(ctx, id)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Status != "pending" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.moveOne(ctx, m, it); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			m.Status, m.UpdatedAt = "failed", s.now().UTC()
			_ = s.st.updateMove(context.WithoutCancel(ctx), m)
			return fmt.Errorf("files: move %s stopped at %s %s: %w", m.ID, it.ObjectKind, it.ObjectID, err)
		}
		m.Progress++
		if s.afterMoveItem != nil {
			s.afterMoveItem(m.Progress)
		}
	}
	m, err = s.st.getMove(ctx, id)
	if err != nil {
		return err
	}
	m.Status, m.UpdatedAt = "checked", s.now().UTC()
	return s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.updateMove(ctx, m); err != nil {
			return err
		}
		return s.record(ctx, domain.SystemPrincipal(), "storage_move", m.ID, "status", "running", "checked")
	})
}

func (s *Service) moveOne(ctx context.Context, m moveRow, it moveItem) error {
	o := object{ID: it.ObjectID, Kind: it.ObjectKind, Size: it.SizeBytes, SHA: it.SHA256}
	from, err := s.st.location(ctx, it.ObjectID, location{Backend: it.FromBackendID, Key: it.FromKey})
	if err != nil {
		return err
	}
	if from.Backend == m.FromBackendID {
		src, err := s.at(ctx, from)
		if err != nil {
			return err
		}
		body, err := storage.OpenVerified(ctx, src, from.Key, o.Size, o.SHA)
		if err != nil {
			return err
		}
		body.OnMismatch = func(err error) { s.alert(context.WithoutCancel(ctx), from.Backend, o, err) }
		to, err := s.putOn(ctx, m.ToBackendID, string(it.ObjectID), body, o.Size)
		_ = body.Close()
		if errors.Is(err, storage.ErrExists) {
			// An earlier run copied it but stopped before the record moved. Keep a
			// copy that checks out; replace one that does not.
			_, row, aerr := s.adapter(ctx, m.ToBackendID)
			if aerr != nil {
				return aerr
			}
			to = location{Backend: m.ToBackendID, Key: string(it.ObjectID), Sealed: sealsNew(row), known: true}
			dst, aerr := s.at(ctx, to)
			if aerr != nil {
				return aerr
			}
			if storage.Check(ctx, dst, to.Key, o.Size, o.SHA) != nil {
				s.dropObject(ctx, to)
				return s.moveOne(ctx, m, it)
			}
			err = nil
		}
		if err != nil {
			return err
		}
		dst, err := s.at(ctx, to)
		if err != nil {
			return err
		}
		if err := storage.Check(ctx, dst, to.Key, o.Size, o.SHA); err != nil {
			s.dropObject(ctx, to)
			return fmt.Errorf("the copy on the new backend did not check out: %w", err)
		}
		from = to
	}
	return s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.setLocation(ctx, it.ObjectID, it.ObjectKind, from, us(s.now())); err != nil {
			return err
		}
		if err := s.st.setMoveItem(ctx, m.ID, it.ObjectID, "checked"); err != nil {
			return err
		}
		cur, err := s.st.getMove(ctx, m.ID)
		if err != nil {
			return err
		}
		cur.Progress++
		cur.UpdatedAt = s.now().UTC()
		return s.st.updateMove(ctx, cur)
	})
}

// ResumeMoves restarts every move that was running when the core stopped.
func (s *Service) ResumeMoves(ctx context.Context) error {
	rows, err := s.st.moves(ctx)
	if err != nil {
		return err
	}
	for _, m := range rows {
		if m.Status == "running" || m.Status == "pending" {
			go func(id domain.ID) { _ = s.RunMove(ctx, id) }(m.ID)
		}
	}
	return nil
}

// ConfirmMove is the admin's go-ahead after the whole move checked out. Only then
// are the old copies deleted.
func (s *Service) ConfirmMove(ctx context.Context, p domain.Principal, id domain.ID) (MoveView, error) {
	if err := admin(p); err != nil {
		return MoveView{}, err
	}
	m, err := s.loadMove(ctx, id)
	if err != nil {
		return MoveView{}, err
	}
	if m.Status == "confirmed" {
		return s.moveView(m), nil
	}
	if m.Status != "checked" {
		return MoveView{}, domain.Conflict("a move is confirmed only after every file checks out on the new backend")
	}
	items, err := s.st.moveItems(ctx, id)
	if err != nil {
		return MoveView{}, err
	}
	for _, it := range items {
		if it.Status == "pending" {
			return MoveView{}, domain.Conflict("an object has not checked out on the new backend")
		}
	}
	for _, it := range items {
		if it.Status != "checked" {
			continue
		}
		raw, _, err := s.adapter(ctx, it.FromBackendID)
		if err != nil {
			return MoveView{}, err
		}
		if err := raw.Delete(ctx, it.FromKey); err != nil {
			return MoveView{}, err
		}
		if err := s.st.setMoveItem(ctx, id, it.ObjectID, "old_deleted"); err != nil {
			return MoveView{}, err
		}
	}
	m.Status, m.ConfirmedBy, m.ConfirmedAt, m.UpdatedAt = "confirmed", p.UserID, us(s.now()), s.now().UTC()
	err = s.tx(ctx, func(ctx context.Context) error {
		if err := s.st.updateMove(ctx, m); err != nil {
			return err
		}
		return s.record(ctx, p, "storage_move", m.ID, "status", "checked", "confirmed")
	})
	if err != nil {
		return MoveView{}, err
	}
	return s.moveView(m), nil
}

func (s *Service) loadMove(ctx context.Context, id domain.ID) (moveRow, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return moveRow{}, domain.NotFound("storage move")
	}
	m, err := s.st.getMove(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return moveRow{}, domain.NotFound("storage move")
	}
	return m, err
}

// Moves lists storage moves. Admin only.
func (s *Service) Moves(ctx context.Context, p domain.Principal) ([]MoveView, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	rows, err := s.st.moves(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MoveView, 0, len(rows))
	for _, m := range rows {
		out = append(out, s.moveView(m))
	}
	return out, nil
}

// GetMove reads one move's progress. Admin only.
func (s *Service) GetMove(ctx context.Context, p domain.Principal, id domain.ID) (MoveView, error) {
	if err := admin(p); err != nil {
		return MoveView{}, err
	}
	m, err := s.loadMove(ctx, id)
	if err != nil {
		return MoveView{}, err
	}
	return s.moveView(m), nil
}
