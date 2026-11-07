"""Section 5 record names the AC-035 seed must fill.

Names follow the PRD wording. Underscores in the markdown source are escaped.
"""

from __future__ import annotations

SECTION5_RECORDS = (
    "contacts",
    "contact_addresses",
    "contact_emails",
    "contact_phones",
    "contact_tags",
    "matters",
    "matter_parties",
    "matter_walls",
    "practice_areas",
    "counties",
    "referral_sources",
    "users",
    "sessions",
    "password credentials",
    "TOTP secrets",
    "recovery codes",
    "API tokens",
    "settings",
    "fee_agreements",
    "time_entries",
    "timers",
    "expenses",
    "invoices",
    "payments",
    "trust_accounts",
    "trust_ledger",
    "trust_flags",
    "trust_replenishment_requests",
    "trust_reconciliations",
    "notes",
    "conflict_records",
    "conflict_reviews",
    "calendar_events",
    "deadlines",
    "audit_log",
    "events",
    "files",
    "file_versions",
    "file_renditions",
    "file_links",
    "storage_backends",
    "storage_moves",
    "edit_locks",
    "court_profiles",
    "doc_categories",
    "doc_tender_jobs",
    "doc_tender_units",
    "doc_tender_results",
    "doc_tender_exceptions",
    "AI call log",
)

# Introduced after section 5. They do not satisfy AC-035.
LATER_RECORDS = (
    "home_layouts",
    "notifications",
    "help_sections",
    "help_items",
    "custom_field_definitions",
    "custom_field_values",
    "fee_items",
    "matter_markers",
    "notification_preferences",
)


def section5_text(prd: str) -> str:
    start = prd.index("## 5. Core records")
    end = prd.index("## 6. Architecture")
    return prd[start:end]


def record_in_section5(section: str, name: str) -> bool:
    if name in section:
        return True
    return name.replace("_", r"\_") in section
