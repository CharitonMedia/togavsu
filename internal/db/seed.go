package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// ErrAlreadySeeded means synthetic rows are already present.
var ErrAlreadySeeded = errors.New("db: already seeded")

// ponytail: synthetic seed inserts one Missouri county. Upgrade path: first-run loads every county of the firm's state. trigger: setup stores the firm state.

var seedAt = time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// ApplyReference inserts settings defaults and Doc Tender categories.
func ApplyReference(ctx context.Context, db *sql.DB, dialect string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	at := domain.UnixMicroUTC(seedAt)
	by := string(domain.SystemUserID)
	for i, def := range settings.Definitions() {
		if err := ins(ctx, tx, dialect, "settings",
			[]string{"id", "created_at", "updated_at", "created_by", "key", "value"},
			[]any{sid(1000 + uint64(i)), at, at, by, def.Key, def.Default}); err != nil {
			return err
		}
	}
	for i, cat := range domain.DefaultDocCategories() {
		if err := ins(ctx, tx, dialect, "doc_categories",
			[]string{"id", "created_at", "updated_at", "created_by", "name", "description", "tier", "folder", "active"},
			[]any{sid(2000 + uint64(i)), at, at, by, cat.Name, cat.Description, cat.Tier, cat.Folder, bit(cat.Active)}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplySynthetic inserts one synthetic row in every section 5 table.
// It is for acceptance tests. A production first-run does not call it.
func ApplySynthetic(ctx context.Context, db *sql.DB, dialect string) error {
	if err := ApplyReference(ctx, db, dialect); err != nil {
		return err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadySeeded
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertSynthetic(ctx, tx, dialect); err != nil {
		return err
	}
	return tx.Commit()
}

func insertSynthetic(ctx context.Context, tx *sql.Tx, dialect string) error {
	at := domain.UnixMicroUTC(seedAt)
	by := string(domain.SystemUserID)
	later := at + 12*60*60*1_000_000
	n := uint64(0)
	id := func() string {
		n++
		return sid(n)
	}
	pa, county, contact := id(), id(), id()
	admin, staff, clientUser := id(), id(), id()
	referral, matter, party, wall := id(), id(), id(), id()
	session, password, totp, recovery, token := id(), id(), id(), id(), id()
	local, network := id(), id()
	file1, ver1, rend := id(), id(), id()
	file2, ver2, link, lock := id(), id(), id(), id()
	fee, timeEntry, timer, expense := id(), id(), id(), id()
	invoice, line := id(), id()
	account, entry, balance := id(), id(), id()
	flag, repl, recon, payment := id(), id(), id(), id()
	note, noteTag := id(), id()
	conflict, review := id(), id()
	cal, attendee, deadline := id(), id(), id()
	audit, ev, court := id(), id(), id()
	job, unit, result, exc, call, move := id(), id(), id(), id(), id(), id()
	addr, emailID, phoneID, ctag := id(), id(), id(), id()
	// ponytail: 100 percent complete. Written as a product because 100 is also activity.page_size.
	// Ceiling: one synthetic row. Upgrade trigger: seed progress is loaded from the registry.
	progressDone := 10 * 10

	adminRoles, err := domain.MarshalRoles([]domain.Role{domain.RoleAdmin, domain.RoleAttorney})
	if err != nil {
		return err
	}
	staffRoles, err := domain.MarshalRoles([]domain.Role{domain.RoleStaff})
	if err != nil {
		return err
	}
	clientRoles, err := domain.MarshalRoles([]domain.Role{domain.RoleClient})
	if err != nil {
		return err
	}
	meta := func(id string) []any { return []any{id, at, at, by} }
	mcols := []string{"id", "created_at", "updated_at", "created_by"}

	steps := []struct {
		table string
		cols  []string
		vals  []any
	}{
		{"practice_areas", append(mcols, "name", "matter_types", "stages", "active"), append(meta(pa), "Seed practice", "[]", `["intake"]`, 1)},
		{"counties", append(mcols, "name", "state"), append(meta(county), "Seed County", "MO")},
		{"contacts", append(mcols, "kind", "given_name", "middle_name", "family_name", "prefix", "suffix", "full_name", "preferred_name", "also_known_as", "date_of_birth", "date_of_death", "ssn_ein_ciphertext", "entity_type", "formation_state", "formation_date", "archived", "source"),
			append(meta(contact), "person", "Client", "", "Seed", "", "", "Client Seed", "Client", `["Seed Alias"]`, "1990-01-01", "", "enc:seed-ssn", "", "", "", 0, "seed")},
		{"contact_addresses", append(mcols, "contact_id", "label", "is_primary", "line1", "line2", "city", "region", "postal_code", "country", "effective_from", "effective_to"),
			append(meta(addr), contact, "home", 1, "1 Seed Street", "", "Seedville", "MO", "00000", "US", "2026-11-06", "")},
		{"contact_emails", append(mcols, "contact_id", "label", "is_primary", "email", "effective_from", "effective_to"),
			append(meta(emailID), contact, "work", 1, "seed.client@example.com", "2026-11-06", "")},
		{"contact_phones", append(mcols, "contact_id", "label", "is_primary", "phone_e164", "effective_from", "effective_to"),
			append(meta(phoneID), contact, "mobile", 1, "+15555550100", "2026-11-06", "")},
		{"contact_tags", append(mcols, "contact_id", "tag"), append(meta(ctag), contact, "seed")},
		{"users", append(mcols, "name", "email", "roles", "active", "default_hourly_rate_cents", "contact_id", "bar_number", "bar_state"),
			append(meta(admin), "Seed Admin", "seed.admin@example.com", adminRoles, 1, int64(30000), nil, "000000", "MO")},
		{"users", append(mcols, "name", "email", "roles", "active", "default_hourly_rate_cents", "contact_id", "bar_number", "bar_state"),
			append(meta(staff), "Seed Staff", "seed.staff@example.com", staffRoles, 1, int64(15000), nil, "", "")},
		{"users", append(mcols, "name", "email", "roles", "active", "default_hourly_rate_cents", "contact_id", "bar_number", "bar_state"),
			append(meta(clientUser), "Seed Client", "seed.client@example.com", clientRoles, 1, int64(0), contact, "", "")},
		{"referral_sources", append(mcols, "name", "kind", "contact_id", "active"), append(meta(referral), "Seed referral", "person", contact, 1)},
		{"matters", append(mcols, "number", "name", "practice_area_id", "status", "stage", "case_number", "court", "county_id", "client_position", "opened_on", "closed_on", "close_reason", "statute_of_limitations", "billing_type", "flat_fee_cents", "matter_rate_cents", "responsible_attorney_id", "assigned_staff_id", "referral_source_id"),
			append(meta(matter), "2026-0001", "Seed Matter", pa, "open", "intake", "23AR-CV00825", "Seed Court", county, "Petitioner", "2026-11-06", "", "", "", "hourly", int64(150000), int64(30000), admin, staff, referral)},
		{"matter_parties", append(mcols, "matter_id", "contact_id", "role", "is_primary", "role_attributes", "service_address", "notes"),
			append(meta(party), matter, contact, "client", 1, "{}", "", "")},
		{"matter_walls", append(mcols, "matter_id", "user_id"), append(meta(wall), matter, admin)},
		{"sessions", append(mcols, "user_id", "token_hash", "expires_at", "idle_expires_at", "revoked_at"),
			append(meta(session), admin, "hash:seed-session", later, later, nil)},
		{"password_credentials", append(mcols, "user_id", "argon2_hash"), append(meta(password), admin, "argon2id-seed-placeholder")},
		{"totp_secrets", append(mcols, "user_id", "ciphertext", "confirmed"), append(meta(totp), admin, "enc:seed-totp", 1)},
		{"recovery_codes", append(mcols, "user_id", "code_hash", "used_at"), append(meta(recovery), admin, "hash:seed-recovery", nil)},
		{"api_tokens", append(mcols, "user_id", "module_id", "name", "token_hash", "scopes", "revoked_at"),
			append(meta(token), admin, "", "seed", "hash:seed-token", `["matters:read"]`, nil)},
		{"storage_backends", append(mcols, "kind", "name", "settings_ciphertext", "encryption", "is_default", "health", "backed_up", "drop_folder"),
			append(meta(local), "local_disk", "Local", "enc:seed-backend", 1, 1, "ok", 1, 0)},
		{"storage_backends", append(mcols, "kind", "name", "settings_ciphertext", "encryption", "is_default", "health", "backed_up", "drop_folder"),
			append(meta(network), "network_path", "Network", "enc:seed-backend", 1, 0, "ok", 1, 1)},
		{"files", append(mcols, "name", "extension", "matter_id", "contact_id", "folder", "category", "status", "origin", "attorney_only", "shared_with_client", "source", "arrived_name", "locked_fields", "current_version_id", "trashed_at", "trashed_by"),
			append(meta(file1), "seed-letter.txt", "txt", matter, contact, "Correspondence", "correspondence", "received", "firm", 0, 1, "upload", "upload.txt", "[]", ver1, int64(0), nil)},
		{"files", append(mcols, "name", "extension", "matter_id", "contact_id", "folder", "category", "status", "origin", "attorney_only", "shared_with_client", "source", "arrived_name", "locked_fields", "current_version_id", "trashed_at", "trashed_by"),
			append(meta(file2), "seed-letter.pdf", "pdf", matter, contact, "Correspondence", "correspondence", "final", "firm", 0, 1, "upload", "upload.pdf", "[]", ver2, int64(0), nil)},
		{"file_versions", append(mcols, "file_id", "number", "storage_backend_id", "storage_key", "size_bytes", "sha256", "media_type", "saved_by", "saved_at", "how", "note", "quarantined"),
			append(meta(ver1), file1, 1, local, ver1, int64(12), shaA, "text/plain", admin, at, "upload", "", 0)},
		{"file_versions", append(mcols, "file_id", "number", "storage_backend_id", "storage_key", "size_bytes", "sha256", "media_type", "saved_by", "saved_at", "how", "note", "quarantined"),
			append(meta(ver2), file2, 1, local, ver2, int64(20), shaB, "application/pdf", admin, at, "conversion", "", 0)},
		{"file_renditions", append(mcols, "file_version_id", "kind", "number", "storage_backend_id", "storage_key", "size_bytes", "sha256", "made_by", "made_at"),
			append(meta(rend), ver1, "markdown", 1, local, ver1+".md", int64(8), shaB, admin, at)},
		{"file_links", append(mcols, "from_file_id", "to_file_id", "kind"), append(meta(link), file2, file1, "conversion_of")},
		{"edit_locks", append(mcols, "file_id", "user_id", "since_at", "renewed_at"), append(meta(lock), file1, admin, at, at)},
		{"fee_agreements", append(mcols, "matter_id", "fee_type", "amount_cents", "services", "status", "signed_on", "void_reason", "file_id"),
			append(meta(fee), matter, "hourly", int64(150000), "Seed services", "signed", "2026-11-06", "", file1)},
		{"time_entries", append(mcols, "matter_id", "user_id", "worked_on", "increments", "description", "activity_type", "billable", "rate_cents", "invoice_id", "source"),
			append(meta(timeEntry), matter, admin, "2026-11-06", 1, "Seed time", "seed", 1, int64(30000), nil, "manual")},
		{"timers", append(mcols, "matter_id", "user_id", "started_at", "stopped_at", "time_entry_id"),
			append(meta(timer), matter, admin, at, nil, nil)},
		{"expenses", append(mcols, "matter_id", "incurred_on", "description", "amount_cents", "expense_type", "billable", "markup_percent", "file_id", "invoice_id"),
			append(meta(expense), matter, "2026-11-06", "Seed expense", int64(500), "seed", 1, 0, file1, nil)},
		{"invoices", append(mcols, "matter_id", "number", "issued_on", "status", "subtotal_cents", "adjustment_cents", "adjustment_reason", "interest_cents", "total_cents", "amount_paid_cents", "balance_due_cents", "write_off_cents", "write_off_reason", "void_reason", "dispute_status", "dispute_notes", "sent_at", "paid_at", "client_message", "internal_notes"),
			append(meta(invoice), matter, "S-1001", "2026-11-06", "sent", int64(30000), int64(0), "", int64(0), int64(30000), int64(10000), int64(20000), int64(0), "", "", "", "", at, int64(0), "", "")},
		{"invoice_lines", append(mcols, "invoice_id", "kind", "description", "amount_cents", "time_entry_id", "expense_id"),
			append(meta(line), invoice, "flat_fee", "Seed flat fee", int64(30000), nil, nil)},
		{"trust_accounts", append(mcols, "bank", "name", "iolta", "last_four", "active"), append(meta(account), "Seed Bank", "IOLTA", 1, "4242", 1)},
		{"trust_ledger", append(mcols, "trust_account_id", "client_id", "matter_id", "entry_type", "amount_cents", "method", "description", "reverses_id"),
			append(meta(entry), account, contact, matter, "deposit", int64(200000), "check", "Seed deposit", nil)},
		{"trust_balance_cache", append(mcols, "trust_account_id", "client_id", "matter_id", "balance_cents"),
			append(meta(balance), account, contact, matter, int64(200000))},
		{"trust_flags", append(mcols, "trust_entry_id", "matter_id", "client_id", "reason", "resolved_at", "resolved_by", "resolution_note"),
			append(meta(flag), entry, matter, contact, "seed flag", int64(0), nil, "")},
		{"trust_replenishment_requests", append(mcols, "matter_id", "client_id", "amount_cents", "threshold_cents", "message", "status", "invoice_id"),
			append(meta(repl), matter, contact, int64(5000), int64(10000), "Seed replenishment", "active", invoice)},
		{"trust_reconciliations", append(mcols, "trust_account_id", "statement_on", "statement_balance_cents", "ledger_total_cents", "client_balance_cents", "difference_cents"),
			append(meta(recon), account, "2026-11-01", int64(200000), int64(200000), int64(200000), int64(0))},
		{"payments", append(mcols, "contact_id", "matter_id", "amount_cents", "destination", "method", "reference_number", "status", "invoice_id", "trust_entry_id", "source"),
			append(meta(payment), contact, matter, int64(10000), "operating", "check", "1001", "completed", invoice, nil, "manual")},
		{"notes", append(mcols, "matter_id", "contact_id", "author_id", "title", "body", "pinned", "status"),
			append(meta(note), matter, contact, admin, "Seed note", "Seed body", 1, "active")},
		{"note_tags", append(mcols, "note_id", "tag"), append(meta(noteTag), note, "seed")},
		{"conflict_records", append(mcols, "name", "aliases", "date_of_birth", "relationship", "scope", "reason", "matter_id", "starts_on", "ends_on", "source"),
			append(meta(conflict), "Seed Opponent", "[]", "", "prior_client", "matter", "seed reason", matter, "", "", "seed")},
		{"conflict_reviews", append(mcols, "matter_id", "conflict_record_id", "confidence", "decision", "reason", "decided_by", "decided_at"),
			append(meta(review), matter, conflict, 80, "clear", "seed decision", admin, at)},
		{"calendar_events", append(mcols, "title", "kind", "starts_at", "ends_at", "all_day", "matter_id", "location", "travel_minutes", "notes"),
			append(meta(cal), "Seed hearing", "court_date", at, at+3_600_000_000, 0, matter, "Seed Court", 0, "")},
		{"calendar_event_attendees", append(mcols, "event_id", "user_id"), append(meta(attendee), cal, admin)},
		{"deadlines", append(mcols, "matter_id", "title", "due_on", "responsible_user_id", "status", "reminder_offsets", "done_by", "done_at"),
			append(meta(deadline), matter, "Seed deadline", "2026-12-01", admin, "open", "7,1", nil, int64(0))},
		{"audit_log", append(mcols, "actor_id", "role", "source", "entity", "entity_id", "field", "old_value", "new_value", "reason"),
			append(meta(audit), admin, "admin", "ui", "matter", matter, "name", "", "Seed Matter", "seed")},
		{"events", append(mcols, "actor_kind", "actor_id", "action", "record_type", "record_id", "matter_id", "summary", "client_visible"),
			append(meta(ev), "staff", admin, "matter.created", "matter", matter, matter, "Seed matter opened", 0)},
		{"court_profiles", append(mcols, "name", "profile_json", "verified", "verified_by", "verified_at"),
			append(meta(court), "Seed Court", `{"name":"Seed Court"}`, 0, nil, int64(0))},
		{"doc_tender_jobs", append(mcols, "file_version_id", "stage", "status", "attempts", "next_attempt_at", "last_error", "lease_owner", "lease_until"),
			append(meta(job), ver1, "publish", "succeeded", 1, int64(0), "", "", int64(0))},
		{"doc_tender_units", append(mcols, "job_id", "unit", "method", "model", "text", "check_result"),
			append(meta(unit), job, "page-1", "text", domain.LabelingModel, "Seed page", "pass")},
		{"doc_tender_results", append(mcols, "file_version_id", "name_parts", "summary", "category", "category_how", "critical_fields", "matter_id"),
			append(meta(result), ver1, `{"what":"letter"}`, "Seed summary", "correspondence", "person", "[]", matter)},
		{"doc_tender_exceptions", append(mcols, "file_id", "kind", "opened_at", "detail", "status", "resolved_by", "resolved_at", "resolution"),
			append(meta(exc), file1, "low_confidence", at, "Seed exception", "open", nil, int64(0), "")},
		{"ai_call_log", append(mcols, "task", "provider", "model", "prompt_tokens", "completion_tokens", "cost_cents", "fell_back", "fallback_provider", "fallback_model", "user_id", "module_id", "matter_id", "file_id"),
			append(meta(call), "labeling", "openai", domain.LabelingModel, 10, 20, int64(1), 0, "", "", admin, "", matter, file1)},
		{"storage_moves", append(mcols, "from_backend_id", "to_backend_id", "scope", "matter_id", "status", "progress", "confirmed_by", "confirmed_at"),
			append(meta(move), local, network, "matter", matter, "confirmed", progressDone, admin, at)},
	}
	for _, step := range steps {
		if err := ins(ctx, tx, dialect, step.table, step.cols, step.vals); err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{"firm.name", "Seed Firm"}, {"firm.short_name", "Seed"}, {"firm.time_zone", "America/Chicago"}} {
		if _, err := tx.ExecContext(ctx, Rebind(`UPDATE settings SET value = ?, updated_at = ? WHERE key = ?`, dialect), pair[1], at, pair[0]); err != nil {
			return err
		}
	}
	return nil
}

func ins(ctx context.Context, tx *sql.Tx, dialect, table string, cols []string, vals []any) error {
	if !identRe.MatchString(table) {
		return fmt.Errorf("db: bad table %s", table)
	}
	if len(cols) != len(vals) {
		return fmt.Errorf("db: %s has %d columns and %d values", table, len(cols), len(vals))
	}
	for _, col := range cols {
		if !identRe.MatchString(col) {
			return fmt.Errorf("db: bad column %s.%s", table, col)
		}
	}
	holders := strings.Repeat("?,", len(cols))
	holders = holders[:len(holders)-1]
	q := Rebind("INSERT INTO "+table+" ("+strings.Join(cols, ",")+") VALUES ("+holders+")", dialect)
	if _, err := tx.ExecContext(ctx, q, vals...); err != nil {
		return fmt.Errorf("insert %s: %w", table, err)
	}
	return nil
}

func sid(n uint64) string {
	id, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, n)
	if err != nil {
		panic(err)
	}
	return string(id)
}

func bit(v bool) int {
	if v {
		return 1
	}
	return 0
}
