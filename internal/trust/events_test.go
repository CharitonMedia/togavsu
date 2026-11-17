package trust

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestEntryPostedSummaryAndStampActor(t *testing.T) {
	matter := domain.Matter{Meta: domain.Meta{ID: "mat_1"}, Name: "Estate of Roe", Number: "2024-001"}
	e := Entry{ID: "ent_1", EntryType: TypeDeposit, Signed: 125_00, AmountCents: 125_00, MatterID: matter.ID}
	sum := entryPostedSummary(e, matter)
	if !strings.Contains(sum, "Trust deposit") || !strings.Contains(sum, "$125.00") || !strings.Contains(sum, "Estate of Roe") {
		t.Fatalf("summary: %q", sum)
	}

	who := domain.Principal{UserID: "usr_staff", Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}
	ev := stampActor(who, Event{
		Name: "trust.entry_posted", RecordID: e.ID, MatterID: matter.ID, Summary: sum,
		Data: map[string]string{"entry_type": TypeDeposit, "amount_cents": "125.00"},
	})
	if ev.ActorKind != "staff" || ev.ActorID != "usr_staff" || ev.Summary != sum {
		t.Fatalf("stamped: %+v", ev)
	}

	flag := stampActor(who, Event{Name: "trust.flag_raised", RecordID: "flg_1", MatterID: matter.ID})
	if flag.ActorKind != "staff" || flag.Summary != "Trust balance flag raised" {
		t.Fatalf("flag stamp: %+v", flag)
	}

	sys := stampActor(domain.Principal{Source: domain.SourceSystem}, Event{Name: "trust_account.created", RecordID: "acct_1"})
	if sys.ActorKind != "system" || sys.ActorID != domain.SystemUserID || sys.Summary != "Trust account created" {
		t.Fatalf("system stamp: %+v", sys)
	}
}
