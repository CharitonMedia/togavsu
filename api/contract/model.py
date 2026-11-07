"""Operation builder for the Togavsu OpenAPI 3.1 proposal.

ponytail: authors the contract proposal and does not serve HTTP.
Ceiling: later handlers and the MCP endpoint generate this document from code.
Upgrade path: diff the generated document against api/openapi/v1/openapi.json.
Trigger: the core serves /api/v1/openapi.json.
"""

from __future__ import annotations

from contract.policy import BACKGROUND_FILE_PUBLICATION, MALWARE_DELIVERY, NONINTERACTIVE_OPERATIONS, PAYMENT_PROCESSOR
from contract.schemas import COMPONENTS, PARAM_SCHEMA, QUERY

PUBLIC_SECURITY: list = []
AUTH_SECURITY = [{"sessionCookie": []}, {"bearerAuth": []}]

# Scopes a module manifest may be granted for core records and AI.
# Profile, token, settings, user, backup, update, and module-admin scopes are not delegated.
DELEGABLE_SCOPES = {
    "contacts:read",
    "contacts:write",
    "matters:read",
    "matters:write",
    "notes:read",
    "notes:write",
    "files:read",
    "files:write",
    "time:read",
    "time:write",
    "expenses:read",
    "expenses:write",
    "invoices:read",
    "invoices:write",
    "invoices:approve",
    "invoices:send",
    "payments:read",
    "payments:write",
    "trust:read",
    "trust:write",
    "trust:withdraw",
    "calendar:read",
    "calendar:write",
    "deadlines:read",
    "deadlines:write",
    "conflicts:read",
    "conflicts:write",
    "conflicts:decide",
    "editor:run",
    "doc_tender:read",
    "doc_tender:write",
    "ai:use",
    "events:read",
    "events:write",
    "notifications:read",
    "notifications:write",
    "search:use",
    "reports:read",
    "help:read",
}

# Background calls use a core-issued workflow grant. The set is the union of those workflows.

