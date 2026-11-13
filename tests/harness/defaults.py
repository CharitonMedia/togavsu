"""Named defaults from the PRD.

AC-029 stays pending until the settings registry and the admin tabs serve this
list. An anchor that is missing from the PRD means this inventory drifted.
Fixed rules are listed so a later build cannot turn them into optional switches.
"""

from __future__ import annotations

import re

# setting True: the admin tab must show this default and Reset to default.
# setting False: a fixed rule. It is not a switch.
DEFAULTS = (
    {"key": "matter_number_format", "value": "YYYY-NNNN", "tab": "Firm profile", "setting": True, "anchor": "default YYYY-NNNN"},
    {"key": "totp_by_role", "value": "optional for every role, admin included; the admin can require it for any role", "tab": "Security", "setting": True, "anchor": "default optional for every role, admin included"},
    {"key": "idle_timeout", "value": "12 hours", "tab": "Security", "setting": True, "anchor": "default 12 hours"},
    {"key": "failed_sign_ins_before_lockout", "value": "5", "tab": "Security", "setting": True, "anchor": "failed sign-ins before lockout (default 5)"},
    {"key": "lockout_length", "value": "15 minutes", "tab": "Security", "setting": True, "anchor": "lockout length (default 15 minutes)"},
    {"key": "api_rate_limit", "value": "600 requests a minute per token", "tab": "Security", "setting": True, "anchor": "default 600 requests a minute per token"},
    {"key": "case_number_pattern", "value": "Missouri docket numbers", "tab": "Practice", "setting": True, "anchor": "default Missouri docket numbers"},
    {"key": "conflict_score_floor", "value": "60 out of 100", "tab": "Practice", "setting": True, "anchor": "default 60 out of 100"},
    {"key": "time_increment", "value": "6 minutes", "tab": "Time and billing", "setting": True, "anchor": "default 6 minutes"},
    {"key": "time_increment_choices", "value": "1, 6, 10, 15", "tab": "Time and billing", "setting": False, "validates": "time_increment", "allowed": [1, 6, 10, 15], "anchor": "The admin can set 1, 6, 10 or 15 minutes."},
    {"key": "expense_markup", "value": "0 percent", "tab": "Time and billing", "setting": True, "anchor": "default 0 percent"},
    {"key": "payment_terms", "value": "due in 30 days", "tab": "Time and billing", "setting": True, "anchor": "default due in 30 days"},
    {"key": "grace_period", "value": "15 days", "tab": "Time and billing", "setting": True, "anchor": "grace period (default 15 days)"},
    {"key": "late_interest", "value": "off", "tab": "Time and billing", "setting": True, "anchor": "late interest (default off"},
    {"key": "evergreen_minimum", "value": "none", "tab": "Trust", "setting": True, "anchor": "default evergreen minimum for new matters (default none)"},
    {"key": "reconciliation_reminder_day", "value": "the 5th", "tab": "Trust", "setting": True, "anchor": "default the 5th"},
    {"key": "deadline_reminders", "value": "7 days and 1 day before", "tab": "Calendar", "setting": True, "anchor": "deadlines 7 days and 1 day before"},
    {"key": "event_reminder", "value": "1 day before", "tab": "Calendar", "setting": True, "anchor": "events 1 day before"},
    {"key": "available_dates_range", "value": "three months", "tab": "Calendar", "setting": True, "anchor": "range three months"},
    {"key": "available_dates_purpose", "value": "one day trial", "tab": "Calendar", "setting": True, "anchor": 'purpose "one day trial"'},
    {"key": "available_dates_title", "value": "AVAILABLE DATES FOR HEARING", "tab": "Calendar", "setting": True, "anchor": 'title "AVAILABLE DATES FOR HEARING"'},
    {"key": "default_storage_backend", "value": "local disk", "tab": "Files and storage", "setting": True, "anchor": "the default backend (default local disk)"},
    {"key": "encryption_local_and_network", "value": "on", "tab": "Files and storage", "setting": True, "anchor": "default on for local disk and network paths"},
    {"key": "backup_backend_scope", "value": "all", "tab": "Files and storage", "setting": True, "anchor": "which backends backups copy (default all"},
    {"key": "largest_upload", "value": "2 GB", "tab": "Files and storage", "setting": True, "anchor": "largest upload (default 2 GB)"},
    {"key": "upload_chunk", "value": "8 MB", "tab": "Files and storage", "setting": True, "anchor": "Uploads go in 8 MB chunks"},
    {"key": "default_folders", "value": "Correspondence, Pleadings, Discovery, Evidence, Client documents, Billing, Notes", "tab": "Files and storage", "setting": True, "anchor": "default Correspondence, Pleadings, Discovery, Evidence, Client documents, Billing, Notes"},
    {"key": "edit_lock_timeout", "value": "5 minutes", "tab": "Document editor", "setting": True, "anchor": "default 5 minutes without a renewal"},
    {"key": "editor_runs_at_once", "value": "2", "tab": "Document editor", "setting": True, "anchor": "editor runs at once (default 2)"},
    {"key": "editor_run_limits", "value": "60 seconds and 1 GB", "tab": "Document editor", "setting": True, "anchor": "default 60 seconds and 1 GB"},
    {"key": "pdf_previews", "value": "on", "tab": "Document editor", "setting": True, "anchor": "PDF previews (default on)"},
    {"key": "doc_tender_enabled", "value": "on", "tab": "Doc Tender", "setting": True, "anchor": "on or off (default on), the naming pattern"},
    {"key": "naming_pattern", "value": "<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>", "tab": "Doc Tender", "setting": True, "anchor": "default `<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>`"},
    {"key": "review_confidence", "value": "0.90", "tab": "Doc Tender", "setting": True, "anchor": "default 0.90"},
    {"key": "photo_group_quiet_time", "value": "10 minutes", "tab": "Doc Tender", "setting": True, "anchor": "default 10 minutes"},
    {"key": "retry_schedule", "value": "6 attempts: 2 at once, then 15 minutes, 2 hours, 12 hours, and 24 hours", "tab": "Doc Tender", "setting": True, "anchor": "2 at once, then after 15 minutes, 2 hours, 12 hours and 24 hours"},
    {"key": "review_queue_recipients", "value": "the admins", "tab": "Doc Tender", "setting": True, "anchor": "review queue recipients (default the admins)"},
    {"key": "daily_summary_email", "value": "on, 7:00 a.m. firm time", "tab": "Doc Tender", "setting": True, "anchor": "default on, 7:00 a.m. firm time"},
    {"key": "exception_email_bundle", "value": "at most one email every 15 minutes", "tab": "Doc Tender", "setting": True, "anchor": "at most one email every 15 minutes"},
    {"key": "saved_layouts_cap", "value": "10", "tab": "Home screen", "setting": True, "anchor": "saved layouts allowed per user (default 10)"},
    {"key": "search_start", "value": "2 characters", "tab": "Lists and search", "setting": True, "anchor": "default 2 characters"},
    {"key": "search_delay", "value": "180 ms", "tab": "Lists and search", "setting": True, "anchor": "default 180 ms"},
    {"key": "search_hits", "value": "5 in All, 8 in one scope", "tab": "Lists and search", "setting": True, "anchor": "default 5 in All, 8 in one scope"},
    {"key": "activity_window", "value": "30 days", "tab": "Lists and search", "setting": True, "anchor": "Activities Feed window (default 30 days)"},
    {"key": "activity_page_size", "value": "100", "tab": "Lists and search", "setting": True, "anchor": "page size (default 100)"},
    {"key": "notes_page_size", "value": "25", "tab": "Lists and search", "setting": True, "anchor": "notes page size (default 25)"},
    {"key": "problem_report_recipients", "value": "the admins", "tab": "Notifications and mail", "setting": True, "anchor": "problem report recipients (default the admins)"},
    {"key": "notification_channel_defaults", "value": "every kind in-app; email also for trust flags, deadline reminders, update failures, and module alerts", "tab": "Notifications and mail", "setting": True, "anchor": "Defaults: every kind in-app; email also for trust flags, deadline reminders, update failures and module alerts."},
    {"key": "appearance_mode", "value": "follow the device", "tab": "Appearance", "setting": True, "anchor": "default follow the device"},
    {"key": "staff_portal_label", "value": "Staff portal", "tab": "Appearance", "setting": True, "anchor": 'default "Staff portal"'},
    {"key": "marketplace_address", "value": "the Togavsu marketplace", "tab": "Modules and marketplace", "setting": True, "anchor": "default the Togavsu marketplace"},
    {"key": "marketplace_alert_frequency", "value": "daily", "tab": "Modules and marketplace", "setting": True, "anchor": "alert check frequency (default daily)"},
    {"key": "update_window", "value": "2:00 to 4:00 a.m. firm time", "tab": "Updates", "setting": True, "anchor": "default 2:00 to 4:00 a.m. firm time"},
    {"key": "nightly_backup_time", "value": "1:00 a.m. firm time", "tab": "Backups and logs", "setting": True, "anchor": "default 1:00 a.m. firm time"},
    {"key": "local_backup_retention", "value": "30 days", "tab": "Backups and logs", "setting": True, "anchor": "local retention (default 30 days)"},
    {"key": "test_restore_day", "value": "the 1st", "tab": "Backups and logs", "setting": True, "anchor": "default the 1st"},
    {"key": "log_retention", "value": "400 days", "tab": "Backups and logs", "setting": True, "anchor": "default 400 days"},
    {"key": "webhook_retries", "value": "8 over 24 hours", "tab": "Agents and API", "setting": True, "anchor": "default 8 over 24 hours"},
    {"key": "ai_timeout", "value": "30 seconds", "tab": "AI and inference", "setting": True, "anchor": "default 30 seconds"},
    {"key": "labeling_model", "value": "gpt-6.1-sol", "tab": "AI and inference", "setting": True, "anchor": "default to gpt-6.1-sol"},
    {"key": "labeling_model_with_openai_key", "value": "gpt-6.1-sol when an OpenAI key is set", "tab": "AI and inference", "setting": True, "anchor": "Labeling and categorizing default to gpt-6.1-sol on OpenAI once an OpenAI key is set"},
    {"key": "categorizing_model_with_openai_key", "value": "gpt-6.1-sol when an OpenAI key is set", "tab": "AI and inference", "setting": True, "anchor": "Labeling and categorizing default to gpt-6.1-sol on OpenAI once an OpenAI key is set"},
    {"key": "labeling_model_without_openai_key", "value": "primary provider's strong model", "tab": "AI and inference", "setting": True, "anchor": "to the primary provider's strong model otherwise"},
    {"key": "categorizing_model_without_openai_key", "value": "primary provider's strong model", "tab": "AI and inference", "setting": True, "anchor": "to the primary provider's strong model otherwise"},
    {"key": "labeling_check_model_with_secondary", "value": "secondary provider's model for labeling", "tab": "AI and inference", "setting": True, "anchor": "Each check model defaults to the secondary provider's model for that task"},
    {"key": "categorizing_check_model_with_secondary", "value": "secondary provider's model for categorizing", "tab": "AI and inference", "setting": True, "anchor": "Each check model defaults to the secondary provider's model for that task"},
    {"key": "labeling_check_model_without_secondary", "value": "primary provider's other tier", "tab": "AI and inference", "setting": True, "anchor": "or to the primary provider's other tier when there's no secondary"},
    {"key": "categorizing_check_model_without_secondary", "value": "primary provider's other tier", "tab": "AI and inference", "setting": True, "anchor": "or to the primary provider's other tier when there's no secondary"},
    {"key": "ocr_check_model_with_secondary", "value": "secondary provider's model for OCR", "tab": "AI and inference", "setting": True, "anchor": "OCR, photo description and grouping, transcription, labeling and categorizing"},
    {"key": "ocr_check_model_without_secondary", "value": "primary provider's other tier for OCR", "tab": "AI and inference", "setting": True, "anchor": "Each check model defaults to the secondary provider's model for that task, or to the primary provider's other tier when there's no secondary."},
    {"key": "photo_description_grouping_check_model_with_secondary", "value": "secondary provider's model for photo description and grouping", "tab": "AI and inference", "setting": True, "anchor": "OCR, photo description and grouping, transcription, labeling and categorizing"},
    {"key": "photo_description_grouping_check_model_without_secondary", "value": "primary provider's other tier for photo description and grouping", "tab": "AI and inference", "setting": True, "anchor": "Each check model defaults to the secondary provider's model for that task, or to the primary provider's other tier when there's no secondary."},
    {"key": "transcription_check_model_with_secondary", "value": "secondary provider's model for transcription", "tab": "AI and inference", "setting": True, "anchor": "OCR, photo description and grouping, transcription, labeling and categorizing"},
    {"key": "transcription_check_model_without_secondary", "value": "primary provider's other tier for transcription", "tab": "AI and inference", "setting": True, "anchor": "Each check model defaults to the secondary provider's model for that task, or to the primary provider's other tier when there's no secondary."},
    {"key": "high_tier_categories", "value": "court_filing, court_notice, discovery, agency_notice, police_criminal, estate_planning, contracts_agreements", "tab": "Doc Tender", "setting": True, "anchor": "default `court_filing`, `court_notice`, `discovery`, `agency_notice`, `police_criminal`, `estate_planning`, `contracts_agreements`"},
    {"key": "trust_ledger_append_only", "value": "fixed", "tab": "", "setting": False, "anchor": "the trust ledger is append-only"},
    {"key": "audit_kept_forever", "value": "fixed", "tab": "", "setting": False, "anchor": "the audit log is kept forever"},
    {"key": "below_zero_always_flags", "value": "fixed", "tab": "", "setting": False, "anchor": "a below-zero trust entry always flags"},
    {"key": "service_layer_permissions", "value": "fixed", "tab": "", "setting": False, "anchor": "permissions are checked in the service layer"},
    {"key": "s3_and_dropbox_encryption_always_on", "value": "fixed on", "tab": "Files and storage", "setting": False, "anchor": "always on for S3-compatible and Dropbox"},
    {"key": "labeling_categorizing_no_local_endpoint", "value": "fixed off", "tab": "AI and inference", "setting": False, "anchor": "they never run on a local endpoint"},
)

