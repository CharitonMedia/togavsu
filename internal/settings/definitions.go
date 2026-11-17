package settings

import (
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

var fonts = []string{
	"Lora", "Merriweather", "Libre Baskerville", "Source Serif",
	"Inter", "IBM Plex Sans", "Source Sans", "Atkinson Hyperlegible",
}

var presets = []string{"Navy and gold", "Charcoal and copper", "Forest and cream", "Maximum contrast"}

var backends = []string{"local_disk", "network_path", "s3", "dropbox"}

// definitions is the single catalog. Defaults named in the PRD are marked PRD.
var definitions = []Definition{
	{Key: "firm.name", Label: "Firm Name", Kind: KindString, Tab: TabFirm, Help: "Firm name shown on documents and invoices.", Special: "required"},
	{Key: "firm.short_name", Label: "Short Name", Kind: KindString, Tab: TabFirm, Help: "Short name shown in the sidebar."},
	{Key: "firm.address", Label: "Firm Mailing Address", Kind: KindString, Tab: TabFirm, Help: "Firm mailing address."},
	{Key: "firm.phone", Label: "Firm Phone", Kind: KindString, Tab: TabFirm, Help: "Firm phone in E.164.", Optional: true, Special: "phone"},
	{Key: "firm.email", Label: "Firm Email", Kind: KindString, Tab: TabFirm, Help: "Firm email address.", Optional: true, Special: "email"},
	{Key: "firm.website", Label: "Firm Website", Kind: KindString, Tab: TabFirm, Help: "Firm website."},
	{Key: "firm.logo_file_id", Label: "Firm Logo", Kind: KindString, Tab: TabFirm, Help: "File id of the firm logo.", Optional: true, Special: "file_id"},
	{Key: "firm.time_zone", Label: "Time Zone", Kind: KindString, Tab: TabFirm, Help: "IANA time zone used to display UTC timestamps.", Optional: true, Special: "zone"},
	{Key: "firm.matter_number_format", Label: "Matter Number Format", Kind: KindString, Default: domain.MatterNumberFormat, Tab: TabFirm, Help: "Matter number format. Default YYYY-NNNN.", PRD: true},

	{Key: "security.totp_required.admin", Label: "Require Two-Factor for Admins", Kind: KindBool, Default: "false", Tab: TabSecurity, Help: "Admins sign in with a password only unless this is turned on.", PRD: true},
	{Key: "security.totp_required.attorney", Label: "Require Two-Factor for Attorneys", Kind: KindBool, Default: "false", Tab: TabSecurity, Help: "Attorneys sign in with a password only unless this is turned on.", PRD: true},
	{Key: "security.totp_required.staff", Label: "Require Two-Factor for Staff", Kind: KindBool, Default: "false", Tab: TabSecurity, Help: "Staff sign in with a password only unless this is turned on.", PRD: true},
	{Key: "security.totp_required.accountant", Label: "Require Two-Factor for Accountants", Kind: KindBool, Default: "false", Tab: TabSecurity, Help: "Accountants sign in with a password only unless this is turned on.", PRD: true},
	{Key: "security.totp_required.client", Label: "Require Two-Factor for Clients", Kind: KindBool, Default: "false", Tab: TabSecurity, Help: "Client users sign in with a password only unless this is turned on.", PRD: true},
	{Key: "security.idle_timeout", Label: "Idle Sign-Out Time", Kind: KindDuration, Default: "12h0m0s", Min: int64(time.Minute), Max: int64(30 * 24 * time.Hour), HasMin: true, HasMax: true, Tab: TabSecurity, Help: "Longest idle time before sign-out. Default 12 hours.", PRD: true},
	{Key: "security.lockout_attempts", Label: "Failed Sign-Ins Before Lockout", Kind: KindInt, Default: "5", Min: 1, Max: 100, HasMin: true, HasMax: true, Tab: TabSecurity, Help: "Failed sign-ins before lockout. Default 5.", PRD: true},
	{Key: "security.lockout_length", Label: "Lockout Length", Kind: KindDuration, Default: "15m0s", Min: int64(time.Minute), Max: int64(24 * time.Hour), HasMin: true, HasMax: true, Tab: TabSecurity, Help: "Lockout length. Default 15 minutes.", PRD: true},
	{Key: "security.api_rate_per_minute", Label: "API Requests per Minute", Kind: KindInt, Default: "600", Min: 1, Max: 1000000, HasMin: true, HasMax: true, Tab: TabSecurity, Help: "API requests allowed per minute per token. Default 600.", PRD: true},

	{Key: "practice.case_number_examples", Label: "Case Number Formats", Kind: KindJSON, Default: jsonText([]string{"23AR-CV00825"}), Tab: TabPractice, Help: "Type one real case number per line in each format your courts use. Togavsu learns the format from the example.", Special: "case_examples"},
	{Key: "practice.case_number_patterns", Label: "Case Number Patterns", Kind: KindJSON, Default: jsonText([]string{domain.DefaultCasePattern}), Tab: TabPractice, Help: "Case-number patterns used when matching matters that still need a case number. Prefer Case Number Formats unless you need a custom pattern.", PRD: true, Special: "regexp_list"},
	{Key: "practice.conflict_score_minimum", Label: "Lowest Conflict Score Shown", Kind: KindInt, Default: strconv.Itoa(domain.ConflictScoreMinimum), Min: 0, Max: 100, HasMin: true, HasMax: true, Tab: TabPractice, Help: "Lowest conflict score shown, out of 100. Default 60.", PRD: true},
	{Key: "practice.party_roles", Label: "Party Roles", Kind: KindJSON, Default: jsonText(domain.DefaultPartyRoles()), Tab: TabPractice, Help: "Party roles offered on a matter.", PRD: true},
	{Key: "practice.markers", Label: "Matter Markers", Kind: KindJSON, Default: jsonText(domain.DefaultMarkers()), Tab: TabPractice, Help: "Core matter markers. The firm can add its own.", PRD: true},

	{Key: "billing.time_increment_minutes", Label: "Time Increment", Kind: KindEnum, Default: strconv.Itoa(domain.BillingTimeIncrementMinutes), Allowed: []string{"1", strconv.Itoa(domain.BillingTimeIncrementMinutes), "10", "15"}, Tab: TabBilling, Help: "Time increment in minutes. Default 6.", PRD: true},
	{Key: "billing.default_hourly_rate_cents", Label: "Default Hourly Rate", Kind: KindCents, Default: "0.00", Min: 0, HasMin: true, Tab: TabBilling, Help: "Default hourly rate for new users."},
	{Key: "billing.expense_markup_percent", Label: "Expense Markup", Kind: KindInt, Default: "0", Min: 0, Max: 1000, HasMin: true, HasMax: true, Tab: TabBilling, Help: "Default expense markup percent. Default 0.", PRD: true},
	{Key: "billing.payment_terms_days", Label: "Payment Terms", Kind: KindInt, Default: "30", Min: 0, Max: 3650, HasMin: true, HasMax: true, Tab: TabBilling, Help: "Invoice payment terms in days. Default 30.", PRD: true},
	{Key: "billing.processor_authorization_lifetime", Label: "Payment Authorization Lifetime", Kind: KindDuration, Default: "30m0s", Min: int64(time.Minute), Max: int64(24 * time.Hour), HasMin: true, HasMax: true, Tab: TabBilling, Help: "How long a payment processor module may use the authorization a staff member gave it for one payment. Default 30 minutes."},
	{Key: "billing.grace_period_days", Label: "Grace Period", Kind: KindInt, Default: "15", Min: 0, Max: 3650, HasMin: true, HasMax: true, Tab: TabBilling, Help: "Grace period before late interest, in days. Default 15.", PRD: true},
	{Key: "billing.late_interest_enabled", Label: "Charge Late Interest", Kind: KindBool, Default: "false", Tab: TabBilling, Help: "Late interest is off unless this is turned on.", PRD: true},
	{Key: "billing.late_interest_annual_percent", Label: "Late Interest Rate", Kind: KindInt, Default: "0", Min: 0, Max: 100, HasMin: true, HasMax: true, Tab: TabBilling, Help: "Annual late-interest percent used when late interest is on."},
	{Key: "billing.invoice_number_format", Label: "Invoice Number Format", Kind: KindString, Tab: TabBilling, Help: "Invoice number format."},
	{Key: "billing.default_client_message", Label: "Default Invoice Message", Kind: KindString, Tab: TabBilling, Help: "Default client message on invoices."},
	{Key: "billing.activity_types", Label: "Activity Types", Kind: KindJSON, Default: "[]", Tab: TabBilling, Help: "Activity types offered on time entries."},
	{Key: "billing.expense_types", Label: "Expense Types", Kind: KindJSON, Default: "[]", Tab: TabBilling, Help: "Expense types offered on expenses."},

	{Key: "trust.evergreen_minimum_cents", Label: "Default Evergreen Minimum", Kind: KindCents, Tab: TabTrust, Help: "Default evergreen minimum for new matters. Empty means none.", Optional: true, PRD: true, HasMin: true},
	{Key: "trust.reconciliation_reminder_day", Label: "Reconciliation Reminder Day", Kind: KindInt, Default: "5", Min: 1, Max: 28, HasMin: true, HasMax: true, Tab: TabTrust, Help: "Day of the month for the reconciliation reminder. Default the 5th.", PRD: true},
	{Key: "trust.flag_email_extra", Label: "Extra Trust Flag Recipients", Kind: KindString, Tab: TabTrust, Help: "Extra trust-flag email recipients. Admins and the responsible attorney always receive them."},

	{Key: "calendar.deadline_reminder_days", Label: "Deadline Reminders", Kind: KindString, Default: "7,1", Tab: TabCalendar, Help: "Deadline reminder offsets in days. Default 7 and 1.", PRD: true, Special: "day_list"},
	{Key: "calendar.event_reminder_days", Label: "Event Reminder", Kind: KindInt, Default: "1", Min: 0, Max: 365, HasMin: true, HasMax: true, Tab: TabCalendar, Help: "Event reminder lead time in days. Default 1.", PRD: true},
	{Key: "calendar.ical_enabled", Label: "iCal Feeds", Kind: KindBool, Default: "true", Tab: TabCalendar, Help: "Publish read-only iCal feeds."},
	{Key: "calendar.available_dates_range_months", Label: "Available Dates Range", Kind: KindInt, Default: "3", Min: 1, Max: 36, HasMin: true, HasMax: true, Tab: TabCalendar, Help: "Default available-dates range in months. Default 3.", PRD: true},
	{Key: "calendar.available_dates_purpose", Label: "Available Dates Purpose", Kind: KindString, Default: domain.AvailableDatesPurpose, Tab: TabCalendar, Help: "Default available-dates purpose.", PRD: true},
	{Key: "calendar.available_dates_title", Label: "Available Dates Title", Kind: KindString, Default: domain.AvailableDatesTitle, Tab: TabCalendar, Help: "Default available-dates title.", PRD: true},

	{Key: "files.default_backend", Label: "Default Storage", Kind: KindEnum, Default: "local_disk", Allowed: backends, Tab: TabFiles, Help: "Default storage backend. Default local disk.", PRD: true},
	{Key: "files.encrypt_local_disk", Label: "Encrypt Local Disk", Kind: KindBool, Default: "true", Tab: TabFiles, Help: "Encrypt bytes on local disk. Default on.", PRD: true},
	{Key: "files.encrypt_network", Label: "Encrypt Network Path", Kind: KindBool, Default: "true", Tab: TabFiles, Help: "Encrypt bytes on a network path. Default on.", PRD: true},
	{Key: "files.encrypt_s3", Label: "Encrypt S3 Storage", Kind: KindBool, Default: "true", Tab: TabFiles, Help: "S3-compatible storage is always encrypted.", Locked: true, PRD: true},
	{Key: "files.encrypt_dropbox", Label: "Encrypt Dropbox", Kind: KindBool, Default: "true", Tab: TabFiles, Help: "Dropbox storage is always encrypted.", Locked: true, PRD: true},
	{Key: "files.backup_scope", Label: "Storage Included in Backups", Kind: KindString, Default: "all", Tab: TabFiles, Help: "Which backends backups copy. Default all.", PRD: true, Special: "backup_scope"},
	{Key: "files.max_upload_bytes", Label: "Largest Upload", Kind: KindInt, Default: "2147483648", Min: 1, Max: 1 << 40, HasMin: true, HasMax: true, Tab: TabFiles, Help: "Largest upload in bytes. Default 2 GiB.", PRD: true},
	{Key: "files.upload_chunk_bytes", Label: "Upload Chunk Size", Kind: KindInt, Default: "8388608", Min: 1, Max: 1 << 30, HasMin: true, HasMax: true, Tab: TabFiles, Help: "Upload chunk size in bytes. Default 8 MiB.", PRD: true},
	{Key: "files.default_folders", Label: "Default Matter Folders", Kind: KindJSON, Default: jsonText(domain.DefaultFolders()), Tab: TabFiles, Help: "Default folders for a new matter.", PRD: true},
	{Key: "files.drop_steady_seconds", Label: "Drop Folder Wait", Kind: KindInt, Default: "30", Min: 1, Max: 3600, HasMin: true, HasMax: true, Tab: TabFiles, Help: "Seconds a drop-folder file must stay unchanged before intake. Default 30.", PRD: true},
	{Key: "files.publication_grant_minutes", Label: "Module File Access Window", Kind: KindInt, Default: "60", Min: 1, Max: 1440, HasMin: true, HasMax: true, Tab: TabFiles, Help: "Minutes a background file publication grant stays valid after the core issues it to a module. Default 60."},

	{Key: "editor.lawcraft_commit", Label: "LawCraft Version", Kind: KindString, Default: domain.LawCraftCommit, Tab: TabEditor, Help: "LawCraft commit pinned in this build. Read only.", Locked: true, PRD: true},
	{Key: "editor.lock_timeout", Label: "Edit Lock Timeout", Kind: KindDuration, Default: "5m0s", Min: int64(time.Minute), Max: int64(time.Hour), HasMin: true, HasMax: true, Tab: TabEditor, Help: "Edit lock timeout without a renewal. Default 5 minutes.", PRD: true},
	{Key: "editor.max_runs", Label: "Editor Runs at Once", Kind: KindInt, Default: "2", Min: 1, Max: 32, HasMin: true, HasMax: true, Tab: TabEditor, Help: "Editor runs allowed at once. Default 2.", PRD: true},
	{Key: "editor.run_timeout", Label: "Editor Run Time Limit", Kind: KindDuration, Default: "1m0s", Min: int64(time.Second), Max: int64(time.Hour), HasMin: true, HasMax: true, Tab: TabEditor, Help: "Time limit for one editor run. Default 60 seconds.", PRD: true},
	{Key: "editor.run_memory_bytes", Label: "Editor Run Memory Limit", Kind: KindInt, Default: "1073741824", Min: 1, Max: 64 << 30, HasMin: true, HasMax: true, Tab: TabEditor, Help: "Memory limit for one editor run. Default 1 GiB.", PRD: true},
	{Key: "editor.pdf_previews", Label: "PDF Previews", Kind: KindBool, Default: "true", Tab: TabEditor, Help: "Build a PDF preview when a version is saved. Default on.", PRD: true},

	{Key: "doctender.enabled", Label: "Doc Tender On", Kind: KindBool, Default: "true", Tab: TabDocTender, Help: "Doc Tender processes files as they arrive. Default on.", PRD: true},
	{Key: "doctender.naming_pattern", Label: "File Naming Pattern", Kind: KindString, Default: domain.NamingPattern, Tab: TabDocTender, Help: "File naming pattern. A status token is refused.", PRD: true, Special: "naming_pattern"},
	{Key: "doctender.review_confidence", Label: "Review Confidence Threshold", Kind: KindString, Default: "0.90", Tab: TabDocTender, Help: "Confidence below which an item goes to review. Default 0.90.", PRD: true, Special: "confidence"},
	{Key: "doctender.photo_quiet", Label: "Photo Grouping Wait", Kind: KindDuration, Default: "10m0s", Min: int64(time.Minute), Max: int64(24 * time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "Quiet time before photos are grouped. Default 10 minutes.", PRD: true},
	{Key: "doctender.retry_schedule", Label: "Retry Schedule", Kind: KindJSON, Default: domain.DefaultRetrySchedule, Tab: TabDocTender, Help: "Retry delays for one unit. Default is six attempts over about 38 hours.", PRD: true},
	{Key: "doctender.review_recipients", Label: "Review Queue Recipients", Kind: KindString, Default: "admins", Tab: TabDocTender, Help: "Review queue recipients. Default the admins.", PRD: true},
	{Key: "doctender.daily_summary_enabled", Label: "Daily Summary Email", Kind: KindBool, Default: "true", Tab: TabDocTender, Help: "Send the daily Doc Tender summary. Default on.", PRD: true},
	{Key: "doctender.daily_summary_time", Label: "Daily Summary Time", Kind: KindString, Default: "07:00", Tab: TabDocTender, Help: "Firm-local time of the daily summary. Default 7:00 a.m.", PRD: true, Special: "time_of_day"},
	{Key: "doctender.exception_email_interval", Label: "Exception Email Spacing", Kind: KindDuration, Default: "15m0s", Min: int64(time.Minute), Max: int64(24 * time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "Minimum gap between exception emails. Default 15 minutes.", PRD: true},
	{Key: "doctender.lease_timeout", Label: "Worker Lease Length", Kind: KindDuration, Default: "15m0s", Min: int64(time.Minute), Max: int64(24 * time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "How long a worker lease lasts. Default 15 minutes.", PRD: true},
	{Key: "doctender.max_attempts", Label: "Attempts Before Exception", Kind: KindInt, Default: "6", Min: 1, Max: 20, HasMin: true, HasMax: true, Tab: TabDocTender, Help: "Attempts per unit before an exception. Default 6.", PRD: true},
	{Key: "doctender.audio_max_gap", Label: "Largest Transcript Gap", Kind: KindDuration, Default: "30s", Min: int64(time.Second), Max: int64(time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "Largest unexplained transcript gap. Default 30 seconds.", PRD: true},
	{Key: "doctender.audio_check_window", Label: "Transcript Check Window", Kind: KindDuration, Default: "5m0s", Min: int64(time.Minute), Max: int64(time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "Window used to compare a second transcription. Default 5 minutes.", PRD: true},
	{Key: "doctender.video_frame_interval", Label: "Video Frame Interval", Kind: KindDuration, Default: "1s", Min: int64(time.Second), Max: int64(time.Minute), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "How often a video frame is described. Default 1 second.", PRD: true},
	{Key: "doctender.provider_down_threshold", Label: "Provider Outage Threshold", Kind: KindDuration, Default: "1h0m0s", Min: int64(time.Minute), Max: int64(48 * time.Hour), HasMin: true, HasMax: true, Tab: TabDocTender, Help: "How long a provider stays down before an exception. Default 1 hour.", PRD: true},

	{Key: "home.max_saved_layouts", Label: "Saved Layouts per User", Kind: KindInt, Default: "10", Min: 1, Max: 100, HasMin: true, HasMax: true, Tab: TabHome, Help: "Saved layouts allowed per user. Default 10.", PRD: true},

	{Key: "search.min_chars", Label: "Search Start", Kind: KindInt, Default: strconv.Itoa(domain.SearchMinChars), Min: 1, Max: 10, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Characters before search runs. Default 2.", PRD: true},
	{Key: "search.delay", Label: "Search Delay", Kind: KindDuration, Default: "180ms", Min: 0, Max: int64(5 * time.Second), HasMin: true, HasMax: true, Tab: TabSearch, Help: "Delay after typing stops. Default 180 ms.", PRD: true},
	{Key: "search.hits_all", Label: "Results per Scope on All", Kind: KindInt, Default: strconv.Itoa(domain.SearchHitsAll), Min: 1, Max: 50, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Hits per scope on All. Default 5.", PRD: true},
	{Key: "search.hits_scope", Label: "Results in One Scope", Kind: KindInt, Default: strconv.Itoa(domain.SearchHitsScope), Min: 1, Max: 50, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Hits shown in one scope. Default 8.", PRD: true},
	{Key: "search.excerpt_chars", Label: "File Excerpt Length", Kind: KindInt, Default: "120", Min: 40, Max: 1000, HasMin: true, HasMax: true, Tab: TabSearch, Help: "File-search excerpt length. Default 120 characters.", PRD: true},
	{Key: "activity.window_days", Label: "Activities Feed Window", Kind: KindInt, Default: strconv.Itoa(domain.ActivityWindowDays), Min: 1, Max: 3650, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Activities Feed window in days. Default 30.", PRD: true},
	{Key: "activity.page_size", Label: "Activities Feed Page Size", Kind: KindInt, Default: strconv.Itoa(domain.ActivityPageSize), Min: 1, Max: 500, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Activities Feed page size. Default 100.", PRD: true},
	{Key: "notes.page_size", Label: "Notes Page Size", Kind: KindInt, Default: strconv.Itoa(domain.NotesPageSize), Min: 1, Max: 200, HasMin: true, HasMax: true, Tab: TabSearch, Help: "Notes page size. Default 25.", PRD: true},

	{Key: "mail.smtp_host", Label: "Mail Server", Kind: KindString, Tab: TabMail, Help: "SMTP host for system mail."},
	{Key: "mail.smtp_port", Label: "Mail Server Port", Kind: KindInt, Min: 1, Max: 65535, HasMin: true, HasMax: true, Optional: true, Tab: TabMail, Help: "SMTP port."},
	{Key: "mail.smtp_timeout_seconds", Label: "Mail Send Time Limit", Kind: KindInt, Default: "60", Min: 1, Max: 600, HasMin: true, HasMax: true, Tab: TabMail, Help: "Seconds one system mail send may take. A send cut off is retried later. Default 60."},
	{Key: "mail.smtp_username", Label: "Mail Username", Kind: KindString, Tab: TabMail, Help: "SMTP username."},
	{Key: "mail.smtp_password", Label: "Mail Password", Kind: KindString, Secret: true, Optional: true, Tab: TabMail, Help: "SMTP password. Stored encrypted and omitted from the audit log."},
	{Key: "mail.smtp_starttls", Label: "Require STARTTLS", Kind: KindBool, Default: "true", Tab: TabMail, Help: "Use STARTTLS for system mail. When on, mail waits in the queue with an error rather than go to a server that does not offer STARTTLS. Default on."},
	{Key: "mail.smtp_from", Label: "System Mail From Address", Kind: KindString, Tab: TabMail, Help: "From address for system mail.", Optional: true, Special: "email"},
	{Key: "mail.route_through_email_module", Label: "Send Through Email Module", Kind: KindBool, Default: "false", Tab: TabMail, Help: "Send system mail through the email module when one is installed."},
	{Key: "mail.problem_recipients", Label: "Problem Report Recipients", Kind: KindString, Default: "admins", Tab: TabMail, Help: "Problem-report recipients. Default the admins.", PRD: true},
	{Key: "notifications.defaults", Label: "Default Notifications", Kind: KindJSON, Default: domain.DefaultNotificationPrefs, Tab: TabMail, Help: "Firm default channels. Trust-flag email stays on.", PRD: true},

	{Key: "appearance.preset", Label: "Color Preset", Kind: KindEnum, Allowed: presets, Optional: true, Tab: TabAppearance, Help: "Color preset. Empty uses the firm's custom colors."},
	{Key: "appearance.heading_font", Label: "Heading Font", Kind: KindEnum, Allowed: fonts, Optional: true, Tab: TabAppearance, Help: "Heading font."},
	{Key: "appearance.body_font", Label: "Body Font", Kind: KindEnum, Allowed: fonts, Optional: true, Tab: TabAppearance, Help: "Body font."},
	{Key: "appearance.text_size", Label: "Text Size", Kind: KindString, Tab: TabAppearance, Help: "Firm text size."},
	{Key: "appearance.staff_portal_label", Label: "Staff Portal Label", Kind: KindString, Default: domain.StaffPortalLabel, Tab: TabAppearance, Help: "Label under the firm short name. Default Staff portal.", PRD: true},
	{Key: "appearance.client_portal_label", Label: "Client Portal Label", Kind: KindString, Tab: TabAppearance, Help: "Client portal label."},
	{Key: "appearance.mode", Label: "Color Mode", Kind: KindEnum, Default: "device", Allowed: []string{"light", "dark", "device"}, Tab: TabAppearance, Help: "Firm default color mode. Default follow the device.", PRD: true},
	{Key: "appearance.colors", Label: "Custom Colors", Kind: KindJSON, Default: "{}", Tab: TabAppearance, Help: "Custom colors for the sidebar, header, page, and accents."},

	{Key: "ai.timeout", Label: "AI Wait Before Fallback", Kind: KindDuration, Default: "30s", Min: int64(time.Second), Max: int64(10 * time.Minute), HasMin: true, HasMax: true, Tab: TabAI, Help: "How long to wait for a model before fallback. Default 30 seconds.", PRD: true},
	{Key: "ai.primary_provider", Label: "Primary AI Provider", Kind: KindString, Tab: TabAI, Help: "Primary AI provider."},
	{Key: "ai.secondary_provider", Label: "Secondary AI Provider", Kind: KindString, Tab: TabAI, Help: "Secondary AI provider. Empty means no fallback provider is set."},
	{Key: "ai.primary_fast_model", Label: "Primary Fast Model", Kind: KindString, Tab: TabAI, Help: "Fast model on the primary provider."},
	{Key: "ai.primary_strong_model", Label: "Primary Strong Model", Kind: KindString, Tab: TabAI, Help: "Strong model on the primary provider."},
	{Key: "ai.secondary_fast_model", Label: "Secondary Fast Model", Kind: KindString, Tab: TabAI, Help: "Fast model on the secondary provider."},
	{Key: "ai.secondary_strong_model", Label: "Secondary Strong Model", Kind: KindString, Tab: TabAI, Help: "Strong model on the secondary provider."},
	{Key: "ai.openai_api_key", Label: "OpenAI API Key", Kind: KindString, Secret: true, Optional: true, Tab: TabAI, Help: "OpenAI API key. Stored encrypted and omitted from the audit log."},
	{Key: "ai.anthropic_api_key", Label: "Anthropic API Key", Kind: KindString, Secret: true, Optional: true, Tab: TabAI, Help: "Anthropic API key. Stored encrypted and omitted from the audit log."},
	{Key: "ai.compatible_api_key", Label: "Compatible Endpoint API Key", Kind: KindString, Secret: true, Optional: true, Tab: TabAI, Help: "API key for an OpenAI-compatible endpoint. Stored encrypted and omitted from the audit log."},
	{Key: "ai.local_api_key", Label: "Local Endpoint API Key", Kind: KindString, Secret: true, Optional: true, Tab: TabAI, Help: "API key for the local endpoint, when it asks for one. Stored encrypted and omitted from the audit log."},
	{Key: "ai.compatible_base_url", Label: "Compatible Endpoint URL", Kind: KindString, Tab: TabAI, Help: "Base URL of an OpenAI-compatible endpoint."},
	{Key: "ai.local_base_url", Label: "Local Model URL", Kind: KindString, Tab: TabAI, Help: "Base URL of a local model endpoint."},
	{Key: "ai.speech_to_text_model", Label: "Speech-to-Text Model", Kind: KindString, Tab: TabAI, Help: "Speech-to-text model used for transcription."},
	{Key: "ai.labeling_model", Label: "Labeling Model", Kind: KindString, Default: domain.LabelingModel, Tab: TabAI, Help: "Labeling model. Default gpt-6.1-sol once an OpenAI key is set; otherwise the primary strong model.", PRD: true},
	{Key: "ai.categorizing_model", Label: "Categorizing Model", Kind: KindString, Default: domain.LabelingModel, Tab: TabAI, Help: "Categorizing model. Default gpt-6.1-sol once an OpenAI key is set; otherwise the primary strong model.", PRD: true},
	{Key: "ai.labeling_allow_local", Label: "Allow Local Labeling", Kind: KindBool, Default: "false", Tab: TabAI, Help: "Labeling never runs on a local endpoint.", Locked: true, PRD: true},
	{Key: "ai.categorizing_allow_local", Label: "Allow Local Categorizing", Kind: KindBool, Default: "false", Tab: TabAI, Help: "Categorizing never runs on a local endpoint.", Locked: true, PRD: true},
	{Key: "ai.model_prices", Label: "Model Prices", Kind: KindJSON, Default: "{}", Tab: TabAI, Help: "Price per model in cents per million tokens. A model with no price costs 0 cents in the AI call log.", Special: "model_prices"},
	{Key: "ai.task_overrides", Label: "Per-Task Model Overrides", Kind: KindJSON, Default: "{}", Tab: TabAI, Help: "Per-task provider and model overrides."},

	{Key: "modules.marketplace_address", Label: "Marketplace Address", Kind: KindString, Default: domain.MarketplaceAddress, Tab: TabModules, Help: "Marketplace the firm checks: an https address, or http://togavsu-marketplace:8090 for the marketplace that runs beside Togavsu, which installs only modules signed by your trusted local keys.", PRD: true},
	{Key: "modules.alert_check", Label: "Module Check Frequency", Kind: KindEnum, Default: "daily", Allowed: []string{"hourly", "daily", "weekly"}, Tab: TabModules, Help: "How often installed modules are checked for flags and pulls. Default daily.", PRD: true},
	{Key: "modules.replace_time", Label: "Time and Billing Replacement Module", Kind: KindString, Tab: TabModules, Help: "Module id that replaces time and invoicing. Empty keeps the core."},
	{Key: "modules.replace_trust", Label: "Trust Replacement Module", Kind: KindString, Tab: TabModules, Help: "Module id that replaces trust. Empty keeps the core."},
	{Key: "modules.replace_calendar", Label: "Calendar Replacement Module", Kind: KindString, Tab: TabModules, Help: "Module id that replaces calendar. Empty keeps the core."},

	{Key: "updates.window_start", Label: "Update Window Start", Kind: KindString, Default: "02:00", Tab: TabUpdates, Help: "Start of the update window, firm time. Default 2:00 a.m.", PRD: true, Special: "time_of_day"},
	{Key: "updates.window_end", Label: "Update Window End", Kind: KindString, Default: "04:00", Tab: TabUpdates, Help: "End of the update window, firm time. Default 4:00 a.m.", PRD: true, Special: "time_of_day"},
	{Key: "updates.paused", Label: "Pause Automatic Updates", Kind: KindBool, Default: "false", Tab: TabUpdates, Help: "Pause automatic updates."},

	{Key: "backups.nightly_time", Label: "Nightly Backup Time", Kind: KindString, Default: "01:00", Tab: TabBackups, Help: "Nightly backup time, firm time. Default 1:00 a.m.", PRD: true, Special: "time_of_day"},
	{Key: "backups.local_retention_days", Label: "Local Backup Retention", Kind: KindInt, Default: "30", Min: 1, Max: 36500, HasMin: true, HasMax: true, Tab: TabBackups, Help: "Days to keep local backups. Default 30.", PRD: true},
	{Key: "backups.test_restore_day", Label: "Test Restore Day", Kind: KindInt, Default: "1", Min: 1, Max: 28, HasMin: true, HasMax: true, Tab: TabBackups, Help: "Day of the month for the test restore. Default the 1st.", PRD: true},
	{Key: "backups.log_retention_days", Label: "Log Retention", Kind: KindInt, Default: "400", Min: 1, Max: 36500, HasMin: true, HasMax: true, Tab: TabBackups, Help: "Days to keep application and access logs. Default 400.", PRD: true},
	{Key: "backups.passphrase", Label: "Backup Passphrase", Kind: KindString, Secret: true, Optional: true, Special: "required", Tab: TabBackups, Help: "Backup archive passphrase set at first run. Stored encrypted and omitted from the audit log."},

	{Key: "api.page_size", Label: "API Page Size", Kind: KindInt, Default: "50", Min: 1, Max: 10000, HasMin: true, HasMax: true, Tab: TabAPI, Help: "Default rows per API page unless the list has its own page-size setting. The effective size is limited by api.max_page_size. Default 50."},
	{Key: "api.max_page_size", Label: "API Maximum Page Size", Kind: KindInt, Default: "200", Min: 1, Max: 10000, HasMin: true, HasMax: true, Tab: TabAPI, Help: "Maximum rows per API page for lists using general pagination. Default 200."},
	{Key: "api.idempotency_retention_hours", Label: "Idempotency Key Retention", Kind: KindInt, Default: "24", Min: 1, HasMin: true, Tab: TabAPI, Help: "How long a stored Idempotency-Key result is replayed, in hours. Default 24."},
	{Key: "api.bulk_batch_max", Label: "Bulk Request Limit", Kind: KindInt, Default: "500", Min: 1, HasMin: true, Tab: TabAPI, Help: "Maximum items in one bulk request; the whole batch is one transaction. Default 500."},
	{Key: "api.webhook_attempts", Label: "Webhook Attempts", Kind: KindInt, Default: "8", Min: 1, Max: 100, HasMin: true, HasMax: true, Tab: TabAPI, Help: "Webhook delivery attempts. Default 8.", PRD: true},
	{Key: "api.webhook_window_hours", Label: "Webhook Retry Window", Kind: KindInt, Default: "24", Min: 1, Max: 168, HasMin: true, HasMax: true, Tab: TabAPI, Help: "Hours over which webhook retries are spread. Default 24.", PRD: true},
	{Key: "llms.show_firm_name", Label: "Show Firm Name on llms.txt", Kind: KindBool, Default: "true", Tab: TabAPI, Help: "Show the firm name on /llms.txt. The admin can hide it.", PRD: true},
	{Key: "llms.firm_paragraph", Label: "llms.txt Firm Paragraph", Kind: KindString, Tab: TabAPI, Help: "Optional firm paragraph on /llms.txt. It must not contain client or matter data."},
}
