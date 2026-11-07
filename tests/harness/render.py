"""Render the acceptance matrix as markdown and JSON."""

from __future__ import annotations

import json
from pathlib import Path

from .catalog import build_matrix, repo_root


def _phase_list(phases: list[int]) -> str:
    return ", ".join(str(phase) for phase in phases) if phases else "none"


def render_markdown(matrix: list[dict]) -> str:
    lines = [
        "# Acceptance matrix",
        "",
        "Task `qa`. PRD sections 11 and 12. This map does not accept the product.",
        "",
        "Every criterion is `pending`. A pass needs product evidence for each positive case and each failure case. The harness refuses mocks, skips, oracle passes, scaffold output, and the two structural Doc Tender stand-ins. `app_complete` stays false until all 52 criteria have that evidence and a clean Docker Desktop install is recorded.",
        "",
        "Requirement sentences are copied from `docs/build/acceptance.json` at build time. Domains come from `docs/build/tasks.json`.",
        "",
        "## Summary",
        "",
        "| ID | Full phase | Recheck | Implementing domains | Cases | Status |",
        "| --- | --- | --- | --- | --- | --- |",
    ]
    for item in matrix:
        domains = ", ".join(item["implementing_domains"]) or "(release gate)"
        count = len(item["positive"]) + len(item["failure"])
        recheck = _phase_list(item["recheck_phases"])
        lines.append(
            f"| {item['id']} | {item['full_behavior_phase']} | {recheck} | {domains} | {count} | pending |"
        )
    lines.extend(["", "## Criteria", ""])
    for item in matrix:
        lines.extend([
            f"## {item['id']}",
            "",
            item["requirement"],
            "",
            f"Phases in scope: {_phase_list(item['in_scope_phases'])}.",
            f"Full behavior phase: {item['full_behavior_phase']}.",
            f"Recheck phases: {_phase_list(item['recheck_phases'])}.",
            f"Responsible domains: {', '.join(item['responsible_domains'])}.",
            f"Implementing domains: {', '.join(item['implementing_domains']) or 'none before release'}.",
            f"Pending dependencies: {', '.join(item['pending_dependencies']) or 'none'}.",
            f"Legacy Doc Tender failures: {', '.join(item['legacy_failures']) or 'none'}.",
            f"Status: {item['status']}.",
            "",
            item["note"],
            "",
            "### Positive",
            "",
        ])
        for case in item["positive"]:
            lines.extend(_case_lines(case))
        lines.extend(["### Failure", ""])
        for case in item["failure"]:
            lines.extend(_case_lines(case))
    lines.append("")
    return "\n".join(lines)


def _case_lines(case: dict) -> list[str]:
    expect = "; ".join(case["expect"])
    evidence = "; ".join(case["evidence"])
    doubles = " In-process doubles are refused." if case.get("rejects_doubles") else ""
    return [
        f"#### {case['id']} {case['title']}",
        "",
        f"Phase {case['phase']}. Kind: {case['kind']}. Status: {case['status']}. Expected exit code: {case['expected_exit_code']}.{doubles}",
        "",
        f"Arrange: {case['arrange']}",
        "",
        f"Action: {case['action']}",
        "",
        f"Expect: {expect}",
        "",
        f"Evidence: {evidence}",
        "",
    ]


def render_json(matrix: list[dict]) -> str:
    return json.dumps(matrix, indent=2) + "\n"


def write_outputs(root: Path | None = None) -> tuple[Path, Path]:
    root = root or repo_root()
    matrix = build_matrix(root)
    markdown_path = root / "docs/build/acceptance-matrix.md"
    json_path = root / "tests/acceptance/matrix.json"
    json_path.parent.mkdir(parents=True, exist_ok=True)
    markdown_path.write_text(render_markdown(matrix))
    json_path.write_text(render_json(matrix))
    return markdown_path, json_path
