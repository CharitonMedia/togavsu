"""Component schemas for the Togavsu OpenAPI 3.1 proposal."""

from __future__ import annotations

import copy

from contract.settings import SETTINGS, setting_keys

PROBLEM_CODES = [
    "validation_failed",
    "unauthenticated",
    "csrf_required",
    "ambiguous_auth",
    "forbidden",
    "not_found",
    "conflict",
    "idempotency_conflict",
    "append_only",
    "layout_limit",
    "batch_rejected",
    "rate_limited",
    "locked_out",
    "payload_too_large",
    "edit_lock_held",
    "status_token_refused",
    "local_endpoint_refused",
    "command_refused",
    "confirm_name_mismatch",
    "unsigned_module",
    "tampered_module",
    "permission_declined",
    "migration_refused",
    "marketplace_unconfigured",
    "duplicate_active",
    "quarantine_confirmation_required",
    "setup_required",
    "already_set_up",
    "use_provider_endpoint",
    "setting_locked",
    "scope_denied",
    "field_locked",
    "matter_mismatch",
    "duplicate_email",
]


def _trust_reversal_schema() -> dict:
    """Read response only. A client cannot post this shape through TrustEntryWrite."""
    return _obj(
        {
            "id": _uuid(),
            "created_at": _dt(),
            "created_by": _uuid("User who posted the compensation, or the nil id for System."),
            "trust_account_id": _uuid(),
            "client_id": _uuid(),
            "matter_id": _uuid(),
            "entry_type": {"const": "reversal"},
            "amount_cents": {
                "type": "integer",
                "not": {"const": 0},
                "description": "Signed cents. Negative debits the client and pairs with direction debit. Positive credits the client and pairs with direction credit.",
            },
            "direction": _enum(["credit", "debit"], "debit undoes a deposit. credit undoes a withdrawal, transfer, or refund."),
            "method": _PAY_METHOD,
            "description": {"type": "string"},
            "reverses_entry_id": _uuid("The original ledger row. This compensation does not edit that row."),
        },
        [
            "id",
            "created_at",
            "created_by",
            "trust_account_id",
            "client_id",
            "matter_id",
            "entry_type",
            "amount_cents",
            "direction",
            "method",
            "description",
            "reverses_entry_id",
        ],
        "Read-only reversal. reversePayment and reverseTrustEntry append it. The write schema rejects entry_type reversal.",
    )


def _trust_entry_schema(*, read: bool, adjustment: bool) -> dict:
    non_adjust = ["deposit", "withdrawal", "transfer_to_operating", "refund"]
    properties = {
        "trust_account_id": _uuid(),
        "client_id": _uuid(),
        "matter_id": _uuid(),
        "method": _PAY_METHOD,
        "description": {"type": "string", "minLength": 1} if not read else {"type": "string"},
        "reverses_entry_id": _null(_uuid()),
    }
    required = ["trust_account_id", "client_id", "matter_id", "entry_type", "amount_cents", "method", "description"]
    if adjustment:
        properties["entry_type"] = {"const": "adjustment"}
        properties["amount_cents"] = {
            "type": "integer",
            "not": {"const": 0},
            "description": "Signed cents. Positive credits the client. Negative debits the client.",
        }
        properties["adjustment_direction"] = _enum(
            ["credit", "debit"],
            "credit matches a positive amount. debit matches a negative amount.",
        )
        required.append("adjustment_direction")
    else:
        properties["entry_type"] = _enum(non_adjust)
        properties["amount_cents"] = _cents("Zero or greater. Direction comes from entry_type, not from the sign.")
    if read:
        properties = {
            "id": _uuid(),
            "created_at": _dt(),
            "created_by": _uuid("User who posted the row."),
            **properties,
        }
        required = ["id", "created_at", "created_by", *required]
    return _obj(properties, required)


def _obj(properties: dict, required: list[str] | None = None, description: str = "") -> dict:
    schema: dict = {"type": "object", "additionalProperties": False, "properties": properties}
    if required:
        schema["required"] = required
    if description:
        schema["description"] = description
    return schema


def _uuid(description: str = "UUIDv7.") -> dict:
    return {"type": "string", "format": "uuid", "description": description}


def _dt(description: str = "UTC timestamp.") -> dict:
    return {"type": "string", "format": "date-time", "description": description}


def _date(description: str = "Calendar date.") -> dict:
    return {"type": "string", "format": "date", "description": description}


def _cents(description: str = "Integer cents, zero or greater.") -> dict:
    return {"type": "integer", "minimum": 0, "description": description}


def _signed_cents(description: str = "Integer cents. Negative is a balance or an adjustment, not a ledger amount.") -> dict:
    return {"type": "integer", "description": description}


def _null(schema: dict) -> dict:
    return {"anyOf": [schema, {"type": "null"}]}


def _ref(name: str) -> dict:
    return {"$ref": f"#/components/schemas/{name}"}


def _enum(values: list[str], description: str = "") -> dict:
    schema: dict = {"type": "string", "enum": values}
    if description:
        schema["description"] = description
    return schema


def _readable_text(description: str = "") -> dict:
    """Plain string or the shared attorney-only marker. Write schemas stay plain strings."""
    return {
        "description": description or 'Plain text, or {"redacted": true} when the caller cannot read an attorney-only value.',
        "anyOf": [{"type": "string"}, _ref("RedactedText")],
    }


def _arr(items: dict, description: str = "") -> dict:
    schema: dict = {"type": "array", "items": items}
    if description:
        schema["description"] = description
    return schema


def _meta(extra: dict, required: list[str], description: str = "", mutable: bool = True) -> dict:
    properties = {
        "id": _uuid(),
        "created_at": _dt(),
        "created_by": _uuid("User who created the row, or the nil id for System."),
    }
    base_required = ["id", "created_at", "created_by"]
    if mutable:
        properties["updated_at"] = _dt()
        base_required.append("updated_at")
    properties.update(extra)
    return _obj(properties, base_required + required, description)


def _redacted(value_schema: dict, description: str) -> dict:
    hidden = _obj({"redacted": {"const": True}}, ["redacted"])
    shown = _obj({"redacted": {"const": False}, "value": value_schema}, ["redacted", "value"])
    return {"description": description, "anyOf": [hidden, shown]}


def _register(schemas: dict[str, dict]) -> dict[str, dict]:
    generated: dict[str, dict] = {}
    for name, schema in schemas.items():
        if name.endswith("Write"):
            patch = copy.deepcopy(schema)
            patch.pop("required", None)
            note = " Patch: every field is optional."
            patch["description"] = (patch.get("description") or name) + note
            generated[name + "Patch"] = patch
    schemas.update(generated)
    return schemas


_ROLE = _enum(["admin", "attorney", "staff", "accountant", "client"], "One role. A user may hold several.")
_KIND = _enum(["person", "organization"])
_MATTER_STATUS = _enum(["prospect", "open", "on_hold", "closed"])
_BILLING = _enum(["flat", "hourly", "mixed", "contingency", "pro_bono"])
_PARTY = _enum(
    [
        "client",
        "opposing_party",
        "opposing_counsel",
        "prosecutor",
        "judge",
        "expert",
        "witness",
        "co_counsel",
        "guardian_ad_litem",
        "referrer",
        "officer",
        "authorized_user",
    ],
    "Seeded party roles. The admin can add more; those use their own key.",
)
_INVOICE_STATUS = _enum(["draft", "sent", "viewed", "paid", "void"])
_PAY_STATUS = _enum(["pending", "completed", "failed", "reversed"])
_PAY_DEST = _enum(["operating", "trust"])
_PAY_METHOD = _enum(["cash", "check", "card", "ach", "internal_transfer", "other"])
_TRUST_TYPE = _enum(["deposit", "withdrawal", "transfer_to_operating", "refund", "adjustment"])
_FILE_STATUS = _enum(["draft", "final", "executed", "filed", "received"])
_SOURCE = _enum(["upload", "client_upload", "editor", "email", "zoom", "drop_folder", "importer", "module"])
_VERSION_HOW = _enum(["upload", "editor", "editor_run", "conversion", "grouping", "module"])
_ACTOR = _enum(["staff", "client", "agent", "module", "system"])
_FONTS = ["Lora", "Merriweather", "Libre Baskerville", "Source Serif", "Inter", "IBM Plex Sans", "Source Sans", "Atkinson Hyperlegible"]

