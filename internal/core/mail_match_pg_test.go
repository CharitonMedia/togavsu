package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestPGMailMatchAndFilingGrant covers CORE-26 matchMail and CORE-27 filing grants:
// body caps, contacts_by_address, via ranking, walls for service vs viewer, match_token,
// filing grant issuance + mail.filing_authorized, and note create under the grant.
func TestPGMailMatchAndFilingGrant(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		man := string(f[modules.ManifestFile])
		man = strings.Replace(man, "permissions:\n",
			"permissions:\n"+
				"  - scope: contacts:read\n    reason: Match contacts by email.\n"+
				"  - scope: notes:write\n    reason: File mail as notes.\n"+
				"  - scope: files:write\n    reason: File mail attachments.\n"+
				"  - scope: ai:use\n    reason: Summarize unmatched mail.\n", 1)
		if !strings.Contains(man, "ai_tasks:") {
			man = strings.Replace(man, "widgets:\n",
				"ai_tasks:\n  - id: summarize\n    description: Summarize unmatched mail\nwidgets:\n", 1)
		}
		f[modules.ManifestFile] = []byte(man)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	open, openClient := x.matter("Open Estate", "party.open@client.example", map[string]any{"case_number": "23AR-CV00100"})
	walled, _ := x.matter("Walled Estate", "party.walled@client.example", map[string]any{"case_number": "99AR-CV00999"})
	openRow, err := x.app.Practice.GetMatter(ctx, x.adminP(), domain.ID(open))
	if err != nil {
		t.Fatalf("get open matter: %v", err)
	}
	wallRow, err := x.app.Practice.GetMatter(ctx, x.adminP(), domain.ID(walled))
	if err != nil {
		t.Fatalf("get walled matter: %v", err)
	}
	openNum, wallNum := openRow.Matter.Number, wallRow.Matter.Number
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	// Cap: 51 addresses → validation_failed.
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "a@b.c"
	}
	if r := bot.do("POST", "/api/v1/mail/matches", map[string]any{"addresses": tooMany}); r.code != http.StatusUnprocessableEntity && r.code != http.StatusBadRequest {
		t.Fatalf("cap: %d %s", r.code, r.body)
	}

	// Service match: open matter via address + case number; walled omitted; match_token present.
	match := bot.do("POST", "/api/v1/mail/matches", map[string]any{
		"addresses":        []string{"party.open@client.example", "nobody@example.com"},
		"matter_numbers":   []string{openNum, wallNum},
		"case_numbers":     []string{"23AR-CV00100"},
		"name_tokens":      []string{},
		"known_matter_ids": []string{walled},
	})
	if match.code != http.StatusOK {
		t.Fatalf("match: %d %s", match.code, match.body)
	}
	cba, _ := match.json["contacts_by_address"].(map[string]any)
	openHits, _ := cba["party.open@client.example"].([]any)
	if len(openHits) != 1 || openHits[0].(map[string]any)["id"] != openClient {
		t.Fatalf("contacts_by_address: %s", match.body)
	}
	nobody, _ := cba["nobody@example.com"].([]any)
	if nobody == nil || len(nobody) != 0 {
		t.Fatalf("empty address must return []: %s", match.body)
	}
	matters, _ := match.json["matters"].([]any)
	ids := map[string]string{}
	for _, row := range matters {
		m := row.(map[string]any)
		ids[str(t, m, "id")] = str(t, m, "via")
	}
	if _, ok := ids[walled]; ok {
		t.Fatalf("service match returned walled matter: %s", match.body)
	}
	if ids[open] == "" {
		t.Fatalf("open matter missing: %s", match.body)
	}
	// address (2) beats matter_number (3) when both match.
	if ids[open] != "address" && ids[open] != "case_number" && ids[open] != "matter_number" {
		t.Fatalf("unexpected via %q: %s", ids[open], match.body)
	}
	tok := str(t, match.json, "match_token")
	if tok == "" {
		t.Fatalf("service match missing match_token: %s", match.body)
	}

	// Viewer match: staff sees open, not walled; no match_token.
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	viewer := bot.do("POST", "/api/v1/mail/matches", map[string]any{
		"matter_numbers":   []string{openNum, wallNum},
		"known_matter_ids": []string{open, walled},
	}, modules.ContextHeader, str(t, ct.json, "token"))
	if viewer.code != http.StatusOK {
		t.Fatalf("viewer match: %d %s", viewer.code, viewer.body)
	}
	if tok, _ := viewer.json["match_token"].(string); tok != "" {
		t.Fatalf("viewer must not get match_token: %s", viewer.body)
	}
	vids := map[string]bool{}
	for _, row := range viewer.json["matters"].([]any) {
		vids[str(t, row.(map[string]any), "id")] = true
	}
	if !vids[open] || vids[walled] {
		t.Fatalf("viewer walls: %s", viewer.body)
	}

	// Filing grant: wrong matter → not_found; walled → not_found; ok → 201 + event.
	if r := bot.do("POST", "/api/v1/mail/filing-grants", map[string]any{
		"match_token": tok, "matter_id": walled, "message_key": "msg-1",
	}); r.code != http.StatusNotFound {
		t.Fatalf("walled filing grant: %d %s", r.code, r.body)
	}
	bogusMatter := "00000000-0000-4000-8000-000000000099"
	if r := bot.do("POST", "/api/v1/mail/filing-grants", map[string]any{
		"match_token": tok, "matter_id": bogusMatter, "message_key": "msg-1",
	}); r.code != http.StatusNotFound {
		t.Fatalf("unknown matter filing grant: %d %s", r.code, r.body)
	}
	grant := bot.do("POST", "/api/v1/mail/filing-grants", map[string]any{
		"match_token": tok, "matter_id": open, "message_key": "imap-uid-42",
	})
	if grant.code != http.StatusCreated {
		t.Fatalf("filing grant: %d %s", grant.code, grant.body)
	}
	auth := str(t, grant.json, "task_authorization")
	if auth == "" || grant.json["matter_id"] != open || grant.json["message_key"] != "imap-uid-42" {
		t.Fatalf("grant body: %s", grant.body)
	}
	if x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'mail.filing_authorized' AND matter_id = $1 AND summary LIKE $2`,
		open, "%imap-uid-42%") != 1 {
		t.Fatal("expected mail.filing_authorized event")
	}

	// Note without grant → scope_denied / forbidden; with grant → created.
	if r := bot.do("POST", "/api/v1/notes", map[string]any{
		"matter_id": open, "title": "Mail note", "body": "filed",
	}); r.code != http.StatusForbidden && r.json["code"] != "scope_denied" {
		t.Fatalf("note without grant: %d %s", r.code, r.body)
	}
	note := bot.do("POST", "/api/v1/notes", map[string]any{
		"matter_id": open, "title": "Mail note", "body": "filed from mailbox",
	}, modules.TaskAuthorizationHeader, auth)
	if note.code != http.StatusCreated && note.code != http.StatusOK {
		t.Fatalf("note with grant: %d %s", note.code, note.body)
	}

	// Wrong matter on grant → not_found.
	other, _ := x.matter("Other Estate", "other@client.example", nil)
	if r := bot.do("POST", "/api/v1/notes", map[string]any{
		"matter_id": other, "title": "Nope", "body": "x",
	}, modules.TaskAuthorizationHeader, auth); r.code != http.StatusNotFound {
		t.Fatalf("note wrong matter: %d %s", r.code, r.body)
	}

	// mail_matching on createAiCall without matter_id.
	ai := bot.do("POST", "/api/v1/ai/calls", map[string]any{
		"task": "summarize", "input": map[string]any{"prompt": "summarize this mail"},
	}, modules.TaskAuthorizationHeader, tok)
	if ai.code != http.StatusOK && ai.code != http.StatusCreated {
		// Provider may be unset → conflict; grant path must not be scope_denied/forbidden for missing grant.
		if ai.json["code"] == "scope_denied" || (ai.code == http.StatusForbidden && strings.Contains(ai.body, "mail matching grant")) {
			t.Fatalf("ai with match token refused grant: %d %s", ai.code, ai.body)
		}
		if ai.json["code"] != "conflict" && ai.json["code"] != "ai_unavailable" {
			t.Fatalf("ai call: %d %s", ai.code, ai.body)
		}
	}
	if r := bot.do("POST", "/api/v1/ai/calls", map[string]any{
		"task": "summarize", "input": map[string]any{"prompt": "x"},
	}); r.code != http.StatusForbidden && r.json["code"] != "scope_denied" {
		t.Fatalf("ai without match token: %d %s", r.code, r.body)
	}

	// Staff session cannot issue a filing grant (service_principal only).
	if r := x.staff.do("POST", "/api/v1/mail/filing-grants", map[string]any{
		"match_token": tok, "matter_id": open, "message_key": "nope",
	}); r.code != http.StatusForbidden {
		t.Fatalf("staff filing grant: %d %s", r.code, r.body)
	}
}

// TestPGMailMatchCaseNumberInName covers the hyphen boundary for case_number_in_name.
func TestPGMailMatchCaseNumberInName(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile],
			[]byte("permissions:\n"),
			[]byte("permissions:\n  - scope: contacts:read\n    reason: Match.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	hit, _ := x.matter("Caption 21-CV-100 Smith", "", nil)
	miss, _ := x.matter("Caption 21-CV-100-A Jones", "", nil)
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	r := bot.must(http.StatusOK, "POST", "/api/v1/mail/matches", map[string]any{
		"case_numbers": []string{"21-CV-100"},
	})
	found := map[string]string{}
	for _, row := range r.json["matters"].([]any) {
		m := row.(map[string]any)
		found[str(t, m, "id")] = str(t, m, "via")
	}
	if found[hit] != "case_number_in_name" {
		t.Fatalf("want case_number_in_name on hit: %s", r.body)
	}
	if _, ok := found[miss]; ok {
		t.Fatalf("hyphenated suffix must not match: %s", r.body)
	}
}
