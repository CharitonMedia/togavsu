"""Public delivery sender contract; behavior is exercised in Go/Postgres tests."""

import json
from pathlib import Path
import unittest


class SenderContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = json.loads(
            (Path(__file__).resolve().parents[1] / "openapi/v1/openapi.json").read_text()
        )

    def test_sender_binding_is_a_required_header_without_changing_v1(self):
        webhook = self.spec["webhooks"]["moduleEvent"]["post"]
        headers = {p["name"]: p for p in webhook["parameters"]}
        for name in ("X-Togavsu-Sender", "X-Togavsu-Sender-Signature"):
            self.assertTrue(headers[name]["required"])
            self.assertEqual(headers[name]["in"], "header")
        self.assertEqual(
            headers["X-Togavsu-Signature"]["schema"]["pattern"],
            "^t=-?[0-9]+,v1=[a-f0-9]{64}$",
        )
        self.assertEqual(
            headers["X-Togavsu-Sender-Signature"]["schema"]["pattern"],
            "^t=-?[0-9]+,v2=[a-f0-9]{64}$",
        )
        self.assertRegex("phone-zoom", headers["X-Togavsu-Sender"]["schema"]["pattern"])
        self.assertNotRegex("phone.zoom", headers["X-Togavsu-Sender"]["schema"]["pattern"])
        for operation in (
            webhook,
            self.spec["paths"]["/api/v1/modules/contracts/{contract}/{operation}"]["post"],
            self.spec["paths"]["/api/v1/modules/contracts/{contract}/events"]["post"],
        ):
            for rule in (
                "v2.<unix>.<sender>.<raw body>", "module_deliveries.created_by",
                "retries keep that sender after unlinking", "VerifyWebhookSender",
            ):
                self.assertIn(rule, operation["description"])
