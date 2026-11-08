"""Route table for the Togavsu core API proposal."""

from __future__ import annotations

from contract.model import Contract, _batch
from contract.metadata import FILE_DOWNLOAD, MODULE_GUIDE, finalize, version_publication

ADMIN = ["admin"]
ATTORNEY = ["admin", "attorney"]
STAFF = ["admin", "attorney", "staff"]
BILLING = ["admin", "attorney", "staff", "accountant"]
FIRM = ["admin", "attorney", "staff", "accountant"]
ANY = ["admin", "attorney", "staff", "accountant", "client"]

WALL = {"matter_wall": True, "access_inherits": "matter"}
FILE_ACCESS_CASES = [
    {"role": "staff", "attorney_only": True, "result": "not_found"},
    {"role": "attorney", "attorney_only": True, "result": "allowed"},
    {"role": "staff", "matter_walled": True, "on_wall": False, "result": "not_found"},
]
FILE_WALL = {
    "matter_wall": True,
    "access_inherits": "file",
    "attorney_only_files": "not_found",
    "access_cases": FILE_ACCESS_CASES,
}
SHARED = {
    "matter_wall": True,
    "access_inherits": "file",
    "client": "shared",
    "accountant": "receipts-and-invoice-pdfs",
    "attorney_only_files": "not_found",
    "access_cases": FILE_ACCESS_CASES,
}
PERSON_LOCK = {
    "fields": ["name", "category", "matter", "grouping"],
    "human_edit": "allowed",
    "automated_actor": "field_locked",
    "blocked_actors": ["doc_tender"],
}
TRUST_BILLING_DISPATCH = {
    "primary_domain": "time",
    "trust_entry": {
        "posted_by": "trust_handler",
        "append_only": True,
        "exactly_once": True,
        "time_module_also_posts": False,
        "below_zero": "posts",
    },
}
REVERSAL_DISPATCH = {**TRUST_BILLING_DISPATCH, "applies_when": "payment_has_trust_entry"}
BATCH_WALL = {
    "matter_wall": True,
    "access_inherits": "matter",
    "batch_authorization": "all_or_nothing",
    "inaccessible_result": "not_found",
}
ICAL_POLICY = {
    "matter_wall": True,
    "access_inherits": "matter",
    "feed_owner": "self_or_admin",
    "firm_feed_roles": ["admin"],
    "download_filter": "omit_events_on_walled_matters",
    "access_cases": [
        {"action": "read", "role": "staff", "owner": "other_user", "result": "not_found"},
        {"action": "read", "role": "staff", "owner": "self", "result": "allowed"},
        {"action": "create", "role": "staff", "owner": "firm", "result": "forbidden"},
        {"action": "create", "role": "admin", "owner": "firm", "result": "allowed"},
        {"action": "download", "event_on_walled_matter": True, "result": "omitted"},
    ],
}
MODULE_HELP = {
    "client": "deny",
    "module_principal": {
        "actor_kind": "module",
        "scope": "help:write",
        "module_id_param": "module_id",
        "own_heading_only": True,
        "grants_admin": False,
    },
}
MODULE_MARKER = {
    "client": "deny",
    "module_principal": {
        "actor_kind": "module",
        "scope": "markers:write",
        "module_id_param": "module_id",
        "own_namespace_only": True,
        "grants_admin": False,
        "ownership": "signed_module_principal",
        "firm_markers": "forbidden",
        "other_module": "forbidden",
        "caller_supplied_owner": "ignored",
    },
}
OWN = {"client": "own"}
NOTIFICATION_RECIPIENT = {
    "owner": "self",
    "recipient": "self",
    "roles_bound": ["admin", "attorney", "staff", "accountant", "client"],
    "filter_before": ["pagination", "unread_count"],
    "admin_sees_others": False,
    "inherits": "source_record",
    "matter_wall": True,
    "admin_bypasses_wall": False,
    "attorney_only": "redacted",
}
TOKEN_OWNERSHIP = {
    "client": "own",
    "owner": "self_or_admin",
    "secret_on_read": False,
    "access_cases": [
        {"action": "read", "role": "client", "owner": "self", "result": "allowed"},
        {"action": "read", "role": "client", "owner": "other_user", "result": "not_found"},
        {"action": "read", "role": "staff", "owner": "other_user", "result": "not_found"},
        {"action": "read", "role": "staff", "owner": "self", "result": "allowed"},
        {"action": "read", "role": "admin", "owner": "other_user", "result": "allowed"},
        {"action": "revoke", "role": "staff", "owner": "other_user", "result": "not_found"},
        {"action": "revoke", "role": "staff", "owner": "self", "result": "allowed"},
        {"action": "revoke", "role": "client", "owner": "self", "result": "allowed"},
        {"action": "revoke", "role": "admin", "owner": "other_user", "result": "allowed"},
        {"action": "create", "role": "client", "owner": "self", "result": "forbidden"},
        {"action": "create", "role": "staff", "owner": "self", "result": "forbidden"},
        {"action": "create", "role": "accountant", "owner": "self", "result": "forbidden"},
        {"action": "create", "role": "admin", "owner": "named_user", "result": "allowed"},
    ],
}
TOKEN_ISSUANCE = {
    "issuance": "admin_only",
    "owner_binding": "user_or_module",
    "scope_bound": "caller_effective_and_owner",
    "bearer_amplification": False,
    "module_delegation": False,
}
SESSION_OWNERSHIP = {
    "client": "own",
    "owner": "self_or_admin",
    "access_cases": [
        {"role": "staff", "owner": "other_user", "result": "not_found"},
        {"role": "client", "owner": "self", "result": "allowed"},
        {"role": "admin", "owner": "other_user", "result": "allowed"},
    ],
}

ENTRY_TYPE_SCOPES = {
    "deposit": ["trust:write"],
    "withdrawal": ["trust:withdraw"],
    "transfer_to_operating": ["trust:withdraw"],
    "refund": ["trust:withdraw"],
    "adjustment": ["trust:withdraw"],
}
TRUST_SCOPE_CASES = [
    {"token_scopes": ["trust:write"], "entry_type": "deposit", "result": "allowed"},
    {"token_scopes": ["trust:write"], "entry_type": "withdrawal", "result": "scope_denied"},
    {"token_scopes": ["trust:write"], "entry_type": "transfer_to_operating", "result": "scope_denied"},
    {"token_scopes": ["trust:write"], "entry_type": "refund", "result": "scope_denied"},
    {"token_scopes": ["trust:write"], "entry_type": "adjustment", "result": "scope_denied"},
    {"token_scopes": ["trust:withdraw"], "entry_type": "withdrawal", "result": "allowed"},
    {"token_scopes": ["trust:write", "trust:withdraw"], "entry_type": "deposit", "result": "allowed"},
]
TRUST_ENTRY_ROLES = {
    "entry_type_roles": {
        "deposit": STAFF,
        "withdrawal": ATTORNEY,
        "transfer_to_operating": ATTORNEY,
        "refund": ATTORNEY,
        "adjustment": ATTORNEY,
    },
    "entry_type_scopes": ENTRY_TYPE_SCOPES,
    "scope_combination": "per_entry_type",
    "scope_cases": TRUST_SCOPE_CASES,
    "below_zero": "posts",
    "matter_wall": True,
    "access_inherits": "matter",
}


def add_routes(api: Contract) -> None:
    _platform(api)
    _identity(api)
    _practice(api)
    _billing(api)
    _trust(api)
    _calendar(api)
    _files(api)
    _doc_tender(api)
    _shell(api)
    _admin(api)
    _modules(api)
    finalize(api.spec)


def _platform(api: Contract) -> None:
    api.add(
        "get",
        "/api/v1/openapi.json",
        operation_id="getOpenAPI",
        summary="OpenAPI document",
        description="This OpenAPI 3.1 document. Public, so an agent can find the contract before it has a token.",
        tag="Platform",
        features=["openapi"],
        roles=None,
        scopes=[],
        public=True,
        response_schema={"type": "object", "additionalProperties": True},
        writes=False,
    )
    api.add(
        "get",
        "/llms.txt",
        operation_id="getLlmsTxt",
        summary="Agent card",
        description=(
            "Public Markdown card: product, version, firm name when llms.show_firm_name is true, "
            "the OpenAPI and MCP URLs, the bearer-token requirement, and the optional firm paragraph. "
            "It holds no client or matter records."
        ),
        tag="Platform",
        features=["llms"],
        roles=None,
        scopes=[],
        public=True,
        response_content={"text/markdown": {"schema": {"type": "string"}}},
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/llms.txt",
        operation_id="getLlmsTxtV1",
        summary="Agent card under the versioned API",
        description="Same document as GET /llms.txt.",
        tag="Platform",
        features=["llms"],
        roles=None,
        scopes=[],
        public=True,
        response_content={"text/markdown": {"schema": {"type": "string"}}},
        writes=False,
        mcp="alias",
        canonical="getLlmsTxt",
    )
    api.add(
        "get",
        "/healthz",
        operation_id="getHealthz",
        summary="Process health",
        description="Unauthenticated liveness for the proxy and the updater. It returns no firm data.",
        tag="Platform",
        features=["health"],
        roles=None,
        scopes=[],
        public=True,
        response="Health",
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/health",
        operation_id="getHealth",
        summary="API health",
        description="Same check as /healthz, on the versioned API.",
        tag="Platform",
        features=["health"],
        roles=None,
        scopes=[],
        public=True,
        response="Health",
        writes=False,
        mcp="alias",
        canonical="getHealthz",
    )
    api.add(
        "post",
        "/mcp",
        operation_id="postMcp",
        summary="MCP transport",
        description=(
            "JSON-RPC 2.0 endpoint. initialize and tools/list describe the tools and need no credential. "
            "tools/call runs one OpenAPI operation: params.name is the operationId, and params.arguments "
            "carries path parameters, query parameters, idempotency_key, and body. "
            "Authorization is decided from that operation before any effect. "
            "A public operation, including first-run setup, login, and TOTP enrollment, is reachable with no session and no bearer. "
            "Every other tool requires the same session cookie or bearer as its HTTP operation. "
            "An invalid credential is unauthenticated. Cookie and bearer together are ambiguous_auth. "
            "Arguments must not carry Authorization, Host, or roles. "
            "A cookie tools/call that changes state sends X-CSRF-Token. The core verifies that token. A caller flag is not proof. "
            "A bearer call does not send it. A read-only cookie call does not need it. "
            "A tool error uses the problem body and sets the JSON-RPC error data to that problem."
        ),
        tag="Platform",
        features=["mcp"],
        roles=ANY,
        scopes=[],
        body="JsonRpcRequest",
        response="JsonRpcResponse",
        writes=False,
        mcp="transport",
        extra_access={
            "client": "own",
            "tool_authorization": "underlying_http_operation",
            "anonymous_when": "target_operation_security_empty",
        },
    )
    mcp_transport = api.spec["paths"]["/mcp"]["post"]
    mcp_transport["security"] = [{"sessionCookie": []}, {"bearerAuth": []}, {}]
    mcp_transport["parameters"] = [{"$ref": "#/components/parameters/csrfToken"}]
    api.add(
        "get",
        "/api/v1/setup",
        operation_id="getSetup",
        summary="First-run status",
        description="Public. ready is false until the first admin, firm name, time zone, and backup passphrase are stored.",
        tag="Platform",
        features=["setup"],
        roles=None,
        scopes=[],
        public=True,
        response="SetupStatus",
        writes=False,
    )
    api.add(
        "post",
        "/api/v1/setup",
        operation_id="createSetup",
        summary="Run first-run setup",
        description=(
            "Creates the firm and the first admin, then returns an enrollment challenge with otpauth_uri and the TOTP secret. "
            "No session cookie is set. The next call is confirmTotpEnrollment. A later setup call returns already_set_up."
        ),
        tag="Platform",
        features=["setup"],
        roles=None,
        scopes=[],
        public=True,
        body="SetupRequest",
        response="EnrollmentChallenge",
        success="201",
        error_codes=["already_set_up"],
        events=["user.created"],
    )
    api.spec["paths"]["/api/v1/setup"]["post"]["x-togavsu-next"] = "confirmTotpEnrollment"


