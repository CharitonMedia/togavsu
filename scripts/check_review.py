"""Fail closed unless Codex reviewed exactly this base/head and found no blockers."""
import json
import os
import re
import sys
from pathlib import Path


def validate(report, base, head, expected_task=None):
    if expected_task is not None and report.get("task_id") != expected_task:
        raise ValueError("Review covers a different assignment")
    if not re.fullmatch(r"[0-9a-f]{40}", base) or not re.fullmatch(r"[0-9a-f]{40}", head):
        raise ValueError("Invalid expected revision")
    if report.get("reviewed_base") != base or report.get("reviewed_head") != head:
        raise ValueError("Review does not cover the current base and head")
    if report.get("verdict") != "pass":
        raise ValueError("Codex checkpoint did not pass")
    if report.get("findings") != []:
        raise ValueError("Unresolved Codex findings")
    if not isinstance(report.get("summary"), str) or not report["summary"].strip():
        raise ValueError("Missing review summary")


if __name__ == "__main__":
    report = json.loads(Path(sys.argv[1]).read_text())
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        # Text only, no executable interpolation from the model output.
        with open(summary, "a") as out:
            out.write("### Codex checkpoint\n\n" + json.dumps(report, indent=2) + "\n")
    try:
        validate(report, sys.argv[2], sys.argv[3], json.loads(Path(sys.argv[4]).read_text())["task_id"] if len(sys.argv) > 4 else None)
    except (ValueError, TypeError) as exc:
        sys.exit(str(exc))
    print("PASS: independent review covers current base/head; no findings")
