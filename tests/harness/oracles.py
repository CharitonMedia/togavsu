"""Pure checks for PRD rules.

A green oracle is not product acceptance. The evidence gate refuses kind
"oracle". These functions exist so the positive and failure paths can run
before the Go services land.
"""

from __future__ import annotations

import re
from datetime import date, timedelta

MAX_SAVED_LAYOUTS = 10
CONFIDENCE_THRESHOLD_THOUSANDTHS = 900
PERF_USERS = 50
PERF_MATTERS = 20_000
PERF_TIME_ENTRIES = 1_000_000
PERF_P95_MS = 300
HIGH_TIER_CATEGORIES = frozenset({
    "court_filing",
    "court_notice",
    "discovery",
    "agency_notice",
    "police_criminal",
    "estate_planning",
    "contracts_agreements",
})
ALLOWED_NAME_TOKENS = frozenset({
    "clientlast",
    "clientfirst",
    "matter#",
    "mattername",
    "what-it-is",
    "doc-date",
    "received-date",
    "category",
    "ext",
})
STATUS_TOKENS = frozenset({
    "status",
    "draft",
    "final",
    "executed",
    "filed",
    "received",
    "file-status",
    "doc-status",
    "matter-status",
})
LOCKED_FILE_FIELDS = frozenset({"name", "category", "matter", "grouping"})
ACCOUNTANT_READS = frozenset({
    "invoices",
    "payments",
    "trust_ledger",
    "trust_reconciliations",
    "billing_reports",
    "exports",
    "file:receipt",
    "file:invoice_pdf",
})
TOKEN_RE = re.compile(r"<([^>]+)>")
ENV_RE = re.compile(
    r"\bos\.(?:Getenv|LookupEnv|Environ|getenv|environ)\b|\bsyscall\.Getenv\b"
)
TUNABLE_RE = re.compile(r"settings\.(?:Get|Must)\(\"([^\"]+)\"\)")
PONYTAIL_RE = re.compile("pony" + "tail:", re.IGNORECASE)


class LedgerError(PermissionError):
    pass


class TrustLedger:
    """Append-only stand-in for the AC-006 rule. Not the product ledger."""

    def __init__(self):
        self.entries = []

    def post(self, entry: dict) -> dict:
        stored = dict(entry)
        stored["id"] = len(self.entries) + 1
        self.entries.append(stored)
        return dict(stored)

    def get(self, entry_id: int) -> dict:
        for entry in self.entries:
            if entry["id"] == entry_id:
                return dict(entry)
        raise KeyError(entry_id)

    def update(self, entry_id: int, **fields):
        raise LedgerError("ledger entries cannot be edited")

    def delete(self, entry_id: int):
        raise LedgerError("ledger entries cannot be deleted")

    def reverse(self, entry_id: int, user: str) -> dict:
        original = self.get(entry_id)
        snapshot = dict(original)
        reversal = self.post({
            "type": "reversal",
            "reverses": entry_id,
            "client": original["client"],
            "matter": original["matter"],
            "amount_cents": -original["amount_cents"],
            "user": user,
            "description": f"reversal of {entry_id}",
        })
        if self.get(entry_id) != snapshot:
            raise RuntimeError("reversal changed the original entry")
        return reversal


def withdraw_below_zero(ledger: TrustLedger, balance_cents: int, amount_cents: int, admins: list[str], attorney: str) -> dict:
    """Post the withdrawal even when the client balance goes negative."""
    if amount_cents <= 0:
        raise ValueError("withdrawal amount must be positive cents")
    if not admins or not attorney:
        raise ValueError("admins and the responsible attorney are required")
    entry = ledger.post({
        "type": "withdrawal",
        "client": "example",
        "matter": "2026-0001",
        "amount_cents": -amount_cents,
        "user": attorney,
        "description": "synthetic withdrawal",
    })
    new_balance = balance_cents - amount_cents
    flagged = new_balance < 0
    return {
        "posted": True,
        "blocked": False,
        "entry_id": entry["id"],
        "balance_cents": new_balance,
        "flag": flagged,
        "emails": list(admins) + [attorney],
    }


def wall_visible(user_id: str, wall_user_ids: list[str] | None) -> bool:
    """A wall hides the matter from everyone who is not named, including an admin."""
    if not wall_user_ids:
        return True
    return user_id in wall_user_ids


def accountant_allowed(method: str, resource: str) -> bool:
    if method.upper() != "GET":
        return False
    return resource in ACCOUNTANT_READS


def can_save_layout(existing_count: int) -> bool:
    if existing_count < 0:
        raise ValueError("layout count cannot be negative")
    return existing_count < MAX_SAVED_LAYOUTS