def _identity(api: Contract) -> None:
    problem = {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}
    api.add(
        "post",
        "/api/v1/auth/login",
        operation_id="login",
        summary="Sign in",
        description=(
            "Password check. No session cookie is set until TOTP is confirmed. "
            "A user who already enrolled gets status totp_required. "
            "A user who has not enrolled gets status enrollment_required and enrollment_token, then calls startTotpEnrollment. "
            "Repeated failures lock the account."
        ),
        tag="Identity",
        features=["auth"],
        roles=None,
        scopes=[],
        public=True,
        body="LoginRequest",
        response="LoginResponse",
        error_codes=["locked_out", "unauthenticated"],
        extra_responses={"401": {"description": "Wrong email or password.", "content": problem}, "423": {"description": "Locked out.", "content": problem}},
    )
    api.add(
        "post",
        "/api/v1/auth/totp",
        operation_id="verifyTotp",
        summary="Finish sign-in with TOTP",
        description="Exchanges a totp_token and a code for a session. This is only for a user who already finished enrollment. The first enrollment uses confirmTotpEnrollment.",
        tag="Identity",
        features=["auth"],
        roles=None,
        scopes=[],
        public=True,
        body="TotpRequest",
        response="LoginResponse",
        extra_responses={"401": {"description": "Wrong code.", "content": problem}},
    )
    api.add(
        "post",
        "/api/v1/auth/totp/enroll",
        operation_id="startTotpEnrollment",
        summary="Start TOTP enrollment before a session",
        description="Public. Takes the enrollment_token from a login whose status is enrollment_required. Returns otpauth_uri and the secret once. Does not set a session.",
        tag="Identity",
        features=["auth", "setup"],
        roles=None,
        scopes=[],
        public=True,
        body="EnrollmentStart",
        response="EnrollmentChallenge",
        error_codes=["unauthenticated"],
        extra_responses={"401": {"description": "Unknown enrollment token.", "content": problem}},
    )
    api.add(
        "post",
        "/api/v1/auth/totp/confirm",
        operation_id="confirmTotpEnrollment",
        summary="Confirm TOTP enrollment and open a session",
        description=(
            "Public. Confirms the first code for the enrollment_token from setup or startTotpEnrollment. "
            "Sets the session cookie and returns the initial recovery codes once. "
            "An authenticated reset uses resetTotp and confirmTotpReset, not this step."
        ),
        tag="Identity",
        features=["auth", "setup"],
        roles=None,
        scopes=[],
        public=True,
        body="EnrollmentConfirm",
        response="SessionStarted",
        error_codes=["unauthenticated"],
        extra_responses={"401": {"description": "Wrong code.", "content": problem}},
    )
    api.add(
        "post",
        "/api/v1/auth/recovery",
        operation_id="verifyRecoveryCode",
        summary="Finish sign-in with a recovery code",
        description="One recovery code signs the user in and is then spent.",
        tag="Identity",
        features=["auth"],
        roles=None,
        scopes=[],
        public=True,
        body="RecoveryLoginRequest",
        response="LoginResponse",
        extra_responses={"401": {"description": "Wrong recovery code.", "content": problem}},
    )
    api.add(
        "post",
        "/api/v1/auth/logout",
        operation_id="logout",
        summary="Sign out",
        description="Ends the current session.",
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["sessions:write"],
        success="204",
        response_content={},
        extra_access=OWN,
    )
    api.add(
        "post",
        "/api/v1/auth/password-resets",
        operation_id="requestPasswordReset",
        summary="Request a password reset",
        description="Always returns 202. The mail is sent only when the email matches a user.",
        tag="Identity",
        features=["auth"],
        roles=None,
        scopes=[],
        public=True,
        body="PasswordResetRequest",
        success="202",
        response_schema={"type": "object", "additionalProperties": False, "properties": {}},
    )
    api.add(
        "post",
        "/api/v1/auth/password-resets/confirm",
        operation_id="confirmPasswordReset",
        summary="Set a password from a reset token",
        description="Consumes the reset token.",
        tag="Identity",
        features=["auth"],
        roles=None,
        scopes=[],
        public=True,
        body="PasswordResetConfirm",
        success="204",
        response_content={},
    )
    api.add(
        "get",
        "/api/v1/me",
        operation_id="getMe",
        summary="Current user",
        description="The signed-in profile. Password and TOTP material are not included.",
        tag="Identity",
        features=["profile"],
        roles=ANY,
        scopes=["profile:read"],
        response="User",
        writes=False,
        include=True,
        extra_access={**OWN, "include_scope_pattern": "views:{module_id}.{view}:read"},
    )
    api.add(
        "patch",
        "/api/v1/me",
        operation_id="updateMe",
        summary="Update current profile",
        description="Name, phone, initials, and profile_file_id only. roles, email, linked_contact_id, rates, and bar fields are rejected by MeProfileWrite. Those are admin edits on the user record.",
        tag="Identity",
        features=["profile"],
        roles=ANY,
        scopes=["profile:write"],
        body="MeProfileWrite",
        response="User",
        events=["user.updated"],
        extra_access={
            **OWN,
            "rejected_fields": [
                "roles",
                "email",
                "linked_contact_id",
                "default_hourly_rate_cents",
                "bar_number",
                "bar_state",
            ],
        },
    )
    api.add(
        "post",
        "/api/v1/me/email-change",
        operation_id="requestEmailChange",
        summary="Request an email change",
        description=(
            "Starts a verified change of the caller's own email. The address is not saved yet. "
            "A verification message goes to the new address. "
            "An address that belongs to another user returns duplicate_email. "
            "roles and linked_contact_id remain rejected on updateMe."
        ),
        tag="Identity",
        features=["profile"],
        roles=ANY,
        scopes=["profile:write"],
        body="EmailChangeRequest",
        response="EmailChangePending",
        error_codes=["duplicate_email"],
        extra_access=OWN,
    )
    api.add(
        "post",
        "/api/v1/me/email-change/confirm",
        operation_id="confirmEmailChange",
        summary="Confirm an email change",
        description=(
            "Consumes the verification token and sets only the caller's email. "
            "A token issued for another user is forbidden. "
            "Until this call succeeds, getMe still returns the previous email."
        ),
        tag="Identity",
        features=["profile"],
        roles=ANY,
        scopes=["profile:write"],
        body="EmailChangeConfirm",
        response="User",
        events=["user.updated"],
        extra_access={**OWN, "email_uniqueness": "confirmed_address"},
    )
    api.add(
        "post",
        "/api/v1/me/password",
        operation_id="changePassword",
        summary="Change password",
        description="Requires the current password.",
        tag="Identity",
        features=["profile"],
        roles=ANY,
        scopes=["profile:write"],
        body="PasswordChange",
        success="204",
        response_content={},
        extra_access=OWN,
    )
    api.add(
        "post",
        "/api/v1/me/totp/reset",
        operation_id="resetTotp",
        summary="Reset two-factor",
        description=(
            "Starts a replacement enrollment for the signed-in user only after recent reauthentication. "
            "The body is a current password, a code from the current authenticator, an unused recovery code, or a server-issued reauthentication token bound to this user and resetTotp. "
            "A boolean, or a code from the new secret, is not proof. "
            "The current secret stays active until confirmTotpReset accepts the first code. "
            "The response includes a single-use enrollment_token that expires. "
            "Another user's reset is not_found."
        ),
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["profile:write"],
        body="FactorReauthentication",
        response="TotpResetChallenge",
        events=["user.totp_reset_started"],
        extra_access={**OWN, "old_secret": "active_until_confirm", "token": "single_use_bounded"},
    )
    api.add(
        "post",
        "/api/v1/me/totp/reset/confirm",
        operation_id="confirmTotpReset",
        summary="Confirm a two-factor reset",
        description=(
            "Activates the pending secret for the signed-in user and then invalidates the old one. "
            "The enrollment_token is the one resetTotp returned, and it is spent by a correct code. "
            "A wrong code, an expired token, or a replay does not change the active secret. "
            "A token issued for another user is forbidden."
        ),
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["profile:write"],
        body="TotpResetConfirm",
        response="User",
        events=["user.totp_reset"],
        error_codes=["unauthenticated"],
        extra_access={**OWN, "old_secret": "retired_after_valid_code", "token": "single_use_bounded"},
    )
    api.add(
        "post",
        "/api/v1/me/recovery-codes",
        operation_id="regenerateRecoveryCodes",
        summary="Regenerate recovery codes",
        description=(
            "Replaces the user's recovery codes only after the same recent reauthentication as resetTotp. "
            "The proof must be bound to regenerateRecoveryCodes. The new codes are returned once."
        ),
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["profile:write"],
        body="FactorReauthentication",
        response="RecoveryCodes",
        extra_access=OWN,
    )
    api.add(
        "get",
        "/api/v1/me/settings",
        operation_id="getMeSettings",
        summary="My settings",
        description="Personal reminder lead times, display time zone, mode, text size, and idle timeout.",
        tag="Identity",
        features=["me-settings"],
        roles=ANY,
        scopes=["profile:read"],
        response="MeSettings",
        writes=False,
        extra_access=OWN,
    )
    api.add(
        "patch",
        "/api/v1/me/settings",
        operation_id="updateMeSettings",
        summary="Update my settings",
        description="A user can choose a shorter idle timeout than the firm maximum and cannot choose a longer one.",
        tag="Identity",
        features=["me-settings"],
        roles=ANY,
        scopes=["profile:write"],
        body="MeSettings",
        response="MeSettings",
        extra_access=OWN,
    )
    api.add(
        "get",
        "/api/v1/me/notification-preferences",
        operation_id="getNotificationPreferences",
        summary="Notification preferences",
        description="The caller's grid. Trust-flag email to an admin or the responsible attorney is email_required.",
        tag="Identity",
        features=["notification-preferences"],
        roles=FIRM,
        scopes=["notifications:read"],
        response="NotificationPreferences",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/me/notification-preferences",
        operation_id="putNotificationPreferences",
        summary="Replace notification preferences",
        description="Rejects a body that turns off an email_required channel.",
        tag="Identity",
        features=["notification-preferences"],
        roles=FIRM,
        scopes=["notifications:write"],
        body="NotificationPreferences",
        response="NotificationPreferences",
        error_codes=["validation_failed"],
    )
    api.add(
        "get",
        "/api/v1/me/sidebar",
        operation_id="getSidebarState",
        summary="Sidebar collapse state",
        description="Collapsed sidebar and collapsed subgroups for this user.",
        tag="Identity",
        features=["sidebar"],
        roles=FIRM,
        scopes=["profile:read"],
        response="SidebarState",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/me/sidebar",
        operation_id="putSidebarState",
        summary="Save sidebar collapse state",
        description="The saved state follows the user to another computer.",
        tag="Identity",
        features=["sidebar"],
        roles=FIRM,
        scopes=["profile:write"],
        body="SidebarState",
        response="SidebarState",
    )
    api.add(
        "get",
        "/api/v1/sessions",
        operation_id="listSessions",
        summary="List sessions",
        description="Sessions for the current user. An admin may pass user_id.",
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["sessions:read"],
        response_schema=_page("Session"),
        paginate=True,
        query=["user_id"],
        writes=False,
        extra_access=SESSION_OWNERSHIP,
    )
    api.add(
        "delete",
        "/api/v1/sessions/{session_id}",
        operation_id="deleteSession",
        summary="End a session",
        description="Revokes one session.",
        tag="Identity",
        features=["auth"],
        roles=ANY,
        scopes=["sessions:write"],
        success="204",
        response_content={},
        extra_access=SESSION_OWNERSHIP,
    )
    api.resource(
        collection="/api/v1/users",
        schema="User",
        write="UserCreate",
        patch_schema="UserWritePatch",
        id_param="user_id",
        tag="Identity",
        feature="users",
        singular="user",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="users:read",
        write_scope="users:write",
        events={"created": "user.created", "updated": "user.updated"},
        create=True,
        update=True,
        include=True,
    )
    api.add(
        "post",
        "/api/v1/users/{user_id}/deactivate",
        operation_id="deactivateUser",
        summary="Deactivate a user",
        description="The user can no longer sign in. Rows they created stay attributed to them.",
        tag="Identity",
        features=["users"],
        roles=ADMIN,
        scopes=["users:write"],
        response="User",
        events=["user.updated"],
    )
    api.resource(
        collection="/api/v1/api-tokens",
        schema="ApiToken",
        write="ApiTokenWrite",
        id_param="token_id",
        tag="Identity",
        feature="api-tokens",
        singular="API token",
        plural="API tokens",
        read_roles=ANY,
        write_roles=ANY,
        read_scope="tokens:read",
        write_scope="tokens:write",
        create=False,
        update=False,
        extra_features=["profile"],
        extra_access=TOKEN_OWNERSHIP,
    )
    api.add(
        "post",
        "/api/v1/api-tokens",
        operation_id="createApiToken",
        summary="Issue an API token",
        description=(
            "An admin issues the secret once, from the admin dashboard. "
            "The token is bound to one user or one module. "
            "Requested scopes must already be in the issuing credential and in that owner's role or approved module permissions. "
            "A bearer cannot obtain a scope it does not hold. Staff, accountants, and clients cannot issue tokens."
        ),
        tag="Identity",
        features=["api-tokens"],
        roles=ADMIN,
        scopes=["tokens:write"],
        body="ApiTokenWrite",
        response="ApiTokenCreated",
        success="201",
        extra_access={**TOKEN_OWNERSHIP, **TOKEN_ISSUANCE},
    )
    api.add(
        "delete",
        "/api/v1/api-tokens/{token_id}",
        operation_id="revokeApiToken",
        summary="Revoke an API token",
        description="The owner or an admin revokes. Another user's token is not_found. A revoked token fails closed on the next call.",
        tag="Identity",
        features=["api-tokens"],
        roles=ANY,
        scopes=["tokens:write"],
        success="204",
        response_content={},
        extra_access=TOKEN_OWNERSHIP,
    )


def _practice(api: Contract) -> None:
    api.resource(
        collection="/api/v1/contacts",
        schema="Contact",
        write="ContactWrite",
        id_param="contact_id",
        tag="Contacts",
        feature="contacts",
        singular="contact",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="contacts:read",
        write_scope="contacts:write",
        bulk=True,
        history=True,
        archive=True,
        include=True,
        csv=True,
        filters=["q", "archived", "tag", "kind", "custom"],
        events={"created": "contact.created", "updated": "contact.updated", "archived": "contact.archived"},
        extra_access={"attorney_only": ["identifier"]},
    )
    for schema, singular, feature in (
        ("ContactAddress", "address", "contact-channels"),
        ("ContactEmail", "email", "contact-channels"),
        ("ContactPhone", "phone", "contact-channels"),
        ("ContactTag", "tag", "contact-channels"),
    ):
        api.resource(
            collection=f"/api/v1/contact-{_slug(schema)}",
            schema=schema,
            write=schema + "Write",
            id_param=f"{_slug(schema)[:-1]}_id" if schema != "ContactAddress" else "address_id",
            tag="Contacts",
            feature=feature,
            singular=singular,
            read_roles=STAFF,
            write_roles=STAFF,
            read_scope="contacts:read",
            write_scope="contacts:write",
            bulk=True,
            filters=["contact_id"],
            events={"created": "contact.updated", "updated": "contact.updated"},
        )
    api.add(
        "post",
        "/api/v1/contacts/{contact_id}/merge",
        operation_id="mergeContact",
        summary="Merge a contact",
        description="Relinks module rows to the survivor in the same transaction and archives this contact.",
        tag="Contacts",
        features=["contacts", "merge"],
        roles=ATTORNEY,
        scopes=["contacts:write"],
        body="MergeRequest",
        response="MergeResult",
        events=["contact.merged"],
    )
    api.resource(
        collection="/api/v1/matters",
        schema="Matter",
        write="MatterWrite",
        id_param="matter_id",
        tag="Matters",
        feature="matters",
        singular="matter",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="matters:read",
        write_scope="matters:write",
        bulk=True,
        history=True,
        include=True,
        csv=True,
        filters=["q", "status", "practice_area_id", "responsible_user_id", "billing_type", "archived", "custom"],
        archive=True,
        events={"created": "matter.created", "updated": "matter.updated", "archived": "matter.archived"},
        extra_access=WALL,
        extra_features=["matter-glance", "include-extensions"],
    )
    api.spec["paths"]["/api/v1/matters/{matter_id}"]["patch"]["description"] = (
        "Patches ordinary fields. status may move among prospect, open, and on_hold. "
        "It cannot set closed and it cannot change a closed matter. "
        "closeMatter closes a matter. reopenMatter, for an attorney or admin with a reason, restores the status from before the close and emits matter.reopened."
    )
    api.add(
        "get",
        "/api/v1/matters/{matter_id}/close-preview",
        operation_id="previewMatterClose",
        summary="Preview matter close warnings",
        description="Returns unpaid-invoice and nonzero-trust warnings and writes nothing.",
        tag="Matters",
        features=["matter-close"],
        roles=STAFF,
        scopes=["matters:read"],
        response="CloseResult",
        writes=False,
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/matters/{matter_id}/close",
        operation_id="closeMatter",
        summary="Close a matter",
        description="Closes the matter. An unpaid invoice or a leftover trust balance is a warning on the result and does not stop the close.",
        tag="Matters",
        features=["matter-close"],
        roles=STAFF,
        scopes=["matters:write"],
        body="CloseMatter",
        response="CloseResult",
        events=["matter.closed"],
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/matters/{matter_id}/reopen",
        operation_id="reopenMatter",
        summary="Reopen a matter",
        description="Requires a reason. Restores the status from before the close. The close date and close reason stay on the record and on the timeline.",
        tag="Matters",
        features=["matter-reopen"],
        roles=ATTORNEY,
        scopes=["matters:write"],
        body="ReopenMatter",
        response="Matter",
        events=["matter.reopened"],
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/matters/{matter_id}/merge",
        operation_id="mergeMatter",
        summary="Merge a matter",
        description=(
            "Relinks module rows to the survivor in the same transaction and archives this matter. "
            "The caller must be allowed to see both this matter and the survivor. "
            "A missing or unresolved survivor is not treated as accessible. "
            "An inaccessible matter is not_found. Each matter's wall state must be a resolved boolean. "
            "Nothing is relinked, archived, or emitted unless both are allowed. "
            "A hard delete is not available."
        ),
        tag="Matters",
        features=["matters", "merge"],
        roles=ATTORNEY,
        scopes=["matters:write"],
        body="MergeRequest",
        response="MergeResult",
        events=["matter.merged"],
        extra_access=WALL,
    )
    api.add(
        "get",
        "/api/v1/worklists/matters-missing-case-number",
        operation_id="listMattersMissingCaseNumber",
        summary="Matters missing a case number",
        description="Open matters with no case number that have a court or a name matching a configured docket pattern. The list changes no data.",
        tag="Matters",
        features=["missing-case-number"],
        roles=STAFF,
        scopes=["matters:read"],
        response_schema=_page("Matter"),
        paginate=True,
        writes=False,
        include=True,
        extra_access={**WALL, "include_scope_pattern": "views:{module_id}.{view}:read"},
    )
    api.add(
        "get",
        "/api/v1/matters/{matter_id}/timeline",
        operation_id="listMatterTimeline",
        summary="Case timeline",
        description="Events for one matter, newest first. Client actions are marked client_visible. Walled matters are not_found.",
        tag="Matters",
        features=["activity"],
        roles=STAFF,
        scopes=["events:read"],
        response_schema=_page("Event"),
        paginate=True,
        writes=False,
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/matter-parties",
        schema="MatterParty",
        write="MatterPartyWrite",
        id_param="party_id",
        tag="Matters",
        feature="matter-parties",
        singular="matter party",
        plural="matter parties",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="matters:read",
        write_scope="matters:write",
        bulk=True,
        filters=["matter_id", "contact_id"],
        events={"created": "matter.updated", "updated": "matter.updated"},
        include=True,
        extra_access=WALL,
    )
    api.add(
        "put",
        "/api/v1/matters/{matter_id}/wall",
        operation_id="putMatterWall",
        summary="Replace a matter wall",
        description=(
            "Replaces the wall only after the caller is allowed through the current wall, the same check as reading it. "
            "An admin who is off the wall, including a module acting for that admin, receives not_found. "
            "The membership is not replaced and matter.wall_changed is not emitted. "
            "An open matter can gain its first wall. A caller on the wall can replace the list or clear it."
        ),
        tag="Matters",
        features=["matter-walls"],
        roles=ADMIN,
        scopes=["matters:write"],
        body="MatterWall",
        response="MatterWall",
        events=["matter.wall_changed"],
        extra_access={
            **WALL,
            "admin_bypasses_wall": False,
            "authorize_before": "any_mutation",
            "on_denial": "no_mutation_no_event",
            "open_matter": "may_create_wall",
        },
    )
    api.add(
        "get",
        "/api/v1/matters/{matter_id}/wall",
        operation_id="getMatterWall",
        summary="Read a matter wall",
        description="The allowed user ids. A caller who is not on the wall receives not_found for the matter, including this path.",
        tag="Matters",
        features=["matter-walls"],
        roles=ADMIN,
        scopes=["matters:read"],
        response="MatterWall",
        writes=False,
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/matter-markers",
        schema="MatterMarker",
        write="MatterMarkerWrite",
        id_param="matter_marker_id",
        tag="Matters",
        feature="matter-markers",
        singular="matter marker",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="matters:read",
        write_scope="matters:write",
        filters=["matter_id"],
        events={"created": "matter.updated", "updated": "matter.updated", "deleted": "matter.updated"},
        delete=True,
        bulk=True,
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/marker-definitions",
        schema="MarkerDefinition",
        write="MarkerDefinitionWrite",
        id_param="marker_id",
        tag="Matters",
        feature="matter-markers",
        singular="marker definition",
        read_roles=STAFF,
        write_roles=ADMIN,
        read_scope="settings:read",
        write_scope="settings:write",
        filters=["active"],
    )
    api.spec["paths"]["/api/v1/marker-definitions"]["post"]["description"] += (
        " Admin only, with settings:write. A module cannot call this route. "
        "An installed module registers a marker in its own namespace with markers:write. "
        "That scope is not settings:write and it does not make the module an admin."
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/markers",
        operation_id="createModuleMarker",
        summary="Register a module marker",
        description=(
            "An approved module token with markers:write registers one marker in its own namespace. "
            "The owner is the signed module id on the token. The path module_id must be that same id. "
            "A caller-supplied owner is ignored. The token cannot create a firm marker or a marker for another module. "
            "settings:write and an admin role are not accepted here. A human session is forbidden. "
            "The label is plain text. The color is a six-digit hex value. "
            "An optional display target is a module-local page; a URL or another scheme is rejected. "
            "A second active marker with the same label in this namespace is a conflict. "
            "Revoke hides the definition and leaves matter pins that already use it."
        ),
        tag="Matters",
        features=["matter-markers"],
        roles=[],
        scopes=["markers:write"],
        body="ModuleMarkerWrite",
        response="ModuleMarker",
        success="201",
        events=["marker.created"],
        error_codes=["forbidden", "scope_denied", "conflict"],
        extra_access=MODULE_MARKER,
    )
    api.add(
        "patch",
        "/api/v1/modules/{module_id}/markers/{marker_id}",
        operation_id="updateModuleMarker",
        summary="Update a module marker",
        description=(
            "Updates the calling module's own marker. A firm marker and another module's marker are forbidden. "
            "The signed module id is the owner. A duplicate active label in this namespace is a conflict. "
            "A revoked marker is not edited back to active; register a new one. "
            "Label, color, and display target use the same checks as createModuleMarker."
        ),
        tag="Matters",
        features=["matter-markers"],
        roles=[],
        scopes=["markers:write"],
        body="ModuleMarkerWritePatch",
        response="ModuleMarker",
        events=["marker.updated"],
        error_codes=["forbidden", "scope_denied", "conflict", "not_found"],
        extra_access=MODULE_MARKER,
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/markers/{marker_id}/revoke",
        operation_id="revokeModuleMarker",
        summary="Revoke a module marker",
        description=(
            "Sets the calling module's marker inactive. The row stays, and matter pins that already use it stay. "
            "A firm marker and another module's marker are forbidden. "
            "Revoking an inactive marker returns the original result and emits nothing."
        ),
        tag="Matters",
        features=["matter-markers"],
        roles=[],
        scopes=["markers:write"],
        response="ModuleMarker",
        events=["marker.revoked"],
        error_codes=["forbidden", "scope_denied", "not_found"],
        extra_access=MODULE_MARKER,
    )
    api.resource(
        collection="/api/v1/fee-agreements",
        schema="FeeAgreement",
        write="FeeAgreementWrite",
        id_param="fee_agreement_id",
        tag="Matters",
        feature="fee-agreements",
        singular="fee agreement",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="matters:read",
        write_scope="matters:write",
        history=True,
        bulk=True,
        filters=["matter_id", "status"],
        events={"created": "fee_agreement.created", "updated": "fee_agreement.updated"},
        include=True,
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/notes",
        schema="Note",
        write="NoteWrite",
        id_param="note_id",
        tag="Notes",
        feature="notes",
        singular="note",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="notes:read",
        write_scope="notes:write",
        bulk=True,
        archive=True,
        filters=["q", "matter_id", "contact_id", "author_id", "tag", "from_date", "to_date", "pinned", "status"],
        events={"created": "note.created", "updated": "note.updated", "archived": "note.archived"},
        include=True,
        extra_access=WALL,
    )
    api.spec["paths"]["/api/v1/notes"]["get"]["description"] = (
        api.spec["paths"]["/api/v1/notes"]["get"]["description"].rstrip()
        + " The default page size is notes.page_size."
    )
    api.resource(
        collection="/api/v1/conflict-records",
        schema="ConflictRecord",
        write="ConflictRecordWrite",
        id_param="conflict_record_id",
        tag="Conflicts",
        feature="conflicts",
        singular="conflict record",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="conflicts:read",
        write_scope="conflicts:write",
        bulk=True,
        filters=["q"],
        events={"created": "conflict_record.created", "updated": "conflict_record.updated"},
        include=True,
        extra_access={"attorney_only": ["reason"], "matter_wall": True, "access_inherits": "matter"},
    )
    api.add(
        "post",
        "/api/v1/matters/{matter_id}/conflict-checks",
        operation_id="runConflictCheck",
        summary="Run a conflict check",
        description="Searches contacts, matter parties, and conflict records by name, alias, and date of birth. Hits below the score floor are omitted.",
        tag="Conflicts",
        features=["conflicts"],
        roles=STAFF,
        scopes=["conflicts:read"],
        response="ConflictCheck",
        success="201",
        events=["conflict_check.created"],
        extra_access=WALL,
    )
    api.add(
        "get",
        "/api/v1/conflict-checks",
        operation_id="listConflictChecks",
        summary="List conflict checks",
        description="Conflict checks the caller can see.",
        tag="Conflicts",
        features=["conflicts"],
        roles=STAFF,
        scopes=["conflicts:read"],
        response_schema=_page("ConflictCheck"),
        paginate=True,
        query=["matter_id"],
        writes=False,
        extra_access=WALL,
    )
    api.add(
        "get",
        "/api/v1/conflict-checks/{conflict_check_id}",
        operation_id="getConflictCheck",
        summary="Get a conflict check",
        description="One check and its hits. Reasons are redacted without attorney-only access.",
        tag="Conflicts",
        features=["conflicts"],
        roles=STAFF,
        scopes=["conflicts:read"],
        response="ConflictCheck",
        writes=False,
        extra_access={**WALL, "attorney_only": ["reason"]},
    )
    api.add(
        "post",
        "/api/v1/conflict-checks/{conflict_check_id}/decisions",
        operation_id="decideConflictHit",
        summary="Record a conflict decision",
        description="An attorney records clear, escalate, or no go for one hit. Staff cannot call this.",
        tag="Conflicts",
        features=["conflicts"],
        roles=ATTORNEY,
        scopes=["conflicts:decide"],
        body="ConflictDecision",
        response="ConflictCheck",
        events=["conflict_check.decided"],
        extra_access=WALL,
    )
    _admin_lists(api)
    api.add(
        "put",
        "/api/v1/matters/{matter_id}/custom-fields",
        operation_id="putMatterCustomFields",
        summary="Set matter custom fields",
        description="Writes values for definitions that apply to the matter's practice area. A field limited to another area is validation_failed.",
        tag="Custom fields",
        features=["custom-fields"],
        roles=STAFF,
        scopes=["matters:write"],
        body="CustomFieldValuesWrite",
        response="Matter",
        events=["matter.updated"],
        extra_access=WALL,
    )
    api.add(
        "put",
        "/api/v1/time-entries/{time_entry_id}/custom-fields",
        operation_id="putTimeEntryCustomFields",
        summary="Set time entry custom fields",
        description="Writes values for definitions of record type time_entry.",
        tag="Custom fields",
        features=["custom-fields"],
        roles=STAFF,
        scopes=["time:write"],
        body="CustomFieldValuesWrite",
        response="TimeEntry",
        events=["time_entry.updated"],
        extra_access=WALL,
        replaceable="time",
    )
    api.add(
        "put",
        "/api/v1/expenses/{expense_id}/custom-fields",
        operation_id="putExpenseCustomFields",
        summary="Set expense custom fields",
        description="Writes values for definitions of record type expense.",
        tag="Custom fields",
        features=["custom-fields"],
        roles=STAFF,
        scopes=["expenses:write"],
        body="CustomFieldValuesWrite",
        response="Expense",
        events=["expense.updated"],
        extra_access=WALL,
        replaceable="time",
    )
    api.add(
        "post",
        "/api/v1/custom-field-values:bulk",
        operation_id="bulkCustomFieldValue",
        summary="Write custom field values in one batch",
        description=(
            "Each item names a record. The bearer token needs that record type's write scope, not all four scopes at once. "
            "time_entry items follow putTimeEntryCustomFields and expense items follow putExpenseCustomFields, including the time replacement domain. "
            "matter and contact items stay on the core. "
            "Every item is authorized and its domain is resolved before any write. "
            "When time is replaced, the core does not write time_entry or expense values. "
            "An unavailable replacement rejects the batch, including any core matter or contact item in the same batch. "
            "One inaccessible matter rejects the batch with not_found and writes nothing."
        ),
        tag="Custom fields",
        features=["custom-fields", "bulk"],
        roles=STAFF,
        scopes=["contacts:write", "matters:write", "time:write", "expenses:write"],
        extra_access={
            **BATCH_WALL,
            "scope_combination": "per_item",
            "record_type_scopes": {
                "matter": ["matters:write"],
                "contact": ["contacts:write"],
                "time_entry": ["time:write"],
                "expense": ["expenses:write"],
            },
            "record_type_domain": {"matter": None, "contact": None, "time_entry": "time", "expense": "time"},
            "domain_routing": "per_item",
            "resolve_before_write": True,
            "replaced_core": "read_only",
            "unavailable_replacement": "batch_rejected",
            "partial_core_write": False,
        },
        body_schema={
            "type": "object",
            "additionalProperties": False,
            "required": ["items"],
            "properties": {
                "items": {
                    "type": "array",
                    "minItems": 1,
                    "items": {
                        "type": "object",
                        "additionalProperties": False,
                        "required": ["record_type", "record_id", "values"],
                        "properties": {
                            "record_type": {"type": "string", "enum": ["matter", "contact", "time_entry", "expense"]},
                            "record_id": {"$ref": "#/components/schemas/Uuid"},
                            "values": {"type": "array", "items": {"$ref": "#/components/schemas/CustomFieldValue"}},
                        },
                    },
                }
            },
        },
        success="201",
        response_schema={"type": "object", "additionalProperties": False, "required": ["updated"], "properties": {"updated": {"type": "integer", "minimum": 0}}},
        events=["custom_field.updated"],
        error_codes=["batch_rejected", "not_found"],
    )
    api.add(
        "put",
        "/api/v1/contacts/{contact_id}/custom-fields",
        operation_id="putContactCustomFields",
        summary="Set contact custom fields",
        description="Writes contact custom-field values.",
        tag="Custom fields",
        features=["custom-fields"],
        roles=STAFF,
        scopes=["contacts:write"],
        body="CustomFieldValuesWrite",
        response="Contact",
        events=["contact.updated"],
    )