MODULE_DELEGATION_POLICY = {
    "mode": "delegated_viewer",
    "context_header": "X-Togavsu-Context-Token",
    "context_required_for_actor": "module",
    "missing_context": "forbidden_for_delegated_viewer",
    "intersection": [
        "approved_scopes",
        "viewer_role",
        "matter_wall",
        "ownership",
        "client_shared",
        "attorney_only",
        "field_restrictions",
    ],
    "scope_bypasses_wall": False,
    "scope_bypasses_viewer_role": False,
    "grants_admin": False,
    "firmwide_bypass": False,
    "viewer_mismatch": "forbidden",
    "noninteractive": {
        "authorization_header": "X-Togavsu-Task-Authorization",
        "viewer_context_required": False,
        "issued_by": "core",
        "binds": ["module_id", "event_id", "resource_ids", "allowed_operations", "allowed_fields", "expires_at"],
        "missing_task_authorization": "forbidden",
        "expired_or_revoked": "forbidden",
        "revoked_module_token": "unauthenticated",
        "unrelated_resource": "not_found",
        "field_outside_capability": "field_locked",
        "grants_admin": False,
        "firmwide_bypass": False,
        "attorney_only_redaction": True,
        "malware_scan": MALWARE_DELIVERY,
        "payment_processor": PAYMENT_PROCESSOR,
        "background_file_publication": BACKGROUND_FILE_PUBLICATION,
        "operations": sorted(NONINTERACTIVE_OPERATIONS),
    },
    "cases": [
        {"operation_scope": "ai:use", "module_scopes": ["ai:use"], "viewer_role": "staff", "result": "allowed"},
        {"operation_scope": "ai:use", "module_scopes": ["files:read"], "viewer_role": "admin", "result": "scope_denied"},
        {
            "operation_scope": "matters:read",
            "module_scopes": ["matters:read"],
            "viewer_role": "staff",
            "matter_walled": True,
            "on_wall": False,
            "result": "not_found",
        },
        {
            "operation_scope": "matters:read",
            "module_scopes": ["matters:read"],
            "viewer_role": "staff",
            "matter_walled": True,
            "on_wall": True,
            "result": "allowed",
        },
        {
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "viewer_role": "client",
            "attorney_only": True,
            "result": "not_found",
        },
        {
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "viewer_role": "client",
            "attorney_only": False,
            "shared_with_client": True,
            "result": "allowed",
        },
        {
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "viewer_role": "staff",
            "operation_roles": ["admin"],
            "result": "forbidden",
        },
        {
            "operation_scope": "files:read",
            "module_scopes": ["files:read"],
            "viewer_role": "client",
            "matter_walled": True,
            "on_wall": False,
            "result": "not_found",
        },
        {
            "operation_scope": "files:read",
            "module_scopes": ["invoices:read"],
            "viewer_role": "client",
            "result": "scope_denied",
        },
        {
            "operation_scope": "invoices:read",
            "module_scopes": ["invoices:read"],
            "viewer_role": "client",
            "attorney_only": False,
            "result": "allowed",
        },
    ],
    "noninteractive_cases": [
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "token_revoked": False,
            "field": "quarantined",
            "capability": {
                "event": "file.version_created",
                "resource": "same",
                "allowed_fields": ["quarantined"],
                "expired": False,
                "revoked": False,
            },
            "result": "allowed",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": {"event": "file.version_created", "resource": "unrelated", "allowed_fields": ["quarantined"]},
            "result": "not_found",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["matters:read"],
            "field": "quarantined",
            "capability": {"resource": "same", "allowed_fields": ["quarantined"]},
            "result": "scope_denied",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": {"resource": "same", "allowed_fields": ["quarantined"], "expired": True},
            "result": "forbidden",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": {"resource": "same", "allowed_fields": ["quarantined"], "revoked": True},
            "result": "forbidden",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "token_revoked": True,
            "field": "quarantined",
            "capability": {"resource": "same", "allowed_fields": ["quarantined"]},
            "result": "unauthenticated",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "name",
            "capability": {"resource": "same", "allowed_fields": ["quarantined"]},
            "result": "field_locked",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": None,
            "result": "forbidden",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": {"resource": "same", "matter": "unrelated", "allowed_fields": ["quarantined"]},
            "result": "not_found",
        },
        {
            "operation": "quarantineFileVersion",
            "operation_scope": "files:write",
            "module_scopes": ["files:write"],
            "field": "quarantined",
            "capability": {
                "resource": "same",
                "allowed_fields": ["quarantined"],
                "attorney_only": True,
            },
            "result": "not_found",
        },
    ],
}


def _delegated_viewer(access: dict, public: bool, mcp: str) -> bool:
    if public or mcp != "required" or access.get("module_principal") or access.get("module_delegation"):
        return False
    scopes = access.get("scopes") or []
    return bool(scopes) and all(scope in DELEGABLE_SCOPES for scope in scopes)


def _delegation_mode(access: dict, public: bool, mcp: str, operation_id: str) -> str | None:
    if not _delegated_viewer(access, public, mcp):
        return None
    if operation_id in NONINTERACTIVE_OPERATIONS:
        return "delegated_viewer_or_noninteractive"
    return "delegated_viewer"

UNSAFE = {"post", "put", "patch", "delete"}


class ContractError(ValueError):
    pass