SCHEMAS = _register(
    {
        "RedactedText": _obj(
            {"redacted": {"const": True}},
            ["redacted"],
            "Attorney-only text withheld from this caller. Events, webhooks, file summaries, and history rows use this same object.",
        ),
        "Uuid": _uuid(),
        "Role": _ROLE,
        "Problem": _obj(
            {
                "type": {"type": "string", "format": "uri", "description": "Problem type URI under the firm origin."},
                "title": {"type": "string"},
                "status": {"type": "integer"},
                "code": _enum(PROBLEM_CODES),
                "detail": {"type": "string"},
                "request_id": _uuid("Request id, also sent as X-Request-Id."),
                "errors": _arr(_ref("FieldError")),
            },
            ["type", "title", "status", "code", "detail", "request_id"],
            "Error body for every non-2xx JSON response.",
        ),
        "FieldError": _obj(
            {
                "path": {"type": "string", "description": "JSON pointer or item index path, such as /items/0/amount_cents."},
                "code": {"type": "string"},
                "detail": {"type": "string"},
            },
            ["path", "code", "detail"],
        ),
        "Page": _obj(
            {
                "limit": {"type": "integer", "minimum": 1},
                "next_cursor": _null({"type": "string", "description": "Opaque cursor. Null is the last page."}),
            },
            ["limit", "next_cursor"],
            "Cursor page. The client passes next_cursor back as the cursor parameter.",
        ),
        "Included": _obj(
            {},
            description="Module extension rows keyed by module-id.view. A table that is not keyed to this core record cannot be included.",
        ),
        "Contact": _meta(
            {
                "kind": _KIND,
                "given_name": {"type": "string"},
                "middle_name": {"type": "string"},
                "family_name": {"type": "string"},
                "suffix": {"type": "string"},
                "organization_name": {"type": "string"},
                "full_name": {"type": "string", "description": "Server-built display name."},
                "preferred_name": {"type": "string"},
                "also_known_as": _arr({"type": "string"}),
                "date_of_birth": _null(_date()),
                "date_of_death": _null(_date()),
                "identifier": _redacted({"type": "string"}, "SSN or EIN. Redacted without attorney-only access, including for an accountant."),
                "identifier_kind": _null(_enum(["ssn", "ein"])),
                "entity_type": {"type": "string"},
                "formation_state": {"type": "string"},
                "formation_date": _null(_date()),
                "archived": {"type": "boolean"},
                "source": {"type": "string"},
                "tags": _arr({"type": "string"}),
                "custom_fields": _arr(_ref("CustomFieldValue")),
                "included": _ref("Included"),
            },
            ["kind", "full_name", "archived", "also_known_as", "tags", "custom_fields"],
            "A person or organization.",
        ),
        "ContactWrite": _obj(
            {
                "kind": _KIND,
                "given_name": {"type": "string"},
                "middle_name": {"type": "string"},
                "family_name": {"type": "string"},
                "suffix": {"type": "string"},
                "organization_name": {"type": "string"},
                "preferred_name": {"type": "string"},
                "also_known_as": _arr({"type": "string"}),
                "date_of_birth": _null(_date()),
                "date_of_death": _null(_date()),
                "identifier": {"type": "string", "description": "SSN or EIN. Stored encrypted. Never returned in the clear to a caller without attorney-only access."},
                "identifier_kind": _null(_enum(["ssn", "ein"])),
                "entity_type": {"type": "string"},
                "formation_state": {"type": "string"},
                "formation_date": _null(_date()),
                "source": {"type": "string"},
                "custom_fields": _arr(_ref("CustomFieldInput")),
            },
            ["kind"],
            "Writable contact fields. full_name is computed. custom_fields is written in the same transaction as the contact. Required definitions that apply must be present.",
        ),
        "ContactAddress": _meta(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "line1": {"type": "string"},
                "line2": {"type": "string"},
                "city": {"type": "string"},
                "region": {"type": "string"},
                "postal_code": {"type": "string"},
                "country": {"type": "string"},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "label", "primary", "line1", "city", "region", "postal_code", "country"],
            "Postal address. History is a new row, not an overwrite of an effective range.",
        ),
        "ContactAddressWrite": _obj(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "line1": {"type": "string"},
                "line2": {"type": "string"},
                "city": {"type": "string"},
                "region": {"type": "string"},
                "postal_code": {"type": "string"},
                "country": {"type": "string"},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "label", "line1"],
        ),
        "ContactEmail": _meta(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "email": {"type": "string", "format": "email"},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "label", "primary", "email"],
            "Email address kept with its effective dates.",
        ),
        "ContactEmailWrite": _obj(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "email": {"type": "string", "format": "email"},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "email"],
        ),
        "ContactPhone": _meta(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "e164": {"type": "string", "pattern": "^\\+[1-9]\\d{1,14}$", "description": "E.164 phone."},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "label", "primary", "e164"],
            "Phone number stored in E.164.",
        ),
        "ContactPhoneWrite": _obj(
            {
                "contact_id": _uuid(),
                "label": {"type": "string"},
                "primary": {"type": "boolean"},
                "e164": {"type": "string", "pattern": "^\\+[1-9]\\d{1,14}$"},
                "effective_from": _null(_date()),
                "effective_to": _null(_date()),
            },
            ["contact_id", "e164"],
        ),
        "ContactTag": _meta(
            {"contact_id": _uuid(), "tag": {"type": "string", "minLength": 1}},
            ["contact_id", "tag"],
            "Free tag on a contact.",
        ),
        "ContactTagWrite": _obj({"contact_id": _uuid(), "tag": {"type": "string", "minLength": 1}}, ["contact_id", "tag"]),
        "Matter": _meta(
            {
                "number": {"type": "string", "description": "Matter number from the firm format, assigned by the server."},
                "name": {"type": "string"},
                "practice_area_id": _uuid(),
                "status": _MATTER_STATUS,
                "stage": {"type": "string"},
                "case_number": {"type": "string"},
                "state_case_number": {"type": "string"},
                "court": {"type": "string"},
                "county_id": _null(_uuid()),
                "client_position": {"type": "string"},
                "opened_on": _date(),
                "closed_on": _null(_date("Last close date. Reopen does not clear it.")),
                "close_reason": {"type": "string"},
                "status_before_close": _null(_MATTER_STATUS),
                "statute_of_limitations_on": _null(_date()),
                "billing_type": _BILLING,
                "flat_fee_cents": _null(_cents()),
                "matter_rate_cents": _null(_cents()),
                "responsible_attorney_user_id": _null(_uuid()),
                "assigned_staff_user_ids": _arr(_uuid()),
                "referral_source_id": _null(_uuid()),
                "evergreen_minimum_cents": _null(_cents()),
                "pro_bono": {"type": "boolean"},
                "zero_fee": {"type": "boolean", "description": "Zero fee is not pro bono. These matters drop out of AR aging and collections."},
                "also_asked_about_practice_area_id": _null(_uuid()),
                "unchecked_conflicts": {"type": "boolean", "description": "True until every party has a conflict check."},
                "archived": {"type": "boolean", "description": "Set by archiveMatter. Distinct from closed."},
                "custom_fields": _arr(_ref("CustomFieldValue")),
                "glance": _ref("MatterGlance"),
                "included": _ref("Included"),
            },
            ["number", "name", "practice_area_id", "status", "billing_type", "opened_on", "pro_bono", "zero_fee", "unchecked_conflicts", "archived", "custom_fields", "glance"],
            "A matter. Walled matters are omitted for a caller who is not on the wall.",
        ),
        "MatterWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "practice_area_id": _uuid(),
                "status": _MATTER_STATUS,
                "stage": {"type": "string"},
                "case_number": {"type": "string"},
                "state_case_number": {"type": "string"},
                "court": {"type": "string"},
                "county_id": _null(_uuid()),
                "client_position": {"type": "string"},
                "opened_on": _date(),
                "statute_of_limitations_on": _null(_date()),
                "billing_type": _BILLING,
                "flat_fee_cents": _null(_cents()),
                "matter_rate_cents": _null(_cents()),
                "responsible_attorney_user_id": _null(_uuid()),
                "assigned_staff_user_ids": _arr(_uuid()),
                "referral_source_id": _null(_uuid()),
                "evergreen_minimum_cents": _null(_cents()),
                "pro_bono": {"type": "boolean"},
                "zero_fee": {"type": "boolean"},
                "also_asked_about_practice_area_id": _null(_uuid()),
                "custom_fields": _arr(_ref("CustomFieldInput")),
            },
            ["name", "practice_area_id", "billing_type", "opened_on"],
            "Writable matter fields. number is assigned. Close and reopen use their own methods. custom_fields is written in the same transaction. A required field for this practice area must be present.",
        ),
        "MatterGlance": _obj(
            {
                "stage": {"type": "string"},
                "trust_balance_cents": _signed_cents(),
                "time_logged_hours": {"type": "string", "description": "Decimal hours derived from increments."},
                "time_entry_count": {"type": "integer", "minimum": 0},
                "days_open": {"type": "integer", "minimum": 0},
                "invoice_count": {"type": "integer", "minimum": 0},
                "file_count": {"type": "integer", "minimum": 0},
                "open_doc_tender_exceptions": {"type": "integer", "minimum": 0},
            },
            ["stage", "trust_balance_cents", "time_logged_hours", "time_entry_count", "days_open", "invoice_count", "file_count", "open_doc_tender_exceptions"],
            "At a Glance card.",
        ),
        "CloseMatter": _obj({"reason": {"type": "string"}}, [], "Close reason. The close is stored even when warnings are present."),
        "CloseResult": _obj(
            {
                "matter": _ref("Matter"),
                "warnings": _arr(
                    _obj(
                        {
                            "code": _enum(["unpaid_invoice", "nonzero_trust"]),
                            "detail": {"type": "string"},
                        },
                        ["code", "detail"],
                    )
                ),
            },
            ["matter", "warnings"],
            "Close result. warnings does not stop the close. The preview method returns the same shape and writes nothing.",
        ),
        "ReopenMatter": _obj({"reason": {"type": "string", "minLength": 1}}, ["reason"], "Required reason. The previous status is restored. Close date and close reason stay on the record."),
        "MergeRequest": _obj({"survivor_id": _uuid("Record that remains.")}, ["survivor_id"]),
        "MergeResult": _obj(
            {
                "survivor_id": _uuid(),
                "archived_id": _uuid(),
                "modules_relinked": _arr(_obj({"module_id": {"type": "string"}, "rows": {"type": "integer", "minimum": 0}}, ["module_id", "rows"])),
            },
            ["survivor_id", "archived_id", "modules_relinked"],
            "Merge moves module foreign keys in the same transaction and archives the other record. It does not hard-delete.",
        ),
        "MatterParty": _meta(
            {
                "matter_id": _uuid(),
                "contact_id": _uuid(),
                "role": {"type": "string", "description": "A seeded role key or an admin-defined party role key."},
                "primary": {"type": "boolean"},
                "role_attributes": {"type": "object", "additionalProperties": True},
                "service_address_id": _null(_uuid()),
                "notes": {"type": "string"},
                "included": _ref("Included"),
            },
            ["matter_id", "contact_id", "role", "primary"],
            "A contact's role on a matter.",
        ),
        "MatterPartyWrite": _obj(
            {
                "matter_id": _uuid(),
                "contact_id": _uuid(),
                "role": {"type": "string", "minLength": 1},
                "primary": {"type": "boolean"},
                "role_attributes": {"type": "object", "additionalProperties": True},
                "service_address_id": _null(_uuid()),
                "notes": {"type": "string"},
            },
            ["matter_id", "contact_id", "role"],
        ),
        "MatterWall": _obj(
            {
                "matter_id": _uuid(),
                "user_ids": _arr(_uuid(), "Users allowed through the wall. Empty removes the wall."),
            },
            ["matter_id", "user_ids"],
            "Replacement set of users on the wall. Anyone else loses the matter from lists, search, widgets, reports, and the API.",
        ),
        "MatterMarker": _meta(
            {
                "matter_id": _uuid(),
                "marker_id": _uuid(),
                "practice_area_id": _null(_uuid("Set when the marker is Also asked about.")),
            },
            ["matter_id", "marker_id"],
            "A marker pinned to a matter. Pro bono and zero fee are also booleans on the matter.",
        ),
        "MatterMarkerWrite": _obj(
            {"matter_id": _uuid(), "marker_id": _uuid(), "practice_area_id": _null(_uuid())},
            ["matter_id", "marker_id"],
        ),
        "MarkerDefinition": _meta(
            {"label": {"type": "string"}, "color": {"type": "string"}, "active": {"type": "boolean"}, "module_id": {"type": "string"}},
            ["label", "color", "active"],
            "Firm or module marker definition.",
        ),
        "MarkerDefinitionWrite": _obj(
            {"label": {"type": "string", "minLength": 1}, "color": {"type": "string"}, "active": {"type": "boolean"}},
            ["label"],
        ),
        "FeeAgreement": _meta(
            {
                "matter_id": _uuid(),
                "fee_type": _BILLING,
                "amount_cents": _null(_cents()),
                "services_description": {"type": "string"},
                "status": _enum(["draft", "sent", "signed", "void"]),
                "signed_on": _null(_date()),
                "void_reason": {"type": "string"},
                "signed_file_id": _null(_uuid()),
                "included": _ref("Included"),
            },
            ["matter_id", "fee_type", "services_description", "status"],
            "One signed agreement is active per matter. A second active agreement returns duplicate_active.",
        ),
        "FeeAgreementWrite": _obj(
            {
                "matter_id": _uuid(),
                "fee_type": _BILLING,
                "amount_cents": _null(_cents()),
                "services_description": {"type": "string"},
                "status": _enum(["draft", "sent", "signed", "void"]),
                "signed_on": _null(_date()),
                "void_reason": {"type": "string"},
                "signed_file_id": _null(_uuid()),
            },
            ["matter_id", "fee_type", "services_description"],
        ),
        "Note": _meta(
            {
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
                "author_id": _uuid(),
                "title": {"type": "string"},
                "body": {"type": "string"},
                "pinned": {"type": "boolean"},
                "tags": _arr({"type": "string"}),
                "status": _enum(["active", "archived"]),
                "rich_text": {"type": "boolean", "description": "True when an agent or module wrote the note. The server sanitizes it before display."},
                "included": _ref("Included"),
            },
            ["author_id", "title", "body", "pinned", "tags", "status", "rich_text"],
            "A note on a matter or a contact. One of matter_id or contact_id is required.",
        ),
        "NoteWrite": _obj(
            {
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
                "title": {"type": "string"},
                "body": {"type": "string"},
                "pinned": {"type": "boolean"},
                "tags": _arr({"type": "string"}),
                "status": _enum(["active", "archived"]),
            },
            ["title", "body"],
            "One of matter_id or contact_id is required.",
        ),
        "TimeEntry": _meta(
            {
                "matter_id": _uuid(),
                "user_id": _uuid(),
                "worked_on": _date(),
                "increments": {"type": "integer", "minimum": 0, "description": "Count of the firm's time increment."},
                "description": {"type": "string"},
                "activity_type_id": _uuid(),
                "billable": {"type": "boolean"},
                "rate_cents": _cents(),
                "invoice_id": _null(_uuid()),
                "source": {"type": "string", "description": "manual, timer, api, or a module id."},
                "custom_fields": _arr(_ref("CustomFieldValue")),
                "included": _ref("Included"),
            },
            ["matter_id", "user_id", "worked_on", "increments", "description", "activity_type_id", "billable", "rate_cents", "source", "custom_fields"],
            "A time entry. Increments follow billing.time_increment_minutes.",
        ),
        "TimeEntryWrite": _obj(
            {
                "matter_id": _uuid(),
                "user_id": _uuid(),
                "worked_on": _date(),
                "increments": {"type": "integer", "minimum": 0},
                "description": {"type": "string"},
                "activity_type_id": _uuid(),
                "billable": {"type": "boolean"},
                "rate_cents": _cents(),
                "source": {"type": "string"},
                "custom_fields": _arr(_ref("CustomFieldInput")),
            },
            ["matter_id", "worked_on", "increments", "description", "activity_type_id"],
            "custom_fields is written in the same transaction as the time entry. Definitions follow the matter's practice area.",
        ),
        "Timer": _meta(
            {
                "matter_id": _uuid(),
                "user_id": _uuid(),
                "started_at": _dt(),
                "accumulated_seconds": {"type": "integer", "minimum": 0, "description": "Seconds already counted. Pause stores them. Resume does not zero them."},
                "status": _enum(["running", "paused"]),
            },
            ["matter_id", "user_id", "started_at", "accumulated_seconds", "status"],
            "One running or paused timer per user per matter. accumulated_seconds survives pause and resume.",
        ),
        "TimerWrite": _obj(
            {"matter_id": _uuid()},
            ["matter_id"],
            "Starts a timer for the caller on this matter. If a paused timer already exists for that user and matter, the server resumes it and keeps accumulated_seconds instead of creating a second timer.",
        ),
        "TimerStopResult": _obj({"timer": _ref("Timer"), "time_entry": _ref("TimeEntry")}, ["timer", "time_entry"], "Stopping a timer creates a draft time entry."),
        "Expense": _meta(
            {
                "matter_id": _uuid(),
                "incurred_on": _date(),
                "description": {"type": "string"},
                "amount_cents": _cents(),
                "expense_type_id": _uuid(),
                "billable": {"type": "boolean"},
                "markup_percent": {"type": "integer", "minimum": 0},
                "receipt_file_id": _null(_uuid()),
                "invoice_id": _null(_uuid()),
                "source": {"type": "string"},
                "custom_fields": _arr(_ref("CustomFieldValue")),
                "included": _ref("Included"),
            },
            ["matter_id", "incurred_on", "description", "amount_cents", "expense_type_id", "billable", "markup_percent", "source", "custom_fields"],
            "An expense. The receipt is a core file record.",
        ),
        "ExpenseWrite": _obj(
            {
                "matter_id": _uuid(),
                "incurred_on": _date(),
                "description": {"type": "string"},
                "amount_cents": _cents(),
                "expense_type_id": _uuid(),
                "billable": {"type": "boolean"},
                "markup_percent": {"type": "integer", "minimum": 0},
                "receipt_file_id": _null(_uuid()),
                "source": {"type": "string"},
                "custom_fields": _arr(_ref("CustomFieldInput")),
            },
            ["matter_id", "incurred_on", "description", "amount_cents", "expense_type_id"],
            "custom_fields is written in the same transaction as the expense. Definitions follow the matter's practice area.",
        ),
        "InvoiceLine": _obj(
            {
                "kind": _enum(["time", "expense", "flat_fee", "adjustment"]),
                "source_id": _null(_uuid()),
                "description": {"type": "string"},
                "amount_cents": _signed_cents(),
                "fee_item_id": _null(_uuid()),
            },
            ["kind", "description", "amount_cents"],
        ),
        "Invoice": _meta(
            {
                "matter_id": _uuid(),
                "number": {"type": "string"},
                "issued_on": _date(),
                "status": _INVOICE_STATUS,
                "lines": _arr(_ref("InvoiceLine")),
                "subtotal_cents": _signed_cents(),
                "adjustment_cents": _signed_cents(),
                "adjustment_reason": {"type": "string"},
                "interest_cents": _cents(),
                "total_cents": _signed_cents(),
                "amount_paid_cents": _cents(),
                "balance_due_cents": _signed_cents(),
                "write_off_cents": _cents(),
                "write_off_reason": {"type": "string"},
                "void_reason": {"type": "string"},
                "dispute_status": {"type": "string"},
                "dispute_notes": {"type": "string"},
                "sent_at": _null(_dt()),
                "paid_at": _null(_dt()),
                "client_message": {"type": "string"},
                "internal_notes": {"type": "string", "description": "Staff only. Absent from InvoiceClient."},
                "included": _ref("Included"),
            },
            ["matter_id", "number", "issued_on", "status", "lines", "subtotal_cents", "adjustment_cents", "interest_cents", "total_cents", "amount_paid_cents", "balance_due_cents", "write_off_cents"],
            "An invoice for staff, attorneys, admins, and accountants. A client receives InvoiceClient.",
        ),
        "InvoiceClient": _obj(
            {
                "id": _uuid(),
                "matter_id": _uuid(),
                "number": {"type": "string"},
                "issued_on": _date(),
                "status": _INVOICE_STATUS,
                "lines": _arr(_ref("InvoiceLine")),
                "subtotal_cents": _signed_cents(),
                "adjustment_cents": _signed_cents(),
                "interest_cents": _cents(),
                "total_cents": _signed_cents(),
                "amount_paid_cents": _cents(),
                "balance_due_cents": _signed_cents(),
                "sent_at": _null(_dt()),
                "paid_at": _null(_dt()),
                "client_message": {"type": "string"},
            },
            ["id", "matter_id", "number", "issued_on", "status", "lines", "subtotal_cents", "adjustment_cents", "interest_cents", "total_cents", "amount_paid_cents", "balance_due_cents"],
            "Invoice fields a client may see. internal_notes is not a property.",
        ),
        "InvoiceClientHistoryEntry": _obj(
            {
                "at": _dt(),
                "actor_kind": _ACTOR,
                "field": _enum(["status", "total_cents", "amount_paid_cents", "balance_due_cents", "sent_at", "paid_at", "client_message", "lines"]),
            },
            ["at", "actor_kind", "field"],
            "Client invoice history. old_value and new_value are absent. internal_notes is not a field.",
        ),
        "InvoicePatch": _obj(
            {
                "client_message": {"type": "string"},
                "internal_notes": {"type": "string"},
                "dispute_status": {"type": "string"},
                "dispute_notes": {"type": "string"},
                "issued_on": _date(),
            },
            description="Fields that can change after the invoice leaves draft. Draft lines, fee items, and the adjustment use updateInvoiceDraft.",
        ),
        "InvoiceDraftEdit": _obj(
            {
                "lines": _arr(_ref("InvoiceLine")),
                "adjustment_cents": _signed_cents(),
                "adjustment_reason": {"type": "string"},
                "client_message": {"type": "string"},
                "issued_on": _date(),
            },
            ["lines"],
            "Replaces the lines on a draft invoice. A line may set fee_item_id. The description and amount stay editable after the catalog fills them in. adjustment_cents and adjustment_reason are the invoice adjustment.",
        ),
        "InvoiceDraftRequest": _obj(
            {
                "matter_id": _null(_uuid()),
                "matter_ids": _arr(_uuid()),
                "from_date": _null(_date()),
                "to_date": _null(_date()),
            },
            description="Drafts invoices from unbilled time and expenses. A flat-fee matter bills the fee. One of matter_id or matter_ids is required.",
        ),
        "InvoiceSend": _obj(
            {"deliver": _enum(["email", "file"])},
            ["deliver"],
            "email sends the PDF by system mail and marks the invoice sent. file marks it sent without mail; the PDF is GET .../pdf.",
        ),
        "Reason": _obj({"reason": {"type": "string", "minLength": 1}}, ["reason"]),
        "ApplyTrust": _obj(
            {
                "client_id": _uuid("The client whose trust is debited. A matter can have more than one client."),
                "trust_account_id": _uuid("The active firm account that holds this client's matter balance. The server does not pick another account."),
                "amount_cents": _cents("Explicit amount. The server does not substitute the balance."),
            },
            ["client_id", "trust_account_id", "amount_cents"],
            "Moves trust funds from the named account to operating, posts one ledger entry, and applies a payment to the invoice.",
        ),
        "WriteOff": _obj({"amount_cents": _cents(), "reason": {"type": "string", "minLength": 1}}, ["amount_cents", "reason"]),
        "Payment": _meta(
            {
                "contact_id": _uuid(),
                "matter_id": _uuid(),
                "amount_cents": _cents(),
                "destination": _PAY_DEST,
                "method": _PAY_METHOD,
                "reference_number": {"type": "string"},
                "status": _PAY_STATUS,
                "invoice_id": _null(_uuid()),
                "trust_account_id": _null(_uuid("Set when destination is trust. Null when destination is operating.")),
                "trust_entry_id": _null(_uuid()),
                "source": {"type": "string", "description": "manual, api, or a module id."},
                "included": _ref("Included"),
            },
            ["contact_id", "matter_id", "amount_cents", "destination", "method", "status", "source", "trust_account_id"],
            "A payment. Amount, destination, and trust account do not change after create. A correction is a reversal.",
        ),
        "PaymentWrite": {
            "description": "Records a payment. destination trust requires trust_account_id of an active authorized firm account. destination operating omits trust_account_id. internal_transfer is reserved for apply-trust.",
            "oneOf": [
                _obj(
                    {
                        "contact_id": _uuid(),
                        "matter_id": _uuid(),
                        "amount_cents": _cents(),
                        "destination": {"const": "operating"},
                        "method": _PAY_METHOD,
                        "reference_number": {"type": "string"},
                        "status": _PAY_STATUS,
                        "invoice_id": _null(_uuid()),
                        "source": {"type": "string"},
                    },
                    ["contact_id", "matter_id", "amount_cents", "destination", "method"],
                ),
                _obj(
                    {
                        "contact_id": _uuid(),
                        "matter_id": _uuid(),
                        "amount_cents": _cents(),
                        "destination": {"const": "trust"},
                        "trust_account_id": _uuid(),
                        "method": _PAY_METHOD,
                        "reference_number": {"type": "string"},
                        "status": _PAY_STATUS,
                        "invoice_id": _null(_uuid()),
                        "source": {"type": "string"},
                    },
                    ["contact_id", "matter_id", "amount_cents", "destination", "trust_account_id", "method"],
                ),
            ],
        },
        "PaymentStatusWrite": _obj(
            {"status": _enum(["pending", "completed", "failed"])},
            ["status"],
            "Processor status transition. reversed is only produced by the reverse method. Amount stays as it was.",
        ),
        "ProcessorInitializationWrite": {
            "description": "Asks the core to authorize one registered processor. destination trust requires trust_account_id. destination operating omits it. The grant is delivered to the module and is not in this request.",
            "oneOf": [
                _obj(
                    {
                        "processor_module_id": {"type": "string", "minLength": 1},
                        "contact_id": _uuid(),
                        "matter_id": _uuid(),
                        "amount_cents": _cents(),
                        "destination": {"const": "operating"},
                        "invoice_id": _null(_uuid()),
                    },
                    ["processor_module_id", "contact_id", "matter_id", "amount_cents", "destination"],
                ),
                _obj(
                    {
                        "processor_module_id": {"type": "string", "minLength": 1},
                        "contact_id": _uuid(),
                        "matter_id": _uuid(),
                        "amount_cents": _cents(),
                        "destination": {"const": "trust"},
                        "trust_account_id": _uuid(),
                        "invoice_id": _null(_uuid()),
                    },
                    ["processor_module_id", "contact_id", "matter_id", "amount_cents", "destination", "trust_account_id"],
                ),
            ],
        },
        "ProcessorInitialization": _obj(
            {
                "id": _uuid(),
                "payment_id": _uuid("Reserved at authorization. createPayment must use this id and cannot insert a second payment for this initialization."),
                "processor_module_id": {"type": "string"},
                "contact_id": _uuid(),
                "matter_id": _uuid(),
                "amount_cents": _cents(),
                "destination": _PAY_DEST,
                "trust_account_id": _null(_uuid("Null when destination is operating.")),
                "invoice_id": _null(_uuid()),
                "status": _enum(["authorized", "denied"]),
                "expires_at": _dt(),
            },
            ["id", "payment_id", "processor_module_id", "contact_id", "matter_id", "amount_cents", "destination", "trust_account_id", "status", "expires_at"],
            "Core authorization record. The payment id is reserved here. No payment row is created here. processor_authorization is delivered on moduleEvent.",
        ),
        "ProcessorAuthorization": _obj(
            {
                "module_id": {"type": "string", "minLength": 1},
                "initialization_id": _uuid(),
                "payment_id": _uuid("Reserved at authorization. One initialization binds this payment id exactly once."),
                "matter_id": _uuid(),
                "contact_id": _uuid(),
                "invoice_id": _null(_uuid()),
                "amount_cents": _cents(),
                "destination": _PAY_DEST,
                "trust_account_id": _null(_uuid("Null when destination is operating. Otherwise the authorized account.")),
                "allowed_operations": _arr({"type": "string"}),
                "allowed_fields": _arr({"type": "string"}),
                "expires_at": _dt(),
                "revoked": {"type": "boolean"},
                "issued_by": {"const": "core"},
            },
            [
                "module_id",
                "initialization_id",
                "payment_id",
                "matter_id",
                "contact_id",
                "amount_cents",
                "destination",
                "trust_account_id",
                "allowed_operations",
                "allowed_fields",
                "expires_at",
                "revoked",
                "issued_by",
            ],
            "Bounded processor capability issued by the core. It is not an admin grant and it does not require a signed-in viewer.",
        ),
        "TrustAccount": _meta(
            {
                "bank": {"type": "string"},
                "name": {"type": "string"},
                "iolta": {"type": "boolean"},
                "last_four": {"type": "string", "pattern": "^\\d{4}$"},
                "active": {"type": "boolean"},
                "included": _ref("Included"),
            },
            ["bank", "name", "iolta", "last_four", "active"],
            "A firm trust bank account. The full account number is not stored.",
        ),
        "TrustAccountWrite": _obj(
            {
                "bank": {"type": "string", "minLength": 1},
                "name": {"type": "string", "minLength": 1},
                "iolta": {"type": "boolean"},
                "last_four": {"type": "string", "pattern": "^\\d{4}$"},
                "active": {"type": "boolean"},
            },
            ["bank", "name", "last_four"],
        ),
        "TrustEntry": {
            "description": (
                "Append-only ledger row. Deposit, withdrawal, transfer, and refund amounts are zero or greater. "
                "An adjustment amount is signed: positive credits the client and negative debits the client. "
                "A reversal is read-only compensation: amount_cents is signed, direction is debit when the amount is negative, "
                "and reverses_entry_id names the original row. TrustEntryWrite does not accept entry_type reversal."
            ),
            "oneOf": [
                _trust_entry_schema(read=True, adjustment=False),
                _trust_entry_schema(read=True, adjustment=True),
                _trust_reversal_schema(),
            ],
        },
        "TrustEntryWrite": {
            "description": "Posts one append-only row. Non-adjustment amounts are nonnegative. An adjustment requires adjustment_direction and a signed amount_cents.",
            "oneOf": [
                _trust_entry_schema(read=False, adjustment=False),
                _trust_entry_schema(read=False, adjustment=True),
            ],
        },
        "TrustPostResult": _obj(
            {
                "entry": _ref("TrustEntry"),
                "balances": _ref("TrustBalances"),
                "flags": _arr(_ref("TrustFlag")),
            },
            ["entry", "balances", "flags"],
            "The entry is posted. flags is non-empty when a client balance went below zero.",
        ),
        "TrustBalances": _obj(
            {
                "matter_cents": _signed_cents(),
                "client_cents": _signed_cents(),
                "account_cents": _signed_cents(),
            },
            ["matter_cents", "client_cents", "account_cents"],
            "Snapshot returned with one posted entry. The entry already names the client, matter, and account. The list endpoint uses TrustBalanceRow.",
        ),
        "TrustBalanceRow": {
            "description": "One balance the caller can tie to an owner. matter sets client_id and matter_id. client sets client_id. account sets trust_account_id.",
            "oneOf": [
                _obj(
                    {
                        "scope": {"const": "matter"},
                        "client_id": _uuid(),
                        "matter_id": _uuid(),
                        "trust_account_id": _uuid(),
                        "balance_cents": _signed_cents(),
                    },
                    ["scope", "client_id", "matter_id", "trust_account_id", "balance_cents"],
                ),
                _obj(
                    {
                        "scope": {"const": "client"},
                        "client_id": _uuid(),
                        "balance_cents": _signed_cents(),
                    },
                    ["scope", "client_id", "balance_cents"],
                ),
                _obj(
                    {
                        "scope": {"const": "account"},
                        "trust_account_id": _uuid(),
                        "balance_cents": _signed_cents(),
                    },
                    ["scope", "trust_account_id", "balance_cents"],
                ),
            ],
        },
        "TrustFlag": _meta(
            {
                "trust_entry_id": _uuid(),
                "client_id": _uuid(),
                "matter_id": _uuid(),
                "balance_cents": _signed_cents(),
                "scope": _enum(["matter", "client"]),
                "status": _enum(["open", "resolved"]),
                "resolution_note": {"type": "string"},
                "resolved_by": _null(_uuid()),
                "resolved_at": _null(_dt()),
            },
            ["trust_entry_id", "client_id", "matter_id", "balance_cents", "scope", "status"],
            "A below-zero flag. Email goes to the admins and the responsible attorney.",
        ),
        "TrustFlagResolve": _obj({"note": {"type": "string", "minLength": 1}}, ["note"], "An attorney or admin resolves the flag with a note."),
        "TrustReplenishment": _meta(
            {
                "matter_id": _uuid(),
                "client_id": _uuid(),
                "amount_requested_cents": _cents(),
                "threshold_cents": _null(_cents()),
                "invoice_id": _null(_uuid()),
                "message": {"type": "string"},
                "status": _enum(["active", "satisfied", "cancelled"]),
            },
            ["matter_id", "client_id", "amount_requested_cents", "message", "status"],
            "A replenishment request from the evergreen minimum or from collections.",
        ),
        "TrustReplenishmentWrite": _obj(
            {
                "matter_id": _uuid(),
                "client_id": _uuid(),
                "amount_requested_cents": _cents(),
                "invoice_id": _null(_uuid()),
                "message": {"type": "string"},
            },
            ["matter_id", "client_id", "amount_requested_cents"],
        ),
        "TrustReconciliation": _meta(
            {
                "trust_account_id": _uuid(),
                "statement_on": _date(),
                "statement_balance_cents": _signed_cents(),
                "ledger_total_cents": _signed_cents(),
                "client_balances_sum_cents": _signed_cents(),
                "statement_difference_cents": _signed_cents("statement_balance_cents minus ledger_total_cents."),
                "client_sum_difference_cents": _signed_cents("ledger_total_cents minus client_balances_sum_cents."),
            },
            ["trust_account_id", "statement_on", "statement_balance_cents", "ledger_total_cents", "client_balances_sum_cents", "statement_difference_cents", "client_sum_difference_cents"],
            "A monthly reconciliation. Differences are stored, including zero.",
        ),
        "TrustReconciliationWrite": _obj(
            {
                "trust_account_id": _uuid(),
                "statement_on": _date(),
                "statement_balance_cents": _signed_cents(),
            },
            ["trust_account_id", "statement_on", "statement_balance_cents"],
            "The caller enters the statement. The server computes the ledger total, the client sum, and both differences.",
        ),
        "CalendarEvent": _meta(
            {
                "title": {"type": "string"},
                "kind": _enum(["court_date", "client_meeting", "deadline", "block", "other"]),
                "starts_at": _dt(),
                "ends_at": _dt(),
                "all_day": {"type": "boolean"},
                "matter_id": _null(_uuid()),
                "location": {"type": "string"},
                "travel_minutes": {"type": "integer", "minimum": 0},
                "notes": {"type": "string"},
                "attendee_user_ids": _arr(_uuid()),
                "included": _ref("Included"),
            },
            ["title", "kind", "starts_at", "ends_at", "all_day", "attendee_user_ids"],
            "A calendar event.",
        ),
        "CalendarEventWrite": _obj(
            {
                "title": {"type": "string", "minLength": 1},
                "kind": _enum(["court_date", "client_meeting", "deadline", "block", "other"]),
                "starts_at": _dt(),
                "ends_at": _dt(),
                "all_day": {"type": "boolean"},
                "matter_id": _null(_uuid()),
                "location": {"type": "string"},
                "travel_minutes": {"type": "integer", "minimum": 0},
                "notes": {"type": "string"},
                "attendee_user_ids": _arr(_uuid()),
            },
            ["title", "kind", "starts_at", "ends_at"],
        ),
        "Deadline": _meta(
            {
                "matter_id": _uuid(),
                "title": {"type": "string"},
                "due_on": _date(),
                "responsible_user_id": _uuid(),
                "status": _enum(["open", "done", "waived"]),
                "reminder_offsets_days": _arr({"type": "integer"}),
                "done_by": _null(_uuid()),
                "done_at": _null(_dt()),
                "included": _ref("Included"),
            },
            ["matter_id", "title", "due_on", "responsible_user_id", "status", "reminder_offsets_days"],
            "A deadline. Reminders use the offsets, or the firm default when the list is empty on create.",
        ),
        "DeadlineWrite": _obj(
            {
                "matter_id": _uuid(),
                "title": {"type": "string", "minLength": 1},
                "due_on": _date(),
                "responsible_user_id": _uuid(),
                "status": _enum(["open", "done", "waived"]),
                "reminder_offsets_days": _arr({"type": "integer"}),
            },
            ["matter_id", "title", "due_on", "responsible_user_id"],
        ),
        "AvailableDatesRequest": _obj(
            {
                "user_id": _uuid("Attorney whose calendar is read."),
                "start_date": _date(),
                "end_date": _null(_date("Defaults to the configured number of months after start_date.")),
                "include_half_days": {"type": "boolean"},
                "matter_id": _null(_uuid()),
                "court": {"type": "string"},
                "caption": {"type": "string"},
                "party_labels": _obj({"left": {"type": "string"}, "right": {"type": "string"}}, ["left", "right"]),
                "left_party_name": {"type": "string"},
                "right_party_name": {"type": "string"},
                "case_number": {"type": "string"},
                "firm_side": _enum(["left", "right"]),
                "purpose": {"type": "string"},
                "coordinate_with": {"type": "string"},
                "title": {"type": "string"},
                "service_date": _null(_date()),
                "letter_recipient": {"type": "string"},
                "letter_address": {"type": "string"},
                "letter_re": {"type": "string"},
                "letter_salutation": {"type": "string"},
            },
            ["user_id", "start_date"],
            "Read-only available-dates request. It creates, changes, and deletes no calendar event.",
        ),
        "AvailableDatesResponse": _obj(
            {
                "dates": _arr(_obj({"date": _date(), "portion": _enum(["full", "morning", "afternoon"])}, ["date", "portion"])),
                "pleading": {"type": "string"},
                "letter": {"type": "string"},
            },
            ["dates", "pleading", "letter"],
            "Days with no court date, block, or all-day event. Half days appear only when requested.",
        ),
        "IcalFeed": _meta(
            {
                "owner_user_id": _null(_uuid("Null is the firm feed.")),
                "revoked": {"type": "boolean"},
            },
            ["revoked"],
            "An iCal feed. The secret URL is returned only from create.",
        ),
        "IcalFeedCreated": _obj({"feed": _ref("IcalFeed"), "url": {"type": "string"}}, ["feed", "url"]),
        "IcalFeedWrite": _obj({"owner_user_id": _null(_uuid())}, description="Null owner creates the firm feed. A user id creates that user's feed."),
        "File": _meta(
            {
                "name": {"type": "string", "description": "The file's only name. The extension is part of the name."},
                "extension": {"type": "string"},
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
                "folder_id": _null(_uuid()),
                "category_id": _null(_uuid()),
                "status": _null(_FILE_STATUS),
                "origin": _enum(["firm", "received"]),
                "attorney_only": {"type": "boolean"},
                "shared_with_client": {"type": "boolean"},
                "source": {"type": "string"},
                "arrived_as": {"type": "string", "description": "Name the file arrived with. Kept for audit and page order. Not the display name."},
                "locked_fields": _arr(_enum(["name", "category", "matter", "grouping"])),
                "summary": _readable_text("Hover summary from the published Doc Tender result. Omitted when that result does not exist yet. Attorney-only text is {\"redacted\": true}."),
                "current_version_id": _null(_uuid()),
                "trashed_at": _null(_dt()),
                "trashed_by": _null(_uuid()),
                "warnings": _arr(_obj({"code": {"type": "string"}, "detail": {"type": "string"}}, ["code", "detail"])),
                "included": _ref("Included"),
            },
            ["name", "extension", "origin", "attorney_only", "shared_with_client", "source", "locked_fields", "warnings"],
            "A core file record. Bytes live on a storage backend under an opaque key that this API does not return.",
        ),
        "FileRename": _obj({"name": {"type": "string", "minLength": 1}}, ["name"], "The extension stays. A rename is edit history."),
        "FileMove": _obj({"folder_id": _null(_uuid())}, ["folder_id"], "Moves the record between folders. Bytes stay where they are."),
        "FileStatusWrite": _obj({"status": _FILE_STATUS}, ["status"], "Mark draft, final, executed, or filed. A model does not set status."),
        "FileTrash": _obj({}, description="Moves the file to the trash. It stays listed and restorable."),
        "FilePurge": _obj({"confirm_name": {"type": "string", "minLength": 1}}, ["confirm_name"], "Admin only. The typed name must match. The audit row remains."),
        "FileVersion": _meta(
            {
                "file_id": _uuid(),
                "number": {"type": "integer", "minimum": 1},
                "storage_backend_id": _uuid(),
                "size_bytes": {"type": "integer", "minimum": 0},
                "sha256": {"type": "string", "pattern": "^[a-f0-9]{64}$"},
                "media_type": {"type": "string"},
                "saved_by": _uuid(),
                "how": _VERSION_HOW,
                "note": {"type": "string"},
                "quarantined": {"type": "boolean"},
            },
            ["file_id", "number", "storage_backend_id", "size_bytes", "sha256", "media_type", "saved_by", "how", "quarantined"],
            "An immutable file version. The storage key is not in the API.",
            mutable=False,
        ),
        "FileGrantView": _obj(
            {
                "name": {"type": "string"},
                "extension": {"type": "string"},
            },
            ["name", "extension"],
            "Complete file metadata for a file.version_created task grant. origin, created_by, and every other file field are absent.",
        ),
        "FileVersionGrantView": _obj(
            {
                "media_type": {"type": "string"},
                "size_bytes": {"type": "integer", "minimum": 0},
                "sha256": {"type": "string", "pattern": "^[a-f0-9]{64}$"},
                "quarantined": {"type": "boolean"},
            },
            ["media_type", "size_bytes", "sha256", "quarantined"],
            "Complete version metadata for a file.version_created task grant. storage_backend_id, saved_by, and created_by are absent.",
        ),
        "FileVersionGrantPage": _obj(
            {
                "projection": {"const": "grant"},
                "data": _arr(_ref("FileVersionGrantView")),
                "page": _ref("Page"),
            },
            ["projection", "data", "page"],
            "One page of grant version projections. projection is grant so an empty data array is not also a FileVersion page.",
        ),
        "FileRendition": _meta(
            {
                "file_version_id": _uuid(),
                "kind": _enum(["markdown", "pdf_preview", "grouped_pdf"]),
                "number": {"type": "integer", "minimum": 1},
                "size_bytes": {"type": "integer", "minimum": 0},
                "sha256": {"type": "string", "pattern": "^[a-f0-9]{64}$"},
                "made_by": {"type": "string"},
            },
            ["file_version_id", "kind", "number", "size_bytes", "sha256", "made_by"],
            "A rendition. Markdown copies are kept. Reprocessing adds a version and does not overwrite.",
            mutable=False,
        ),
        "FileUpload": _obj(
            {
                "id": _uuid(),
                "owner_user_id": _uuid("The caller who started the upload. The client cannot name another user."),
                "chunk_size_bytes": {"type": "integer", "minimum": 1},
                "next_offset": {"type": "integer", "minimum": 0, "description": "Byte offset of the next chunk after a dropped connection."},
                "size_bytes": {"type": "integer", "minimum": 0},
                "expires_at": _dt(),
                "status": _enum(["receiving", "staged", "published", "expired"]),
            },
            ["id", "owner_user_id", "chunk_size_bytes", "next_offset", "size_bytes", "expires_at", "status"],
            "A resumable upload owned by the caller. Chunks are files.upload_chunk_bytes, starting at 8 MB, and the last chunk may be shorter. status staged means the bytes are stored and no file record exists yet. getFileUpload returns next_offset.",
        ),
        "FileUploadWrite": _obj(
            {
                "file_name": {"type": "string", "minLength": 1},
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
                "media_type": {"type": "string"},
                "size_bytes": {"type": "integer", "minimum": 0},
                "sha256": {"type": "string", "pattern": "^[a-f0-9]{64}$"},
            },
            ["file_name", "size_bytes"],
            "Starts an upload. size_bytes above files.max_upload_bytes is payload_too_large.",
        ),
        "FileBulkItem": _obj(
            {
                "upload_id": _uuid(),
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
                "name": {"type": "string"},
                "attorney_only": {"type": "boolean"},
                "shared_with_client": {"type": "boolean"},
            },
            ["upload_id"],
            "Publishes one staged upload. upload_id is unique in the batch. A repeated id rejects the batch. Publishing an upload that already has a file replays that file and emits no new version event.",
        ),
        "BackgroundFileGrantRequest": _obj(
            {
                "matter_id": _uuid("The matter the service principal can already access. Another matter is not_found."),
                "source": _enum(["email", "zoom", "importer", "drop_folder"]),
            },
            ["matter_id", "source"],
            "Asks the core for a pre-upload publication grant. The body cannot name a module id, a role, or an expiry. The core sets those.",
        ),
        "BackgroundFileAuthorization": _obj(
            {
                "module_id": {"type": "string", "minLength": 1},
                "matter_id": _uuid(),
                "source": _enum(["email", "zoom", "importer", "drop_folder"]),
                "allowed_operations": _arr({"type": "string"}),
                "expires_at": _dt("After this instant the grant is forbidden."),
                "revoked": {"type": "boolean"},
                "expired": {"type": "boolean"},
                "issued_by": {"const": "core"},
                "upload_ids": _arr(_uuid(), "Uploads created under this grant. Empty until createFileUpload."),
            },
            ["module_id", "matter_id", "source", "allowed_operations", "expires_at", "revoked", "expired", "issued_by", "upload_ids"],
            "Core-issued pre-upload grant for one installed module, one matter, and the publication sequence. It is not an admin grant.",
        ),
        "FileMetadataWrite": _obj(
            {
                "attorney_only": {"type": "boolean"},
                "shared_with_client": {"type": "boolean"},
                "category_id": _null(_uuid()),
                "matter_id": _null(_uuid()),
                "contact_id": _null(_uuid()),
            },
            description="Metadata a person may edit without a new file version. Doc Tender still cannot replace a person-locked name, category, matter, or grouping. Name stays on the rename method. Folder stays on the move method.",
        ),
        "FileLink": _meta(
            {
                "from_file_id": _uuid(),
                "to_file_id": _uuid(),
                "relationship": _enum(["conversion_of", "grouped_photo_source", "attachment"]),
            },
            ["from_file_id", "to_file_id", "relationship"],
            "A typed link. conversion_of: to_file was converted from from_file. grouped_photo_source: from_file is a photo in the grouped to_file. attachment: to_file is attached to from_file. A caller who cannot read either file does not receive the link. The relationship does not change after create.",
            mutable=False,
        ),
        "FileLinkWrite": _obj(
            {
                "to_file_id": _uuid(),
                "relationship": {"const": "attachment"},
            },
            ["to_file_id", "relationship"],
            "Creates an attachment link only. conversion_of and grouped_photo_source are produced by conversion and Doc Tender and are rejected here.",
        ),
        "ConversionResult": _obj(
            {"file": _ref("File"), "link": _ref("FileLink")},
            ["file", "link"],
            "The new file and the conversion_of link back to the source.",
        ),
        "FileHistoryEntry": _obj(
            {
                "at": _dt(),
                "actor_id": _null(_uuid()),
                "actor_kind": _enum(["staff", "client", "agent", "module", "system"]),
                "kind": _enum(["field_edit", "version_save"]),
                "field": {"type": "string"},
                "old_value": _readable_text("Previous value. Attorney-only text is {\"redacted\": true}."),
                "new_value": _readable_text("New value. Attorney-only text is {\"redacted\": true}."),
                "version_id": _null(_uuid("Set when kind is version_save.")),
            },
            ["at", "actor_kind", "kind"],
            "One file history row: a field edit or a version save. Visible to anyone who can read the file.",
        ),
        "FileArchiveRequest": _obj(
            {
                "file_ids": _arr(_uuid()),
                "folder_id": _null(_uuid()),
                "confirm_quarantine": {"type": "boolean", "description": "Must be true when any selected version is quarantined."},
            },
            description="One of file_ids or folder_id. confirm_quarantine defaults to false. A quarantined version without confirmation returns quarantine_confirmation_required and no zip.",
        ),
        "QuarantineWrite": _obj({"quarantined": {"type": "boolean"}}, ["quarantined"], "A malware-scanning module sets the flag. The version stays visible."),
        "EditLock": _obj(
            {"file_id": _uuid(), "user_id": _uuid(), "since": _dt(), "renewed_at": _dt()},
            ["file_id", "user_id", "since", "renewed_at"],
            "The lock lapses after editor.lock_timeout_seconds without a renewal. The starting value is 5 minutes.",
        ),
        "Folder": _meta(
            {"matter_id": _uuid(), "name": {"type": "string"}, "sort_order": {"type": "integer"}},
            ["matter_id", "name", "sort_order"],
            "A folder record. Moving a file between folders does not move bytes.",
        ),
        "FolderWrite": _obj({"matter_id": _uuid(), "name": {"type": "string", "minLength": 1}, "sort_order": {"type": "integer"}}, ["matter_id", "name"]),
        "StorageBackend": _meta(
            {
                "kind": _enum(["local_disk", "network_path", "s3", "dropbox"]),
                "name": {"type": "string"},
                "encryption": {"type": "boolean"},
                "default": {"type": "boolean"},
                "health": _enum(["unknown", "ok", "failed"]),
                "backed_up": {"type": "boolean"},
                "drop_folder": {"type": "boolean"},
            },
            ["kind", "name", "encryption", "default", "health", "backed_up", "drop_folder"],
            "Storage adapter settings. Secrets are write-only. S3 and Dropbox encryption cannot be turned off.",
        ),
        "StorageBackendWrite": _obj(
            {
                "kind": _enum(["local_disk", "network_path", "s3", "dropbox"]),
                "name": {"type": "string", "minLength": 1},
                "encryption": {"type": "boolean"},
                "default": {"type": "boolean"},
                "backed_up": {"type": "boolean"},
                "drop_folder": {"type": "boolean"},
                "settings": {"type": "object", "additionalProperties": True, "description": "Backend settings. Secret fields are stored encrypted and are not returned."},
            },
            ["kind", "name"],
        ),
        "StorageMove": _meta(
            {
                "from_backend_id": _uuid(),
                "to_backend_id": _uuid(),
                "scope": _enum(["all", "matter"]),
                "matter_id": _null(_uuid()),
                "status": _enum(["running", "stopped", "checking", "awaiting_confirmation", "completed", "failed"]),
                "progress_done": {"type": "integer", "minimum": 0},
                "progress_total": {"type": "integer", "minimum": 0},
                "confirmed_by": _null(_uuid()),
            },
            ["from_backend_id", "to_backend_id", "scope", "status", "progress_done", "progress_total"],
            "A backend move. Old copies are deleted only after confirmation.",
        ),
        "StorageMoveWrite": _obj(
            {
                "from_backend_id": _uuid(),
                "to_backend_id": _uuid(),
                "scope": _enum(["all", "matter"]),
                "matter_id": _null(_uuid()),
            },
            ["from_backend_id", "to_backend_id", "scope"],
        ),
        "CourtProfile": _meta(
            {
                "name": {"type": "string"},
                "verified": {"type": "boolean"},
                "verified_by": _null(_uuid()),
                "verified_at": _null(_dt()),
                "body": {"type": "object", "additionalProperties": True, "description": "LawCraft court profile JSON, stored whole."},
            },
            ["name", "verified", "body"],
            "A firm court profile. An agent-made profile stays unverified until a person approves it.",
        ),
        "CourtProfileWrite": _obj(
            {"name": {"type": "string", "minLength": 1}, "body": {"type": "object", "additionalProperties": True}},
            ["name", "body"],
        ),
        "EditorInfo": _obj(
            {
                "commit": {"type": "string", "const": "770a8fab12392485c0c2789fb254da7d9296df4d"},
                "source": {"type": "string", "const": "CharitonMedia/LawCraft"},
                "read_only": {"type": "boolean", "const": True},
            },
            ["commit", "source", "read_only"],
            "Pinned LawCraft commit. It is not a setting.",
        ),
        "EditorCommand": _obj(
            {
                "id": {"type": "string"},
                "title": {"type": "string"},
                "parameters_schema": {"type": "object", "additionalProperties": True},
            },
            ["id", "title", "parameters_schema"],
            "An allowed headless command. Filesystem and network commands are absent from this list.",
        ),
        "EditorRun": _obj(
            {
                "commands": _arr(_obj({"id": {"type": "string"}, "parameters": {"type": "object", "additionalProperties": True}}, ["id"])),
                "save_as": _enum(["version", "new_file"]),
                "file_name": {"type": "string"},
            },
            ["commands", "save_as"],
            "Runs allowed commands on the current version. A filesystem or network command is command_refused.",
        ),
        "EditorRunResult": _obj(
            {"file": _ref("File"), "version": _ref("FileVersion")},
            ["file", "version"],
        ),
        "Conversion": _obj(
            {"format": _enum(["pdf", "docx", "odt", "rtf", "html", "markdown", "text"])},
            ["format"],
            "Makes a new file in the requested format, linked to its source.",
        ),
        "DocTenderJob": _meta(
            {
                "file_version_id": _uuid(),
                "stage": {"type": "string"},
                "status": _enum(["queued", "running", "published", "exception", "not_processed_matter_closed", "held_for_provider", "held_for_ai_setup"]),
                "attempts": {"type": "integer", "minimum": 0},
                "next_attempt_at": _null(_dt()),
                "last_error": {"type": "string"},
            },
            ["file_version_id", "stage", "status", "attempts"],
            "A Doc Tender job. A closed matter is stored as not_processed_matter_closed and makes no model call. The published labels are getDocTenderResult, not this object.",
            mutable=False,
        ),
        "DocTenderException": _meta(
            {
                "file_id": _uuid(),
                "kind": _enum(["low_confidence", "check_disagreement", "unmatched_matter", "extraction_failed", "password_protected", "damaged", "unsupported_format", "provider_down"]),
                "detail": {"type": "string"},
                "best_answer": {"type": "object", "additionalProperties": True},
                "status": _enum(["open", "resolved"]),
                "resolved_by": _null(_uuid()),
                "resolved_at": _null(_dt()),
                "resolution": {"type": "string"},
            },
            ["file_id", "kind", "detail", "status"],
            "A review-queue item. The file stays listed and downloadable.",
        ),
        "DocTenderResolve": _obj(
            {
                "action": _enum(["accept", "correct", "assign_matter", "retry", "keep"]),
                "name": {"type": "string"},
                "category_id": _null(_uuid()),
                "matter_id": _null(_uuid()),
                "grouping": {"type": "string"},
                "note": {"type": "string"},
            },
            ["action"],
            "A person resolving an exception locks name, category, matter, or grouping when they set one. Doc Tender does not change a locked field later.",
        ),
        "Citation": {
            "description": "Where the field was read. At least one of page or timestamp is non-null.",
            "oneOf": [
                _obj(
                    {"page": {"type": "integer", "minimum": 1}, "timestamp": {"type": "null"}},
                    ["page"],
                    "Page only.",
                ),
                _obj(
                    {"page": {"type": "null"}, "timestamp": {"type": "string", "minLength": 1}},
                    ["timestamp"],
                    "Timestamp only.",
                ),
                _obj(
                    {
                        "page": {"type": "integer", "minimum": 1},
                        "timestamp": {"type": "string", "minLength": 1},
                    },
                    ["page", "timestamp"],
                    "Page and timestamp.",
                ),
            ],
        },
        "CitedField": _obj(
            {
                "name": {"type": "string"},
                "value": {"type": "string"},
                "confidence": {"type": "number", "minimum": 0, "maximum": 1},
                "citation": _ref("Citation"),
            },
            ["name", "value", "confidence", "citation"],
            "One critical field from labeling, with its confidence and citation.",
        ),
        "DocTenderResult": _obj(
            {
                "file_id": _uuid(),
                "file_version_id": _uuid(),
                "summary": _readable_text(),
                "name_parts": _obj(
                    {"what_it_is": {"type": "string"}, "doc_date": _null(_date())},
                    ["what_it_is"],
                ),
                "category_id": _null(_uuid()),
                "category_source": _enum(["model", "model_agreement", "person"]),
                "fields": _arr(_ref("CitedField")),
                "matter_id": _null(_uuid()),
                "markdown_rendition_id": _null(_uuid()),
            },
            ["file_id", "file_version_id", "summary", "name_parts", "fields"],
            "Published Doc Tender labels for one file version. fields are the cited critical values.",
        ),
        "DocCategory": _meta(
            {
                "key": {"type": "string"},
                "description": {"type": "string"},
                "tier": _enum(["high", "standard"]),
                "folder_name": {"type": "string"},
                "active": {"type": "boolean"},
            },
            ["key", "description", "tier", "folder_name", "active"],
            "A Doc Tender category. Retiring sets active false and leaves the category on files that have it.",
        ),
        "DocCategoryWrite": _obj(
            {
                "key": {"type": "string", "minLength": 1},
                "description": {"type": "string"},
                "tier": _enum(["high", "standard"]),
                "folder_name": {"type": "string", "minLength": 1},
                "active": {"type": "boolean"},
            },
            ["key", "description", "tier", "folder_name"],
        ),
        "NamingPattern": _obj(
            {
                "pattern": {"type": "string", "minLength": 1},
                "allowed_tokens": _arr({"type": "string"}),
            },
            ["pattern", "allowed_tokens"],
            "The naming pattern setting. Allowed tokens are clientlast, clientfirst, matter#, mattername, what-it-is, doc-date, received-date, and category. A status token is refused.",
        ),
        "User": _meta(
            {
                "name": {"type": "string"},
                "email": {"type": "string", "format": "email"},
                "phone": {"type": "string"},
                "initials": {"type": "string"},
                "roles": _arr(_ROLE),
                "active": {"type": "boolean"},
                "default_hourly_rate_cents": _null(_cents()),
                "bar_number": {"type": "string"},
                "bar_state": {"type": "string"},
                "linked_contact_id": _null(_uuid()),
                "profile_file_id": _null(_uuid()),
                "included": _ref("Included"),
            },
            ["name", "email", "roles", "active"],
            "A user. Password, TOTP secret, and recovery codes are not on this object.",
        ),
        "UserCreate": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "email": {"type": "string", "format": "email"},
                "phone": {"type": "string"},
                "roles": _arr(_ROLE),
                "password": {"type": "string", "minLength": 12},
                "default_hourly_rate_cents": _null(_cents()),
                "bar_number": {"type": "string"},
                "bar_state": {"type": "string"},
                "linked_contact_id": _null(_uuid()),
            },
            ["name", "email", "roles", "password"],
        ),
        "UserWrite": _obj(
            {
                "name": {"type": "string"},
                "email": {"type": "string", "format": "email"},
                "phone": {"type": "string"},
                "roles": _arr(_ROLE),
                "default_hourly_rate_cents": _null(_cents()),
                "bar_number": {"type": "string"},
                "bar_state": {"type": "string"},
                "linked_contact_id": _null(_uuid()),
                "initials": {"type": "string"},
            },
            description="Admin edits to a user. Self-service profile edits use MeProfileWrite.",
        ),
        "MeProfileWrite": _obj(
            {
                "name": {"type": "string"},
                "phone": {"type": "string"},
                "initials": {"type": "string"},
                "profile_file_id": _null(_uuid("Profile picture file record.")),
            },
            description="Fields the signed-in user may change on PATCH /api/v1/me. roles, email, and linked_contact_id are absent. Email changes use requestEmailChange and confirmEmailChange.",
        ),
        "EmailChangeRequest": _obj(
            {"email": {"type": "string", "format": "email"}},
            ["email"],
            "The address the caller wants. It is not saved until confirmEmailChange.",
        ),
        "EmailChangePending": _obj(
            {"status": {"const": "verification_sent"}, "email": {"type": "string", "format": "email"}},
            ["status", "email"],
            "A verification message was sent to the new address. The current email is unchanged.",
        ),
        "EmailChangeConfirm": _obj(
            {"token": {"type": "string", "minLength": 1}},
            ["token"],
            "Token from the verification message. It changes only the caller's email.",
        ),
        "Session": _obj(
            {"id": _uuid(), "user_id": _uuid(), "created_at": _dt(), "expires_at": _dt(), "current": {"type": "boolean"}},
            ["id", "user_id", "created_at", "expires_at", "current"],
        ),
        "ApiToken": _meta(
            {
                "name": {"type": "string"},
                "owner_user_id": _null(_uuid()),
                "module_id": {"type": "string"},
                "scopes": _arr({"type": "string"}),
                "revoked": {"type": "boolean"},
            },
            ["name", "scopes", "revoked"],
            "Token metadata. The secret is returned only from create.",
        ),
        "ApiTokenWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "owner_user_id": _null(_uuid()),
                "scopes": _arr({"type": "string"}),
            },
            ["name", "scopes"],
            "Issues a token. A non-admin may issue a token only for themselves, and only with scopes their role already has.",
        ),
        "ApiTokenCreated": _obj({"token": _ref("ApiToken"), "secret": {"type": "string"}}, ["token", "secret"]),
        "Setting": _obj(
            {
                "key": {"type": "string"},
                "type": _enum(["string", "integer", "number", "boolean", "json", "uuid"]),
                "tab": {"type": "string"},
                "value": {"description": "Current value. A secret setting returns null and sets configured instead."},
                "default": {"description": "Starting value from the registry."},
                "configured": {"type": "boolean"},
                "locked": {"type": "boolean"},
                "help": {"type": "string"},
                "allowed": {"description": "Allowed values when the setting has a closed set."},
            },
            ["key", "type", "tab", "configured", "locked", "help"],
            "One settings-registry row.",
        ),
        "SettingKey": _enum(setting_keys()),
        "SettingUpdate": _obj({"value": {"description": "New value, checked against the registry type and allowed set."}}, ["value"]),
        "Appearance": _obj(
            {
                "preset": _enum(["navy_gold", "charcoal_copper", "forest_cream", "maximum_contrast"]),
                "staff_sidebar": {"type": "string"},
                "staff_header": {"type": "string"},
                "staff_page": {"type": "string"},
                "staff_accent": {"type": "string"},
                "client_header": {"type": "string"},
                "heading_font": _enum(_FONTS),
                "body_font": _enum(_FONTS),
                "text_size": _enum(["sm", "md", "lg"]),
                "mode": _enum(["light", "dark", "device"]),
            },
            ["preset", "staff_sidebar", "staff_header", "staff_page", "staff_accent", "client_header", "heading_font", "body_font", "text_size", "mode"],
            "The same rows as the appearance.* settings. A change is visible on the next page load.",
        ),
        "Me": _ref("User"),
        "MeSettings": _obj(
            {
                "event_reminder_offsets_days": _arr({"type": "integer"}),
                "deadline_reminder_offsets_days": _arr({"type": "integer"}),
                "time_zone": {"type": "string", "description": "Personal display zone. Empty uses the firm zone."},
                "mode": _enum(["light", "dark", "device", "firm"]),
                "text_size": _enum(["sm", "md", "lg", "firm"]),
                "idle_timeout_seconds": {"type": "integer", "minimum": 60, "description": "At most the firm security.idle_timeout_seconds."},
            },
            ["event_reminder_offsets_days", "deadline_reminder_offsets_days", "time_zone", "mode", "text_size", "idle_timeout_seconds"],
        ),
        "PasswordChange": _obj({"current_password": {"type": "string"}, "new_password": {"type": "string", "minLength": 12}}, ["current_password", "new_password"]),
        "RecoveryCodes": _obj({"codes": _arr({"type": "string"})}, ["codes"], "Shown once."),
        "SidebarState": _obj(
            {
                "collapsed": {"type": "boolean"},
                "subgroups": _arr(_obj({"id": {"type": "string"}, "collapsed": {"type": "boolean"}}, ["id", "collapsed"])),
            },
            ["collapsed", "subgroups"],
            "Saved per user and applied on the next computer.",
        ),
        "NotificationPreference": _obj(
            {
                "kind": {"type": "string"},
                "in_app": {"type": "boolean"},
                "email": {"type": "boolean"},
                "email_required": {"type": "boolean"},
            },
            ["kind", "in_app", "email", "email_required"],
            "One cell of the preference grid. email_required true cannot be turned off.",
        ),
        "NotificationPreferences": _obj({"items": _arr(_ref("NotificationPreference"))}, ["items"]),
        "HomeLayout": _meta(
            {
                "name": {"type": "string"},
                "default": {"type": "boolean"},
                "widgets": _arr(_ref("LayoutWidget")),
            },
            ["name", "default", "widgets"],
            "A named layout. The cap is home.saved_layouts_per_user, starting at 10. The working layout is a different resource and does not count.",
        ),
        "HomeLayoutWrite": _obj(
            {"name": {"type": "string", "minLength": 1}, "widgets": _arr(_ref("LayoutWidget"))},
            ["name", "widgets"],
        ),
        "LayoutWidget": _obj(
            {
                "widget_id": {"type": "string"},
                "x": {"type": "integer", "minimum": 0, "maximum": 11},
                "y": {"type": "integer", "minimum": 0},
                "w": {"type": "integer", "minimum": 1, "maximum": 12},
                "h": {"type": "integer", "minimum": 1},
                "filters": {"type": "object", "additionalProperties": True},
            },
            ["widget_id", "x", "y", "w", "h"],
        ),
        "RoleLayout": _obj({"role": _ROLE, "widgets": _arr(_ref("LayoutWidget"))}, ["role", "widgets"], "The admin-set default layout for a role."),
        "WidgetCatalogItem": _obj(
            {
                "id": {"type": "string"},
                "title": {"type": "string"},
                "target_path": {"type": "string"},
                "page_path": {"type": "string"},
            },
            ["id", "title", "target_path", "page_path"],
        ),
        "Widget": _obj(
            {
                "id": {"type": "string"},
                "title": {"type": "string"},
                "target": _obj({"path": {"type": "string"}, "query": {"type": "object", "additionalProperties": True}}, ["path", "query"]),
                "page_path": {"type": "string"},
                "rows": _arr(
                    _obj(
                        {"id": {"type": "string"}, "title": {"type": "string"}, "subtitle": {"type": "string"}, "target_path": {"type": "string"}},
                        ["id", "title", "target_path"],
                    )
                ),
            },
            ["id", "title", "target", "page_path", "rows"],
            "Widget payload. The title target carries the same filters as the rows. A walled matter is absent.",
        ),
        "Notification": _meta(
            {
                "user_id": _uuid(),
                "kind": {"type": "string"},
                "title": {"type": "string"},
                "link": {"type": "string"},
                "read_at": _null(_dt()),
            },
            ["user_id", "kind", "title", "link"],
            "A notification for one user.",
        ),
        "NotificationWrite": _obj(
            {"user_id": _uuid(), "kind": {"type": "string"}, "title": {"type": "string", "minLength": 1}, "link": {"type": "string"}},
            ["user_id", "kind", "title", "link"],
            "A module sends a notification. The actor is the token, not a field in the body.",
        ),
        "SearchHit": _obj(
            {
                "title": {"type": "string"},
                "subtitle": {"type": "string"},
                "record_type": {"type": "string"},
                "record_id": _uuid(),
                "target_path": {"type": "string"},
                "excerpt": _readable_text("Matching text. Attorney-only text is {\"redacted\": true}."),
            },
            ["title", "subtitle", "record_type", "record_id", "target_path"],
        ),
        "SearchResponse": _obj(
            {
                "query": {"type": "string"},
                "scope": {"type": "string"},
                "groups": _arr(_obj({"scope": {"type": "string"}, "hits": _arr(_ref("SearchHit"))}, ["scope", "hits"])),
            },
            ["query", "scope", "groups"],
            "Grouped hits. An empty group is the no-match result. The UI sentence is: No matches for {query} in {scope}.",
        ),
        "NavContext": _obj(
            {
                "is_admin": {"type": "boolean"},
                "firm_short_name": {"type": "string"},
                "staff_portal_label": {"type": "string"},
                "sidebar_collapsed": {"type": "boolean"},
                "subgroups": _arr(_obj({"id": {"type": "string"}, "collapsed": {"type": "boolean"}}, ["id", "collapsed"])),
                "badges": {"type": "object", "additionalProperties": {"type": "integer"}},
                "entries": {"type": "array", "items": {"type": "object", "additionalProperties": True}},
                "modules": {"type": "array", "items": {"type": "object", "additionalProperties": True}},
            },
            ["is_admin", "firm_short_name", "staff_portal_label", "sidebar_collapsed", "subgroups", "badges", "entries", "modules"],
            "Everything the sidebar needs. Hiding an entry here is not the access check.",
        ),
        "HelpSection": _meta({"title": {"type": "string"}, "sort_order": {"type": "integer"}}, ["title", "sort_order"]),
        "HelpSectionWrite": _obj({"title": {"type": "string", "minLength": 1}, "sort_order": {"type": "integer"}}, ["title"]),
        "HelpItem": _meta(
            {
                "section_id": _uuid(),
                "title": {"type": "string"},
                "kind": _enum(["text", "video", "image"]),
                "body": {"type": "string"},
                "file_id": _null(_uuid()),
                "sort_order": {"type": "integer"},
            },
            ["section_id", "title", "kind", "sort_order"],
        ),
        "HelpItemWrite": _obj(
            {
                "section_id": _uuid(),
                "title": {"type": "string", "minLength": 1},
                "kind": _enum(["text", "video", "image"]),
                "body": {"type": "string"},
                "file_id": _null(_uuid()),
                "sort_order": {"type": "integer"},
            },
            ["section_id", "title", "kind"],
        ),
        "HelpPage": _obj(
            {
                "id": {"type": "string"},
                "module_id": {"type": "string"},
                "title": {"type": "string"},
                "markdown": {"type": "string"},
                "keywords": _arr({"type": "string"}),
                "covers": _arr({"type": "string"}),
            },
            ["id", "module_id", "title", "markdown", "keywords", "covers"],
            "A module guide page under that module's heading.",
        ),
        "HelpPageWrite": _obj(
            {
                "id": {"type": "string", "minLength": 1},
                "title": {"type": "string", "minLength": 1},
                "markdown": {"type": "string"},
                "keywords": _arr({"type": "string"}),
                "covers": _arr({"type": "string"}),
            },
            ["id", "title", "markdown"],
        ),
        "HelpTopic": _obj(
            {"id": {"type": "string"}, "title": {"type": "string"}, "screen": {"type": "string"}, "path": {"type": "string"}},
            ["id", "title", "screen", "path"],
            "A core guide topic opened from a screen's ? icon.",
        ),
        "AuditEntry": _obj(
            {
                "id": _uuid(),
                "at": _dt(),
                "actor_id": _null(_uuid()),
                "actor_kind": _ACTOR,
                "role": {"type": "string"},
                "source": {"type": "string", "description": "ui, api token id, or module id."},
                "entity_type": {"type": "string"},
                "entity_id": {"type": "string"},
                "field": {"type": "string"},
                "old_value": _readable_text("Previous value. Attorney-only text is {\"redacted\": true}."),
                "new_value": _readable_text("New value. Attorney-only text is {\"redacted\": true}."),
                "reason": _readable_text("Why the row changed. Attorney-only text is {\"redacted\": true}."),
                "kind": _enum(["create", "update", "delete"]),
            },
            ["id", "at", "actor_kind", "source", "entity_type", "entity_id", "kind"],
            "One append-only audit row.",
        ),
        "Event": _meta(
            {
                "actor_kind": _ACTOR,
                "actor_id": _null(_uuid()),
                "action": {"type": "string"},
                "record_type": {"type": "string"},
                "record_id": {"type": "string"},
                "matter_id": _null(_uuid()),
                "summary": _readable_text(),
                "client_visible": {"type": "boolean"},
            },
            ["actor_kind", "action", "record_type", "record_id", "summary", "client_visible"],
            "An activity-stream event. Clients cannot spoof actor_kind or actor_id.",
            mutable=False,
        ),
        "EventWrite": _obj(
            {
                "action": {"type": "string", "minLength": 1},
                "record_type": {"type": "string"},
                "record_id": {"type": "string"},
                "matter_id": _null(_uuid()),
                "summary": {"type": "string", "minLength": 1},
                "client_visible": {"type": "boolean"},
            },
            ["action", "record_type", "record_id", "summary"],
            "A module appends an event. The server sets the actor from the token.",
        ),
        "TaskAuthorization": _obj(
            {
                "module_id": {"type": "string", "minLength": 1, "description": "The registered module this delivery is for."},
                "event_id": _uuid("The event id. A different event cannot reuse the grant."),
                "event_type": {"type": "string"},
                "file_id": _uuid(),
                "version_id": _uuid(),
                "matter_id": _null(_uuid()),
                "allowed_operations": _arr({"type": "string"}, "Operation ids the grant permits. Others are forbidden."),
                "allowed_fields": _arr({"type": "string"}, "Metadata fields the grant permits."),
                "expires_at": _dt("After this instant the grant is forbidden."),
                "revoked": {"type": "boolean", "description": "A core revocation makes the grant forbidden."},
            },
            [
                "module_id",
                "event_id",
                "event_type",
                "file_id",
                "version_id",
                "allowed_operations",
                "allowed_fields",
                "expires_at",
                "revoked",
            ],
            "Core-issued capability for one event's file and version. It is not an admin grant and it does not require an iframe token.",
        ),
        "WebhookEvent": _obj(
            {
                "id": _uuid(),
                "type": {"type": "string"},
                "occurred_at": _dt(),
                "actor_kind": _ACTOR,
                "actor_id": _null(_uuid()),
                "record_type": {"type": "string"},
                "record_id": {"type": "string"},
                "matter_id": _null(_uuid()),
                "summary": _readable_text("Plain summary, or {\"redacted\": true} when the text is attorney-only. Null is not a summary."),
                "task_authorization": _null(_ref("TaskAuthorization")),
                "processor_authorization": _null(_ref("ProcessorAuthorization")),
                "background_file_authorization": _null(_ref("BackgroundFileAuthorization")),
            },
            ["id", "type", "occurred_at", "actor_kind", "record_type", "record_id", "summary"],
            "Signed webhook body. Signature header X-Togavsu-Signature is sha256 of the raw body, hex, prefixed with sha256=. file.version_created includes task_authorization for the subscribed module. payment.processor_authorized includes processor_authorization for the registered processor. file.background_authorized includes background_file_authorization for the installed module. Other events set all three to null.",
        ),
        "Backup": _meta(
            {
                "status": _enum(["running", "completed", "failed"]),
                "checksum": {"type": "string"},
                "bytes": {"type": "integer", "minimum": 0},
                "kind": _enum(["nightly", "pre_update", "manual"]),
            },
            ["status", "kind"],
            "A backup archive. The bytes are encrypted with the backup passphrase.",
            mutable=False,
        ),
        "BackupDestination": _meta(
            {
                "kind": _enum(["local_path", "network_path", "s3", "sftp"]),
                "name": {"type": "string"},
                "retention_days": {"type": "integer", "minimum": 1},
            },
            ["kind", "name", "retention_days"],
        ),
        "BackupDestinationWrite": _obj(
            {
                "kind": _enum(["local_path", "network_path", "s3", "sftp"]),
                "name": {"type": "string", "minLength": 1},
                "retention_days": {"type": "integer", "minimum": 1},
                "settings": {"type": "object", "additionalProperties": True},
            },
            ["kind", "name", "retention_days"],
        ),
        "Restore": _meta(
            {
                "backup_id": _null(_uuid()),
                "status": _enum(["running", "completed", "failed"]),
                "interrupts_users": {"type": "boolean", "const": True},
            },
            ["status", "interrupts_users"],
            "A restore. This is the operation that interrupts users.",
            mutable=False,
        ),
        "BackupUpload": _obj(
            {
                "upload_token": {"type": "string"},
                "checksum": {"type": "string"},
                "bytes": {"type": "integer", "minimum": 0},
            },
            ["upload_token", "checksum", "bytes"],
            "Produced by uploading a backup archive the admin downloaded. The token is the source for a restore. It is not itself a restore.",
        ),
        "RestoreWrite": _obj(
            {
                "backup_id": _null(_uuid("A stored backup. Omit when upload_token is set.")),
                "upload_token": {"type": "string", "description": "From createBackupUpload, including an archive that was downloaded and uploaded again."},
                "passphrase": {"type": "string", "minLength": 1, "description": "Backup passphrase set at first-run setup."},
            },
            ["passphrase"],
            "One source is required: backup_id or upload_token. passphrase is always required. A body with neither source is validation_failed.",
        ),
        "UpdateRecord": _meta(
            {
                "status": _enum(["running", "completed", "aborted"]),
                "from_image": {"type": "string"},
                "to_image": {"type": "string"},
                "reason": {"type": "string"},
            },
            ["status"],
            "One core update attempt.",
            mutable=False,
        ),
        "UpdateStart": _obj(
            {},
            description="Starts an update now. A scheduled run stays inside the window. This admin action may run outside the window.",
        ),
        "Module": _obj(
            {
                "id": {"type": "string"},
                "name": {"type": "string"},
                "version": {"type": "string"},
                "enabled": {"type": "boolean"},
                "permissions": _arr(_obj({"scope": {"type": "string"}, "required": {"type": "boolean"}, "approved": {"type": "boolean"}}, ["scope", "required", "approved"])),
                "replaces": _null(_enum(["time", "trust", "calendar"])),
                "status": _enum(["awaiting_approval", "installed", "disabled", "uninstalling"]),
            },
            ["id", "name", "version", "enabled", "permissions", "status"],
            "An installed module.",
        ),
        "ModuleInstall": _obj(
            {"source": _enum(["local", "marketplace"]), "listing_id": {"type": "string"}, "version": {"type": "string"}},
            ["source"],
            "Marketplace install. A local package is multipart on the same path.",
        ),
        "ModuleUpdate": _obj(
            {
                "source": _enum(["marketplace", "local"]),
                "version": {"type": "string", "description": "Required when source is marketplace. The package manifest supplies it for a local upload."},
            },
            ["source"],
            "Selects the next version. A local package is the multipart alternative on the same path.",
        ),
        "ModuleUpdateResult": _obj(
            {
                "module": _ref("Module"),
                "previous_version": {"type": "string"},
                "outcome": _enum(["updated", "awaiting_approval", "kept_previous"]),
            },
            ["module", "previous_version", "outcome"],
            "awaiting_approval means new permissions are waiting and the old version keeps running. kept_previous means a failed signature, migration, or health check left the installed version in place.",
        ),
        "ModulePermissions": _obj(
            {"decisions": _arr(_obj({"scope": {"type": "string"}, "decision": _enum(["approve", "decline"])}, ["scope", "decision"]))},
            ["decisions"],
            "A declined required permission cancels the install. The previous version keeps running when a new version asks for more.",
        ),
        "ModuleUninstall": _obj(
            {"data": _enum(["keep", "delete"]), "confirmation": {"type": "string"}, "backup_id": _null(_uuid())},
            ["data", "confirmation"],
            "delete drops the schema only after backup_id names a completed backup and confirmation equals the module id.",
        ),
        "TrustedKey": _meta({"name": {"type": "string"}, "public_key_pem": {"type": "string"}}, ["name", "public_key_pem"]),
        "TrustedKeyWrite": _obj({"name": {"type": "string", "minLength": 1}, "public_key_pem": {"type": "string", "minLength": 1}}, ["name", "public_key_pem"]),
        "DomainHandler": _obj(
            {"domain": _enum(["time", "trust", "calendar"]), "module_id": {"type": "string"}, "handler": _enum(["core", "module"])},
            ["domain", "handler"],
            "Which implementation serves the domain. Switching back sets module_id empty and handler core. Core data stays readable.",
        ),
        "DomainHandlerWrite": _obj({"module_id": {"type": "string"}}, description="Empty module_id restores the core."),
        "MarketplaceListing": _obj(
            {
                "id": {"type": "string"},
                "name": {"type": "string"},
                "author": {"type": "string"},
                "version": {"type": "string"},
                "signing_key": {"type": "string", "minLength": 1, "description": "Author's verified signing key."},
                "permissions": _arr({"type": "string"}, "Scopes the module requests."),
                "version_history": _arr(
                    _obj(
                        {"version": {"type": "string"}, "released_at": _dt()},
                        ["version", "released_at"],
                    )
                ),
                "price_note": {"type": "string", "description": "Empty when the module is free."},
                "flagged": {"type": "boolean"},
                "flag_note": {"type": "string"},
                "pulled": {"type": "boolean"},
                "notice": {"type": "string"},
            },
            ["id", "name", "author", "version", "signing_key", "permissions", "version_history", "price_note", "flagged", "pulled", "notice"],
            "A marketplace listing as the core shows it. The notice states that the marketplace does not review modules.",
        ),
        "ModuleAlert": _meta(
            {"module_id": {"type": "string"}, "kind": _enum(["flag", "pull"]), "note": {"type": "string"}},
            ["module_id", "kind", "note"],
            "An alert delivered to admins by dashboard and email.",
        ),
        "ContextToken": _obj(
            {
                "token": {"type": "string"},
                "expires_at": _dt(),
                "viewer_user_id": _uuid("The caller. A client cannot name another user."),
                "roles": _arr(_ROLE, "The caller's roles. A client token does not gain firm roles."),
                "matter_id": _null(_uuid()),
                "allowed_operations": _arr({"type": "string"}, "Operation ids this token may accompany."),
            },
            ["token", "expires_at", "viewer_user_id", "roles", "allowed_operations"],
            "Short-lived signed context. It is not the session cookie. A client receives only their own shared matter and the upload operations.",
        ),
        "ContextTokenRequest": _obj(
            {"matter_id": _null(_uuid())},
            description="The viewer is the authenticated caller. user_id and roles are not accepted.",
        ),
        "ModuleManifest": _obj(
            {
                "id": {"type": "string"},
                "name": {"type": "string"},
                "version": {"type": "string"},
                "author": {"type": "string"},
                "homepage": {"type": "string"},
                "license": {"type": "string"},
                "price_note": {"type": "string"},
                "image": _obj({"reference": {"type": "string"}, "digest": {"type": "string"}}, ["reference", "digest"]),
                "permissions": _arr({"type": "string"}),
                "widgets": _arr(_ref("ManifestWidget")),
                "sidebar": _arr(_ref("ManifestSidebarEntry")),
                "quick_create": _arr(_ref("ManifestQuickCreate")),
                "search_scopes": _arr(_ref("ManifestSearchScope")),
                "matter_screen": _ref("ManifestMatterScreen"),
                "help": {"type": "object", "additionalProperties": True},
                "tabs": {"type": "array", "items": {"type": "object", "additionalProperties": True}},
                "event_subscriptions": _arr({"type": "string"}),
                "replaces": _null(_enum(["time", "trust", "calendar"])),
                "settings_schema": {"type": "object", "additionalProperties": True},
                "data": {"type": "object", "additionalProperties": True},
            },
            ["id", "name", "version", "image", "permissions"],
            "Signed togavsu-module.yaml as JSON. A widget without a click-through target fails install.",
        ),
        "ManifestWidget": _obj(
            {
                "id": {"type": "string"},
                "title": {"type": "string"},
                "default_size": _obj({"w": {"type": "integer"}, "h": {"type": "integer"}}, ["w", "h"]),
                "allowed_sizes": _arr(_obj({"w": {"type": "integer"}, "h": {"type": "integer"}}, ["w", "h"])),
                "target": _obj({"path": {"type": "string"}, "filters": {"type": "object", "additionalProperties": True}}, ["path"]),
            },
            ["id", "title", "default_size", "allowed_sizes", "target"],
            "target is required. Install refuses a widget that omits it. allowed_sizes lists the grid sizes the widget may take.",
        ),
        "ManifestSidebarEntry": _obj(
            {
                "id": {
                    "type": "string",
                    "minLength": 1,
                    "maxLength": 64,
                    "pattern": "^[a-z0-9][a-z0-9-]{0,63}$",
                    "description": "Stable id unique within the module manifest.",
                },
                "section": {"type": "string"},
                "subgroup": {"type": "string"},
                "label": {"type": "string"},
                "icon": {"type": "string"},
                "order": {"type": "integer"},
                "badge": _enum(["normal", "warning"]),
                "target": {
                    "type": "string",
                    "minLength": 1,
                    "maxLength": 128,
                    "pattern": "^[a-z0-9][a-z0-9-]{0,63}(/[a-z0-9][a-z0-9-]{0,63})*$",
                    "description": "Module-local path. The core opens /m/{module_id}/{target}. Schemes, absolute URLs, and .. are refused.",
                },
                "scopes": _arr({"type": "string"}, "Approved scopes required to render the entry and to open the target."),
                "roles": _arr(_ROLE, "Roles that may render the entry and open the target."),
            },
            ["id", "section", "label", "icon", "order", "target", "scopes", "roles"],
            "A sidebar entry. id is unique in the manifest. target is a module-local path, not a core route or another module's path. Two ids with different targets open two pages.",
        ),
        "ManifestQuickCreate": _obj(
            {"label": {"type": "string"}, "target": {"type": "string"}},
            ["label", "target"],
            "A quick-create item. target is the path the core opens.",
        ),
        "ManifestSearchScope": _obj(
            {"id": {"type": "string"}, "label": {"type": "string"}},
            ["id", "label"],
            "A search scope the core merges into header search.",
        ),
        "ManifestMatterScreen": _obj(
            {
                "panels": _arr(_obj({"id": {"type": "string"}, "title": {"type": "string"}}, ["id", "title"])),
                "actions": _arr(_obj({"id": {"type": "string"}, "label": {"type": "string"}}, ["id", "label"])),
            },
            ["panels", "actions"],
            "Matter-screen panels and actions.",
        ),
        "ModulePackageUpload": _obj(
            {
                "package": {
                    "type": "string",
                    "contentMediaType": "application/octet-stream",
                    "description": "Signed module package bytes.",
                }
            },
            ["package"],
            "Multipart body for a local signed package. The JSON ModuleInstall body is the marketplace alternative on the same path.",
        ),
        "AiProvider": _obj(
            {
                "provider": _enum(["anthropic", "openai", "openai_compatible", "local"]),
                "configured": {"type": "boolean"},
                "last4": {"type": "string"},
                "base_url": {"type": "string"},
            },
            ["provider", "configured"],
            "Provider status. The API key is never returned.",
        ),
        "AiKey": _obj({"api_key": {"type": "string", "minLength": 1}, "base_url": {"type": "string"}}, ["api_key"]),
        "ProviderCheck": _obj({"ok": {"type": "boolean"}, "detail": {"type": "string"}}, ["ok"]),
        "AiCallRequest": _obj(
            {"task": {"type": "string", "minLength": 1}, "input": {"type": "object", "additionalProperties": True}, "matter_id": _null(_uuid())},
            ["task", "input"],
            "A module asks the core to run a model. The module does not receive the provider key.",
        ),
        "AiCall": _meta(
            {
                "task": {"type": "string"},
                "provider": {"type": "string"},
                "model": {"type": "string"},
                "tokens_in": {"type": "integer", "minimum": 0},
                "tokens_out": {"type": "integer", "minimum": 0},
                "cost_micro_usd": {
                    "type": "integer",
                    "minimum": 0,
                    "description": "Sum of attempt costs in millionths of a US dollar. 15000 is $0.015000. Zero is an unpriced call.",
                },
                "fell_back": {"type": "boolean"},
                "attempts": _arr(_ref("AiCallAttempt")),
                "output": {"type": "object", "additionalProperties": True},
                "requester": {"type": "string"},
            },
            ["task", "provider", "model", "tokens_in", "tokens_out", "cost_micro_usd", "fell_back", "attempts", "requester"],
            "One AI call log row. cost_micro_usd is an integer count of millionths of a US dollar and equals the sum of attempts.",
            mutable=False,
        ),
        "AiCallAttempt": _obj(
            {
                "provider": {"type": "string"},
                "model": {"type": "string"},
                "status": {"type": "string"},
                "tokens_in": {"type": "integer", "minimum": 0},
                "tokens_out": {"type": "integer", "minimum": 0},
                "cost_micro_usd": {
                    "type": "integer",
                    "minimum": 0,
                    "description": "This attempt's cost in millionths of a US dollar, including a failed attempt that was billed.",
                },
            },
            ["provider", "model", "status", "tokens_in", "tokens_out", "cost_micro_usd"],
            "One provider attempt inside an AI call log row.",
        ),
        "ProblemReport": _meta({"page": {"type": "string"}, "message": {"type": "string"}, "user_id": _uuid()}, ["page", "message", "user_id"]),
        "ProblemReportWrite": _obj({"page": {"type": "string"}, "message": {"type": "string", "minLength": 1}}, ["message"]),
        "SeatCount": _obj(
            {"active_seats": {"type": "integer", "minimum": 0}, "by_role": {"type": "object", "additionalProperties": {"type": "integer"}}},
            ["active_seats", "by_role"],
            "Active seats for a commercial host to bill from. Client-only users are counted in by_role and not in active_seats. No license key is checked.",
        ),
        "ConflictRecord": _meta(
            {
                "name": {"type": "string"},
                "aliases": _arr({"type": "string"}),
                "date_of_birth": _null(_date()),
                "relationship": _enum(["prior_client", "opposing_party", "other"]),
                "scope": _enum(["client", "matter"]),
                "reason": _redacted({"type": "string"}, "Attorney-only reason."),
                "matter_id": _null(_uuid()),
                "from_date": _null(_date()),
                "to_date": _null(_date()),
                "source": {"type": "string"},
                "included": _ref("Included"),
            },
            ["name", "aliases", "relationship", "scope", "reason"],
        ),
        "ConflictRecordWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "aliases": _arr({"type": "string"}),
                "date_of_birth": _null(_date()),
                "relationship": _enum(["prior_client", "opposing_party", "other"]),
                "scope": _enum(["client", "matter"]),
                "reason": {"type": "string"},
                "matter_id": _null(_uuid()),
                "from_date": _null(_date()),
                "to_date": _null(_date()),
                "source": {"type": "string"},
            },
            ["name", "relationship", "scope"],
        ),
        "ConflictHit": _obj(
            {
                "id": _uuid(),
                "matched_record_type": _enum(["contact", "party", "conflict_record"]),
                "matched_id": _uuid(),
                "score": {"type": "integer", "minimum": 0, "maximum": 100},
                "name": {"type": "string"},
                "decision": _null(_enum(["clear", "escalate", "no_go"])),
                "reason": _redacted({"type": "string"}, "Attorney-only decision reason."),
            },
            ["id", "matched_record_type", "matched_id", "score", "name"],
        ),
        "ConflictCheck": _meta(
            {"matter_id": _uuid(), "hits": _arr(_ref("ConflictHit"))},
            ["matter_id", "hits"],
            "Hits at or above practice.conflict_score_floor. Scoring runs in the application, not in SQL.",
        ),
        "ConflictDecision": _obj(
            {"hit_id": _uuid(), "decision": _enum(["clear", "escalate", "no_go"]), "reason": {"type": "string"}},
            ["hit_id", "decision"],
            "An attorney records one decision. The reason is attorney-only.",
        ),
        "PracticeArea": _meta(
            {
                "name": {"type": "string"},
                "matter_types": _arr({"type": "string"}),
                "stages": _arr({"type": "string"}),
                "active": {"type": "boolean"},
                "default_folders": _arr({"type": "string"}),
            },
            ["name", "matter_types", "stages", "active", "default_folders"],
        ),
        "PracticeAreaWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "matter_types": _arr({"type": "string"}),
                "stages": _arr({"type": "string"}),
                "active": {"type": "boolean"},
                "default_folders": _arr({"type": "string"}),
            },
            ["name"],
        ),
        "County": _meta({"name": {"type": "string"}, "state": {"type": "string"}}, ["name", "state"]),
        "CountyWrite": _obj({"name": {"type": "string", "minLength": 1}, "state": {"type": "string", "minLength": 2, "maxLength": 2}}, ["name", "state"]),
        "ReferralSource": _meta(
            {
                "name": {"type": "string"},
                "kind": _enum(["person", "organization", "advertising", "web", "other"]),
                "contact_id": _null(_uuid()),
                "active": {"type": "boolean"},
            },
            ["name", "kind", "active"],
        ),
        "ReferralSourceWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "kind": _enum(["person", "organization", "advertising", "web", "other"]),
                "contact_id": _null(_uuid()),
                "active": {"type": "boolean"},
            },
            ["name", "kind"],
        ),
        "PartyRole": _meta({"key": {"type": "string"}, "label": {"type": "string"}, "active": {"type": "boolean"}}, ["key", "label", "active"]),
        "PartyRoleWrite": _obj({"key": {"type": "string"}, "label": {"type": "string", "minLength": 1}, "active": {"type": "boolean"}}, ["label"]),
        "FeeItem": _meta(
            {
                "name": {"type": "string"},
                "description": {"type": "string"},
                "default_amount_cents": _cents(),
                "practice_area_id": _null(_uuid()),
                "active": {"type": "boolean"},
            },
            ["name", "description", "default_amount_cents", "active"],
            "A catalog item. Deactivating it leaves past invoices as they were.",
        ),
        "FeeItemWrite": _obj(
            {
                "name": {"type": "string", "minLength": 1},
                "description": {"type": "string"},
                "default_amount_cents": _cents(),
                "practice_area_id": _null(_uuid()),
                "active": {"type": "boolean"},
            },
            ["name", "default_amount_cents"],
        ),
        "ActivityType": _meta({"name": {"type": "string"}, "active": {"type": "boolean"}}, ["name", "active"]),
        "ActivityTypeWrite": _obj({"name": {"type": "string", "minLength": 1}, "active": {"type": "boolean"}}, ["name"]),
        "ExpenseType": _meta({"name": {"type": "string"}, "active": {"type": "boolean"}}, ["name", "active"]),
        "ExpenseTypeWrite": _obj({"name": {"type": "string", "minLength": 1}, "active": {"type": "boolean"}}, ["name"]),
        "CustomFieldDefinition": _meta(
            {
                "record_type": _enum(["matter", "contact", "time_entry", "expense"]),
                "key": {"type": "string"},
                "label": {"type": "string"},
                "field_type": _enum(["text", "long_text", "number", "money", "date", "boolean", "pick_list", "contact"]),
                "sort_order": {"type": "integer"},
                "required": {"type": "boolean"},
                "practice_area_ids": _arr(_uuid(), "Empty means every practice area."),
                "options": _arr({"type": "string"}),
                "retired": {"type": "boolean"},
                "module_id": {"type": "string"},
            },
            ["record_type", "key", "label", "field_type", "sort_order", "required", "practice_area_ids", "retired"],
            "A custom field. Adding one does not change the database schema. Retiring hides it and keeps values.",
        ),
        "CustomFieldDefinitionWrite": _obj(
            {
                "record_type": _enum(["matter", "contact", "time_entry", "expense"]),
                "key": {"type": "string"},
                "label": {"type": "string", "minLength": 1},
                "field_type": _enum(["text", "long_text", "number", "money", "date", "boolean", "pick_list", "contact"]),
                "sort_order": {"type": "integer"},
                "required": {"type": "boolean"},
                "practice_area_ids": _arr(_uuid()),
                "options": _arr({"type": "string"}),
            },
            ["record_type", "label", "field_type"],
        ),
        "CustomFieldValue": _obj(
            {"definition_id": _uuid(), "key": {"type": "string"}, "value": {"description": "JSON value matching the field type."}},
            ["definition_id", "key"],
        ),
        "CustomFieldInput": {
            "description": "One custom-field value written with the record. Exactly one value key is set, and it must match the definition type. Other properties are rejected.",
            "oneOf": [
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "text": {"type": "string"}}, ["definition_id", "key", "text"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "long_text": {"type": "string"}}, ["definition_id", "key", "long_text"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "number": {"type": "number"}}, ["definition_id", "key", "number"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "money_cents": {"type": "integer"}}, ["definition_id", "key", "money_cents"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "date": _date()}, ["definition_id", "key", "date"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "boolean": {"type": "boolean"}}, ["definition_id", "key", "boolean"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "pick": {"type": "string"}}, ["definition_id", "key", "pick"]),
                _obj({"definition_id": _uuid(), "key": {"type": "string", "minLength": 1}, "contact_id": _uuid()}, ["definition_id", "key", "contact_id"]),
            ],
        },
        "CustomFieldValuesWrite": _obj({"values": _arr(_ref("CustomFieldValue"))}, ["values"]),
        "ReportTable": _obj(
            {
                "report": {"type": "string"},
                "columns": _arr({"type": "string"}),
                "rows": {"type": "array", "items": {"type": "object", "additionalProperties": True}},
            },
            ["report", "columns", "rows"],
            "A report. format=csv returns the same rows as text/csv.",
        ),
        "BillingWorklist": _obj(
            {
                "flat_fee": _arr(_ref("FlatFeeRow")),
                "hourly": _arr(_ref("HourlyRow")),
            },
            ["flat_fee", "hourly"],
        ),
        "FlatFeeRow": _obj(
            {
                "matter_id": _uuid(),
                "client_id": _null(_uuid()),
                "flat_fee_cents": _null(_cents()),
                "trust_balance_cents": _signed_cents(),
                "status": _enum(["needs_fee_amount", "awaiting_trust_funds", "ready_to_apply", "collected"]),
            },
            ["matter_id", "trust_balance_cents", "status"],
        ),
        "HourlyRow": _obj(
            {"matter_id": _uuid(), "unbilled_cents": _cents()},
            ["matter_id", "unbilled_cents"],
        ),
        "FlatFeeApply": _obj(
            {
                "client_id": _uuid("Which client's trust is debited when the matter has more than one client."),
                "trust_account_id": _uuid("The active firm account that holds that client's matter balance."),
                "amount_cents": _cents("Explicit amount. The UI may prefill the matter flat fee; the server does not."),
            },
            ["client_id", "trust_account_id", "amount_cents"],
            "Applies the flat fee from the named client and trust account. A different account on the same matter is not debited.",
        ),
        "LoginRequest": _obj({"email": {"type": "string", "format": "email"}, "password": {"type": "string"}}, ["email", "password"]),
        "LoginResponse": _obj(
            {
                "status": _enum(["totp_required", "enrollment_required", "authenticated"]),
                "totp_token": {"type": "string"},
                "enrollment_token": {"type": "string"},
                "user": _ref("User"),
                "csrf_token": {"type": "string"},
            },
            ["status"],
            "totp_required carries totp_token for a user who already enrolled. enrollment_required carries enrollment_token and no session; the secret is returned by startTotpEnrollment or by first-run setup, not here. authenticated sets the session cookie and returns csrf_token.",
        ),
        "EnrollmentChallenge": _obj(
            {
                "status": {"const": "totp_enrollment_required"},
                "enrollment_token": {"type": "string"},
                "otpauth_uri": {"type": "string"},
                "secret": {"type": "string", "description": "Base32 TOTP secret, shown once."},
            },
            ["status", "enrollment_token", "otpauth_uri", "secret"],
            "Issued before a session exists. The next call is confirmTotpEnrollment.",
        ),
        "EnrollmentStart": _obj(
            {"enrollment_token": {"type": "string", "minLength": 1}},
            ["enrollment_token"],
            "Token from a login response whose status is enrollment_required.",
        ),
        "EnrollmentConfirm": _obj(
            {
                "enrollment_token": {"type": "string", "minLength": 1},
                "code": {"type": "string", "minLength": 6},
            },
            ["enrollment_token", "code"],
            "Confirms the first TOTP code. No session is required.",
        ),
        "SessionStarted": _obj(
            {
                "status": {"const": "authenticated"},
                "user": _ref("User"),
                "csrf_token": {"type": "string"},
                "recovery_codes": _arr({"type": "string"}),
            },
            ["status", "user", "csrf_token", "recovery_codes"],
            "Session cookie plus the initial recovery codes, returned once.",
        ),
        "TotpRequest": _obj({"totp_token": {"type": "string"}, "code": {"type": "string", "minLength": 6}}, ["totp_token", "code"]),
        "RecoveryLoginRequest": _obj({"totp_token": {"type": "string"}, "recovery_code": {"type": "string"}}, ["totp_token", "recovery_code"]),
        "PasswordResetRequest": _obj({"email": {"type": "string", "format": "email"}}, ["email"]),
        "PasswordResetConfirm": _obj({"token": {"type": "string"}, "new_password": {"type": "string", "minLength": 12}}, ["token", "new_password"]),
        "SetupStatus": _obj({"ready": {"type": "boolean"}}, ["ready"]),
        "SetupRequest": _obj(
            {
                "firm_name": {"type": "string", "minLength": 1},
                "time_zone": {"type": "string", "minLength": 1},
                "admin_name": {"type": "string", "minLength": 1},
                "admin_email": {"type": "string", "format": "email"},
                "password": {"type": "string", "minLength": 12},
                "backup_passphrase": {"type": "string", "minLength": 12},
            },
            ["firm_name", "time_zone", "admin_name", "admin_email", "password", "backup_passphrase"],
            "First-run setup. A second call returns already_set_up. The response then requires TOTP enrollment before the admin session is usable.",
        ),
        "Health": _obj({"status": {"type": "string", "const": "ok"}, "version": {"type": "string"}}, ["status", "version"]),
        "MailPassword": _obj({"password": {"type": "string", "minLength": 1}}, ["password"]),
        "MailTest": _obj({"to": {"type": "string", "format": "email"}}, ["to"]),
        "JsonRpcRequest": _obj(
            {
                "jsonrpc": {"const": "2.0"},
                "id": {"description": "Request id."},
                "method": {"type": "string"},
                "params": {"type": "object", "additionalProperties": True},
            },
            ["jsonrpc", "method"],
            "MCP JSON-RPC. Methods: initialize, tools/list, tools/call. tools/call params.name is an operationId. params.arguments contains path parameters, query parameters, idempotency_key, and body.",
        ),
        "JsonRpcResponse": _obj(
            {
                "jsonrpc": {"const": "2.0"},
                "id": {"description": "Request id."},
                "result": {"description": "Tool result or tools list."},
                "error": {"type": "object", "additionalProperties": True},
            },
            ["jsonrpc"],
        ),
        "ScopeCatalogEntry": _obj({"scope": {"type": "string"}, "description": {"type": "string"}}, ["scope", "description"]),
    }
)