def _admin_lists(api: Contract) -> None:
    specs = [
        ("PracticeArea", "practice area", "practice areas", "practice-areas", "practice_area_id"),
        ("County", "county", "counties", "counties", "county_id"),
        ("ReferralSource", "referral source", "referral sources", "referral-sources", "referral_source_id"),
        ("PartyRole", "party role", "party roles", "party-roles", "party_role_id"),
        ("FeeItem", "fee item", "fee items", "fee-items", "fee_item_id"),
        ("ActivityType", "activity type", "activity types", "activity-types", "activity_type_id"),
        ("ExpenseType", "expense type", "expense types", "expense-types", "expense_type_id"),
        ("CustomFieldDefinition", "custom field definition", "custom field definitions", "custom-fields", "definition_id"),
    ]
    for schema, singular, plural, feature, id_param in specs:
        api.resource(
            collection=f"/api/v1/{feature}" if feature != "custom-fields" else "/api/v1/custom-field-definitions",
            schema=schema,
            write=schema + "Write",
            id_param=id_param,
            tag="Admin lists",
            feature=feature,
            singular=singular,
            plural=plural,
            read_roles=STAFF,
            write_roles=ADMIN,
            read_scope="settings:read",
            write_scope="settings:write",
            filters=["active"] if schema != "CustomFieldDefinition" else ["record_type", "practice_area_id"],
            events={"created": "setting.updated", "updated": "setting.updated"},
        )
    api.add(
        "post",
        "/api/v1/custom-field-definitions/{definition_id}/retire",
        operation_id="retireCustomFieldDefinition",
        summary="Retire a custom field",
        description="Hides the field and keeps its values. The database schema does not change.",
        tag="Admin lists",
        features=["custom-fields"],
        roles=ADMIN,
        scopes=["settings:write"],
        response="CustomFieldDefinition",
        events=["setting.updated"],
    )


