"""The signed-release gate in CI (PRD 12, release-final Codex finding P1).

scripts/release/verify-release.sh is the signed-release check that Full acceptance runs. These
tests run the real script in a throwaway git repository, with fake cosign and docker, and prove
it finds the release tag from git, verifies through COSIGN_BIN, and fails or stays PENDING
otherwise. docs/release/proposed-ci/product-ci.yml is the exact Product CI the integration lead
lands for that: the live workflow plus a v* tag trigger, a full checkout with tags and a pinned
cosign. The lane may not edit .github/, so the proposed copy is checked against the live file
here: it must be the live workflow plus exactly those lines.
"""

import difflib
import os
import re
import shutil
import stat
import subprocess
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LIVE = os.path.join(ROOT, ".github", "workflows", "product-ci.yml")
PROPOSED = os.path.join(ROOT, "docs", "release", "proposed-ci", "product-ci.yml")
RELEASE = os.path.join(ROOT, "docs", "release", "proposed-ci", "release.yml")
GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.invalid",
       "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"]

# The only lines the proposed workflow adds to the live one, in order.
ADDED = [
    "    tags: ['v*']",
    "          fetch-depth: 0",
    "          fetch-tags: true",
    "      - name: Install cosign",
    "        if: steps.scope.outputs.applies == 'true'",
    "        env:",
    "          COSIGN_VERSION: v2.4.1",
    "          COSIGN_SHA256_AMD64: 8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b",
    "        run: |",
    '          curl -fsSL -o "$RUNNER_TEMP/cosign" "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-amd64"',
    '          echo "$COSIGN_SHA256_AMD64  $RUNNER_TEMP/cosign" | sha256sum -c -',
    '          chmod 755 "$RUNNER_TEMP/cosign"',
    "          COSIGN_BIN: ${{ runner.temp }}/cosign",
]


def _load(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


class ProposedProductCI(unittest.TestCase):
    # Text checks only: the QA venv has no YAML parser, and the exact-diff test below already
    # pins every line the proposal adds.

    def setUp(self):
        self.live = _load(LIVE)
        self.proposed = _load(PROPOSED)
        lines = self.proposed.splitlines()
        start = lines.index("  full-acceptance:")
        end = next((i for i in range(start + 1, len(lines)) if re.match(r"^  \S", lines[i])), len(lines))
        self.job = lines[start:end]

    def test_proposed_is_the_live_workflow_plus_only_the_release_lines(self):
        diff = list(difflib.ndiff(self.live.splitlines(), self.proposed.splitlines()))
        removed = [d[2:] for d in diff if d.startswith("- ")]
        added = [d[2:] for d in diff if d.startswith("+ ")]
        self.assertEqual(removed, [], "the proposed workflow must not drop or change a live line; refresh it from the live file")
        self.assertEqual(added, ADDED)

    def test_tag_pushes_trigger_product_ci(self):
        self.assertIn("on:\n  push:\n    branches: [main]\n    tags: ['v*']\n  pull_request:\n", self.proposed)

    def test_full_acceptance_job_can_find_and_verify_a_signed_tag(self):
        job = self.job
        self.assertIn("    name: Full acceptance", job)
        checkout = job.index("      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262")
        self.assertEqual(job[checkout + 1:checkout + 5], [
            "        with:",
            "          persist-credentials: false",
            "          fetch-depth: 0",
            "          fetch-tags: true",
        ])
        install = job.index("      - name: Install cosign")
        run = job.index("      - name: Full acceptance (all 52 criteria and every release gate)")
        self.assertLess(checkout, install)
        self.assertLess(install, run)
        self.assertEqual(job[install + 1], "        if: steps.scope.outputs.applies == 'true'")
        step = job[run:job.index("        run: sh scripts/ci/full-acceptance.sh", run) + 1]
        self.assertIn("          COSIGN_BIN: ${{ runner.temp }}/cosign", step)
        # A tag push is a push, so the scope step runs Full acceptance on it.
        self.assertIn("            push:*|pull_request:claude/togavsu-release-final-*) echo \"applies=true\" >> \"$GITHUB_OUTPUT\" ;;", job)

    def test_cosign_pin_matches_the_release_workflow(self):
        release = _load(RELEASE)
        for name in ("COSIGN_VERSION", "COSIGN_SHA256_AMD64"):
            pinned = re.search(r"^  %s: (\S+)$" % name, release, re.M).group(1)
            self.assertIn("          %s: %s" % (name, pinned), self.job)

    def test_no_new_permissions_or_secrets(self):
        self.assertIn("\npermissions:\n  contents: read\njobs:\n", self.proposed)
        for line in ADDED:
            self.assertNotIn("secrets", line)
            self.assertNotIn("permissions", line)


def _exe(path, body):
    with open(path, "w", encoding="utf-8") as f:
        f.write("#!/bin/sh\n" + body)
    os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR)


