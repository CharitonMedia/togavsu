"""Print the qa harness summary. Pass --write to refresh the matrix files."""

from __future__ import annotations

import sys

from tests.harness.catalog import build_matrix, integrity_errors, load_acceptance, load_tasks
from tests.harness.corpus import build_files
from tests.harness.evidence import summarize
from tests.harness.render import write_outputs


def main(argv: list[str]) -> int:
    if "--write" in argv:
        markdown_path, json_path = write_outputs()
        print(f"wrote {markdown_path}")
        print(f"wrote {json_path}")
    matrix = build_matrix()
    errors = integrity_errors(matrix, load_acceptance(), load_tasks())
    manifest, _files = build_files()
    report = summarize(matrix, standins=frozenset())
    print(f"task: {report['task']}")
    print(f"criteria: {report['criteria']}")
    print(f"pending: {report['pending_count']}")
    print(f"accepted: {report['accepted_count']}")
    print(f"app_complete: {str(report['app_complete']).lower()}")
    print(f"product_acceptance: {report['product_acceptance']}")
    print(f"legacy_failures: {', '.join(manifest['legacy_ids'])}")
    print(f"integrity_errors: {len(errors)}")
    for error in errors:
        print(f"integrity: {error}")
    return 1 if errors or report["app_complete"] else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