def _billing(api: Contract) -> None:
    api.resource(
        collection="/api/v1/time-entries",
        schema="TimeEntry",
        write="TimeEntryWrite",
        id_param="time_entry_id",
        tag="Billing",
        feature="time-entries",
        singular="time entry",
        plural="time entries",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="time:read",
        write_scope="time:write",
        bulk=True,
        history=True,
        include=True,
        csv=True,
        filters=["matter_id", "user_id", "from_date", "to_date", "billable", "invoice_id", "custom"],
        events={"created": "time_entry.created", "updated": "time_entry.updated"},
        replaceable="time",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/timers",
        schema="Timer",
        write="TimerWrite",
        id_param="timer_id",
        tag="Billing",
        feature="timers",
        singular="timer",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="time:read",
        write_scope="time:write",
        update=False,
        events={"created": "timer.started"},
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/timers/{timer_id}/pause",
        operation_id="pauseTimer",
        summary="Pause a timer",
        description="Pauses the caller's timer. The timer's matter wall applies; a walled matter the caller cannot see is not_found.",
        tag="Billing",
        features=["timers"],
        roles=STAFF,
        scopes=["time:write"],
        response="Timer",
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/timers/{timer_id}/stop",
        operation_id="stopTimer",
        summary="Stop a timer",
        description="Stops the timer and creates one draft time entry. The response timer status is stopped, not running or paused. The timer's matter wall applies.",
        tag="Billing",
        features=["timers"],
        roles=STAFF,
        scopes=["time:write"],
        response="TimerStopResult",
        events=["time_entry.created"],
        replaceable="time",
        extra_access=WALL,
    )
    api.spec["paths"]["/api/v1/timers"]["post"]["description"] = (
        "Starts a timer for the caller on this matter. "
        "If a paused timer already exists for that user and matter, that timer resumes and accumulated_seconds is kept. "
        "A second timer is not created."
    )
    api.add(
        "post",
        "/api/v1/timers/{timer_id}/resume",
        operation_id="resumeTimer",
        summary="Resume a paused timer",
        description="Sets a paused timer back to running. accumulated_seconds is kept, so elapsed time is not reset. The matter wall applies.",
        tag="Billing",
        features=["timers"],
        roles=STAFF,
        scopes=["time:write"],
        response="Timer",
        replaceable="time",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/expenses",
        schema="Expense",
        write="ExpenseWrite",
        id_param="expense_id",
        tag="Billing",
        feature="expenses",
        singular="expense",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="expenses:read",
        write_scope="expenses:write",
        bulk=True,
        history=True,
        include=True,
        csv=True,
        filters=["matter_id", "from_date", "to_date", "billable", "invoice_id", "custom"],
        events={"created": "expense.created", "updated": "expense.updated"},
        replaceable="time",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/invoices",
        schema="Invoice",
        write="InvoicePatch",
        patch_schema="InvoicePatch",
        id_param="invoice_id",
        tag="Billing",
        feature="invoices",
        singular="invoice",
        read_roles=BILLING + ["client"],
        write_roles=STAFF,
        read_scope="invoices:read",
        write_scope="invoices:write",
        create=False,
        history=True,
        include=True,
        csv=True,
        filters=["matter_id", "status", "from_date", "to_date"],
        events={"updated": "invoice.updated"},
        replaceable="time",
        extra_access={**WALL, "client": "shared"},
    )
    _project_client_invoices(api)
    api.add(
        "post",
        "/api/v1/invoice-drafts",
        operation_id="createInvoiceDrafts",
        summary="Draft invoices from unbilled time",
        description=(
            "Pulls unbilled time and expenses for one matter, a list of matters, or a date range. "
            "A flat-fee matter bills the fee. Exactly one selector is set: matter_id, or matter_ids, or from_date with to_date. "
            "A date range does not require matter_id or matter_ids. from_date must be on or before to_date. "
            "The date range drafts only matters this caller is allowed to bill, one matter id at a time. "
            "An off-wall matter is omitted even when on_wall is true for another matter, and an admin does not bypass that wall. "
            "Naming a matter the caller cannot see returns not_found and drafts nothing. "
            "An empty selector, two selectors, or a reversed date range returns validation_failed and drafts nothing."
        ),
        extra_access={
            **WALL,
            "draft_selector": {
                "alternatives": ["matter_id", "matter_ids", "date_range"],
                "mutual_exclusion": True,
                "date_order": "from_on_or_before_to",
                "date_range_matters": "authorized_only",
                "named_matter_hidden": "not_found",
                "empty_selector": "validation_failed",
                "matters_required": False,
            },
        },
        tag="Billing",
        features=["invoices", "billing-worklist"],
        roles=STAFF,
        scopes=["invoices:write"],
        body="InvoiceDraftRequest",
        response_schema=_batch_result("Invoice"),
        success="201",
        events=["invoice.drafted"],
        replaceable="time",
    )
    api.add(
        "post",
        "/api/v1/invoices:bulk",
        operation_id="bulkInvoice",
        summary="Create invoices in one batch",
        description=(
            "Importer write for invoice records. A draft uses the same role and invoices:write scope as createInvoiceDrafts. "
            "sent and viewed use sendInvoice. void uses voidInvoice. A write-off uses writeOffInvoice. "
            "Amount paid uses createPayment. paid also uses sendInvoice, so staff cannot mark an invoice paid. "
            "One item the caller cannot perform rejects the whole batch before any row is written. "
            "Validates every item, reconciles integer cents, and checks each matter wall. "
            "One validation, reconciliation, role, scope, or wall failure writes nothing. "
            "A repeated Idempotency-Key with the same body returns the original invoices and emits nothing. "
            "A different body with that key is idempotency_conflict and writes nothing. "
            "A committed batch emits invoice.created once per item. HTTP and MCP use this same operation. "
            "A client projection keeps only InvoiceClient fields, on JSON and on a later read. Staff fields are not returned."
        ),
        tag="Billing",
        features=["invoices", "bulk"],
        roles=STAFF,
        scopes=["invoices:write"],
        body_schema=_batch("InvoiceWrite"),
        response_schema=_batch_result("Invoice"),
        success="201",
        events=["invoice.created"],
        error_codes=["batch_rejected", "validation_failed", "not_found", "idempotency_conflict", "forbidden", "scope_denied"],
        replaceable="time",
        extra_access={
            **WALL,
            "admin_bypasses_wall": False,
            "batch_authorization": "all_or_nothing",
            "reconciliation": "exact_cents",
            "idempotency": "same_body_replays",
            "client": "deny",
            "client_schema": "InvoiceClient",
            "events_on": "commit",
            "replay_events": False,
        },
    )
    api.add(
        "put",
        "/api/v1/invoices/{invoice_id}/draft",
        operation_id="updateInvoiceDraft",
        summary="Edit a draft invoice",
        description=(
            "Replaces lines on a draft, including a catalog fee item whose description and amount stay editable. "
            "Sets adjustment_cents and adjustment_reason. "
            "The invoice must be status draft; any other status returns conflict and writes nothing. "
            "Approval is approveInvoice. An accountant cannot call this."
        ),
        tag="Billing",
        features=["invoices"],
        roles=STAFF,
        scopes=["invoices:write"],
        body="InvoiceDraftEdit",
        response="Invoice",
        events=["invoice.updated"],
        error_codes=["conflict"],
        replaceable="time",
        extra_access={**WALL, "required_status": "draft"},
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/approve",
        operation_id="approveInvoice",
        summary="Approve an invoice",
        description="An attorney or admin approves a draft.",
        tag="Billing",
        features=["invoices"],
        roles=ATTORNEY,
        scopes=["invoices:approve"],
        response="Invoice",
        events=["invoice.approved"],
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/send",
        operation_id="sendInvoice",
        summary="Send an invoice",
        description="deliver=email sends the PDF by system mail. deliver=file marks it sent without mail. Either way the status becomes sent.",
        tag="Billing",
        features=["invoices"],
        roles=ATTORNEY,
        scopes=["invoices:send"],
        body="InvoiceSend",
        response="Invoice",
        events=["invoice.sent"],
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "get",
        "/api/v1/invoices/{invoice_id}/pdf",
        operation_id="getInvoicePdf",
        summary="Download an invoice PDF",
        description="Renders the PDF from the firm logo and settings. Does not change status.",
        tag="Billing",
        features=["invoices"],
        roles=BILLING + ["client"],
        scopes=["invoices:read"],
        response_content={"application/pdf": {"schema": {"type": "string", "contentMediaType": "application/pdf"}}},
        writes=False,
        replaceable="time",
        extra_access={**SHARED, "client": "shared"},
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/void",
        operation_id="voidInvoice",
        summary="Void an invoice",
        description="Voids the invoice and returns its time and expenses to unbilled.",
        tag="Billing",
        features=["invoices"],
        roles=ATTORNEY,
        scopes=["invoices:write"],
        body="Reason",
        response="Invoice",
        events=["invoice.voided"],
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/apply-trust",
        operation_id="applyTrustToInvoice",
        summary="Apply trust to an invoice",
        description=(
            "Posts one trust transfer to operating and a matching payment. The amount is required in the body. An attorney or admin must call it. "
            "The invoice update follows the time handler. The ledger row is posted once by the trust handler. "
            "The time module does not post a second ledger row. The trust row stays append-only, and a below-zero balance still flags."
        ),
        tag="Billing",
        features=["collections", "trust-ledger"],
        roles=ATTORNEY,
        scopes=["trust:withdraw"],
        body="ApplyTrust",
        response="Payment",
        events=["payment.completed", "trust.entry_posted"],
        replaceable="time",
        domain_dispatch=TRUST_BILLING_DISPATCH,
        extra_access={**TRUST_ENTRY_ROLES},
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/write-off",
        operation_id="writeOffInvoice",
        summary="Write off an invoice balance",
        description="Records a write-off and a reason. Zero-fee matters are already excluded from collections.",
        tag="Billing",
        features=["invoices", "collections"],
        roles=ATTORNEY,
        scopes=["invoices:write"],
        body="WriteOff",
        response="Invoice",
        events=["invoice.updated"],
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/invoices/{invoice_id}/replenishment-requests",
        operation_id="requestInvoiceReplenishment",
        summary="Request replenishment for an invoice balance",
        description="Creates a replenishment request linked to the invoice. The amount is required.",
        tag="Billing",
        features=["collections", "trust-replenishment"],
        roles=STAFF,
        scopes=["trust:write"],
        body="TrustReplenishmentWrite",
        response="TrustReplenishment",
        success="201",
        events=["trust.replenishment_requested"],
        replaceable="trust",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/payments",
        schema="Payment",
        write="PaymentWrite",
        id_param="payment_id",
        tag="Billing",
        feature="payments",
        singular="payment",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="payments:read",
        write_scope="payments:write",
        bulk=True,
        update=False,
        include=True,
        csv=True,
        filters=["matter_id", "invoice_id", "from_date", "to_date", "status"],
        events={"created": "payment.created"},
        replaceable="time",
        extra_access=WALL,
        extra_features=["collections"],
    )
    api.spec["paths"]["/api/v1/payments"]["post"]["description"] = (
        "Records a payment. A repeated Idempotency-Key with the same body returns the original payment. "
        "A different body with that key returns idempotency_conflict and writes nothing. "
        "destination operating needs payments:write. "
        "A completed trust destination also needs trust:write, and that check runs before any payment or ledger row is written. "
        "A registered processor records this without a viewer by presenting the grant from initiateProcessorPayment. "
        "payments:write alone, with no viewer and no grant, is forbidden. "
        "payment.created is emitted when the row is inserted. payment.completed is emitted only when that row's resulting status is completed. "
        "A pending or failed create does not emit payment.completed. A replay emits nothing."
    )
    for payment_path in ("/api/v1/payments", "/api/v1/payments:bulk"):
        payment_operation = api.spec["paths"][payment_path]["post"]
        if "payment.completed" not in payment_operation["x-togavsu-events"]:
            payment_operation["x-togavsu-events"].append("payment.completed")
        payment_operation["x-togavsu-access"]["payment_events"] = {
            "selection": "resulting_state",
            "created_when": "row_inserted",
            "completed_when": "status_becomes_completed",
            "updated_when": "status_changes_without_completion",
            "replay": "no_event",
            "bulk": "per_item",
        }
    api.add(
        "post",
        "/api/v1/payments/processor-initializations",
        operation_id="initiateProcessorPayment",
        summary="Authorize a registered payment processor",
        description=(
            "A staff session asks the core to authorize one installed processor for this matter, contact, amount, and destination. "
            "The core delivers processor_authorization on moduleEvent payment.processor_authorized to that module only. "
            "This call does not create a payment and does not post trust. "
            "The initiation response does not contain the grant. "
            "An unregistered module, a different recipient, or a module id that is not the processor receives no grant. "
            "A walled matter the caller cannot see is not_found and issues nothing. "
            "Operating needs payments:write on the caller and on the module. "
            "Trust also needs trust:write on both, checked before delivery. "
            "The grant is not an admin or firm-wide token. The later callback does not need an iframe or a signed-in viewer."
        ),
        tag="Billing",
        features=["payments"],
        roles=STAFF,
        scopes=["payments:write"],
        body="ProcessorInitializationWrite",
        response="ProcessorInitialization",
        success="201",
        events=["payment.processor_authorized"],
        error_codes=["forbidden", "scope_denied"],
        replaceable="time",
        extra_access={**WALL, "processor_issuance": "payment_processor", "event_read_scopes": ["payments:write"]},
    )
    api.add(
        "post",
        "/api/v1/payments/{payment_id}/status",
        operation_id="updatePaymentStatus",
        summary="Update a payment status",
        description=(
            "Pending may become completed or failed. The same status is idempotent and does not write. "
            "Completed, failed, and reversed are terminal here. A late callback that would leave a terminal status is conflict and changes neither the payment nor the ledger. "
            "A settled correction is reversePayment, not status failed. reversed cannot become completed. "
            "The amount does not change. "
            "A registered processor calls this with the core-issued grant and no viewer session. "
            "The payment id, matter, and amount must be the ones bound on that grant. "
            "The first completion of a trust destination needs trust:write before any ledger row is written. "
            "A later completion does not post a second row. "
            "An ambient payments:write token with no viewer and no grant is forbidden. "
            "payment.completed is emitted when pending becomes completed. "
            "A change to failed emits payment.updated and not payment.completed. "
            "A same-status replay emits nothing."
        ),
        tag="Billing",
        features=["payments"],
        roles=STAFF,
        scopes=["payments:write"],
        body="PaymentStatusWrite",
        response="Payment",
        events=["payment.updated", "payment.completed"],
        replaceable="time",
        extra_access={
            **WALL,
            "payment_events": {
                "selection": "resulting_state",
                "created_when": "row_inserted",
                "completed_when": "status_becomes_completed",
                "updated_when": "status_changes_without_completion",
                "replay": "no_event",
                "bulk": "per_item",
            },
        },
    )
    api.add(
        "post",
        "/api/v1/payments/{payment_id}/reverse",
        operation_id="reversePayment",
        summary="Reverse a payment",
        description=(
            "Posts a reversal. The original payment amount is not edited and the original trust row is not edited. "
            "An operating payment, one with no trust entry, needs payments:write. "
            "A trust-linked payment also needs trust:withdraw. "
            "That scope check runs before any payment status change and before any ledger write. "
            "Insufficient scopes, the wrong role, or a walled matter write nothing. "
            "When the scopes and the wall allow it, the trust handler appends exactly one reversal. "
            "The time module does not post another. "
            "A reversal that leaves a negative balance still posts and raises trust.flag_raised. "
            "A non-negative reversal does not raise the flag. Rollback, denial, and replay emit nothing."
        ),
        tag="Billing",
        features=["payments"],
        roles=ATTORNEY,
        scopes=["payments:write"],
        body="Reason",
        response="Payment",
        events=["payment.reversed"],
        error_codes=["scope_denied"],
        replaceable="time",
        domain_dispatch=REVERSAL_DISPATCH,
        extra_access={
            **WALL,
            "payment_events": {
                "selection": "resulting_state",
                "replay": "no_event",
                "completed_when": "status_becomes_completed",
                "updated_when": "status_changes_without_completion",
            },
        },
    )
    api.add(
        "get",
        "/api/v1/billing/worklist",
        operation_id="getBillingWorklist",
        summary="Billing worklist",
        description="Flat-fee rows with Needs fee amount, Awaiting trust funds, Ready to apply, or Collected, and hourly rows with an unbilled total.",
        tag="Billing",
        features=["billing-worklist"],
        roles=BILLING,
        scopes=["invoices:read"],
        response="BillingWorklist",
        writes=False,
        replaceable="time",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/billing/flat-fees/{matter_id}/apply",
        operation_id="applyFlatFee",
        summary="Apply a flat fee from trust",
        description=(
            "Moves the confirmed amount from trust to operating. Only an attorney or admin can post it. The row can then show Collected. "
            "The trust handler posts the one ledger row. A replaced time module does not post a second row. "
            "The entry stays append-only, and a below-zero balance still flags."
        ),
        tag="Billing",
        features=["billing-worklist", "trust-ledger"],
        roles=ATTORNEY,
        scopes=["trust:withdraw"],
        body="FlatFeeApply",
        response="TrustPostResult",
        events=["trust.entry_posted"],
        replaceable="time",
        domain_dispatch=TRUST_BILLING_DISPATCH,
        extra_access=TRUST_ENTRY_ROLES,
    )
    for report_id, feature, query, required in (
        ("unbilled-time", "reports", ["matter_id", "from_date", "to_date", "user_id"], []),
        ("time-by-user", "reports", ["from_date", "to_date", "user_id"], ["from_date", "to_date"]),
        ("ar-aging", "reports", ["as_of"], []),
        ("payments-received", "reports", ["from_date", "to_date"], ["from_date", "to_date"]),
        ("write-offs", "reports", ["from_date", "to_date"], ["from_date", "to_date"]),
        ("trust-balances", "reports", ["trust_account_id"], []),
        ("trust-client-ledger", "reports", ["client_id", "matter_id"], ["client_id"]),
        ("trust-account-ledger", "reports", ["trust_account_id", "from_date", "to_date"], ["trust_account_id"]),
        ("reconciliations", "reports", ["trust_account_id"], []),
        ("doc-tender-spend", "reports", ["from_date", "to_date", "matter_id"], ["from_date", "to_date"]),
    ):
        scopes = ["reports:read"]
        roles = BILLING
        replaceable = None
        if report_id.startswith("trust") or report_id == "reconciliations":
            scopes = ["trust:read"]
            replaceable = "trust"
        elif report_id in {"unbilled-time", "time-by-user", "ar-aging", "payments-received", "write-offs"}:
            replaceable = "time"
        api.add(
            "get",
            f"/api/v1/reports/{report_id}",
            operation_id="report" + "".join(part.title() for part in report_id.split("-")),
            summary=f"Report {report_id}",
            description=_report_description(report_id),
            tag="Reports",
            features=[feature, "trust-reports"] if replaceable else [feature],
            roles=roles,
            scopes=scopes,
            query=query,
            required_query=required,
            csv=True,
            response="ReportTable",
            writes=False,
            replaceable=replaceable,
            extra_access=WALL,
        )


def _trust(api: Contract) -> None:
    api.resource(
        collection="/api/v1/trust-accounts",
        schema="TrustAccount",
        write="TrustAccountWrite",
        id_param="trust_account_id",
        tag="Trust",
        feature="trust-accounts",
        singular="trust account",
        read_roles=BILLING,
        write_roles=ADMIN,
        read_scope="trust:read",
        write_scope="trust:write",
        filters=["active"],
        events={"created": "trust_account.created", "updated": "trust_account.updated"},
        include=True,
        replaceable="trust",
    )
    api.resource(
        collection="/api/v1/trust-ledger",
        schema="TrustEntry",
        write="TrustEntryWrite",
        id_param="trust_entry_id",
        tag="Trust",
        feature="trust-ledger",
        singular="trust ledger entry",
        plural="trust ledger entries",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="trust:read",
        write_scope="trust:write",
        create=False,
        update=False,
        filters=["matter_id", "client_id", "trust_account_id", "from_date", "to_date"],
        replaceable="trust",
        extra_access=WALL,
        extra_features=["trust-below-zero"],
    )
    api.add(
        "post",
        "/api/v1/trust-ledger",
        operation_id="postTrustEntry",
        summary="Post a trust ledger entry",
        description=(
            "Appends a deposit, withdrawal, transfer to operating, refund, or adjustment. "
            "The bearer scope comes from entry_type_scopes: deposit needs trust:write, and a withdrawal, transfer, refund, or adjustment needs trust:withdraw. "
            "A token that holds only trust:write is scope_denied on those four types. "
            "A withdrawal, transfer, refund, or adjustment also requires an attorney or admin. "
            "Staff may post a deposit. "
            "An adjustment amount is signed: positive credits the client and negative debits the client, with adjustment_direction set to match. "
            "An entry that leaves a negative client balance, per matter or overall, still posts. "
            "The core raises a trust flag, trust.flag_raised, only for that committed below-zero result and emails the admins and the responsible attorney. "
            "A non-negative post does not raise the flag. A rollback, a denial, and a replay emit nothing. "
            "The balance is a warning. It is never a reason to reject the post."
        ),
        tag="Trust",
        features=["trust-ledger", "trust-below-zero"],
        roles=STAFF,
        scopes=["trust:write", "trust:withdraw"],
        body="TrustEntryWrite",
        response="TrustPostResult",
        success="201",
        events=["trust.entry_posted", "trust.flag_raised"],
        replaceable="trust",
        error_codes=["forbidden", "scope_denied"],
        extra_access=TRUST_ENTRY_ROLES,
    )
    api.add(
        "post",
        "/api/v1/trust-ledger:bulk",
        operation_id="bulkTrustEntry",
        summary="Post trust ledger entries in one batch",
        description=(
            "One transaction. A failed item posts nothing. Items apply in order, and each item sees the balance left by the item before it. "
            "A below-zero result still posts and raises trust.flag_raised for that item. The batch is not rejected for the balance. "
            "A rollback, a denial, and a replay emit neither trust.entry_posted nor trust.flag_raised. "
            "Each item uses entry_type_scopes. A trust:write token is scope_denied for a withdrawal, transfer, refund, or adjustment in the batch, and nothing is posted. "
            "There is no bulk edit and no bulk delete."
        ),
        tag="Trust",
        features=["trust-ledger", "trust-below-zero", "bulk"],
        roles=STAFF,
        scopes=["trust:write", "trust:withdraw"],
        body_schema={
            "type": "object",
            "additionalProperties": False,
            "required": ["items"],
            "properties": {"items": {"type": "array", "minItems": 1, "items": {"$ref": "#/components/schemas/TrustEntryWrite"}}},
        },
        response_schema=_batch_result("TrustPostResult"),
        success="201",
        events=["trust.entry_posted", "trust.flag_raised"],
        replaceable="trust",
        error_codes=["batch_rejected", "scope_denied"],
        extra_access=TRUST_ENTRY_ROLES,
    )
    api.add(
        "post",
        "/api/v1/trust-ledger/{trust_entry_id}/reversals",
        operation_id="reverseTrustEntry",
        summary="Reverse a trust ledger entry",
        description=(
            "Appends a reversal entry. The original row stays. Update and delete are not operations on this ledger. "
            "Reversing a deposit subtracts that amount and can leave a negative balance. "
            "The reversal still posts and raises trust.flag_raised when the committed balance is below zero. "
            "The balance does not block the reversal. A rollback, a denial, and a replay emit nothing."
        ),
        tag="Trust",
        features=["trust-ledger"],
        roles=ATTORNEY,
        scopes=["trust:withdraw"],
        body="Reason",
        response="TrustPostResult",
        success="201",
        events=["trust.entry_posted", "trust.flag_raised"],
        replaceable="trust",
        extra_access={"below_zero": "posts", "matter_wall": True, "access_inherits": "matter"},
    )
    api.add(
        "get",
        "/api/v1/trust/balances",
        operation_id="getTrustBalances",
        summary="Trust balances",
        description="Identified rows. A matter row names client_id, matter_id, and trust_account_id. A client row names client_id. An account row names trust_account_id. The three-amount snapshot on a posted entry is TrustBalances, not this list.",
        tag="Trust",
        features=["trust-ledger"],
        roles=BILLING,
        scopes=["trust:read"],
        query=["matter_id", "client_id", "trust_account_id"],
        response_schema=_page("TrustBalanceRow"),
        paginate=True,
        writes=False,
        replaceable="trust",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/trust-flags",
        schema="TrustFlag",
        write="TrustFlagResolve",
        id_param="trust_flag_id",
        tag="Trust",
        feature="trust-flags",
        singular="trust flag",
        read_roles=BILLING,
        write_roles=ATTORNEY,
        read_scope="trust:read",
        write_scope="trust:write",
        create=False,
        update=False,
        filters=["status", "matter_id", "trust_account_id", "flag_scope"],
        replaceable="trust",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/trust-flags/{trust_flag_id}/resolve",
        operation_id="resolveTrustFlag",
        summary="Resolve a trust flag",
        description="An attorney or admin resolves the flag with a note. The ledger entry stays.",
        tag="Trust",
        features=["trust-flags"],
        roles=ATTORNEY,
        scopes=["trust:write"],
        body="TrustFlagResolve",
        response="TrustFlag",
        events=["trust.flag_resolved"],
        replaceable="trust",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/trust-replenishment-requests",
        schema="TrustReplenishment",
        write="TrustReplenishmentWrite",
        id_param="replenishment_id",
        tag="Trust",
        feature="trust-replenishment",
        singular="replenishment request",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="trust:read",
        write_scope="trust:write",
        update=False,
        bulk=True,
        filters=["matter_id", "status"],
        events={"created": "trust.replenishment_requested"},
        replaceable="trust",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/trust-replenishment-requests/{replenishment_id}/cancel",
        operation_id="cancelTrustReplenishment",
        summary="Cancel a replenishment request",
        description="Sets status cancelled. The request's matter wall applies. The event is emitted only after the status commit. A denied call writes nothing and emits nothing.",
        tag="Trust",
        features=["trust-replenishment"],
        roles=STAFF,
        scopes=["trust:write"],
        response="TrustReplenishment",
        events=["trust.replenishment_cancelled"],
        replaceable="trust",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/trust-replenishment-requests/{replenishment_id}/satisfy",
        operation_id="satisfyTrustReplenishment",
        summary="Satisfy a replenishment request",
        description="Sets status satisfied. The request's matter wall applies. The event is emitted only after the status commit. A denied call writes nothing and emits nothing.",
        tag="Trust",
        features=["trust-replenishment"],
        roles=STAFF,
        scopes=["trust:write"],
        response="TrustReplenishment",
        events=["trust.replenishment_satisfied"],
        replaceable="trust",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/trust-reconciliations",
        schema="TrustReconciliation",
        write="TrustReconciliationWrite",
        id_param="reconciliation_id",
        tag="Trust",
        feature="trust-reconciliation",
        singular="reconciliation",
        read_roles=BILLING,
        write_roles=STAFF,
        read_scope="trust:read",
        write_scope="trust:write",
        update=False,
        bulk=True,
        filters=["trust_account_id"],
        events={"created": "trust.reconciled"},
        replaceable="trust",
    )