class VerifyRelease(unittest.TestCase):
    """The real verify-release.sh in a throwaway repository with fake cosign and docker."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.repo = os.path.join(self.tmp, "repo")
        os.makedirs(os.path.join(self.repo, "scripts", "release"))
        os.makedirs(os.path.join(self.repo, "deploy", "release"))
        shutil.copy(os.path.join(ROOT, "scripts", "release", "verify-release.sh"),
                    os.path.join(self.repo, "scripts", "release"))
        self.key = os.path.join(self.repo, "deploy", "release", "cosign.pub")
        with open(self.key, "w", encoding="utf-8") as f:
            f.write("-----BEGIN PUBLIC KEY-----\nsynthetic\n-----END PUBLIC KEY-----\n")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        subprocess.run(GIT + ["-C", self.repo, "add", "-A"], check=True)
        subprocess.run(GIT + ["-C", self.repo, "commit", "-qm", "synthetic"], check=True)
        self.bin = os.path.join(self.tmp, "bin")
        os.makedirs(self.bin)
        self.log = os.path.join(self.tmp, "cosign.log")
        self.cosign = os.path.join(self.bin, "fake-cosign")
        self.docker = os.path.join(self.bin, "fake-docker")
        self.fake_cosign(0)
        self.fake_docker(self.key)

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def fake_cosign(self, code):
        _exe(self.cosign, 'echo "$@" >> "%s"\nexit %d\n' % (self.log, code))

    def fake_docker(self, shipped):
        _exe(self.docker, 'cat "%s"\n' % shipped)

    def tag(self, name="v1.0.0"):
        subprocess.run(GIT + ["-C", self.repo, "tag", name], check=True)

    def run_verify(self, **extra):
        env = {k: v for k, v in os.environ.items() if not k.startswith(("TOGAVSU_RELEASE_", "COSIGN_"))}
        env.update({"COSIGN_BIN": self.cosign, "DOCKER": self.docker, "TOGAVSU_RELEASE_REGISTRY": "registry.invalid/synthetic"})
        env.update(extra)
        return subprocess.run(["sh", os.path.join(self.repo, "scripts", "release", "verify-release.sh")],
                              env=env, capture_output=True, text=True)

    def cosign_calls(self):
        if not os.path.exists(self.log):
            return []
        return _load(self.log).splitlines()

    def test_untagged_revision_is_pending_and_never_verifies(self):
        r = self.run_verify()
        self.assertEqual(r.returncode, 3, r.stderr)
        self.assertIn("PENDING signed release", r.stdout)
        self.assertEqual(self.cosign_calls(), [])

    def test_tag_from_git_verifies_both_images_through_cosign_bin(self):
        # What fetch-tags gives the CI checkout: the tag is found from git, not from the env.
        self.tag("v1.0.0")
        r = self.run_verify()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        calls = self.cosign_calls()
        self.assertEqual(len(calls), 2)
        for image, call in zip(("togavsu-core", "togavsu-supervisor"), calls):
            self.assertEqual(call, "verify --key %s --insecure-ignore-tlog=true registry.invalid/synthetic/%s:v1.0.0" % (self.key, image))
        self.assertIn("release v1.0.0 images verify", r.stdout)

    def test_an_unsigned_image_fails(self):
        self.tag()
        self.fake_cosign(1)
        r = self.run_verify()
        self.assertNotEqual(r.returncode, 0)
        self.assertNotEqual(r.returncode, 3)
        self.assertNotIn("images verify", r.stdout)

    def test_supervisor_shipping_a_different_key_fails(self):
        self.tag()
        other = os.path.join(self.tmp, "other.pub")
        with open(other, "w", encoding="utf-8") as f:
            f.write("different\n")
        self.fake_docker(other)
        r = self.run_verify()
        self.assertEqual(r.returncode, 1)
        self.assertIn("ships a different cosign.pub", r.stderr)

    def test_missing_cosign_fails_rather_than_pending(self):
        # The live Full acceptance job today: tag present but no cosign installed.
        self.tag()
        r = self.run_verify(COSIGN_BIN=os.path.join(self.bin, "absent"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("cosign is required", r.stderr)

    def test_missing_release_key_fails(self):
        self.tag()
        os.remove(self.key)
        r = self.run_verify()
        self.assertEqual(r.returncode, 1)
        self.assertIn("cosign.pub", r.stderr)
        self.assertEqual(self.cosign_calls(), [])


if __name__ == "__main__":
    unittest.main()
