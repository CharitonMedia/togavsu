package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestPRDDefaults(t *testing.T) {
	if err := settings.ValidateCatalog(); err != nil {
		t.Fatal(err)
	}
	patterns, _ := json.Marshal([]string{`^\d{2}[A-Z]{2}-[A-Z]{2}\d+$`})
	roles, _ := json.Marshal([]string{
		"client", "opposing party", "opposing counsel", "prosecutor", "judge", "expert",
		"witness", "co-counsel", "guardian ad litem", "referrer", "officer", "authorized user",
	})
	markers, _ := json.Marshal([]string{"Pro bono", "Zero fee", "Also asked about"})
	folders, _ := json.Marshal([]string{"Correspondence", "Pleadings", "Discovery", "Evidence", "Client documents", "Billing", "Notes"})
	want := map[string]string{
		"firm.matter_number_format":             "YYYY-NNNN",
		"security.totp_required.admin":          "false",
		"security.totp_required.attorney":       "false",
		"security.totp_required.staff":          "false",
		"security.totp_required.accountant":     "false",
		"security.totp_required.client":         "false",
		"security.idle_timeout":                 "12h0m0s",
		"security.lockout_attempts":             "5",
		"security.lockout_length":               "15m0s",
		"security.api_rate_per_minute":          "600",
		"practice.case_number_patterns":         string(patterns),
		"practice.conflict_score_minimum":       "60",
		"practice.party_roles":                  string(roles),
		"practice.markers":                      string(markers),
		"billing.time_increment_minutes":        "6",
		"billing.expense_markup_percent":        "0",
		"billing.payment_terms_days":            "30",
		"billing.grace_period_days":             "15",
		"billing.late_interest_enabled":         "false",
		"trust.evergreen_minimum_cents":         "",
		"trust.reconciliation_reminder_day":     "5",
		"calendar.deadline_reminder_days":       "7,1",
		"calendar.event_reminder_days":          "1",
		"calendar.available_dates_range_months": "3",
		"calendar.available_dates_purpose":      "one day trial",
		"calendar.available_dates_title":        "AVAILABLE DATES FOR HEARING",
		"files.default_backend":                 "local_disk",
		"files.encrypt_local_disk":              "true",
		"files.encrypt_network":                 "true",
		"files.encrypt_s3":                      "true",
		"files.encrypt_dropbox":                 "true",
		"files.backup_scope":                    "all",
		"files.max_upload_bytes":                "2147483648",
		"files.upload_chunk_bytes":              "8388608",
		"files.default_folders":                 string(folders),
		"files.drop_steady_seconds":             "30",
		"editor.lawcraft_commit":                "770a8fab12392485c0c2789fb254da7d9296df4d",
		"editor.lock_timeout":                   "5m0s",
		"editor.max_runs":                       "2",
		"editor.run_timeout":                    "1m0s",
		"editor.run_memory_bytes":               "1073741824",
		"editor.pdf_previews":                   "true",
		"doctender.enabled":                     "true",
		"doctender.naming_pattern":              "<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>",
		"doctender.review_confidence":           "0.90",
		"doctender.photo_quiet":                 "10m0s",
		"doctender.retry_schedule":              `["0s","0s","15m0s","2h0m0s","12h0m0s","24h0m0s"]`,
		"doctender.review_recipients":           "admins",
		"doctender.daily_summary_enabled":       "true",
		"doctender.daily_summary_time":          "07:00",
		"doctender.exception_email_interval":    "15m0s",
		"doctender.lease_timeout":               "15m0s",
		"doctender.max_attempts":                "6",
		"doctender.audio_max_gap":               "30s",
		"doctender.audio_check_window":          "5m0s",
		"doctender.video_frame_interval":        "1s",
		"doctender.provider_down_threshold":     "1h0m0s",
		"home.max_saved_layouts":                "10",
		"search.min_chars":                      "2",
		"search.delay":                          "180ms",
		"search.hits_all":                       "5",
		"search.hits_scope":                     "8",
		"search.excerpt_chars":                  "120",
		"activity.window_days":                  "30",
		"activity.page_size":                    "100",
		"notes.page_size":                       "25",
		"mail.problem_recipients":               "admins",
		"notifications.defaults":                `{"email":["trust_flag","deadline_reminder","update_failure","module_alert"],"email_locked":["trust_flag"],"in_app":["trust_flag","deadline_reminder","replenishment","matter_assignment","update_failure","module_alert"]}`,
		"appearance.staff_portal_label":         "Staff portal",
		"appearance.mode":                       "device",
		"ai.timeout":                            "30s",
		"ai.labeling_model":                     "gpt-6.1-sol",
		"ai.categorizing_model":                 "gpt-6.1-sol",
		"ai.labeling_allow_local":               "false",
		"ai.categorizing_allow_local":           "false",
		"modules.marketplace_address":           "togavsu-marketplace",
		"modules.alert_check":                   "daily",
		"updates.window_start":                  "02:00",
		"updates.window_end":                    "04:00",
		"backups.nightly_time":                  "01:00",
		"backups.local_retention_days":          "30",
		"backups.test_restore_day":              "1",
		"backups.log_retention_days":            "400",
		"api.webhook_attempts":                  "8",
		"api.webhook_window_hours":              "24",
		"llms.show_firm_name":                   "true",
	}
	got := map[string]string{}
	for _, def := range settings.Definitions() {
		if def.Help == "" || def.Tab == "" {
			t.Fatal(def.Key)
		}
		if def.PRD {
			got[def.Key] = def.Default
		}
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s got %q want %q", key, got[key], value)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected PRD setting %s=%q", key, got[key])
		}
	}
	reg, err := settings.New(settings.NewMemory(), nopAudit{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"trust.append_only", "audit.retention_days", "trust.block_below_zero", "updates.release_signing_key"} {
		if _, err := reg.Get(context.Background(), key); !errors.Is(err, settings.ErrUnknown) {
			t.Fatalf("%s should not be a setting: %v", key, err)
		}
	}
}

type nopAudit struct{}

func (nopAudit) SettingChanged(context.Context, settings.Change) error { return nil }