def _calendar(api: Contract) -> None:
    api.resource(
        collection="/api/v1/calendar-events",
        schema="CalendarEvent",
        write="CalendarEventWrite",
        id_param="calendar_event_id",
        tag="Calendar",
        feature="calendar",
        singular="calendar event",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="calendar:read",
        write_scope="calendar:write",
        bulk=True,
        include=True,
        filters=["matter_id", "user_id", "from_time", "to_time", "kind", "view"],
        events={"created": "calendar_event.created", "updated": "calendar_event.updated", "deleted": "calendar_event.deleted"},
        delete=True,
        replaceable="calendar",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/deadlines",
        schema="Deadline",
        write="DeadlineWrite",
        id_param="deadline_id",
        tag="Calendar",
        feature="deadlines",
        singular="deadline",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="deadlines:read",
        write_scope="deadlines:write",
        bulk=True,
        include=True,
        filters=["matter_id", "responsible_user_id", "status", "from_date", "to_date"],
        events={"created": "deadline.created", "updated": "deadline.updated"},
        replaceable="calendar",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/calendar/available-dates",
        operation_id="previewAvailableDates",
        summary="List available hearing dates",
        description="Returns days with no court date, block, or all-day event, as a pleading and as a letter. It does not create, change, or delete a calendar event.",
        tag="Calendar",
        features=["available-dates"],
        roles=STAFF,
        scopes=["calendar:read"],
        body="AvailableDatesRequest",
        response="AvailableDatesResponse",
        writes=False,
        events=[],
        replaceable="calendar",
        extra_access=WALL,
    )
    api.resource(
        collection="/api/v1/ical-feeds",
        schema="IcalFeed",
        write="IcalFeedWrite",
        id_param="feed_id",
        tag="Calendar",
        feature="ical",
        singular="iCal feed",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="calendar:read",
        write_scope="calendar:write",
        create=False,
        update=False,
        replaceable="calendar",
        extra_access=ICAL_POLICY,
    )
    api.add(
        "post",
        "/api/v1/ical-feeds",
        operation_id="createIcalFeed",
        summary="Create an iCal feed",
        description=(
            "Returns the secret URL once. A staff user can create only their own feed. "
            "A firm feed, owner_user_id null, is admin only. "
            "Another user's owner_user_id is not_found. The feed is empty when calendar.ical_enabled is false."
        ),
        tag="Calendar",
        features=["ical"],
        roles=STAFF,
        scopes=["calendar:write"],
        body="IcalFeedWrite",
        response="IcalFeedCreated",
        success="201",
        replaceable="calendar",
        extra_access=ICAL_POLICY,
    )
    api.add(
        "post",
        "/api/v1/ical-feeds/{feed_id}/revoke",
        operation_id="revokeIcalFeed",
        summary="Revoke an iCal feed",
        description="The owner or an admin revokes the feed. Another staff user receives not_found. The secret URL stops working.",
        tag="Calendar",
        features=["ical"],
        roles=STAFF,
        scopes=["calendar:write"],
        response="IcalFeed",
        replaceable="calendar",
        extra_access=ICAL_POLICY,
    )
    api.add(
        "get",
        "/ical/{secret}.ics",
        operation_id="downloadIcal",
        summary="Download an iCal feed",
        description=(
            "The secret URL is the credential. Revocation and calendar.ical_enabled false return not_found. "
            "Events on matters the feed owner cannot currently see are omitted, including matters walled after the feed was created. "
            "The secret does not publish walled events."
        ),
        tag="Calendar",
        features=["ical"],
        roles=None,
        scopes=[],
        public=True,
        response_content={"text/calendar": {"schema": {"type": "string"}}},
        writes=False,
        replaceable="calendar",
        extra_access=ICAL_POLICY,
    )


