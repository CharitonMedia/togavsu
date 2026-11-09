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
print("compose static check passed")