# Included is an open object. additionalProperties false with no properties rejects extensions.
SCHEMAS["Included"] = {
    "type": "object",
    "description": SCHEMAS["Included"]["description"],
    "additionalProperties": {"type": "object", "additionalProperties": True},
}


def _query(name: str, schema: dict, description: str, required: bool = False) -> dict:
    return {"name": name, "in": "query", "required": required, "schema": schema, "description": description}


QUERY = {
    "cursor": {
        "name": "cursor",
        "in": "query",
        "required": False,
        "schema": {"type": "string"},
        "description": "Opaque cursor from page.next_cursor.",
    },
    "limit": {
        "name": "limit",
        "in": "query",
        "required": False,
        "schema": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
        "description": "Page size. Default 50. Maximum 200. A few lists default lower because a setting says so.",
    },
    "include": _query(
        "include",
        {"type": "string"},
        "Comma-separated module-id.view names to join. Example: matter-clock.targets.",
    ),
    "format": _query("format", _enum(["json", "csv"]), "json is the default. csv returns text/csv."),
    "q": _query("q", {"type": "string"}, "Case-insensitive substring."),
    "archived": _query("archived", {"type": "boolean"}, "Filter by the archived flag. Default lists omit archived rows."),
    "kind": _query("kind", {"type": "string"}, "Kind filter. The resource description names the values."),
    "tag": _query("tag", {"type": "string"}, "Tag filter."),
    "matter_id": _query("matter_id", _ref("Uuid"), "Matter filter."),
    "contact_id": _query("contact_id", _ref("Uuid"), "Contact filter."),
    "status": _query("status", {"type": "string"}, "Status filter."),
    "user_id": _query("user_id", _ref("Uuid"), "User filter."),
    "author_id": _query("author_id", _ref("Uuid"), "Note author filter."),
    "from_date": _query("from_date", _date(), "Inclusive start date."),
    "to_date": _query("to_date", _date(), "Inclusive end date."),
    "from_time": _query("from_time", _dt(), "Inclusive start timestamp."),
    "to_time": _query("to_time", _dt(), "Exclusive end timestamp."),
    "scope": _query("scope", {"type": "string"}, "Search scope: all, matters, people, invoices, files, or a module scope."),
    "billable": _query("billable", {"type": "boolean"}, "Billable filter."),
    "invoice_id": _query("invoice_id", _ref("Uuid"), "Invoice filter."),
    "trust_account_id": _query("trust_account_id", _ref("Uuid"), "Trust account filter."),
    "client_id": _query("client_id", _ref("Uuid"), "Client contact filter."),
    "actor_kind": _query("actor_kind", _ACTOR, "Activity actor kind."),
    "actor_id": _query("actor_id", _ref("Uuid"), "Activity actor id."),
    "entity_type": _query("entity_type", {"type": "string"}, "Audit entity type."),
    "entity_id": _query("entity_id", {"type": "string"}, "Audit entity id."),
    "folder_id": _query("folder_id", _ref("Uuid"), "Folder filter."),
    "category_id": _query("category_id", _ref("Uuid"), "Category filter."),
    "trashed": _query("trashed", {"type": "boolean"}, "Trash filter. Default is false."),
    "attorney_only": _query("attorney_only", {"type": "boolean"}, "Attorney-only file filter."),
    "practice_area_id": _query("practice_area_id", _ref("Uuid"), "Practice area filter."),
    "active": _query("active", {"type": "boolean"}, "Active filter."),
    "view": _query("view", _enum(["day", "week", "month"]), "Calendar view."),
    "disposition": _query("disposition", _enum(["inline", "attachment"]), "Download disposition."),
    "confirm": _query("confirm", {"type": "boolean"}, "Must be true to download a quarantined version."),
    "confirm_quarantine": _query("confirm_quarantine", {"type": "boolean"}, "Must be true to download a quarantined file. Omitted or false returns quarantine_confirmation_required and no bytes."),
    "pinned": _query("pinned", {"type": "boolean"}, "Pinned note filter."),
    "read": _query("read", {"type": "boolean"}, "Read notification filter."),
    "billing_type": _query("billing_type", _BILLING, "Matter billing type."),
    "responsible_user_id": _query("responsible_user_id", _ref("Uuid"), "Responsible attorney filter."),
    "record_type": _query("record_type", {"type": "string"}, "Custom field record type."),
    "screen": _query("screen", {"type": "string"}, "Guide topic screen id."),
    "section_id": _query("section_id", _ref("Uuid"), "Help section filter."),
    "as_of": _query("as_of", _date(), "Report as-of date."),
    "custom": {
        "name": "custom",
        "in": "query",
        "required": False,
        "style": "deepObject",
        "explode": True,
        "schema": {"type": "object", "additionalProperties": {"type": "string"}},
        "description": "Custom field filters, as custom[key]=value.",
    },
}