def perf_within_budget(p95_page_ms: int, p95_api_ms: int, users: int, matters: int, time_entries: int) -> bool:
    return (
        p95_page_ms < PERF_P95_MS
        and p95_api_ms < PERF_P95_MS
        and users >= PERF_USERS
        and matters >= PERF_MATTERS
        and time_entries >= PERF_TIME_ENTRIES
    )


def low_confidence(score_thousandths: int, threshold_thousandths: int = CONFIDENCE_THRESHOLD_THOUSANDTHS) -> bool:
    return score_thousandths < threshold_thousandths


def naming_pattern_errors(pattern: str) -> list[str]:
    errors = []
    tokens = TOKEN_RE.findall(pattern)
    if not tokens:
        errors.append("pattern has no tokens")
    for token in tokens:
        key = token.strip().lower()
        if key in STATUS_TOKENS or key.startswith("status"):
            errors.append(f"status token <{token}> is refused")
            continue
        if key not in ALLOWED_NAME_TOKENS:
            errors.append(f"token <{token}> is not in the Doc Tender token list")
    return errors


def default_naming_pattern() -> str:
    return "<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>"


def category_publishable(category: str, label_model: str, check_model: str) -> bool:
    if category in HIGH_TIER_CATEGORIES:
        return label_model == category and check_model == category
    return label_model == category


def apply_reprocess(record: dict, model_output: dict, new_markdown: bytes) -> dict:
    locked = set(record.get("locked") or ())
    unknown = locked - LOCKED_FILE_FIELDS
    if unknown:
        raise ValueError(f"unknown locked field {sorted(unknown)}")
    updated = dict(record)
    for key, value in model_output.items():
        if key in locked:
            continue
        updated[key] = value
    for key in locked:
        updated[key] = record[key]
    versions = list(record.get("markdown_versions") or [])
    versions.append(new_markdown)
    updated["markdown_versions"] = versions
    return updated


def plan_doc_job(matter_status: str) -> dict:
    if matter_status == "closed":
        return {"model_call": False, "label": "Not processed (matter closed)"}
    return {"model_call": True, "label": "queued"}


def markdown_copy_name(original_name: str) -> str:
    if original_name.lower().endswith(".md"):
        return original_name
    base, dot, _ext = original_name.rpartition(".")
    if not dot:
        base = original_name
    return base + ".md"


def free_days(start: date, end: date, events: list[dict], half_days: bool = False) -> list:
    """Days with no court date, block, or all-day event. Does not copy events back."""
    if end < start:
        raise ValueError("end before start")
    busy = set()
    halves = {}
    for event in events:
        day = event["date"]
        kind = event.get("kind")
        if event.get("all_day") or kind in {"court date", "block"}:
            part = event.get("part")
            partial = part in {"morning", "afternoon"}
            if half_days and not event.get("all_day") and partial:
                halves.setdefault(day, set()).add(part)
            elif half_days and not event.get("all_day") and kind == "court date":
                halves.setdefault(day, set()).add(part or "full")
            else:
                busy.add(day)
    result = []
    day = start
    while day <= end:
        if day in busy:
            day += timedelta(days=1)
            continue
        if half_days and day in halves:
            parts = halves[day]
            if "full" in parts or {"morning", "afternoon"} <= parts:
                day += timedelta(days=1)
                continue
            if "morning" in parts:
                result.append((day, "afternoon"))
            elif "afternoon" in parts:
                result.append((day, "morning"))
            else:
                result.append(day)
        else:
            if day not in halves:
                result.append(day)
        day += timedelta(days=1)
    return result


def scan_env_and_tunables(files: dict[str, str], registry_keys: set[str]) -> list[str]:
    """Fixture scanner for the AC-030 rule. This is not the product CI check."""
    violations = []
    for path, text in files.items():
        parts = path.split("/")
        in_bootstrap = "bootstrap" in parts
        if not in_bootstrap and ENV_RE.search(text):
            violations.append(f"{path} reads an environment variable outside bootstrap")
        if in_bootstrap:
            continue
        for key in TUNABLE_RE.findall(text):
            if key not in registry_keys:
                violations.append(f"{path} uses tunable {key} that is not in the settings registry")
    return violations


def debt_marker_errors(text: str) -> list[str]:
    errors = []
    for line_no, line in enumerate(text.splitlines(), start=1):
        if PONYTAIL_RE.search(line) and "trigger:" not in line.lower():
            errors.append(f"line {line_no} has a ponytail marker and no upgrade trigger")
    return errors


def phase_report_errors(text: str) -> list[str]:
    errors = []
    lowered = text.lower()
    if "net line" not in lowered:
        errors.append("phase report has no simplification net line count")
    if "debt" not in lowered or "ledger" not in lowered:
        errors.append("phase report has no debt ledger count")
    return errors