# name, covers, folder, tier. Covers text is the PRD table cell.
DOC_CATEGORIES = (
    ("court_filing", "Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing", "Pleadings", "high"),
    ("court_notice", "Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link", "Pleadings", "high"),
    ("discovery", "Interrogatories, requests for production or admission, responses, depositions", "Discovery", "high"),
    ("correspondence", "Letter or email to or from the client, counsel or a third party, including plea offers", "Correspondence", "standard"),
    ("agency_notice", "Letter from a government agency, such as child support, IRS, DOR, Children's Division or MSPD", "Correspondence", "high"),
    ("estate_planning", "The will, trust, power of attorney, directive or beneficiary deed itself", "Client documents", "high"),
    ("property_title", "Deed, deed of trust, mortgage release, secured note, survey, title", "Client documents", "standard"),
    ("financial_record", "Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form", "Client documents", "standard"),
    ("personal_record", "ID, birth, death or dissolution certificate, driving record, completion certificate", "Client documents", "standard"),
    ("medical_insurance", "Medical record or evaluation, health or vision insurance document", "Client documents", "standard"),
    ("contracts_agreements", "Contract between parties, prenup, articles, bylaws or operating agreement", "Client documents", "high"),
    ("police_criminal", "Police report, probable cause statement, citation, bodycam report, restitution", "Evidence", "high"),
    ("evidence_media", "Photo, video, screenshot, text messages or call log, including phone message exports", "Evidence", "standard"),
    ("call_meeting", "Recording, transcript or notes of a phone call, voicemail or meeting", "Notes", "standard"),
    ("engagement_intake", "This firm's fee agreement, invoice, intake or consultation notes, MSPD forms", "Billing", "standard"),
    ("firm_work_product", "This firm's memo, research, hearing or trial prep, case notes", "Notes", "standard"),
    ("reference", "Article, statute, template, logo or blank form; not about a client matter", "Notes", "standard"),
)

