#!/usr/bin/env python3
"""Static checks for the Compose project. Docker config is extra when present."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
text = (root / "compose.yaml").read_text()
required = [
    "name: togavsu",
    "  bootstrap:",
    "  postgres:",
    "  caddy:",
    "  supervisor:",
    "  core:",
    "image: postgres:17",
    "image: caddy:2-alpine",
    "/var/run/docker.sock",
    "POSTGRES_PASSWORD_FILE",
    "SUPERVISOR_TOKEN_FILE",
    "FIELD_KEY_FILE",
    "deploy/core.Dockerfile",
    "togavsu.role: core",
]
missing = [item for item in required if item not in text]
if missing:
    print("compose.yaml missing:", ", ".join(missing), file=sys.stderr)
    sys.exit(1)
published = [line.strip() for line in text.splitlines() if "5432:" in line or "8081:" in line]
if published:
    print("postgres or supervisor port published:", published, file=sys.stderr)
    sys.exit(1)


def services(text):
    """Service name -> (environment lines, volume entries), from the two-space layout."""
    out, name, block = {}, None, None
    in_services = False
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if not line.startswith(" "):
            in_services = line.startswith("services:")
            name = None
            continue
        if not in_services:
            continue
        if line.startswith("  ") and not line.startswith("   "):
            name = line.strip().rstrip(":")
            out[name] = ([], [])
            block = None
        elif line.startswith("    ") and not line.startswith("     "):
            block = line.strip().rstrip(":")
        elif name and block == "environment":
            out[name][0].append(line.strip())
        elif name and block == "volumes" and line.strip().startswith("- "):
            out[name][1].append(line.strip()[2:])
    return out


# PRD section 6, AC-006, AC-024: the serving core never sees the superuser URL. Only
# bootstrap (writes it), migrate (seals the ledgers) and the supervisor (restores) mount
# adminsecrets; the core mounts its own secrets read-only.
svc = services(text)
problems = []
for name, (env, vols) in svc.items():
    if any(v.split(":")[0] == "adminsecrets" for v in vols) and name not in ("bootstrap", "migrate", "supervisor"):
        problems.append(name + " mounts adminsecrets")
    if any("ADMIN_DATABASE_URL" in e for e in env) and name != "supervisor":
        problems.append(name + " is given ADMIN_DATABASE_URL")
core_env, core_vols = svc.get("core", ([], []))
if "appsecrets:/appsecrets:ro" not in core_vols:
    problems.append("core does not mount appsecrets read-only: %s" % core_vols)
for v in core_vols:
    if v.split(":")[0] in ("adminsecrets", "pgsecrets", "pgdata"):
        problems.append("core mounts " + v)
if "adminsecrets:/adminsecrets:ro" not in svc.get("migrate", ([], []))[1]:
    problems.append("migrate does not mount adminsecrets read-only")
sup_env = svc.get("supervisor", ([], []))[0]
# The release key ships in the supervisor image; release signatures are not in the
# public transparency log, so only that lookup is skipped by default.
if "COSIGN_KEY: ${COSIGN_KEY:-/usr/share/togavsu/cosign.pub}" not in sup_env:
    problems.append("supervisor COSIGN_KEY is not the in-image release key")
if "COSIGN_VERIFY_FLAGS: ${COSIGN_VERIFY_FLAGS:---insecure-ignore-tlog=true}" not in sup_env:
    problems.append("supervisor COSIGN_VERIFY_FLAGS does not default to --insecure-ignore-tlog=true")
dockerfile = (root / "Dockerfile").read_text()
if "COPY deploy/release/cosign.pub /usr/share/togavsu/cosign.pub" not in dockerfile:
    problems.append("the supervisor image does not ship deploy/release/cosign.pub")
if problems:
    print("compose.yaml:", "; ".join(problems), file=sys.stderr)
    sys.exit(1)
print("compose static check passed")
