"""Synthetic-data rules. Client files and production records are out of bounds."""

from __future__ import annotations

import re

EMAIL_RE = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
PHONE_RE = re.compile(r"\+\d{8,15}")
SSN_RE = re.compile(r"\b\d{3}-\d{2}-\d{4}\b")
ALLOWED_EMAIL_HOSTS = {"example.com", "example.net", "example.org"}
# NANP 555-0100 through 555-0199 are the fictional range. E.164, no spaces.
FICTIONAL_PHONE_RE = re.compile(r"^\+155555501\d{2}$")
FORBIDDEN_SNIPPETS = (
    "patrick",
    "nolan law",
    "confidential client",
    "client data export",
    "production database",
)


def email_errors(value: str) -> list[str]:
    errors = []
    host = value.rsplit("@", 1)[-1].lower() if "@" in value else ""
    if host not in ALLOWED_EMAIL_HOSTS:
        errors.append(f"email host {host or value!r} is outside example.com, example.net, and example.org")
    return errors


def phone_errors(value: str) -> list[str]:
    if not FICTIONAL_PHONE_RE.match(value):
        return [f"phone {value!r} is outside the fictional E.164 range +15555550100 to +15555550199"]
    return []


def ssn_errors(value: str) -> list[str]:
    if not re.fullmatch(r"9\d{2}-\d{2}-\d{4}", value):
        return [f"SSN {value!r} is not in the unissued 900-range test form"]
    return []


def contact_errors(record: dict) -> list[str]:
    """Return reasons a contact record is not allowed in a Togavsu test."""
    errors = []
    if record.get("origin") != "synthetic":
        errors.append("origin must be synthetic")
    if not record.get("full_name"):
        errors.append("full_name is empty")
    email = record.get("email")
    if email:
        errors.extend(email_errors(email))
    phone = record.get("phone")
    if phone:
        errors.extend(phone_errors(phone))
    ssn = record.get("ssn")
    if ssn:
        errors.extend(ssn_errors(ssn))
    return errors


def byte_errors(payload: bytes, label: str) -> list[str]:
    """Scan fixture bytes for client-data markers and non-synthetic identifiers."""
    errors = []
    try:
        text = payload.decode("utf-8")
    except UnicodeDecodeError:
        text = payload.decode("latin-1")
    lowered = text.lower()
    for snippet in FORBIDDEN_SNIPPETS:
        if snippet in lowered:
            errors.append(f"{label} contains forbidden snippet {snippet!r}")
    for match in EMAIL_RE.findall(text):
        errors.extend(f"{label}: {item}" for item in email_errors(match))
    for match in PHONE_RE.findall(text):
        if not FICTIONAL_PHONE_RE.match(match):
            errors.append(f"{label}: phone {match} is not in the fictional test range")
    for match in SSN_RE.findall(text):
        errors.extend(f"{label}: {item}" for item in ssn_errors(match))
    return errors