class Contract:
    def __init__(self) -> None:
        self.spec = _skeleton()
        self.operation_ids: dict[str, str] = {}

    def add(
        self,
        method: str,
        path: str,
        *,
        operation_id: str,
        summary: str,
        description: str,
        tag: str,
        features: list[str],
        roles: list[str] | None,
        scopes: list[str],
        public: bool = False,
        body: str | None = None,
        body_schema: dict | None = None,
        response: str | None = None,
        response_schema: dict | None = None,
        success: str = "200",
        query: list[str] | None = None,
        required_query: list[str] | None = None,
        paginate: bool = False,
        csv: bool = False,
        include: bool = False,
        events: list[str] | None = None,
        replaceable: str | None = None,
        writes: bool = True,
        mcp: str = "required",
        canonical: str | None = None,
        extra_access: dict | None = None,
        error_codes: list[str] | None = None,
        content_type: str = "application/json",
        request_content: dict | None = None,
        response_content: dict | None = None,
        extra_responses: dict | None = None,
        extra_parameters: list[dict] | None = None,
        domain_dispatch: dict | None = None,
    ) -> None:
        method = method.lower()
        if operation_id in self.operation_ids:
            raise ContractError(f"duplicate operationId {operation_id}")
        self.operation_ids[operation_id] = f"{method} {path}"
        if mcp == "alias" and not canonical:
            raise ContractError(f"{operation_id} alias needs a canonical operation")
        if mcp == "required" and public and roles:
            raise ContractError(f"public operation {operation_id} cannot also require roles")
        module_principal = (extra_access or {}).get("module_principal")
        if not public and mcp == "required" and not roles and not module_principal:
            raise ContractError(f"{operation_id} needs roles")

        parameters: list = []
        for name in _path_params(path):
            parameters.append(_path_parameter(name))
        required = set(required_query or [])
        names = list(query or [])
        if paginate:
            names = ["cursor", "limit", *names]
        if include:
            names.append("include")
        if csv:
            names.append("format")
        seen: set[str] = set()
        for name in names:
            if name in seen:
                continue
            seen.add(name)
            parameter = dict(QUERY[name])
            if name in required:
                parameter["required"] = True
            parameters.append(parameter)
        if method in UNSAFE and mcp != "transport":
            parameters.append({"$ref": "#/components/parameters/idempotencyKey"})
            if not public:
                parameters.append({"$ref": "#/components/parameters/csrfToken"})
        parameters.extend(extra_parameters or [])

        access = {
            "roles": roles or [],
            "scopes": scopes,
            "public": public,
            "matter_wall": bool((extra_access or {}).get("matter_wall", False)),
            "client": (extra_access or {}).get("client", "deny" if not public else "public"),
        }
        if extra_access:
            for key, value in extra_access.items():
                access[key] = value
        delegation_mode = _delegation_mode(access, public, mcp, operation_id)
        if delegation_mode:
            access["module_delegation"] = delegation_mode

        mcp_block: dict = {"parity": mcp}
        if mcp == "required":
            mcp_block["tool"] = operation_id
        elif mcp == "alias":
            mcp_block["canonical"] = canonical
        elif mcp == "transport":
            mcp_block["tool"] = None

        operation: dict = {
            "operationId": operation_id,
            "summary": summary,
            "description": description.strip(),
            "tags": [tag],
            "parameters": parameters,
            "responses": _responses(
                method,
                success,
                response,
                response_schema,
                response_content,
                content_type,
                csv,
                extra_responses,
                public,
            ),
            "x-togavsu-features": features,
            "x-togavsu-access": access,
            "x-togavsu-events": events or [],
            "x-togavsu-writes": writes and method in UNSAFE,
            "x-togavsu-mcp": mcp_block,
        }
        if public:
            operation["security"] = PUBLIC_SECURITY
        else:
            operation["security"] = AUTH_SECURITY
        if replaceable:
            operation["x-togavsu-replaceable-domain"] = replaceable
        if domain_dispatch:
            operation["x-togavsu-domain-dispatch"] = domain_dispatch
        if error_codes:
            operation["x-togavsu-error-codes"] = error_codes
        if request_content is not None:
            operation["requestBody"] = {"required": True, "content": request_content}
        elif body or body_schema is not None:
            schema = body_schema if body_schema is not None else {"$ref": f"#/components/schemas/{body}"}
            operation["requestBody"] = {
                "required": True,
                "content": {"application/json": {"schema": schema}},
            }
        _attach_mcp_transfer(operation)
        if method in UNSAFE and not operation["x-togavsu-writes"] and mcp != "transport":
            operation["x-togavsu-writes"] = False

        path_item = self.spec["paths"].setdefault(path, {})
        if method in path_item:
            raise ContractError(f"duplicate method {method} {path}")
        path_item[method] = operation
        names = {item["name"] for item in self.spec["tags"]}
        if tag not in names:
            self.spec["tags"].append({"name": tag})

    def resource(
        self,
        *,
        collection: str,
        schema: str,
        write: str,
        patch_schema: str | None = None,
        id_param: str,
        tag: str,
        feature: str,
        singular: str,
        plural: str | None = None,
        read_roles: list[str],
        write_roles: list[str],
        read_scope: str,
        write_scope: str,
        create: bool = True,
        bulk: bool = False,
        history: bool = False,
        archive: bool = False,
        update: bool = True,
        delete: bool = False,
        include: bool = False,
        csv: bool = False,
        filters: list[str] | None = None,
        events: dict[str, str] | None = None,
        replaceable: str | None = None,
        matter_wall: bool = False,
        client: str = "deny",
        accountant: str | None = None,
        extra_access: dict | None = None,
        create_success: str = "201",
        extra_features: list[str] | None = None,
    ) -> None:
        events = events or {}
        plural = plural or f"{singular}s"
        features = [feature, *(extra_features or [])]
        access = {"matter_wall": matter_wall, "client": client}
        if accountant:
            access["accountant"] = accountant
        if extra_access:
            access.update(extra_access)
        if include:
            access["include_scope_pattern"] = "views:{module_id}.{view}:read"
        item = f"{collection}/{{{id_param}}}"
        self.add(
            "get",
            collection,
            operation_id=f"list{schema}",
            summary=f"List {plural}",
            description=f"Cursor-paginated {plural} the caller is allowed to see.",
            tag=tag,
            features=features,
            roles=read_roles,
            scopes=[read_scope],
            response_schema=_page(schema),
            paginate=True,
            csv=csv,
            include=include,
            query=filters,
            replaceable=replaceable,
            writes=False,
            extra_access=access,
        )
        if create:
            self.add(
                "post",
                collection,
                operation_id=f"create{schema}",
                summary=f"Create a {singular}",
                description=f"Creates one {singular}. A repeated Idempotency-Key with the same body returns the original result.",
                tag=tag,
                features=features,
                roles=write_roles,
                scopes=[write_scope],
                body=write,
                response=schema,
                success=create_success,
                events=[events["created"]] if events.get("created") else [],
                replaceable=replaceable,
                extra_access=access,
            )
        self.add(
            "get",
            item,
            operation_id=f"get{schema}",
            summary=f"Get a {singular}",
            description=f"Returns the {singular}. A matter wall or a missing record is a not_found problem, with the same response either way.",
            tag=tag,
            features=features,
            roles=read_roles,
            scopes=[read_scope],
            response=schema,
            include=include,
            replaceable=replaceable,
            writes=False,
            extra_access=access,
        )
        if update:
            self.add(
                "patch",
                item,
                operation_id=f"update{schema}",
                summary=f"Update a {singular}",
                description=f"Patches fields the caller may write. Omitted fields stay as they are. Server-computed fields on the write schema are ignored.",
                tag=tag,
                features=features,
                roles=write_roles,
                scopes=[write_scope],
            body=patch_schema or f"{write}Patch",
            response=schema,
            events=[events["updated"]] if events.get("updated") else [],
                replaceable=replaceable,
                extra_access=access,
            )
        if delete:
            self.add(
                "delete",
                item,
                operation_id=f"delete{schema}",
                summary=f"Delete a {singular}",
                description=f"Deletes the {singular}. Core client files and append-only ledgers do not use this method.",
                tag=tag,
                features=features,
                roles=write_roles,
                scopes=[write_scope],
                success="204",
                response_content={},
                events=[events["deleted"]] if events.get("deleted") else [],
                replaceable=replaceable,
                extra_access=access,
            )
        if archive:
            self.add(
                "post",
                f"{item}/archive",
                operation_id=f"archive{schema}",
                summary=f"Archive a {singular}",
                description=f"Sets the archived flag. The record stays readable and is omitted from default lists.",
                tag=tag,
                features=features,
                roles=write_roles,
                scopes=[write_scope],
                response=schema,
                events=[events["archived"]] if events.get("archived") else [],
                replaceable=replaceable,
                extra_access=access,
            )
        if history:
            self.add(
                "get",
                f"{item}/history",
                operation_id=f"history{schema}",
                summary=f"Edit history for a {singular}",
                description="Audit rows for this record, newest first. Attorney-only values are redacted for a caller without that access.",
                tag=tag,
                features=[*features, "edit-history"],
                roles=read_roles,
                scopes=[read_scope],
                response_schema=_page("AuditEntry"),
                paginate=True,
                writes=False,
                replaceable=replaceable,
                extra_access=access,
            )
        if bulk:
            self.add(
                "post",
                f"{collection}:bulk",
                operation_id=f"bulk{schema}",
                summary=f"Create {plural} in one batch",
                description=(
                    "Validates every item, then writes the batch in one transaction. "
                    "Any validation or reconciliation failure rejects the whole batch and writes nothing. "
                    "The maximum number of items is the setting api.bulk_batch_max (starting value 500)."
                ),
                tag=tag,
                features=[*features, "bulk"],
                roles=write_roles,
                scopes=[write_scope],
                body_schema=_batch(write),
                response_schema=_batch_result(schema),
                success="201",
                events=[events["created"]] if events.get("created") else [],
                error_codes=["batch_rejected", "validation_failed"],
                replaceable=replaceable,
                extra_access=access,
            )