PARAM_SCHEMA = {
    "setting_key": _ref("SettingKey"),
    "module_id": {"type": "string", "minLength": 1, "pattern": "^[a-z0-9][a-z0-9-]{0,63}$"},
    "domain": _enum(["time", "trust", "calendar"]),
    "provider": _enum(["anthropic", "openai", "openai_compatible", "local"]),
    "role": _ROLE,
    "widget_id": _enum(
        [
            "my-time",
            "running-timer",
            "unbilled",
            "my-open-matters",
            "upcoming-deadlines",
            "todays-calendar",
            "trust-alerts",
            "replenishment-requests",
            "ar-aging",
            "recent-activity",
            "pending-conflict-checks",
            "module-alerts",
        ]
    ),
    "report_id": _enum(
        [
            "unbilled-time",
            "time-by-user",
            "ar-aging",
            "payments-received",
            "write-offs",
            "trust-balances",
            "trust-client-ledger",
            "trust-account-ledger",
            "reconciliations",
            "doc-tender-spend",
        ]
    ),
    "secret": {"type": "string", "minLength": 20},
    "page_id": {"type": "string", "minLength": 1},
    "listing_id": {"type": "string", "minLength": 1},
    "topic_id": {"type": "string", "minLength": 1},
}

COMPONENTS = {
    "securitySchemes": {
        "sessionCookie": {
            "type": "apiKey",
            "in": "cookie",
            "name": "togavsu_session",
            "description": "Browser session cookie. HTTP-only, Secure, SameSite=Lax. Unsafe cookie requests send X-CSRF-Token.",
        },
        "bearerAuth": {
            "type": "http",
            "scheme": "bearer",
            "bearerFormat": "togavsu-token",
            "description": "Revocable bearer token for an agent or a module. Scopes are required in addition to the role check.",
        },
    },
    "parameters": {
        "idempotencyKey": {
            "name": "Idempotency-Key",
            "in": "header",
            "required": False,
            "schema": {"type": "string", "minLength": 1, "maxLength": 200},
            "description": "Optional on writes. The same key and body replay the original response for api.idempotency_retention_hours. The same key and a different body returns idempotency_conflict.",
        },
        "csrfToken": {
            "name": "X-CSRF-Token",
            "in": "header",
            "required": False,
            "schema": {"type": "string"},
            "description": "Required when the session cookie authenticates an unsafe method. Ignored for a bearer token. Sending both the cookie and a bearer token returns ambiguous_auth.",
        },
    },
    "headers": {
        "RequestId": {
            "description": "Request id.",
            "schema": {"type": "string", "format": "uuid"},
        },
        "RetryAfter": {
            "description": "Seconds to wait.",
            "schema": {"type": "integer", "minimum": 0},
        },
    },
    "responses": {},
    "schemas": SCHEMAS,
}

_PROBLEM_CONTENT = {"application/problem+json": {"schema": _ref("Problem")}}
COMPONENTS["responses"] = {
    "Problem": {"description": "A problem+json error.", "content": _PROBLEM_CONTENT},
    "Unauthorized": {"description": "Missing or invalid authentication.", "content": _PROBLEM_CONTENT},
    "Forbidden": {"description": "Authenticated, but the role or scope does not allow this.", "content": _PROBLEM_CONTENT},
    "NotFound": {
        "description": "The record is missing, or a matter wall hides it. Both cases use not_found.",
        "content": _PROBLEM_CONTENT,
    },
    "Conflict": {"description": "The write conflicts with current state, including an Idempotency-Key reused with a different body.", "content": _PROBLEM_CONTENT},
    "Unprocessable": {"description": "The body or a batch failed validation. A failed batch writes nothing.", "content": _PROBLEM_CONTENT},
    "TooManyRequests": {
        "description": "Rate limit. The limit is security.api_rate_limit_per_minute.",
        "headers": {"Retry-After": {"$ref": "#/components/headers/RetryAfter"}},
        "content": _PROBLEM_CONTENT,
    },
}

# Setting catalog is extension data, not a schema.
SETTINGS_CATALOG = SETTINGS
