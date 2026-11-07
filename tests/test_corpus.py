"""Synthetic Doc Tender corpus, including the four legacy failure slots."""

import unittest
from pathlib import Path
import tempfile

from harness.corpus import LEGACY_IDS, PHOTO_PAGES, build_files, pdf_page_numbers, png_color_count, render_page_png, write_corpus
from harness.policy import byte_errors, contact_errors


class CorpusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest, cls.files = build_files()

    def test_four_legacy_failures_are_present(self):
        self.assertEqual(tuple(self.manifest["legacy_ids"]), LEGACY_IDS)
        found = {item["legacy_id"] for item in self.manifest["files"] if item.get("legacy_id")}
        self.assertEqual(found, set(LEGACY_IDS))

    def test_scanned_pdf_has_120_pages_and_a_page_49_resume_point(self):
        item = next(entry for entry in self.manifest["files"] if entry["path"] == "legacy/scanned-120.pdf")
        payload = self.files[item["path"]]
        self.assertEqual(pdf_page_numbers(payload), list(range(1, 121)))
        self.assertEqual(item["inject_failure_at_page"], 49)
        self.assertEqual(item["resume_at_page"], 49)
        self.assertFalse(item["text_layer"])
        self.assertNotIn(b"BT ", payload)
        self.assertTrue(item["product_ready"])

    def test_merged_then_split_expects_three_and_five_not_four_and_four(self):
        self.assertEqual(self.manifest["expected_groups"], [
            {"id": "letter", "pages": [1, 2, 3]},
            {"id": "lease", "pages": [4, 5, 6, 7, 8]},
        ])
        self.assertEqual(self.manifest["wrong_split_pages"], [4, 4])
        self.assertNotEqual([3, 5], self.manifest["wrong_split_pages"])
        photos = [item for item in self.manifest["files"] if item["path"].endswith(".png")]
        self.assertEqual(len(photos), 8)
        self.assertEqual([item["lines"] for item in PHOTO_PAGES], [item["lines"] for item in photos])
        for item in photos:
            payload = self.files[item["path"]]
            label = " ".join(item["lines"])
            self.assertTrue(payload.startswith(b"\x89PNG\r\n\x1a\n"))
            self.assertEqual(payload, render_page_png(item["lines"]))
            self.assertGreater(png_color_count(payload), 1)
            self.assertTrue(item["product_ready"])
            self.assertTrue(item["acceptance_evidence"])
            if item["group"] == "letter":
                self.assertIn("LETTER", label)
                self.assertIn("OF 3", label)
            else:
                self.assertIn("LEASE", label)
                self.assertIn("OF 5", label)
            self.assertIn(f"PAGE {item['page'] if item['page'] <= 3 else item['page'] - 3}", label)

    def test_old_doc_and_password_pdf_are_standins(self):
        old = next(item for item in self.manifest["files"] if item["legacy_id"] == "old_doc")
        password = next(item for item in self.manifest["files"] if item["legacy_id"] == "password_pdf")
        self.assertFalse(old["product_ready"])
        self.assertFalse(password["product_ready"])
        self.assertFalse(old["acceptance_evidence"])
        self.assertFalse(password["acceptance_evidence"])
        self.assertTrue(self.files[old["path"]].startswith(bytes.fromhex("D0CF11E0A1B11AE1")))
        self.assertIn("WordDocument".encode("utf-16le"), self.files[old["path"]])
        self.assertIn(b"/Encrypt", self.files[password["path"]])
        self.assertIn(b"/Filter /Standard", self.files[password["path"]])
        self.assertTrue(old["ceiling"])
        self.assertTrue(old["trigger"])
        self.assertTrue(password["ceiling"])
        self.assertTrue(password["trigger"])

    def test_damaged_audio_video_and_sources(self):
        damaged = self.files["exceptions/damaged.pdf"]
        self.assertTrue(damaged.startswith(b"%PDF-"))
        self.assertNotIn(b"%%EOF", damaged)
        self.assertTrue(self.files["audio/tone.wav"].startswith(b"RIFF"))
        self.assertIn(b"WAVE", self.files["audio/tone.wav"][:16])
        self.assertTrue(self.files["video/frames.avi"].startswith(b"RIFF"))
        self.assertIn(b"AVI ", self.files["video/frames.avi"][:16])
        for path in (
            "sources/staff-upload.txt",
            "sources/client-upload.txt",
            "sources/drop/2026-0001/dropped.txt",
            "sources/module/matter-clock.txt",
        ):
            self.assertIn(path, self.files)

    def test_corpus_bytes_are_synthetic(self):
        self.assertEqual(contact_errors(self.manifest["person"]), [])
        for path, payload in self.files.items():
            self.assertEqual(byte_errors(payload, path), [], path)

    def test_forbidden_bytes_fail_the_policy(self):
        errors = byte_errors(b"Notes about Patrick from a confidential client export", "bad")
        self.assertTrue(errors)
        self.assertTrue(contact_errors({
            "origin": "client_export",
            "full_name": "Jordan Example",
            "email": "jordan@gmail.com",
            "phone": "+15551212",
        }))

    def test_write_corpus_round_trip(self):
        with tempfile.TemporaryDirectory() as tmp:
            manifest = write_corpus(Path(tmp))
            self.assertTrue((Path(tmp) / "manifest.json").is_file())
            self.assertTrue((Path(tmp) / "legacy/scanned-120.pdf").is_file())
            self.assertEqual(manifest["origin"], "synthetic")
            self.assertFalse(manifest["acceptance_evidence"])


if __name__ == "__main__":
    unittest.main()