def _skeleton() -> dict:
    return {
        "openapi": "3.1.0",
        "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
        "info": {
            "title": "Togavsu Core API",
            "version": "1.0.0",
            "summary": "Versioned HTTP contract for every core feature of the Togavsu practice-management system.",
            "description": (
                "Proposal for /api/v1, the unauthenticated agent card at /llms.txt, and the MCP transport at /mcp. "
                "Handlers are not part of this document. Money is integer cents. Identifiers are UUIDv7. "
                "Timestamps are UTC. Lists use an opaque cursor. Errors are application/problem+json. "
                "See docs/api/conventions.md."
            ),
        },
        "servers": [{"url": "/", "description": "The firm install, same origin."}],
        "tags": [],
        "paths": {},
        "webhooks": {},
        "components": COMPONENTS,
        "x-togavsu-conventions": {
            "version_prefix": "/api/v1",
            "mcp_path": "/mcp",
            "openapi_path": "/api/v1/openapi.json",
            "pagination": "cursor",
            "error_media_type": "application/problem+json",
            "idempotency_header": "Idempotency-Key",
            "csrf_header": "X-CSRF-Token",
            "money": "integer_cents",
            "ids": "uuidv7",
            "timestamps": "utc",
            "auth": ["session_cookie", "bearer_token"],
        },
        "x-togavsu-module-delegation": MODULE_DELEGATION_POLICY,
    }