def _files(api: Contract) -> None:
    api.resource(
        collection="/api/v1/files",
        schema="File",
        write="FileRename",
        id_param="file_id",
        tag="Files",
        feature="files",
        singular="file",
        read_roles=BILLING + ["client"],
        write_roles=STAFF,
        read_scope="files:read",
        write_scope="files:write",
        create=False,
        update=False,
        include=True,
        csv=True,
        filters=["matter_id", "contact_id", "folder_id", "q", "trashed", "attorney_only", "category_id"],
        extra_access=SHARED,
    )
    api.add(
        "post",
        "/api/v1/files:bulk",
        operation_id="bulkFile",
        summary="Publish staged uploads as files in one batch",
        description=(
            "Each item names a staged upload. The batch has no bytes and no storage key. "
            "Importer modules publish through this operation. "
            "upload_id values in one batch are unique. A repeated id rejects the batch and writes nothing. "
            "Publication is one transaction: every new file and first version is created, or none are. "
            "After that transaction commits, each newly published version emits file.version_created. "
            "An upload that already has a file is an atomic replay: the original file is returned and no publication event or grant is added. "
            "A failed item returns batch_rejected, leaves every upload staged, and emits no file.created, no file.version_created, and no scan grant."
        ),
        tag="Files",
        features=["files", "file-uploads", "bulk"],
        roles=STAFF,
        scopes=["files:write"],
        body_schema={
            "type": "object",
            "additionalProperties": False,
            "required": ["items"],
            "properties": {"items": {"type": "array", "minItems": 1, "items": {"$ref": "#/components/schemas/FileBulkItem"}}},
        },
        response_schema=_batch_result("File"),
        success="201",
        events=["file.created", "file.version_created"],
        error_codes=["batch_rejected"],
        extra_access={**FILE_WALL, **version_publication("bulk", "import"), "unique_upload_ids": True},
    )
    api.add(
        "get",
        "/api/v1/files/unassigned/{upload_id}",
        operation_id="reviewUnassignedFile",
        summary="Review an unassigned ingested file",
        description=(
            "Returns one file this module published with no matter. "
            "The response matter_id is null. The module cannot supply a matter, browse another module's unassigned files, or reuse another module's upload. "
            "Doc Tender matching is a later review. This call does not guess a matter."
        ),
        tag="Files",
        features=["files", "file-uploads", "doc-tender"],
        roles=STAFF,
        scopes=["files:read"],
        response="File",
        writes=False,
        include=True,
        extra_access={
            **FILE_WALL,
            "include_scope_pattern": "views:{module_id}.{view}:read",
            "unassigned_review": True,
            "assigns_matter": False,
        },
    )
    api.add(
        "post",
        "/api/v1/files/publication-grants",
        operation_id="issueFilePublicationGrant",
        summary="Issue a background file publication grant",
        description=(
            "The core issues one pre-upload grant to an installed module that already holds files:write. "
            "The grant names that module, one matter the service principal can access, the source, the publication sequence, and an expiry. "
            "A matter outside that access, including a walled matter the principal is not on, is not_found. "
            "An admin role does not widen the matter. The grant is not an admin or firm-wide bypass. "
            "file.background_authorized delivers background_file_authorization to that module. "
            "createFileUpload through bulkFile then run with the grant and no viewer session."
        ),
        tag="Files",
        features=["files", "file-uploads"],
        roles=STAFF,
        scopes=["files:write"],
        body="BackgroundFileGrantRequest",
        response="BackgroundFileAuthorization",
        success="201",
        events=["file.background_authorized"],
        error_codes=["scope_denied", "forbidden", "not_found"],
        extra_access={
            **FILE_WALL,
            "grant_issuance": "background_file_publication",
            "authorization_category": "service_principal_issuance",
            "event_read_scopes": ["files:write"],
            "grants_admin": False,
            "firmwide_bypass": False,
            "viewer_required": False,
            "task_grant_required": False,
            "existing_grant_authorizes_issuance": False,
        },
    )
    for method, path, op, summary, body, event, desc in (
        ("post", "/api/v1/files/{file_id}/rename", "renameFile", "Rename a file", "FileRename", "file.updated", "Changes the only name. The extension stays, except when an editor save converts the file to DOCX. A person may rename a locked file. Doc Tender still cannot replace that name."),
        ("post", "/api/v1/files/{file_id}/move", "moveFile", "Move a file between folders", "FileMove", "file.updated", "Changes the folder record. Bytes stay on the same key."),
        ("post", "/api/v1/files/{file_id}/status", "markFileStatus", "Mark a file status", "FileStatusWrite", "file.updated", "Sets draft, final, executed, or filed. Received is how client and outside files arrive."),
        ("post", "/api/v1/files/{file_id}/trash", "trashFile", "Trash a file", None, "file.trashed", "The file stays listed and restorable."),
        ("post", "/api/v1/files/{file_id}/restore", "restoreFile", "Restore a file from trash", None, "file.updated", "Clears trashed_at."),
    ):
        api.add(
            method,
            path,
            operation_id=op,
            summary=summary,
            description=desc,
            tag="Files",
            features=["files"],
            roles=STAFF,
            scopes=["files:write"],
            body=body,
            response="File",
            events=[event],
            extra_access=FILE_WALL,
        )
    api.spec["paths"]["/api/v1/files/{file_id}/rename"]["post"]["x-togavsu-access"]["person_lock"] = PERSON_LOCK
    api.add(
        "patch",
        "/api/v1/files/{file_id}",
        operation_id="updateFileMetadata",
        summary="Update file metadata",
        description=(
            "Sets attorney_only, shared_with_client, category, matter, or contact without adding a version. "
            "A person with files:write may change a field a person previously locked. "
            "Doc Tender and other automated actors receive field_locked and do not replace it. "
            "Name stays on rename. Folder stays on move. "
            "The edit is audited. Staff are not_found for an attorney-only file or a file on a walled matter. "
            "Moving the file to another matter requires access to both the file's current matter and the destination. "
            "A request flag does not supply that wall state."
        ),
        tag="Files",
        features=["files"],
        roles=STAFF,
        scopes=["files:write"],
        body="FileMetadataWrite",
        response="File",
        events=["file.updated"],
        extra_access={**FILE_WALL, "person_lock": PERSON_LOCK},
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/purge",
        operation_id="purgeFile",
        summary="Empty a file from the trash",
        description="Admin only, after the caller types the file name. The audit row remains. A module foreign key blocks the purge with conflict and names the module.",
        tag="Files",
        features=["files"],
        roles=ADMIN,
        scopes=["files:write"],
        body="FilePurge",
        success="204",
        response_content={},
        events=["file.purged"],
        error_codes=["confirm_name_mismatch", "conflict"],
        extra_access=FILE_WALL,
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/versions",
        operation_id="listFileVersions",
        summary="List file versions",
        description="Immutable versions, newest number last. No version is updated or deleted.",
        tag="Files",
        features=["file-versions"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        response_schema=_page("FileVersion"),
        paginate=True,
        writes=False,
        extra_access=SHARED,
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/versions",
        operation_id="addFileVersion",
        summary="Add a file version from a finished upload",
        description=(
            "Attaches one staged upload as a new immutable version in one transaction and marks that upload published. "
            "The upload must already be status staged. This does not create another file record. "
            "A failed attach leaves the upload staged."
        ),
        tag="Files",
        features=["file-versions", "file-uploads"],
        roles=STAFF,
        scopes=["files:write"],
        body="FileBulkItem",
        response="FileVersion",
        success="201",
        events=["file.version_created"],
        extra_access={**FILE_WALL, **version_publication("new_version")},
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/history",
        operation_id="historyFile",
        summary="File edit and version history",
        description="Field edits and version saves for this file, newest first. Anyone who can read the file can read this. It is not the admin audit log. Attorney-only values are redacted. Staff are not_found when the file is attorney-only or behind a wall.",
        tag="Files",
        features=["files", "edit-history"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        response_schema=_page("FileHistoryEntry"),
        paginate=True,
        writes=False,
        extra_access=SHARED,
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/links",
        operation_id="listFileLinks",
        summary="List typed file links",
        description="conversion_of, grouped_photo_source, and attachment links. A link is omitted unless the caller can read both files. The file wall and attorney-only flag apply.",
        tag="Files",
        features=["files", "conversions"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        response_schema=_page("FileLink"),
        paginate=True,
        writes=False,
        extra_access=SHARED,
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/links",
        operation_id="createFileLink",
        summary="Link an attachment",
        description=(
            "Creates an attachment link from this file to to_file_id. "
            "Both files are authorized. If either file is behind a wall or attorney-only for the caller, the result is not_found. "
            "If both files name a matter and the matters differ, the result is matter_mismatch and no link is written. "
            "conversion_of and grouped_photo_source cannot be created or edited here. The relationship does not change after create."
        ),
        tag="Files",
        features=["files"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        body="FileLinkWrite",
        response="FileLink",
        success="201",
        events=["file.linked"],
        error_codes=["matter_mismatch", "validation_failed"],
        extra_access={
            **FILE_WALL,
            "client": "shared",
            "authorizes": "both_files",
            "relationship_immutable": True,
            "server_owned_relationships": ["conversion_of", "grouped_photo_source"],
            "access_cases": FILE_ACCESS_CASES + [
                {"from_visible": False, "to_visible": True, "result": "not_found"},
                {"from_visible": True, "to_visible": False, "result": "not_found"},
                {"from_visible": True, "to_visible": True, "same_matter": False, "result": "matter_mismatch"},
                {"from_visible": True, "to_visible": True, "same_matter": True, "relationship": "attachment", "result": "allowed"},
                {"relationship": "conversion_of", "result": "validation_failed"},
            ],
        },
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/versions/{version_id}/download",
        operation_id="downloadFileVersion",
        summary="Download a file version",
        description=(
            "Checks SHA-256 before the bytes leave the core. A mismatch fails the read and emails the admins. "
            "A quarantined version requires confirm=true. "
            "The download is written to the audit log and the case timeline. A client download is highlighted."
        ),
        tag="Files",
        features=["file-versions", "quarantine"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        query=["disposition", "confirm"],
        response_content={"application/octet-stream": {"schema": {"type": "string", "contentMediaType": "application/octet-stream"}}},
        writes=False,
        events=["file.downloaded"],
        error_codes=["quarantine_confirmation_required"],
        extra_access={**SHARED, **FILE_DOWNLOAD},
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/versions/{version_id}/restore",
        operation_id="restoreFileVersion",
        summary="Restore an older version",
        description="Copies the chosen version forward as a new version. The old version stays.",
        tag="Files",
        features=["file-versions"],
        roles=STAFF,
        scopes=["files:write"],
        response="FileVersion",
        success="201",
        events=["file.version_created"],
        extra_access={**FILE_WALL, **version_publication("copy")},
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/versions/{version_id}/quarantine",
        operation_id="quarantineFileVersion",
        summary="Set the quarantine flag",
        description=(
            "A module with files:write marks a version quarantined. The version stays visible and downloads only after confirmation. "
            "The file's matter wall applies; a walled file is not_found. "
            "A background scan uses the core-issued task authorization for this version and the quarantined field. It does not need an iframe token and it is not an admin grant."
        ),
        tag="Files",
        features=["quarantine"],
        roles=STAFF,
        scopes=["files:write"],
        body="QuarantineWrite",
        response="FileVersion",
        events=["file.quarantine_changed"],
        extra_access=FILE_WALL,
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/renditions",
        operation_id="listFileRenditions",
        summary="List renditions",
        description="Markdown copies, PDF previews, and grouped PDFs for the file.",
        tag="Files",
        features=["file-renditions"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        query=["kind"],
        response_schema=_page("FileRendition"),
        paginate=True,
        writes=False,
        extra_access=SHARED,
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/renditions/{rendition_id}",
        operation_id="downloadFileRendition",
        summary="Download a rendition",
        description=(
            "Returns the rendition bytes: the Markdown copy, the PDF preview, or the grouped PDF. "
            "The Markdown copy uses the file's base name with .md. "
            "The download is written to the audit log and the case timeline. A client download is highlighted. "
            "The file wall and the attorney-only flag apply. A rendition of a quarantined version requires confirm=true."
        ),
        tag="Files",
        features=["file-renditions"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        query=["confirm"],
        response_content={"application/octet-stream": {"schema": {"type": "string", "contentMediaType": "application/octet-stream"}}},
        writes=False,
        events=["file.downloaded"],
        error_codes=["quarantine_confirmation_required"],
        extra_access={**SHARED, **FILE_DOWNLOAD},
    )
    api.add(
        "post",
        "/api/v1/file-uploads",
        operation_id="createFileUpload",
        summary="Start a chunked upload",
        description="Returns the chunk size and the next offset. A dropped connection resumes with GET and a PUT of the next chunk.",
        tag="Files",
        features=["file-uploads"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        body="FileUploadWrite",
        response="FileUpload",
        success="201",
        error_codes=["payload_too_large"],
        extra_access={
            **WALL,
            "client": "shared",
            "access_cases": [
                {"role": "client", "matter": "own_shared", "result": "allowed"},
                {"role": "client", "matter": "other_client", "result": "not_found"},
            ],
        },
    )
    api.add(
        "get",
        "/api/v1/file-uploads/{upload_id}",
        operation_id="getFileUpload",
        summary="Resume an upload",
        description=(
            "Returns the caller's upload, including next_offset, so a dropped connection can continue. "
            "owner_user_id is the caller. Another client's upload is not_found."
        ),
        tag="Files",
        features=["file-uploads"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        response="FileUpload",
        writes=False,
        extra_access={
            "client": "own",
            "owner": "self",
            "matter_wall": True,
            "access_cases": [
                {"role": "client", "owner": "self", "result": "allowed"},
                {"role": "client", "owner": "other_client", "result": "not_found"},
            ],
        },
    )
    api.add(
        "put",
        "/api/v1/file-uploads/{upload_id}",
        operation_id="putFileChunk",
        summary="Write an upload chunk",
        description=(
            "Body is application/octet-stream. Content-Range selects the offset. Chunks are the configured size, starting at 8 MB. "
            "A resumed chunk uses the next_offset from getFileUpload. Another client's upload is not_found."
        ),
        tag="Files",
        features=["file-uploads"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        extra_parameters=[
            {
                "name": "Content-Range",
                "in": "header",
                "required": True,
                "schema": {"type": "string"},
                "description": "bytes start-end/total",
            }
        ],
        request_content={
            "application/octet-stream": {
                "schema": {"type": "string", "contentMediaType": "application/octet-stream"}
            }
        },
        response="FileUpload",
        extra_access={
            "client": "own",
            "owner": "self",
            "matter_wall": True,
            "access_cases": [
                {"role": "client", "owner": "self", "result": "allowed"},
                {"role": "client", "owner": "other_client", "result": "not_found"},
            ],
        },
    )
    api.add(
        "post",
        "/api/v1/file-uploads/{upload_id}/complete",
        operation_id="completeFileUpload",
        summary="Stage a completed upload",
        description=(
            "Checks the SHA-256 and stores the encrypted bytes with status staged. "
            "It does not create a file, a version, or a Doc Tender job. "
            "publishFile, addFileVersion, or bulkFile publishes the staged upload in a later transaction. "
            "A matching hash already in the matter is reported on that later publish, not here."
        ),
        tag="Files",
        features=["file-uploads"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        response="FileUpload",
        success="200",
        events=["file_upload.staged"],
        extra_access={
            **WALL,
            "client": "shared",
            "owner": "self",
            "access_cases": [
                {"role": "client", "owner": "self", "matter": "own_shared", "result": "allowed"},
                {"role": "client", "owner": "other_client", "result": "not_found"},
                {"role": "client", "matter": "other_client", "result": "not_found"},
            ],
        },
    )
    api.add(
        "post",
        "/api/v1/files",
        operation_id="publishFile",
        summary="Publish a staged upload as a file",
        description=(
            "Creates the file and its first version from one staged upload in one transaction, then marks the upload published. "
            "completeFileUpload must have run first. A failed publish leaves the upload staged, creates no file, and emits no file.version_created. "
            "After the transaction commits, the new first version emits file.version_created."
        ),
        tag="Files",
        features=["files", "file-uploads"],
        roles=STAFF + ["client"],
        scopes=["files:write"],
        body="FileBulkItem",
        response="File",
        success="201",
        events=["file.created", "file.version_created"],
        extra_access={
            **FILE_WALL,
            **version_publication("upload"),
            "client": "shared",
            "owner": "self",
            "access_cases": FILE_ACCESS_CASES + [
                {"role": "client", "owner": "self", "matter": "own_shared", "result": "allowed"},
                {"role": "client", "owner": "other_client", "result": "not_found"},
                {"role": "client", "matter": "other_client", "result": "not_found"},
            ],
        },
    )
    api.add(
        "post",
        "/api/v1/file-archives",
        operation_id="createFileArchive",
        summary="Download a zip of files",
        description=(
            "A folder or a selection. Each included file is written to the audit log and the case timeline as file.downloaded. "
            "A client download is highlighted. "
            "If any selected version is quarantined and confirm_quarantine is false or omitted, the response is quarantine_confirmation_required and no zip is returned. "
            "confirm_quarantine true includes those versions after the same confirmation a single download requires."
        ),
        tag="Files",
        features=["files"],
        roles=BILLING + ["client"],
        scopes=["files:read"],
        body="FileArchiveRequest",
        response_content={"application/zip": {"schema": {"type": "string", "contentMediaType": "application/zip"}}},
        events=["file.downloaded"],
        error_codes=["quarantine_confirmation_required"],
        extra_access={
            **SHARED,
            **FILE_DOWNLOAD,
            "archive_cases": [
                {"confirm_quarantine": False, "contains_quarantined": True, "result": "quarantine_confirmation_required"},
                {"confirm_quarantine": True, "contains_quarantined": True, "result": "allowed"},
            ],
        },
    )
    api.resource(
        collection="/api/v1/folders",
        schema="Folder",
        write="FolderWrite",
        id_param="folder_id",
        tag="Files",
        feature="folders",
        singular="folder",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="files:read",
        write_scope="files:write",
        filters=["matter_id"],
        events={"created": "folder.created", "updated": "folder.updated", "deleted": "folder.deleted"},
        delete=True,
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/lock",
        operation_id="acquireEditLock",
        summary="Take an edit lock",
        description="One editor at a time. A lock held by someone else returns edit_lock_held and the holder's identity.",
        tag="Editor",
        features=["edit-locks", "editor"],
        roles=STAFF,
        scopes=["files:write"],
        response="EditLock",
        error_codes=["edit_lock_held"],
        extra_access=FILE_WALL,
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/lock/renew",
        operation_id="renewEditLock",
        summary="Renew an edit lock",
        description="Keeps the lock while the editor is open. It lapses after editor.lock_timeout_seconds, starting at 5 minutes.",
        tag="Editor",
        features=["edit-locks"],
        roles=STAFF,
        scopes=["files:write"],
        response="EditLock",
        extra_access=FILE_WALL,
    )
    api.add(
        "delete",
        "/api/v1/files/{file_id}/lock",
        operation_id="releaseEditLock",
        summary="Release an edit lock",
        description="The holder can release the lock. An admin can release someone else's lock, and that release is audited.",
        tag="Editor",
        features=["edit-locks"],
        roles=STAFF,
        scopes=["files:write"],
        success="204",
        response_content={},
        events=["file.lock_released"],
        extra_access=FILE_WALL,
    )
    api.add(
        "get",
        "/api/v1/editor",
        operation_id="getEditor",
        summary="Pinned LawCraft commit",
        description="Read-only commit 770a8fab12392485c0c2789fb254da7d9296df4d. It is not a setting.",
        tag="Editor",
        features=["editor"],
        roles=FIRM,
        scopes=["files:read"],
        response="EditorInfo",
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/editor/commands",
        operation_id="listEditorCommands",
        summary="List allowed editor commands",
        description="Commands a headless run may execute. Commands that read or write the file system or the network are absent.",
        tag="Editor",
        features=["editor-runs"],
        roles=STAFF,
        scopes=["editor:run"],
        response_schema=_page("EditorCommand"),
        paginate=True,
        writes=False,
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/editor-runs",
        operation_id="runEditor",
        summary="Run editor commands",
        description="Runs the command list on the current version and saves a new version or a new file. A filesystem or network command is command_refused. The run is sandboxed and audited.",
        tag="Editor",
        features=["editor-runs"],
        roles=STAFF,
        scopes=["editor:run"],
        body="EditorRun",
        response="EditorRunResult",
        success="201",
        events=["file.version_created"],
        error_codes=["command_refused"],
        extra_access={**FILE_WALL, **version_publication("editor")},
    )
    api.add(
        "post",
        "/api/v1/files/{file_id}/conversions",
        operation_id="convertFile",
        summary="Convert a file",
        description=(
            "Creates a new file in the requested format and a conversion_of link to the source, returned together. "
            "The new file's first version emits file.version_created after the transaction commits. "
            "A failed conversion writes no file, emits no file.version_created, and issues no scan grant. "
            "Staff are not_found for an attorney-only source or a walled matter."
        ),
        tag="Editor",
        features=["conversions"],
        roles=STAFF,
        scopes=["editor:run"],
        body="Conversion",
        response="ConversionResult",
        success="201",
        events=["file.created", "file.version_created"],
        extra_access={**FILE_WALL, **version_publication("conversion")},
    )
    api.resource(
        collection="/api/v1/court-profiles",
        schema="CourtProfile",
        write="CourtProfileWrite",
        id_param="court_profile_id",
        tag="Editor",
        feature="court-profiles",
        singular="court profile",
        read_roles=STAFF,
        write_roles=STAFF,
        read_scope="files:read",
        write_scope="files:write",
        events={"created": "court_profile.created", "updated": "court_profile.updated"},
    )
    api.add(
        "post",
        "/api/v1/court-profiles/{court_profile_id}/verify",
        operation_id="verifyCourtProfile",
        summary="Verify a court profile",
        description="A person approves a profile, including one an agent created.",
        tag="Editor",
        features=["court-profiles"],
        roles=ATTORNEY,
        scopes=["files:write"],
        response="CourtProfile",
        events=["court_profile.verified"],
    )
    api.resource(
        collection="/api/v1/storage-backends",
        schema="StorageBackend",
        write="StorageBackendWrite",
        id_param="storage_backend_id",
        tag="Storage",
        feature="storage-backends",
        singular="storage backend",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="files:write",
        write_scope="files:write",
        events={"created": "storage_backend.created", "updated": "storage_backend.updated"},
    )
    api.add(
        "post",
        "/api/v1/storage-backends/{storage_backend_id}/test",
        operation_id="testStorageBackend",
        summary="Test a storage backend",
        description="Runs the adapter health check.",
        tag="Storage",
        features=["storage-backends"],
        roles=ADMIN,
        scopes=["files:write"],
        response="StorageBackend",
    )
    api.resource(
        collection="/api/v1/storage-moves",
        schema="StorageMove",
        write="StorageMoveWrite",
        id_param="storage_move_id",
        tag="Storage",
        feature="storage-moves",
        singular="storage move",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="files:read",
        write_scope="files:write",
        update=False,
        events={"created": "storage_move.started"},
    )
    storage_move = api.spec["paths"]["/api/v1/storage-moves"]["post"]
    storage_move["description"] = (
        storage_move["description"].rstrip()
        + " scope matter requires matter_id. The caller must be on that wall before any byte is copied. "
        "An off-wall admin is not_found and the move does not start. scope all copies the firm and does not name a matter."
    )
    storage_move["x-togavsu-access"].update(
        {
            "matter_wall": True,
            "admin_bypasses_wall": False,
            "authorize_before": "external_effect",
            "external_effects": ["byte_copy"],
            "matter_id": "required_when_scope_matter",
            "off_wall": "not_found",
        }
    )
    api.add(
        "post",
        "/api/v1/storage-moves/{storage_move_id}/confirm",
        operation_id="confirmStorageMove",
        summary="Confirm a storage move",
        description=(
            "Deletes the old copies only after the move has checked every SHA-256 and an admin confirms. "
            "A move whose scope is matter checks that wall again before any old copy is deleted. "
            "An off-wall admin is not_found and nothing is deleted."
        ),
        extra_access={
            "matter_wall": True,
            "admin_bypasses_wall": False,
            "authorize_before": "external_effect",
            "external_effects": ["byte_delete"],
            "off_wall": "not_found",
        },
        tag="Storage",
        features=["storage-moves"],
        roles=ADMIN,
        scopes=["files:write"],
        response="StorageMove",
        events=["storage_move.confirmed"],
    )


def _doc_tender(api: Contract) -> None:
    api.add(
        "get",
        "/api/v1/doc-tender/jobs",
        operation_id="listDocTenderJobs",
        summary="List Doc Tender jobs",
        description="One job per file version. Closed-matter files are not_processed_matter_closed.",
        tag="Doc Tender",
        features=["doc-tender"],
        roles=STAFF,
        scopes=["doc_tender:read"],
        response_schema=_page("DocTenderJob"),
        paginate=True,
        query=["matter_id", "status"],
        writes=False,
        extra_access=FILE_WALL,
    )
    api.add(
        "get",
        "/api/v1/doc-tender/jobs/{job_id}",
        operation_id="getDocTenderJob",
        summary="Get a Doc Tender job",
        description="Status, stage, attempts, and the last error. Labels and citations are getDocTenderResult.",
        tag="Doc Tender",
        features=["doc-tender"],
        roles=STAFF,
        scopes=["doc_tender:read"],
        response="DocTenderJob",
        writes=False,
        extra_access=FILE_WALL,
    )
    api.add(
        "post",
        "/api/v1/doc-tender/jobs/{job_id}/retry",
        operation_id="retryDocTenderJob",
        summary="Retry a Doc Tender job",
        description=(
            "Queues another attempt and emits doc_tender.retry_scheduled. "
            "A passing step is not redone. A person-locked name, category, matter, or grouping is left alone. "
            "doc_tender.published is emitted only by the later transaction that stores checked results. "
            "A retry that is accepted and then fails emits no doc_tender.published."
        ),
        tag="Doc Tender",
        features=["doc-tender"],
        roles=ATTORNEY,
        scopes=["doc_tender:write"],
        response="DocTenderJob",
        events=["doc_tender.retry_scheduled"],
        extra_access={
            **FILE_WALL,
            "doc_tender_retry": {
                "on_accept": "doc_tender.retry_scheduled",
                "on_later_failure": [],
                "published_event": "doc_tender.published",
                "published_on": "checked_results_transaction",
            },
        },
    )
    api.resource(
        collection="/api/v1/doc-tender/exceptions",
        schema="DocTenderException",
        write="DocTenderResolve",
        id_param="exception_id",
        tag="Doc Tender",
        feature="doc-tender",
        singular="Doc Tender exception",
        read_roles=ATTORNEY,
        write_roles=ATTORNEY,
        read_scope="doc_tender:read",
        write_scope="doc_tender:write",
        create=False,
        update=False,
        filters=["status", "kind"],
        extra_access=FILE_WALL,
    )
    api.add(
        "post",
        "/api/v1/doc-tender/exceptions/{exception_id}/resolve",
        operation_id="resolveDocTenderException",
        summary="Resolve a Doc Tender exception",
        description="Accept, correct, assign matter, retry, or keep. Correct and assign lock the fields a person set. The file's matter wall applies.",
        tag="Doc Tender",
        features=["doc-tender"],
        roles=ATTORNEY,
        scopes=["doc_tender:write"],
        body="DocTenderResolve",
        response="DocTenderException",
        events=["doc_tender.exception_resolved"],
        extra_access=FILE_WALL,
    )
    api.add(
        "get",
        "/api/v1/files/{file_id}/doc-tender-result",
        operation_id="getDocTenderResult",
        summary="Read a published Doc Tender result",
        description=(
            "The published summary, name parts, category, and critical fields. "
            "Each field has a confidence and a page or timestamp citation. "
            "The file's matter wall and attorney-only flag apply. A file with no published result is not_found."
        ),
        tag="Doc Tender",
        features=["doc-tender"],
        roles=STAFF,
        scopes=["doc_tender:read"],
        response="DocTenderResult",
        writes=False,
        extra_access=FILE_WALL,
    )
    api.resource(
        collection="/api/v1/doc-tender/categories",
        schema="DocCategory",
        write="DocCategoryWrite",
        id_param="category_id",
        tag="Doc Tender",
        feature="doc-tender",
        singular="Doc Tender category",
        plural="Doc Tender categories",
        read_roles=STAFF,
        write_roles=ADMIN,
        read_scope="doc_tender:read",
        write_scope="doc_tender:write",
        filters=["active"],
        events={"created": "doc_tender.category_updated", "updated": "doc_tender.category_updated"},
    )
    api.add(
        "get",
        "/api/v1/doc-tender/naming",
        operation_id="getDocTenderNaming",
        summary="Get the naming pattern",
        description="Reads doc_tender.naming_pattern.",
        tag="Doc Tender",
        features=["doc-tender"],
        roles=STAFF,
        scopes=["doc_tender:read"],
        response="NamingPattern",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/doc-tender/naming",
        operation_id="putDocTenderNaming",
        summary="Set the naming pattern",
        description="Writes the same setting as doc_tender.naming_pattern. A status token returns status_token_refused. The next file uses the new pattern.",
        tag="Doc Tender",
        features=["doc-tender"],
        roles=ADMIN,
        scopes=["doc_tender:write"],
        body="NamingPattern",
        response="NamingPattern",
        events=["setting.updated"],
        error_codes=["status_token_refused"],
    )


def _shell(api: Contract) -> None:
    api.add(
        "get",
        "/api/v1/search",
        operation_id="search",
        summary="Search",
        description=(
            "Staff search across matters, people, invoices, and files, plus module scopes merged by the core. "
            "q and scope are required by the UI once the minimum length is met; the API requires q of at least search.min_characters. "
            "Matter walls and attorney-only fields apply in every scope. Client users receive forbidden."
        ),
        tag="Shell",
        features=["search"],
        roles=STAFF,
        scopes=["search:use"],
        query=["q", "scope"],
        required_query=["q"],
        response="SearchResponse",
        writes=False,
        extra_access={**WALL, "client": "deny"},
    )
    api.add(
        "get",
        "/api/v1/nav/context",
        operation_id="getNavContext",
        summary="Sidebar context",
        description=(
            "Admin flag, firm short name and label, installed modules, badge counts, and the entries this user's role can see. "
            "Each badge count uses only records that viewer can read. "
            "A walled matter is excluded before the count, including for an admin who is off that wall. "
            "Attorney-only records are excluded for a role that cannot read them. "
            "Hiding a sidebar entry is not the access check."
        ),
        tag="Shell",
        features=["nav"],
        roles=FIRM,
        scopes=["profile:read"],
        response="NavContext",
        writes=False,
        extra_access={
            "matter_wall": True,
            "admin_bypasses_wall": False,
            "access_inherits": "underlying_record",
            "badge_counts": {
                "source": "viewer_authorized_records",
                "filter_before": "count",
                "off_wall": "excluded",
                "membership": "per_matter_id",
                "attorney_only": "excluded",
            },
        },
    )
    api.resource(
        collection="/api/v1/notifications",
        schema="Notification",
        write="NotificationWrite",
        id_param="notification_id",
        tag="Shell",
        feature="notifications",
        singular="notification",
        read_roles=ANY,
        write_roles=STAFF,
        read_scope="notifications:read",
        write_scope="notifications:write",
        update=False,
        filters=["read"],
        delete=True,
        events={"created": "notification.created", "deleted": "notification.deleted"},
        extra_access=NOTIFICATION_RECIPIENT,
    )
    created = api.spec["paths"]["/api/v1/notifications"]["post"]
    for key in ("owner", "recipient", "roles_bound", "filter_before", "admin_sees_others"):
        created["x-togavsu-access"].pop(key, None)
    created["x-togavsu-access"]["creation_separate"] = True
    created["x-togavsu-access"]["recipient"] = "named_on_create"
    api.add(
        "post",
        "/api/v1/notifications/{notification_id}/read",
        operation_id="markNotificationRead",
        summary="Mark a notification read",
        description="The caller can mark only their own notification.",
        tag="Shell",
        features=["notifications"],
        roles=ANY,
        scopes=["notifications:write"],
        response="Notification",
        extra_access=NOTIFICATION_RECIPIENT,
    )
    api.add(
        "post",
        "/api/v1/notifications:read-all",
        operation_id="markAllNotificationsRead",
        summary="Mark all notifications read",
        description="Clears the caller's unread count. The count is the caller's own rows, filtered before the page.",
        tag="Shell",
        features=["notifications"],
        roles=ANY,
        scopes=["notifications:write"],
        success="204",
        response_content={},
        extra_access=NOTIFICATION_RECIPIENT,
    )
    api.add(
        "get",
        "/api/v1/widgets",
        operation_id="listWidgets",
        summary="List widgets the caller can place",
        description="Core widgets the role can read, plus approved module widgets. Each item names a click-through target.",
        tag="Shell",
        features=["widgets"],
        roles=FIRM,
        scopes=["layouts:read"],
        response_schema=_page("WidgetCatalogItem"),
        paginate=True,
        writes=False,
    )
    for widget_id, roles, scope, feature in (
        ("my-time", STAFF, "time:read", "time-entries"),
        ("running-timer", STAFF, "time:read", "timers"),
        ("unbilled", BILLING, "time:read", "time-entries"),
        ("my-open-matters", STAFF, "matters:read", "matters"),
        ("upcoming-deadlines", STAFF, "deadlines:read", "deadlines"),
        ("todays-calendar", STAFF, "calendar:read", "calendar"),
        ("trust-alerts", BILLING, "trust:read", "trust-flags"),
        ("replenishment-requests", BILLING, "trust:read", "trust-replenishment"),
        ("ar-aging", BILLING, "reports:read", "reports"),
        ("recent-activity", STAFF, "events:read", "activity"),
        ("pending-conflict-checks", STAFF, "conflicts:read", "conflicts"),
        ("module-alerts", ADMIN, "modules:read", "modules"),
    ):
        api.add(
            "get",
            f"/api/v1/widgets/{widget_id}",
            operation_id="widget" + "".join(part.title() for part in widget_id.split("-")),
            summary=f"Widget {widget_id}",
            description=(
                "Returns the widget rows and the title target. The target query matches the filters that produced the rows. "
                "upcoming-deadlines uses the same date range as the deadlines page. A walled matter is absent."
            ),
            tag="Shell",
            features=["widgets", feature],
            roles=roles,
            scopes=[scope],
            query=["from_date", "to_date", "matter_id"],
            response="Widget",
            writes=False,
            extra_access=WALL,
        )
    api.add(
        "get",
        "/api/v1/me/layout",
        operation_id="getCurrentLayout",
        summary="Get the working layout",
        description="The layout on the home screen. A widget the user can no longer see is dropped when this is loaded.",
        tag="Shell",
        features=["layouts"],
        roles=FIRM,
        scopes=["layouts:read"],
        response="HomeLayout",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/me/layout",
        operation_id="putCurrentLayout",
        summary="Save the working layout",
        description="Saves show, hide, move, and resize. This is not one of the named layouts and does not count toward the cap.",
        tag="Shell",
        features=["layouts"],
        roles=FIRM,
        scopes=["layouts:write"],
        body="HomeLayoutWrite",
        response="HomeLayout",
    )
    api.resource(
        collection="/api/v1/me/layouts",
        schema="HomeLayout",
        write="HomeLayoutWrite",
        id_param="layout_id",
        tag="Shell",
        feature="layouts",
        singular="saved layout",
        read_roles=FIRM,
        write_roles=FIRM,
        read_scope="layouts:read",
        write_scope="layouts:write",
        delete=True,
        events={"created": "layout.created", "updated": "layout.updated", "deleted": "layout.deleted"},
        extra_access=OWN,
    )
    api.spec["paths"]["/api/v1/me/layouts"]["post"]["x-togavsu-error-codes"] = ["layout_limit"]
    api.spec["paths"]["/api/v1/me/layouts"]["post"]["description"] += (
        " Saving another named layout past home.saved_layouts_per_user returns layout_limit. The starting cap is 10."
    )
    api.add(
        "post",
        "/api/v1/me/layouts/{layout_id}/default",
        operation_id="setDefaultLayout",
        summary="Mark a saved layout as the default",
        description="One saved layout is the user's default.",
        tag="Shell",
        features=["layouts"],
        roles=FIRM,
        scopes=["layouts:write"],
        response="HomeLayout",
        extra_access=OWN,
    )
    api.add(
        "post",
        "/api/v1/me/layouts/{layout_id}/apply",
        operation_id="applyLayout",
        summary="Apply a saved layout",
        description="Copies the saved layout onto the working layout. Widgets the user cannot see are dropped and the rest stay.",
        tag="Shell",
        features=["layouts"],
        roles=FIRM,
        scopes=["layouts:write"],
        response="HomeLayout",
        extra_access=OWN,
    )
    api.add(
        "get",
        "/api/v1/me/layout-reset-options",
        operation_id="listLayoutResetOptions",
        summary="Layouts offered on reset",
        description="The user's default saved layout first, then their other saved layouts, then the role default. A user with no saved layouts gets the role default only.",
        tag="Shell",
        features=["layouts", "role-layouts"],
        roles=FIRM,
        scopes=["layouts:read"],
        response_schema=_page("HomeLayout"),
        paginate=True,
        writes=False,
        extra_access=OWN,
    )
    api.add(
        "get",
        "/api/v1/role-layouts/{role}",
        operation_id="getRoleLayout",
        summary="Get a role default layout",
        description="The layout the admin set for the role.",
        tag="Shell",
        features=["role-layouts"],
        roles=ADMIN,
        scopes=["layouts:read"],
        response="RoleLayout",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/role-layouts/{role}",
        operation_id="putRoleLayout",
        summary="Set a role default layout",
        description="Admin sets the default widgets for a role.",
        tag="Shell",
        features=["role-layouts"],
        roles=ADMIN,
        scopes=["layouts:write"],
        body="RoleLayout",
        response="RoleLayout",
        events=["setting.updated"],
    )
    api.resource(
        collection="/api/v1/help/sections",
        schema="HelpSection",
        write="HelpSectionWrite",
        id_param="section_id",
        tag="Help",
        feature="help",
        singular="help section",
        read_roles=FIRM,
        write_roles=ADMIN,
        read_scope="help:read",
        write_scope="help:write",
        delete=True,
        events={"created": "help.updated", "updated": "help.updated", "deleted": "help.updated"},
    )
    api.resource(
        collection="/api/v1/help/items",
        schema="HelpItem",
        write="HelpItemWrite",
        id_param="item_id",
        tag="Help",
        feature="help",
        singular="help item",
        read_roles=FIRM,
        write_roles=ADMIN,
        read_scope="help:read",
        write_scope="help:write",
        delete=True,
        filters=["section_id"],
        events={"created": "help.updated", "updated": "help.updated", "deleted": "help.updated"},
    )
    api.add(
        "get",
        "/api/v1/help/topics",
        operation_id="listHelpTopics",
        summary="List guide topics",
        description="Topics shipped in the version's guide. screen filters to the topic a ? icon opens.",
        tag="Help",
        features=["help-guide"],
        roles=FIRM,
        scopes=["help:read"],
        query=["q", "screen"],
        response_schema=_page("HelpTopic"),
        paginate=True,
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/help/guide",
        operation_id="getHelpGuide",
        summary="User guide HTML",
        description="The guide for this running version. Staff only. A client user receives not_found.",
        tag="Help",
        features=["help-guide"],
        roles=FIRM + ["client"],
        scopes=["help:read"],
        response_content={"text/html": {"schema": {"type": "string"}}, "text/markdown": {"schema": {"type": "string"}}},
        writes=False,
        extra_access=dict(MODULE_GUIDE),
    )
    api.add(
        "get",
        "/api/v1/help/guide.pdf",
        operation_id="getHelpGuidePdf",
        summary="User guide PDF",
        description="The same guide as a PDF. A client user receives not_found.",
        tag="Help",
        features=["help-guide"],
        roles=FIRM + ["client"],
        scopes=["help:read"],
        response_content={"application/pdf": {"schema": {"type": "string", "contentMediaType": "application/pdf"}}},
        writes=False,
        extra_access=dict(MODULE_GUIDE),
    )
    api.add(
        "get",
        "/help/guide",
        operation_id="getHelpGuideAlias",
        summary="User guide browser path",
        description="Browser path named by the product scope. Same rules as GET /api/v1/help/guide.",
        tag="Help",
        features=["help-guide"],
        roles=FIRM + ["client"],
        scopes=["help:read"],
        response_content={"text/html": {"schema": {"type": "string"}}},
        writes=False,
        mcp="alias",
        canonical="getHelpGuide",
        extra_access=dict(MODULE_GUIDE),
    )
    api.add(
        "get",
        "/help/guide.pdf",
        operation_id="getHelpGuidePdfAlias",
        summary="User guide PDF browser path",
        description="Browser path named by the product scope. Same rules as GET /api/v1/help/guide.pdf.",
        tag="Help",
        features=["help-guide"],
        roles=FIRM + ["client"],
        scopes=["help:read"],
        response_content={"application/pdf": {"schema": {"type": "string", "contentMediaType": "application/pdf"}}},
        writes=False,
        mcp="alias",
        canonical="getHelpGuidePdf",
        extra_access=dict(MODULE_GUIDE),
    )
    api.add(
        "get",
        "/api/v1/help/modules/{module_id}/pages",
        operation_id="listModuleHelpPages",
        summary="List a module's help pages",
        description="Pages under the module heading. Staff sign-in. A client receives not_found. The core sanitizes Markdown before render. Update replaces the module's pages, disable hides them, and uninstall removes them.",
        tag="Help",
        features=["module-help"],
        roles=FIRM,
        scopes=["help:read"],
        response_schema=_page("HelpPage"),
        paginate=True,
        writes=False,
        extra_access=dict(MODULE_GUIDE),
    )
    api.add(
        "post",
        "/api/v1/help/modules/{module_id}/pages",
        operation_id="createModuleHelpPage",
        summary="Register a module help page",
        description=(
            "An approved module token with help:write may register a page under its own heading. "
            "The path module_id must equal the token's module id. "
            "The token is not an admin and cannot write firm help or another module's heading. "
            "A human session, including an admin session, is forbidden here."
        ),
        tag="Help",
        features=["module-help"],
        roles=[],
        scopes=["help:write"],
        body="HelpPageWrite",
        response="HelpPage",
        success="201",
        extra_access=MODULE_HELP,
    )
    api.add(
        "put",
        "/api/v1/help/modules/{module_id}/pages",
        operation_id="replaceModuleHelpPages",
        summary="Replace a module's help pages",
        description="Replaces pages under this module's heading. Same module-token rule as createModuleHelpPage. A human admin session is forbidden. Disable hides the pages. Uninstall removes them.",
        tag="Help",
        features=["module-help"],
        roles=[],
        scopes=["help:write"],
        body_schema={
            "type": "object",
            "additionalProperties": False,
            "required": ["pages"],
            "properties": {"pages": {"type": "array", "items": {"$ref": "#/components/schemas/HelpPageWrite"}}},
        },
        response_schema=_batch_result("HelpPage"),
        extra_access=MODULE_HELP,
    )
    api.add(
        "delete",
        "/api/v1/help/modules/{module_id}/pages/{page_id}",
        operation_id="deleteModuleHelpPage",
        summary="Remove a module help page",
        description="Removes one page under the calling module's heading. A token for another module, and any human session, is forbidden.",
        tag="Help",
        features=["module-help"],
        roles=[],
        scopes=["help:write"],
        success="204",
        response_content={},
        extra_access=MODULE_HELP,
    )
    api.add(
        "get",
        "/api/v1/events",
        operation_id="listEvents",
        summary="Activities feed",
        description="The event stream for the firm, newest first. The default window is activity.window_days and the default page size is activity.page_size. Matter walls apply.",
        tag="Shell",
        features=["activity", "events"],
        roles=STAFF,
        scopes=["events:read"],
        query=["actor_kind", "actor_id", "matter_id", "from_time", "to_time"],
        response_schema=_page("Event"),
        paginate=True,
        writes=False,
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/events",
        operation_id="createEvent",
        summary="Append a module event",
        description="A module adds a timeline and feed event. The server sets the actor from the token.",
        tag="Shell",
        features=["events"],
        roles=STAFF,
        scopes=["events:write"],
        body="EventWrite",
        response="Event",
        success="201",
        extra_access=WALL,
    )
    api.add(
        "post",
        "/api/v1/events:bulk",
        operation_id="bulkEvent",
        summary="Append module events in one batch",
        description="One transaction. Every item's matter is authorized first. One inaccessible matter returns not_found and writes none of the batch.",
        tag="Shell",
        features=["events", "bulk"],
        roles=STAFF,
        scopes=["events:write"],
        body_schema={
            "type": "object",
            "additionalProperties": False,
            "required": ["items"],
            "properties": {"items": {"type": "array", "minItems": 1, "items": {"$ref": "#/components/schemas/EventWrite"}}},
        },
        response_schema=_batch_result("Event"),
        success="201",
        error_codes=["batch_rejected", "not_found"],
        extra_access=BATCH_WALL,
    )
    api.add(
        "get",
        "/api/v1/audit-log",
        operation_id="listAuditLog",
        summary="Audit log",
        description=(
            "Append-only. Every create, update, and delete is recorded with actor, role, and source. There is no write method on this path. "
            "Each row keeps the underlying record's read scope, matter wall, and attorney-only redaction. "
            "An admin outside a matter wall does not receive that row. Attorney-only old and new values are redacted for a caller who cannot read them."
        ),
        tag="Shell",
        features=["audit"],
        roles=ADMIN,
        scopes=["audit:read"],
        query=["entity_type", "entity_id", "actor_id", "from_time", "to_time"],
        response_schema=_page("AuditEntry"),
        paginate=True,
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/webhook-deliveries",
        operation_id="listWebhookDeliveries",
        summary="Webhook deliveries",
        description=(
            "Recent signed deliveries and their retry state. Attempts follow webhooks.retry_attempts over webhooks.retry_window_hours. "
            "Each delivery keeps the underlying record's read scope, matter wall, and summary redaction. "
            "An admin outside a matter wall does not receive that delivery."
        ),
        tag="Shell",
        features=["webhooks"],
        roles=ADMIN,
        scopes=["webhooks:read"],
        response_schema=_page("WebhookEvent"),
        paginate=True,
        writes=False,
    )


def _admin(api: Contract) -> None:
    api.add(
        "get",
        "/api/v1/settings",
        operation_id="listSettings",
        summary="List settings",
        description="Every registry setting with its tab, value, default, and help. Secret values are not returned; configured is.",
        tag="Settings",
        features=["settings"],
        roles=ADMIN,
        scopes=["settings:read"],
        response_schema=_page("Setting"),
        paginate=True,
        writes=False,
    )
    api.add(
        "get",
        "/api/v1/settings/{setting_key}",
        operation_id="getSetting",
        summary="Get one setting",
        description="One registry row.",
        tag="Settings",
        features=["settings"],
        roles=ADMIN,
        scopes=["settings:read"],
        response="Setting",
        writes=False,
    )
    api.add(
        "patch",
        "/api/v1/settings/{setting_key}",
        operation_id="updateSetting",
        summary="Update a setting",
        description="Applies on the next request. The audit log stores the old and new values. A locked setting returns setting_locked. A secret key returns use_provider_endpoint.",
        tag="Settings",
        features=["settings"],
        roles=ADMIN,
        scopes=["settings:write"],
        body="SettingUpdate",
        response="Setting",
        events=["setting.updated"],
        error_codes=["setting_locked", "use_provider_endpoint", "local_endpoint_refused", "status_token_refused"],
    )
    api.add(
        "post",
        "/api/v1/settings/{setting_key}/reset",
        operation_id="resetSetting",
        summary="Reset a setting to its default",
        description="Restores the registry default and audits the change. A locked setting is refused.",
        tag="Settings",
        features=["settings"],
        roles=ADMIN,
        scopes=["settings:write"],
        response="Setting",
        events=["setting.updated"],
        error_codes=["setting_locked"],
    )
    api.add(
        "get",
        "/api/v1/settings/appearance",
        operation_id="getAppearance",
        summary="Get appearance",
        description="The appearance settings as one object. Staff may read. Colors stay the firm's.",
        tag="Settings",
        features=["appearance"],
        roles=FIRM,
        scopes=["settings:read"],
        response="Appearance",
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/settings/appearance",
        operation_id="putAppearance",
        summary="Update appearance",
        description="Admin update. The next page load shows it. No restart.",
        tag="Settings",
        features=["appearance"],
        roles=ADMIN,
        scopes=["settings:write"],
        body="Appearance",
        response="Appearance",
        events=["setting.updated"],
    )
    api.add(
        "get",
        "/api/v1/ai/providers",
        operation_id="listAiProviders",
        summary="List AI providers",
        description="Configured flag and last four characters. Keys are not returned.",
        tag="AI",
        features=["ai"],
        roles=ADMIN,
        scopes=["ai:admin"],
        response_schema=_page("AiProvider"),
        paginate=True,
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/ai/providers/{provider}/key",
        operation_id="putAiProviderKey",
        summary="Set an AI provider key",
        description="Stores the key encrypted. The response does not echo it.",
        tag="AI",
        features=["ai"],
        roles=ADMIN,
        scopes=["ai:admin"],
        body="AiKey",
        response="AiProvider",
        events=["setting.updated"],
    )
    api.add(
        "post",
        "/api/v1/ai/providers/{provider}/check",
        operation_id="checkAiProvider",
        summary="Check an AI provider key",
        description="The Provider check button. A failed check returns ok false.",
        tag="AI",
        features=["ai"],
        roles=ADMIN,
        scopes=["ai:admin"],
        response="ProviderCheck",
    )
    api.add(
        "post",
        "/api/v1/ai/calls",
        operation_id="createAiCall",
        summary="Run an AI call",
        description=(
            "A module with ai:use asks the core to call a model. The key stays in the core. "
            "A primary failure uses the secondary when one is set, and the log lists both attempts. "
            "When matter_id is set, the caller must be on that matter wall before any provider is invoked and before the call is logged. "
            "An off-wall caller, including an admin, is not_found. The provider is not called and no log row is written. "
            "HTTP and MCP use this same check."
        ),
        tag="AI",
        features=["ai"],
        roles=STAFF,
        scopes=["ai:use"],
        body="AiCallRequest",
        response="AiCall",
        success="201",
        events=["ai.call_completed"],
        extra_access={
            "matter_wall": True,
            "admin_bypasses_wall": False,
            "matter_id": "optional",
            "authorize_before": "external_effect",
            "external_effects": ["provider_invocation", "log_write"],
            "off_wall": "not_found",
        },
    )
    api.add(
        "get",
        "/api/v1/ai/calls",
        operation_id="listAiCalls",
        summary="AI call log",
        description="Task, provider, model, tokens, fallback, and the user or module that asked.",
        tag="AI",
        features=["ai"],
        roles=ADMIN,
        scopes=["ai:admin"],
        query=["from_time", "to_time", "matter_id"],
        response_schema=_page("AiCall"),
        paginate=True,
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/mail/smtp-password",
        operation_id="putSmtpPassword",
        summary="Set the SMTP password",
        description="Write-only secret for system mail.",
        tag="Mail",
        features=["mail"],
        roles=ADMIN,
        scopes=["settings:write"],
        body="MailPassword",
        success="204",
        response_content={},
        events=["setting.updated"],
    )
    api.add(
        "post",
        "/api/v1/mail/test",
        operation_id="testMail",
        summary="Send a test system email",
        description="Sends one message with the current SMTP settings, or through the email module when that route is on.",
        tag="Mail",
        features=["mail"],
        roles=ADMIN,
        scopes=["settings:write"],
        body="MailTest",
        success="202",
        response_schema={"type": "object", "additionalProperties": False, "required": ["accepted"], "properties": {"accepted": {"type": "boolean"}}},
    )
    api.resource(
        collection="/api/v1/problem-reports",
        schema="ProblemReport",
        write="ProblemReportWrite",
        id_param="problem_report_id",
        tag="Mail",
        feature="problem-reports",
        singular="problem report",
        read_roles=ADMIN,
        write_roles=FIRM,
        read_scope="settings:read",
        write_scope="profile:write",
        update=False,
        events={"created": "problem_report.created"},
    )
    api.add(
        "get",
        "/api/v1/system/seats",
        operation_id="getSeatCount",
        summary="Seat count",
        description="Active seats so a commercial host can bill. The core does not check a license key.",
        tag="Settings",
        features=["seats"],
        roles=ADMIN,
        scopes=["settings:read"],
        response="SeatCount",
        writes=False,
    )
    api.resource(
        collection="/api/v1/backups",
        schema="Backup",
        write="RestoreWrite",
        id_param="backup_id",
        tag="Backups",
        feature="backups",
        singular="backup",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="backups:admin",
        write_scope="backups:admin",
        create=False,
        update=False,
        delete=True,
        events={"deleted": "backup.deleted"},
    )
    api.add(
        "post",
        "/api/v1/backups",
        operation_id="createBackup",
        summary="Create a backup",
        description="On-demand backup. The archive is encrypted with the passphrase from first-run setup.",
        tag="Backups",
        features=["backups"],
        roles=ADMIN,
        scopes=["backups:admin"],
        response="Backup",
        success="202",
        events=["backup.started"],
    )
    api.add(
        "get",
        "/api/v1/backups/{backup_id}/download",
        operation_id="downloadBackup",
        summary="Download a backup",
        description="The encrypted archive. Admin only.",
        tag="Backups",
        features=["backups"],
        roles=ADMIN,
        scopes=["backups:admin"],
        response_content={"application/octet-stream": {"schema": {"type": "string", "contentMediaType": "application/octet-stream"}}},
        writes=False,
    )
    api.resource(
        collection="/api/v1/backup-destinations",
        schema="BackupDestination",
        write="BackupDestinationWrite",
        id_param="destination_id",
        tag="Backups",
        feature="backup-destinations",
        singular="backup destination",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="backups:admin",
        write_scope="backups:admin",
        delete=True,
        events={"created": "backup_destination.created", "updated": "backup_destination.updated", "deleted": "backup_destination.deleted"},
    )
    api.add(
        "post",
        "/api/v1/backup-destinations/{destination_id}/test",
        operation_id="testBackupDestination",
        summary="Test a backup destination",
        description="Writes and reads a probe object. A failure is returned to the admin.",
        tag="Backups",
        features=["backup-destinations"],
        roles=ADMIN,
        scopes=["backups:admin"],
        response="BackupDestination",
    )
    api.resource(
        collection="/api/v1/restores",
        schema="Restore",
        write="RestoreWrite",
        id_param="restore_id",
        tag="Backups",
        feature="restores",
        singular="restore",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="backups:admin",
        write_scope="backups:admin",
        update=False,
        events={"created": "restore.started"},
    )
    api.spec["paths"]["/api/v1/restores"]["post"]["description"] = (
        "Starts a restore, which interrupts users. "
        "The body requires passphrase and one source: backup_id for a stored backup, or upload_token from createBackupUpload. "
        "upload_token is how an archive the admin downloaded is restored. A body with neither source is validation_failed."
    )
    api.spec["paths"]["/api/v1/restores"]["post"]["x-togavsu-error-codes"] = ["validation_failed"]
    api.add(
        "post",
        "/api/v1/backup-uploads",
        operation_id="createBackupUpload",
        summary="Upload a backup archive",
        description=(
            "Accepts the encrypted archive from downloadBackup, or another backup file the admin holds. "
            "Returns upload_token. This does not start a restore. createRestore consumes the token and the passphrase."
        ),
        tag="Backups",
        features=["backups", "restores"],
        roles=ADMIN,
        scopes=["backups:admin"],
        request_content={
            "application/octet-stream": {
                "schema": {"type": "string", "contentMediaType": "application/octet-stream"}
            }
        },
        response="BackupUpload",
        success="201",
    )
    api.add(
        "get",
        "/api/v1/updates",
        operation_id="listUpdates",
        summary="Update history",
        description="Core update attempts, including aborts that kept the running version.",
        tag="Updates",
        features=["updates"],
        roles=ADMIN,
        scopes=["updates:admin"],
        response_schema=_page("UpdateRecord"),
        paginate=True,
        writes=False,
    )
    api.add(
        "post",
        "/api/v1/updates",
        operation_id="startUpdate",
        summary="Update now",
        description="Admin start. A scheduled run stays inside the update window. A failed signature, migration, or health check aborts and keeps the running version.",
        tag="Updates",
        features=["updates"],
        roles=ADMIN,
        scopes=["updates:admin"],
        body="UpdateStart",
        response="UpdateRecord",
        success="202",
        events=["update.started"],
    )


def _modules(api: Contract) -> None:
    api.add(
        "get",
        "/api/v1/modules",
        operation_id="listModules",
        summary="List installed modules",
        description="Installed modules, their approved permissions, and whether one replaces time, trust, or calendar.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:read"],
        response_schema=_page("Module"),
        paginate=True,
        writes=False,
    )
    api.add(
        "post",
        "/api/v1/modules",
        operation_id="installModule",
        summary="Install a module",
        description=(
            "Installs from a marketplace listing (JSON body) or a local signed package (multipart field package). "
            "The signature is checked before anything runs. Unsigned and tampered packages are refused. "
            "The signed package manifest is ModuleManifest, including sidebar, matter-screen, typed help and tabs, and optional data contributions. "
            "A widget with no click-through target is refused. "
            "A widget target is the module's own page or an allowlisted core page. "
            "Copying manifest help pages into the help center remains pending. "
            "A migration that leaves the module schema, duplicates a core record, or adds a trigger, function, extension, role, or grant is refused. "
            "A changed migration that already ran is refused. "
            "The response waits for permission approval when the admin has not decided."
        ),
        tag="Modules",
        features=["modules", "marketplace"],
        roles=ADMIN,
        scopes=["modules:write"],
        request_content={
            "application/json": {"schema": {"$ref": "#/components/schemas/ModuleInstall"}},
            "multipart/form-data": {"schema": {"$ref": "#/components/schemas/ModulePackageUpload"}},
        },
        response="Module",
        success="202",
        events=["module.install_requested"],
        error_codes=["unsigned_module", "tampered_module", "validation_failed", "migration_refused", "marketplace_unconfigured"],
    )
    api.add(
        "get",
        "/api/v1/modules/{module_id}",
        operation_id="getModule",
        summary="Get an installed module",
        description="One module and its permission decisions.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:read"],
        response="Module",
        writes=False,
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/permissions",
        operation_id="decideModulePermissions",
        summary="Approve module permissions",
        description=(
            "Decisions name scopes from the signed manifest. required on this body is ignored. "
            "A declined required permission cancels the install and issues no token. "
            "Declining an optional permission installs with a token of only the approved scopes. "
            "New permissions on an update wait here while the old version keeps running. "
            "Previously approved scopes stay unless this manifest drops them or the decision explicitly declines them. "
            "Approving only a new scope does not drop the older approved scopes, and a new scope is not approved automatically."
        ),
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        body="ModulePermissions",
        response="Module",
        events=["module.permissions_decided"],
        error_codes=["permission_declined"],
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/enable",
        operation_id="enableModule",
        summary="Enable a module",
        description="Enables the module without restarting the core.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        response="Module",
        events=["module.enabled"],
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/disable",
        operation_id="disableModule",
        summary="Disable a module",
        description="Disables the module and keeps its schema. Help pages hide. No restart.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        response="Module",
        events=["module.disabled"],
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/uninstall",
        operation_id="uninstallModule",
        summary="Uninstall a module",
        description="keep leaves the schema locked for a later reinstall. delete drops it only after a completed backup and the admin types the module id.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        body="ModuleUninstall",
        response="Module",
        events=["module.removed"],
        error_codes=["validation_failed"],
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/update",
        operation_id="updateModule",
        summary="Update an installed module",
        description=(
            "Selects the next marketplace version or uploads a local signed package. "
            "The core verifies the signature and applies the new migrations before the new version starts. "
            "New permissions wait for decideModulePermissions, and the old version keeps running until they are approved. "
            "A failed signature, migration, or start leaves the previous version installed."
        ),
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        request_content={
            "application/json": {"schema": {"$ref": "#/components/schemas/ModuleUpdate"}},
            "multipart/form-data": {"schema": {"$ref": "#/components/schemas/ModulePackageUpload"}},
        },
        response="ModuleUpdateResult",
        success="202",
        events=["module.update_requested"],
        error_codes=["unsigned_module", "tampered_module", "migration_refused", "permission_declined"],
        extra_access={"on_new_permissions": "awaiting_approval_keep_running", "on_failure": "keep_previous_version"},
    )
    api.add(
        "get",
        "/api/v1/modules/{module_id}/settings",
        operation_id="getModuleSettings",
        summary="Get module settings",
        description="Values for the settings schema in the module manifest.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:read"],
        response_schema={"type": "object", "additionalProperties": True},
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/modules/{module_id}/settings",
        operation_id="putModuleSettings",
        summary="Update module settings",
        description="Validates the body against the manifest settings schema and applies it without a restart.",
        tag="Modules",
        features=["modules"],
        roles=ADMIN,
        scopes=["modules:write"],
        body_schema={"type": "object", "additionalProperties": True},
        response_schema={"type": "object", "additionalProperties": True},
        events=["setting.updated"],
    )
    api.add(
        "post",
        "/api/v1/modules/{module_id}/context-tokens",
        operation_id="createModuleContextToken",
        summary="Issue an iframe context token",
        description=(
            "Short-lived signed context for the sandboxed frame: user, roles, and the matter in view. "
            "The module does not receive the session cookie. "
            "When matter_id is set, the viewer's matter wall is checked before the token is signed. "
            "A walled matter the viewer cannot see returns not_found and no token. "
            "A client can obtain a token only for their own shared matter. "
            "The token then allows the upload sequence, including resume through getFileUpload, and every published client read: "
            "shared files, file downloads, and the client's own invoices. "
            "Other clients, unshared files, walled matters, and attorney-only files stay not_found. "
            "Approved module scopes still intersect the viewer's role, walls, ownership, sharing, and field restrictions. "
            "The token's roles are the caller's roles. A client cannot name another client or request a firm role."
        ),
        tag="Modules",
        features=["modules"],
        roles=FIRM + ["client"],
        scopes=["modules:read"],
        body="ContextTokenRequest",
        response="ContextToken",
        success="201",
        extra_access={
            **WALL,
            "client": "own_matter",
            "client_context": {
                "own_matters_only": True,
                "impersonation": "forbidden",
                "firm_roles_unless_held": False,
                "client_session_authorized_by": "role",
                "modules_read_required_for": "firm_bearer",
                "scope_does_not_bypass_matter": True,
                "sequence": [
                    {"step": "obtain_context", "operation": "createModuleContextToken"},
                    {"step": "init_upload", "operation": "createFileUpload"},
                    {"step": "chunk", "operation": "putFileChunk"},
                    {"step": "resume_status", "operation": "getFileUpload"},
                    {"step": "resumed_chunk", "operation": "putFileChunk"},
                    {"step": "complete", "operation": "completeFileUpload"},
                    {"step": "publish", "operation": "publishFile"},
                    {"step": "attachment", "operation": "createFileLink"},
                ],
                "access_cases": [
                    {"role": "client", "matter": "own_shared", "modules_read": False, "result": "allowed"},
                    {"role": "client", "matter": "other_client", "result": "not_found"},
                    {"role": "client", "matter": "own_unshared", "result": "not_found"},
                    {"role": "client", "matter": "other_client", "modules_read": True, "result": "not_found"},
                    {"role": "client", "requested_roles": ["admin"], "result": "forbidden"},
                    {"role": "client", "impersonate": True, "result": "forbidden"},
                ],
            },
        },
    )
    api.add(
        "get",
        "/api/v1/marketplace/listings",
        operation_id="listMarketplaceListings",
        summary="Browse the marketplace",
        description="Proxy of the marketplace read API. Empty modules.marketplace_url returns marketplace_unconfigured. A pulled listing is absent. A flagged listing includes the operator note.",
        tag="Modules",
        features=["marketplace"],
        roles=ADMIN,
        scopes=["modules:read"],
        query=["q"],
        response_schema=_page("MarketplaceListing"),
        paginate=True,
        writes=False,
        error_codes=["marketplace_unconfigured"],
    )
    api.add(
        "get",
        "/api/v1/marketplace/listings/{listing_id}",
        operation_id="getMarketplaceListing",
        summary="Get a marketplace listing",
        description="Author, signing key, permissions, version history, price note, and the marketplace notice.",
        tag="Modules",
        features=["marketplace"],
        roles=ADMIN,
        scopes=["modules:read"],
        response="MarketplaceListing",
        writes=False,
    )
    api.resource(
        collection="/api/v1/module-alerts",
        schema="ModuleAlert",
        write="ModuleAlert",
        id_param="alert_id",
        tag="Modules",
        feature="marketplace",
        singular="module alert",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="modules:read",
        write_scope="modules:write",
        create=False,
        update=False,
    )
    api.resource(
        collection="/api/v1/trusted-signing-keys",
        schema="TrustedKey",
        write="TrustedKeyWrite",
        id_param="key_id",
        tag="Modules",
        feature="modules",
        singular="trusted signing key",
        read_roles=ADMIN,
        write_roles=ADMIN,
        read_scope="modules:read",
        write_scope="modules:write",
        delete=True,
        events={"created": "signing_key.added", "deleted": "signing_key.removed"},
    )
    api.add(
        "get",
        "/api/v1/domain-handlers",
        operation_id="listDomainHandlers",
        summary="List domain handlers",
        description="Whether time, trust, and calendar are served by the core or by a module.",
        tag="Modules",
        features=["domain-replacement"],
        roles=ADMIN,
        scopes=["modules:read"],
        response_schema=_page("DomainHandler"),
        paginate=True,
        writes=False,
    )
    api.add(
        "put",
        "/api/v1/domain-handlers/{domain}",
        operation_id="putDomainHandler",
        summary="Switch a replaceable domain",
        description=(
            "Sends that domain's API calls to the module and keeps core data readable. An empty module id restores the core. "
            "The trust below-zero rule remains part of the contract either way. "
            "Switching time does not let the time module post trust. A billing operation that moves trust still posts exactly one append-only row through the trust handler, whether trust is the core or a module."
        ),
        tag="Modules",
        features=["domain-replacement"],
        roles=ADMIN,
        scopes=["modules:write"],
        body="DomainHandlerWrite",
        response="DomainHandler",
        events=["domain.switched"],
        extra_access={
            "trust_posting": {
                "billing_transfer_posted_by": "trust_handler",
                "exactly_once": True,
                "append_only": True,
                "time_module_also_posts": False,
            }
        },
    )


def _project_client_invoices(api: Contract) -> None:
    staff_item = {"$ref": "#/components/schemas/Invoice"}
    client_item = {"$ref": "#/components/schemas/InvoiceClient"}
    mixed = {"oneOf": [staff_item, client_item]}
    page = {
        "type": "object",
        "additionalProperties": False,
        "required": ["data", "page"],
        "properties": {
            "data": {"type": "array", "items": mixed},
            "page": {"$ref": "#/components/schemas/Page"},
        },
    }
    list_op = api.spec["paths"]["/api/v1/invoices"]["get"]
    get_op = api.spec["paths"]["/api/v1/invoices/{invoice_id}"]["get"]
    list_op["responses"]["200"]["content"]["application/json"]["schema"] = page
    get_op["responses"]["200"]["content"]["application/json"]["schema"] = mixed
    for operation in (list_op, get_op):
        operation["x-togavsu-access"]["client_schema"] = "InvoiceClient"
        operation["x-togavsu-access"]["staff_schema"] = "Invoice"
        operation["x-togavsu-access"]["client_projection"] = "allowlist"
    history = api.spec["paths"]["/api/v1/invoices/{invoice_id}/history"]["get"]
    history["responses"]["200"]["content"]["application/json"]["schema"] = {
        "type": "object",
        "additionalProperties": False,
        "required": ["data", "page"],
        "properties": {
            "data": {
                "type": "array",
                "items": {
                    "oneOf": [
                        {"$ref": "#/components/schemas/AuditEntry"},
                        {"$ref": "#/components/schemas/InvoiceClientHistoryEntry"},
                    ]
                },
            },
            "page": {"$ref": "#/components/schemas/Page"},
        },
    }
    history["x-togavsu-access"]["client_schema"] = "InvoiceClientHistoryEntry"
    history["x-togavsu-access"]["staff_schema"] = "AuditEntry"
    history["x-togavsu-access"]["client_projection"] = "allowlist"


def _slug(schema: str) -> str:
    name = schema.removeprefix("Contact")
    return {
        "Address": "addresses",
        "Email": "emails",
        "Phone": "phones",
        "Tag": "tags",
    }[name]


def _page(schema: str) -> dict:
    return {
        "type": "object",
        "additionalProperties": False,
        "required": ["data", "page"],
        "properties": {
            "data": {"type": "array", "items": {"$ref": f"#/components/schemas/{schema}"}},
            "page": {"$ref": "#/components/schemas/Page"},
        },
    }


def _batch_result(schema: str) -> dict:
    return {
        "type": "object",
        "additionalProperties": False,
        "required": ["data"],
        "properties": {"data": {"type": "array", "items": {"$ref": f"#/components/schemas/{schema}"}}},
    }


def _report_description(report_id: str) -> str:
    notes = {
        "ar-aging": " AR aging omits zero-fee matters.",
        "unbilled-time": " Unbilled time and expenses.",
        "trust-balances": " Trust balances by client.",
        "trust-client-ledger": " One client's ledger.",
        "trust-account-ledger": " One trust account's ledger.",
        "reconciliations": " Reconciliation history.",
        "doc-tender-spend": " Doc Tender spend by day, matter, and file type.",
    }
    return "Report rows. format=csv returns text/csv." + notes.get(report_id, "")
