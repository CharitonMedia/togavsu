"""The committed matrix matches the harness. Drift fails the check."""

import unittest

from harness.catalog import build_matrix, repo_root
from harness.render import render_json, render_markdown


class MatrixFileTests(unittest.TestCase):
    def test_committed_matrix_matches_the_builder(self):
        root = repo_root()
        matrix = build_matrix(root)
        markdown = (root / "docs/build/acceptance-matrix.md").read_text()
        payload = (root / "tests/acceptance/matrix.json").read_text()
        self.assertEqual(markdown, render_markdown(matrix))
        self.assertEqual(payload, render_json(matrix))
        self.assertIn("does not accept the product", markdown)
        self.assertIn("app_complete", markdown)
        self.assertIn("AC-052", markdown)
        self.assertIn("old_doc", markdown)


if __name__ == "__main__":
    unittest.main()