def _path_params(path: str) -> list[str]:
    names = []
    rest = path
    while "{" in rest:
        rest = rest[rest.index("{") + 1 :]
        name, rest = rest.split("}", 1)
        names.append(name)
    return names


def _path_parameter(name: str) -> dict:
    if name in PARAM_SCHEMA:
        schema = PARAM_SCHEMA[name]
    elif name.endswith("_id"):
        schema = {"$ref": "#/components/schemas/Uuid"}
    else:
        raise ContractError(f"no schema for path parameter {name}")
    return {
        "name": name,
        "in": "path",
        "required": True,
        "schema": schema,
        "description": f"Path parameter {name}.",
    }


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


def _batch(write: str) -> dict:
    return {
        "type": "object",
        "additionalProperties": False,
        "required": ["items"],
        "properties": {
            "items": {
                "type": "array",
                "minItems": 1,
                "items": {"$ref": f"#/components/schemas/{write}"},
            }
        },
    }


def _batch_result(schema: str) -> dict:
    return {
        "type": "object",
        "additionalProperties": False,
        "required": ["data"],
        "properties": {
            "data": {"type": "array", "items": {"$ref": f"#/components/schemas/{schema}"}}
        },
    }


def _responses(
    method: str,
    success: str,
    response: str | None,
    response_schema: dict | None,
    response_content: dict | None,
    content_type: str,
    csv: bool,
    extra_responses: dict | None,
    public: bool,
) -> dict:
    headers = {"X-Request-Id": {"$ref": "#/components/headers/RequestId"}}
    responses: dict = {}
    if response_content is not None and success == "204":
        responses[success] = {"description": "No content.", "headers": headers}
    else:
        content: dict = {}
        if response_content is not None:
            content = response_content
        else:
            schema = response_schema if response_schema is not None else {"$ref": f"#/components/schemas/{response}"}
            content[content_type] = {"schema": schema}
            if csv:
                content["text/csv"] = {"schema": {"type": "string"}}
        responses[success] = {
            "description": "Success.",
            "headers": headers,
            "content": content,
        }
    problem = {"$ref": "#/components/responses/Problem"}
    if not public:
        responses["401"] = {"$ref": "#/components/responses/Unauthorized"}
        responses["403"] = {"$ref": "#/components/responses/Forbidden"}
    responses["400"] = problem
    responses["404"] = {"$ref": "#/components/responses/NotFound"}
    if method in UNSAFE:
        responses["409"] = {"$ref": "#/components/responses/Conflict"}
        responses["422"] = {"$ref": "#/components/responses/Unprocessable"}
    responses["429"] = {"$ref": "#/components/responses/TooManyRequests"}
    if extra_responses:
        responses.update(extra_responses)
    return responses