CONDITIONAL_AI_KEYS = (
    "labeling_model_with_openai_key",
    "categorizing_model_with_openai_key",
    "labeling_model_without_openai_key",
    "categorizing_model_without_openai_key",
    "labeling_check_model_with_secondary",
    "categorizing_check_model_with_secondary",
    "labeling_check_model_without_secondary",
    "categorizing_check_model_without_secondary",
    "ocr_check_model_with_secondary",
    "ocr_check_model_without_secondary",
    "photo_description_grouping_check_model_with_secondary",
    "photo_description_grouping_check_model_without_secondary",
    "transcription_check_model_with_secondary",
    "transcription_check_model_without_secondary",
)

DEFAULTS = DEFAULTS + tuple(
    {
        "key": f"doc_category_{name}",
        "value": f"{name}: {covers}; folder {folder}; tier {tier}",
        "tab": "Doc Tender",
        "setting": True,
        "anchor": covers,
    }
    for name, covers, folder, tier in DOC_CATEGORIES
)


def setting_defaults():
    return [item for item in DEFAULTS if item["setting"]]


def missing_anchors(prd: str) -> list[str]:
    missing = []
    for item in DEFAULTS:
        if item["anchor"] not in prd:
            missing.append(item["key"])
    return missing


def prd_category_rows(prd: str) -> list[dict]:
    start = prd.index("| Category | Covers | Folder |")
    rest = prd[start:].split("**Markdown copy:**", 1)[0]
    rows = []
    for line in rest.splitlines():
        if not line.startswith("| `"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        rows.append({
            "name": cells[0].strip("`").strip(),
            "covers": cells[1].strip(),
            "folder": cells[2].strip(),
        })
    return rows


def prd_high_tier(prd: str) -> list[str]:
    marker = "High-tier categories (default "
    start = prd.index(marker) + len(marker)
    end = prd.index(")", start)
    return re.findall(r"`([a-z0-9_]+)`", prd[start:end])


def omitted_prd_defaults(prd: str, inventory=None) -> list[str]:
    """Names from the PRD category table and section 8.7 that the inventory lacks."""
    inventory = DEFAULTS if inventory is None else inventory
    by_key = {item["key"]: item for item in inventory}
    missing = []
    high = set(prd_high_tier(prd))
    for row in prd_category_rows(prd):
        key = f"doc_category_{row['name']}"
        item = by_key.get(key)
        if item is None:
            missing.append(key)
            continue
        value = item.get("value", "")
        tier = "high" if row["name"] in high else "standard"
        if row["covers"] not in value or f"folder {row['folder']}" not in value or f"tier {tier}" not in value:
            missing.append(f"{key} description, folder, or tier")
    for key in CONDITIONAL_AI_KEYS:
        if key not in by_key:
            missing.append(key)
    return missing
