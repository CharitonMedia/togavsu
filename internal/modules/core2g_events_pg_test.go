package modules_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestCoreUpdate2GPumpVisibilityErrorDoesNotStall(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	if _, err := e.svc.Pump(e.ctx); err != nil {
		t.Fatal(err)
	}
	svc, err := modules.New(modules.Deps{
		DB: e.app.Pool, Key: testKey, Audit: e.app.Audit, Settings: e.app.Settings,
		Walls: e.app.Practice.Policy.Walls, Contacts: e.app.Practice, Signing: e.market,
		Users: e.app.Users, Tokens: modules.IdentityTokens{Svc: e.app.Identity}, Runtime: e.rt,
		PaymentReadable: func(context.Context, string, domain.Event) (bool, error) {
			return false, errors.New("synthetic visibility failure")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO core.events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, summary, client_visible)
VALUES ($1,$2,$2,$3,'staff',$3,'matter.created',$4,$1,'synthetic',0)`
	ids := make([]string, 0, 2)
	at := time.Now().UnixMicro()
	for i, recordType := range []string{"payment_refund", "matter"} {
		id, err := domain.NewID()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, string(id))
		if _, err := e.app.Pool.ExecContext(e.ctx, insert, string(id), at+int64(i), string(e.staff.UserID), recordType); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := svc.Pump(e.ctx); err != nil || n != 1 {
		t.Fatalf("pump queued=%d error=%v, want one subsequent event", n, err)
	}
	if e.count("SELECT COUNT(*) FROM core.module_deliveries WHERE event_id=$1", ids[0]) != 0 || e.count("SELECT COUNT(*) FROM core.module_deliveries WHERE event_id=$1", ids[1]) != 1 {
		t.Fatal("visibility failure leaked the unreadable event or blocked its successor")
	}
}