def _attach_mcp_transfer(operation: dict) -> None:
    """Record how tools/call carries headers and non-JSON bodies."""
    mcp = operation["x-togavsu-mcp"]
    if mcp.get("parity") != "required":
        return
    arguments = ["path", "query"]
    transfer: dict = {
        "shape": "path_query_body",
        "path": "Object of path parameters.",
        "query": "Object of query parameters.",
    }
    for parameter in operation.get("parameters", []):
        ref = parameter.get("$ref", "")
        if ref.endswith("/idempotencyKey"):
            arguments.append("idempotency_key")
            break
    headers = [parameter["name"] for parameter in operation.get("parameters", []) if parameter.get("in") == "header"]
    if headers:
        arguments.append("headers")
        transfer["headers"] = headers
    access = operation.get("x-togavsu-access") or {}
    delegation = access.get("module_delegation")
    credentials: dict = {}
    if delegation in ("delegated_viewer", "delegated_viewer_or_noninteractive"):
        credentials["context_token"] = {
            "header": "X-Togavsu-Context-Token",
            "argument": "context_token",
            "roles_from_arguments": False,
        }
        arguments.append("context_token")
    if delegation == "delegated_viewer_or_noninteractive":
        credentials["task_authorization"] = {
            "header": "X-Togavsu-Task-Authorization",
            "argument": "task_authorization",
            "roles_from_arguments": False,
        }
        arguments.append("task_authorization")
    if credentials:
        transfer["credentials"] = credentials
        transfer["forbidden_header_overrides"] = ["Authorization", "Host"]
    content = operation.get("requestBody", {}).get("content", {})
    if "application/json" in content and "multipart/form-data" in content:
        arguments.extend(["body", "package_base64"])
        transfer["body"] = {
            "encoding": "one_of",
            "json": {"media_type": "application/json"},
            "multipart": {
                "package": {
                    "encoding": "base64",
                    "media_type": "application/octet-stream",
                    "argument": "package_base64",
                }
            },
        }
    elif "multipart/form-data" in content:
        arguments.append("package_base64")
        transfer["body"] = {
            "encoding": "multipart",
            "parts": {"package": {"encoding": "base64", "media_type": "application/octet-stream"}},
        }
    elif content:
        media = next(iter(content))
        if media == "application/json":
            arguments.append("body")
            transfer["body"] = {"encoding": "json", "media_type": media}
        else:
            arguments.append("body_base64")
            transfer["body"] = {"encoding": "base64", "media_type": media}
    result_content = {}
    for code, response in operation.get("responses", {}).items():
        if code.startswith("2") and isinstance(response, dict) and "content" in response:
            result_content = response["content"]
            break
    if not result_content:
        transfer["result"] = {"encoding": "empty"}
    elif len(result_content) == 1:
        media = next(iter(result_content))
        transfer["result"] = {"encoding": _mcp_encoding(media), "media_type": media}
    else:
        transfer["result"] = {
            "encoding": "per_media_type",
            "media_types": {media: _mcp_encoding(media) for media in result_content},
        }
    transfer["arguments"] = arguments
    mcp["transfer"] = transfer


def _mcp_encoding(media: str) -> str:
    if media in {"application/json", "application/problem+json"}:
        return "json"
    if media.startswith("text/"):
        return "text"
    return "base64"
