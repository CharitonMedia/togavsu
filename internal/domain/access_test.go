package domain

import (
	"context"
	"errors"
	"testing"
)

func principal(roles ...Role) Principal {
	return Principal{UserID: "user-1", Roles: roles, Source: SourceUI}
}

func TestDecide(t *testing.T) {
	matter := ID("matter-1")
	cases := []struct {
		name string
		p    Principal
		in   AccessInput
		want Decision
	}{
		{"anon", Principal{}, AccessInput{Resource: ResMatter, Action: ActionRead}, Decision{Reason: ReasonUnauthenticated}},
		{"admin settings", principal(RoleAdmin), AccessInput{Resource: ResSettings, Action: ActionWrite}, Decision{Allow: true}},
		{"admin off wall", principal(RoleAdmin), AccessInput{Resource: ResMatter, Action: ActionRead, MatterID: matter, Walled: true}, Decision{Reason: ReasonMatterWall}},
		{"admin on wall", principal(RoleAdmin), AccessInput{Resource: ResMatter, Action: ActionRead, MatterID: matter, Walled: true, WallAllows: true}, Decision{Allow: true}},
		{"staff walled", principal(RoleStaff), AccessInput{Resource: ResFile, Action: ActionRead, MatterID: matter, Walled: true}, Decision{Reason: ReasonMatterWall}},
		{"attorney withdraw", principal(RoleAttorney), AccessInput{Resource: ResTrust, Action: ActionTrustWithdraw, MatterID: matter}, Decision{Allow: true}},
		{"staff withdraw", principal(RoleStaff), AccessInput{Resource: ResTrust, Action: ActionTrustWithdraw, MatterID: matter}, Decision{Reason: ReasonRole}},
		{"staff deposit", principal(RoleStaff), AccessInput{Resource: ResTrust, Action: ActionTrustDeposit, MatterID: matter}, Decision{Allow: true}},
		{"staff trust write", principal(RoleStaff), AccessInput{Resource: ResTrust, Action: ActionWrite, MatterID: matter}, Decision{Reason: ReasonRole}},
		{"staff conflict", principal(RoleStaff), AccessInput{Resource: ResConflictReason, Action: ActionConflictDecide}, Decision{Reason: ReasonRole}},
		{"staff reason redact", principal(RoleStaff), AccessInput{Resource: ResConflictReason, Action: ActionRead}, Decision{Allow: true, Redact: true, Reason: ReasonAttorneyOnly}},
		{"attorney reason", principal(RoleAttorney), AccessInput{Resource: ResConflictReason, Action: ActionRead}, Decision{Allow: true}},
		{"staff ssn redact", principal(RoleStaff), AccessInput{Resource: ResSensitiveContact, Action: ActionRead}, Decision{Allow: true, Redact: true, Reason: ReasonAttorneyOnly}},
		{"staff ssn write", principal(RoleStaff), AccessInput{Resource: ResSensitiveContact, Action: ActionWrite}, Decision{Reason: ReasonAttorneyOnly}},
		{"accountant read invoice", principal(RoleAccountant), AccessInput{Resource: ResInvoice, Action: ActionRead}, Decision{Allow: true}},
		{"accountant write invoice", principal(RoleAccountant), AccessInput{Resource: ResInvoice, Action: ActionWrite}, Decision{Reason: ReasonRole}},
		{"accountant note", principal(RoleAccountant), AccessInput{Resource: ResNote, Action: ActionRead}, Decision{Reason: ReasonRole}},
		{"accountant receipt", principal(RoleAccountant), AccessInput{Resource: ResFile, Action: ActionRead, FileClass: FileReceipt}, Decision{Allow: true}},
		{"accountant pdf", principal(RoleAccountant), AccessInput{Resource: ResFile, Action: ActionRead, FileClass: FileInvoicePDF}, Decision{Allow: true}},
		{"accountant file", principal(RoleAccountant), AccessInput{Resource: ResFile, Action: ActionRead, FileClass: FileGeneral}, Decision{Reason: ReasonRole}},
		{"accountant trust", principal(RoleAccountant), AccessInput{Resource: ResTrust, Action: ActionRead}, Decision{Allow: true}},
		{"accountant report", principal(RoleAccountant), AccessInput{Resource: ResBillingReport, Action: ActionRead}, Decision{Allow: true}},
		{"client wall", principal(RoleClient), AccessInput{Resource: ResFile, Action: ActionRead, MatterID: matter, Walled: true, ClientMatter: true, SharedWithClient: true}, Decision{Reason: ReasonMatterWall}},
		{"client shared", principal(RoleClient), AccessInput{Resource: ResFile, Action: ActionRead, MatterID: matter, ClientMatter: true, SharedWithClient: true}, Decision{Allow: true}},
		{"client hidden", principal(RoleClient), AccessInput{Resource: ResFile, Action: ActionRead, MatterID: matter, ClientMatter: true}, Decision{Reason: ReasonClientScope}},
		{"client other matter", principal(RoleClient), AccessInput{Resource: ResMatter, Action: ActionRead, MatterID: matter, SharedWithClient: true}, Decision{Reason: ReasonClientScope}},
		{"client write", principal(RoleClient), AccessInput{Resource: ResNote, Action: ActionWrite, MatterID: matter, ClientMatter: true, SharedWithClient: true}, Decision{Reason: ReasonRole}},
		{"client trust", principal(RoleClient), AccessInput{Resource: ResTrust, Action: ActionRead, MatterID: matter, ClientMatter: true, SharedWithClient: true}, Decision{Reason: ReasonRole}},
		{"attorney settings write", principal(RoleAttorney), AccessInput{Resource: ResSettings, Action: ActionWrite}, Decision{Reason: ReasonRole}},
		{"staff approve", principal(RoleStaff), AccessInput{Resource: ResInvoice, Action: ActionApproveInvoice}, Decision{Reason: ReasonRole}},
		{"attorney approve", principal(RoleAttorney), AccessInput{Resource: ResInvoice, Action: ActionApproveInvoice}, Decision{Allow: true}},
		{"union write", principal(RoleAccountant, RoleAttorney), AccessInput{Resource: ResInvoice, Action: ActionWrite}, Decision{Allow: true}},
		{"system wall", SystemPrincipal(), AccessInput{Resource: ResMatter, Action: ActionRead, MatterID: matter, Walled: true}, Decision{Allow: true, Reason: ReasonSystem}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.p, tc.in)
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

type fakeWalls struct {
	view WallView
	err  error
}

func (f fakeWalls) View(context.Context, Principal, ID) (WallView, error) {
	return f.view, f.err
}

func TestAuthorize(t *testing.T) {
	p := Policy{}
	if _, err := p.Authorize(context.Background(), principal(RoleAdmin), ResourceRef{Resource: ResMatter, Action: ActionRead, MatterID: "m"}); !errors.Is(err, ErrWallReader) {
		t.Fatal(err)
	}
	p.Walls = fakeWalls{view: WallView{Walled: true, Allows: false}}
	d, err := p.Authorize(context.Background(), principal(RoleStaff), ResourceRef{Resource: ResMatter, Action: ActionRead, MatterID: "m"})
	if err != nil || d.Allow || d.Reason != ReasonMatterWall {
		t.Fatal(d, err)
	}
	p.Walls = fakeWalls{err: errors.New("db down")}
	if _, err := p.Authorize(context.Background(), principal(RoleAdmin), ResourceRef{Resource: ResMatter, Action: ActionRead, MatterID: "m"}); err == nil {
		t.Fatal("expected reader error")
	}
	d, err = p.Authorize(context.Background(), principal(RoleAdmin), ResourceRef{Resource: ResSettings, Action: ActionWrite})
	if err != nil || !d.Allow {
		t.Fatal(d, err)
	}
}
