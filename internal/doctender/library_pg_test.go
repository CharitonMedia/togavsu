package doctender

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// TestPGLibrarySkipsDocTender covers CORE-17: no job, no exception, no tokens, status
// not_processed_library, nightly sweep skip, while file.version_created still fires.
func TestPGLibrarySkipsDocTender(t *testing.T) {
	e := newEnv(t)
	beforeEvents := e.countEvents(`action = 'file.version_created'`)

	lib, err := e.files.Create(e.ctx, e.staff, files.NewFile{Name: "handbook.pdf", Library: true, How: "upload"},
		strings.NewReader("firm handbook"), int64(len("firm handbook")))
	if err != nil || !lib.Library {
		t.Fatalf("create library = %+v %v", lib, err)
	}
	e.drain()

	var jobs int
	if err := e.pool.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM doc_tender_jobs WHERE file_id = $1`, string(lib.ID)).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("library jobs = %d %v", jobs, err)
	}
	var exceptions int
	if err := e.pool.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM doc_tender_exceptions WHERE file_id = $1`, string(lib.ID)).Scan(&exceptions); err != nil || exceptions != 0 {
		t.Fatalf("library exceptions = %d %v", exceptions, err)
	}
	if n := e.aiCalls(`1=1`); n != 0 {
		t.Fatalf("AI calls after library = %d", n)
	}
	if after := e.countEvents(`action = 'file.version_created'`); after != beforeEvents+1 {
		t.Fatalf("file.version_created count %d -> %d", beforeEvents, after)
	}

	st, err := e.svc.Status(e.ctx, e.staff, lib.ID)
	if err != nil || st.Status != StatusLibrary || st.Label != "Not processed (library)" || len(st.Flags) != 0 {
		t.Fatalf("status = %+v %v", st, err)
	}
	if _, err := e.svc.Reprocess(e.ctx, e.attorney, lib.ID); !isCode(err, domain.CodeValidation) {
		t.Fatalf("reprocess library = %v", err)
	}

	n, err := e.svc.Sweep(e.ctx)
	if err != nil || n != 0 {
		t.Fatalf("sweep queued %d library files (%v)", n, err)
	}
	if err := e.pool.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM doc_tender_jobs WHERE file_id = $1`, string(lib.ID)).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs after sweep = %d %v", jobs, err)
	}
}

func (e *env) countEvents(where string) int {
	e.t.Helper()
	var n int
	q := `SELECT COUNT(*) FROM events WHERE ` + where
	if err := e.pool.QueryRowContext(e.ctx, q).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
