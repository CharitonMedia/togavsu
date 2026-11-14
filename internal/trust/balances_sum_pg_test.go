package trust

import (
	"context"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGBalancesSumMatchesEntries: Balances sums the ledger per (account, client, matter)
// in Postgres and checks each group's matter (AC-017 performance). For a reader outside a
// wall, a wall member and the admin, with and without filters, the rows must equal the
// entry-by-entry computation it replaced, including a group whose entries net to zero, and
// the walled matter's money must not reach the client or account rows (AC-003, PRD 8.4).
func TestPGBalancesSumMatchesEntries(t *testing.T) {
	e := newEnv(t)
	m2 := e.newMatter("2026-0002", "Walled Synthetic", "hourly", 0, e.clientID)
	m3 := e.newMatter("2026-0003", "Nets To Zero", "hourly", 0, e.clientID)
	e.wall(m2.ID, e.other.UserID, e.admin.UserID)
	on := func(m domain.ID, typ string, cents int64) PostInput {
		in := e.in(typ, cents)
		in.MatterID = m
		return in
	}
	e.post(e.staff, e.in(TypeDeposit, 120000))
	e.post(e.attorney, e.in(TypeWithdrawal, 20000))
	e.post(e.other, on(m2.ID, TypeDeposit, 512347))
	e.post(e.staff, on(m3.ID, TypeDeposit, 7000))
	e.post(e.attorney, on(m3.ID, TypeWithdrawal, 7000))

	for name, who := range map[string]domain.Principal{"staff": e.staff, "wall member": e.other, "admin": e.admin} {
		for _, f := range []EntryFilter{{}, {ClientID: e.clientID}, {MatterID: m2.ID}, {MatterID: m3.ID}} {
			got, err := e.svc.Balances(e.ctx, who, f)
			if err != nil {
				t.Fatal(name, err)
			}
			want, err := balancesFromEntries(e.ctx, e.svc, who, f)
			if err != nil {
				t.Fatal(name, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %+v:\n got %+v\nwant %+v", name, f, got, want)
			}
		}
	}
	rows, err := e.svc.Balances(e.ctx, e.staff, EntryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var zero bool
	for _, r := range rows {
		if r.MatterID == m2.ID {
			t.Fatalf("staff sees the walled matter's row %+v", r)
		}
		if r.Scope != ScopeMatter && r.BalanceCents != 100000 {
			t.Fatalf("staff %s total %d includes the walled matter", r.Scope, r.BalanceCents)
		}
		zero = zero || (r.MatterID == m3.ID && r.BalanceCents == 0)
	}
	if !zero {
		t.Fatalf("the matter that nets to zero lost its row: %+v", rows)
	}
}

// balancesFromEntries is Balances as it was before the grouped sum: every visible entry
// read and summed in Go, rows ordered by joined ids.
func balancesFromEntries(ctx context.Context, s *Service, who domain.Principal, f EntryFilter) ([]BalanceRow, error) {
	entries, err := s.visibleEntries(ctx, who, EntryFilter{MatterID: f.MatterID, ClientID: f.ClientID, TrustAccountID: f.TrustAccountID})
	if err != nil {
		return nil, err
	}
	type key struct{ a, c, m domain.ID }
	matter, client, account := map[key]int64{}, map[domain.ID]int64{}, map[domain.ID]int64{}
	for _, e := range entries {
		matter[key{e.TrustAccountID, e.ClientID, e.MatterID}] += int64(e.Signed)
		client[e.ClientID] += int64(e.Signed)
		account[e.TrustAccountID] += int64(e.Signed)
	}
	var rows []BalanceRow
	for k, v := range matter {
		rows = append(rows, BalanceRow{Scope: ScopeMatter, TrustAccountID: k.a, ClientID: k.c, MatterID: k.m, BalanceCents: v})
	}
	for k, v := range client {
		rows = append(rows, BalanceRow{Scope: ScopeClient, ClientID: k, BalanceCents: v})
	}
	for k, v := range account {
		rows = append(rows, BalanceRow{Scope: ScopeAccount, TrustAccountID: k, BalanceCents: v})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Scope != b.Scope {
			return scopeRank(a.Scope) < scopeRank(b.Scope)
		}
		return string(a.TrustAccountID)+string(a.ClientID)+string(a.MatterID) < string(b.TrustAccountID)+string(b.ClientID)+string(b.MatterID)
	})
	return rows, nil
}

// TestConcatLessMatchesJoinedStrings: concatLess orders as comparing the joined strings,
// for empty parts, parts of different lengths and shared prefixes.
func TestConcatLessMatchesJoinedStrings(t *testing.T) {
	join := func(p [3]domain.ID) string { return string(p[0]) + string(p[1]) + string(p[2]) }
	cases := [][2][3]domain.ID{
		{{"", "", ""}, {"", "", ""}},
		{{"a", "", ""}, {"", "", "a"}},
		{{"ab", "c", ""}, {"a", "bc", ""}},
		{{"ab", "c", ""}, {"a", "bd", ""}},
		{{"a", "", ""}, {"a", "", "b"}},
		{{"b", "", ""}, {"a", "zz", "z"}},
	}
	r := rand.New(rand.NewSource(17))
	part := func() domain.ID {
		b := make([]byte, r.Intn(4))
		for i := range b {
			b[i] = "ab"[r.Intn(2)]
		}
		return domain.ID(b)
	}
	for i := 0; i < 5000; i++ {
		cases = append(cases, [2][3]domain.ID{{part(), part(), part()}, {part(), part(), part()}})
	}
	for _, c := range cases {
		for _, ab := range [][2][3]domain.ID{c, {c[1], c[0]}} {
			if got, want := concatLess(ab[0], ab[1]), join(ab[0]) < join(ab[1]); got != want {
				t.Fatalf("concatLess(%q, %q) = %v, joined strings say %v", ab[0], ab[1], got, want)
			}
		}
	}
}
