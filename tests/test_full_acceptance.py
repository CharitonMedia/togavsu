"""scripts/ci/full-acceptance.sh and its subset mode (TOGAVSU_ACCEPTANCE_IDS).

Each test copies the real script and runner into a throwaway git repository with synthetic
criteria and checks, so it proves which checks ran and what the exit code was without
Docker, Postgres or the product suites.
"""

import importlib.util
import json
import os
import shutil
import subprocess
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"]


def _check(name, code=0, extra=""):
    # Every check leaves a marker so a test can see exactly which checks ran.
    return {"command": "touch ran-%s; %s exit %d" % (name, extra, code)}


EVIDENCE = {
    "about": "synthetic",
    "checks": {
        "ok-a": _check("ok-a"),
        "ok-b": _check("ok-b"),
        "bad": _check("bad", 1),
        "says-pending": _check("says-pending", 0, "echo PENDING;"),
        "gate-check": _check("gate-check", 1),
    },
    "criteria": {
        "AC-001": {"checks": ["ok-a"], "gaps": []},
        "AC-002": {"checks": ["ok-b"], "gaps": []},
        "AC-003": {"checks": ["bad"], "gaps": []},
        "AC-004": {"checks": ["says-pending"], "gaps": []},
        "AC-005": {"checks": ["ok-a"], "gaps": ["external: needs something this runner lacks"]},
    },
    "release_gates": {"signed-release": {"checks": ["gate-check"]}},
}


class FullAcceptanceSubset(unittest.TestCase):
    def setUp(self):
        self.repo = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.repo)
        for rel in ("scripts/ci/full-acceptance.sh", "scripts/release/full_acceptance.py"):
            os.makedirs(os.path.join(self.repo, os.path.dirname(rel)), exist_ok=True)
            shutil.copy(os.path.join(ROOT, rel), os.path.join(self.repo, rel))
        for rel, data in (("docs/release/acceptance-evidence.json", EVIDENCE),
                          ("docs/build/acceptance.json", [{"id": i} for i in EVIDENCE["criteria"]])):
            os.makedirs(os.path.join(self.repo, os.path.dirname(rel)), exist_ok=True)
            with open(os.path.join(self.repo, rel), "w") as f:
                json.dump(data, f)
        with open(os.path.join(self.repo, ".gitignore"), "w") as f:
            f.write("ran-*\nout/\n")
        subprocess.run(GIT + ["init", "-q"], cwd=self.repo, check=True)
        subprocess.run(GIT + ["add", "-A"], cwd=self.repo, check=True)
        subprocess.run(GIT + ["commit", "-qm", "synthetic"], cwd=self.repo, check=True)

    def run_script(self, ids=None):
        env = dict(os.environ)
        env.pop("TOGAVSU_ACCEPTANCE_IDS", None)
        if ids is not None:
            env["TOGAVSU_ACCEPTANCE_IDS"] = ids
        # A set wordcraft path keeps the script from building the core image.
        env["TOGAVSU_WORDCRAFT_CLI"] = "/nonexistent/wordcraft-cli"
        env["TOGAVSU_ACCEPTANCE_OUT"] = os.path.join(self.repo, "out")
        proc = subprocess.run(["sh", "scripts/ci/full-acceptance.sh"], cwd=self.repo, env=env,
                              capture_output=True, text=True)
        results = None
        path = os.path.join(self.repo, "out", "results.json")
        if os.path.exists(path):
            with open(path) as f:
                results = json.load(f)
        ran = sorted(n[len("ran-"):] for n in os.listdir(self.repo) if n.startswith("ran-"))
        return proc, results, ran

    def test_subset_runs_exactly_the_listed_criteria_and_passes(self):
        proc, res, ran = self.run_script(" AC-002,AC-001,AC-002 ")
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(ran, ["ok-a", "ok-b"])
        self.assertEqual(res["mode"], "subset")
        self.assertEqual(res["acceptance_ids"], ["AC-002", "AC-001"])
        self.assertEqual(res["status"], "pass")
        self.assertEqual(sorted(res["passed_acceptance_ids"]), ["AC-001", "AC-002"])
        self.assertEqual(res["pending_acceptance_ids"], [])
        self.assertEqual(sorted(res["criteria"]), ["AC-001", "AC-002"])
        # The failing release gate belongs to the full run; the subset neither runs nor decides it.
        self.assertEqual(res["release_gates"], {})
        self.assertEqual(len(res["tested_revision"]), 40)

    def test_subset_fails_when_any_listed_criterion_fails(self):
        proc, res, ran = self.run_script("AC-001,AC-003")
        self.assertEqual(proc.returncode, 1)
        self.assertEqual(ran, ["bad", "ok-a"])
        self.assertEqual(res["status"], "pending")
        self.assertEqual(res["passed_acceptance_ids"], ["AC-001"])
        self.assertEqual(res["pending_acceptance_ids"], ["AC-003"])

    def test_subset_treats_reported_pending_and_gaps_as_not_passing(self):
        for ids, pending in (("AC-004", ["AC-004"]), ("AC-005", ["AC-005"])):
            proc, res, _ = self.run_script(ids)
            self.assertEqual(proc.returncode, 1, ids)
            self.assertEqual(res["pending_acceptance_ids"], pending)
            self.assertEqual(res["passed_acceptance_ids"], [])

    def test_subset_rejects_empty_and_unknown_ids_without_running_anything(self):
        for ids in ("", " , ", "AC-001,AC-099", "ac-001"):
            proc, res, ran = self.run_script(ids)
            self.assertEqual(proc.returncode, 2, repr(ids))
            self.assertIsNone(res, repr(ids))
            self.assertEqual(ran, [], repr(ids))

    def test_unset_runs_every_criterion_and_every_gate(self):
        proc, res, ran = self.run_script(None)
        self.assertEqual(proc.returncode, 1)
        self.assertEqual(ran, ["bad", "gate-check", "ok-a", "ok-b", "says-pending"])
        self.assertEqual(res["mode"], "full")
        self.assertEqual(sorted(res["criteria"]), sorted(EVIDENCE["criteria"]))
        self.assertEqual(res["release_gates"], {"signed-release": "pending"})

    def test_subset_on_a_dirty_tree_does_not_pass(self):
        with open(os.path.join(self.repo, "docs", "build", "acceptance.json"), "a") as f:
            f.write("\n")
        proc, res, _ = self.run_script("AC-001")
        self.assertEqual(proc.returncode, 1)
        self.assertTrue(res["working_tree_dirty"])
        self.assertEqual(res["status"], "pending")


def _load_writer():
    spec = importlib.util.spec_from_file_location("write_report", os.path.join(ROOT, "scripts", "release", "write_report.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class ReleaseReport(unittest.TestCase):
    TASK = {"id": "release", "acceptance_ids": ["AC-003", "AC-013", "AC-018"],
            "phase_acceptance": {"candidate-acceptance": ["AC-003", "AC-013", "AC-018"],
                                 "signed-images-updater-load": ["AC-013"], "backup-restore": ["AC-018"]}}
    REV = "a" * 40

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.dir)
        self.writer = _load_writer()

    def write_phase(self, phase, ids, failing=(), code=None, rev=None, mode="subset"):
        res = {"mode": mode, "acceptance_ids": ids, "tested_revision": rev or self.REV, "working_tree_dirty": False,
               "status": "pending" if failing else "pass",
               "passed_acceptance_ids": [i for i in ids if i not in failing],
               "pending_acceptance_ids": [i for i in ids if i in failing],
               "criteria": {i: {"reasons": ["chk fail"] if i in failing else []} for i in ids},
               "checks": {"chk": {"command": "true", "exit_code": 0, "status": "pass", "seconds": 1.0}}}
        with open(os.path.join(self.dir, phase + ".json"), "w") as f:
            json.dump(res, f)
        with open(os.path.join(self.dir, phase + ".exit"), "w") as f:
            f.write(str(code if code is not None else (1 if failing else 0)))

    def write_all(self, **over):
        for phase, ids in self.TASK["phase_acceptance"].items():
            self.write_phase(phase, ids, **over.get(phase, {}))

    def test_passing_subset_runs_give_a_passing_report_over_the_assigned_ids(self):
        self.write_all()
        r = self.writer.build(self.TASK, self.dir)
        self.assertEqual(r["status"], "pass")
        self.assertEqual(r["exit_code"], 0)
        self.assertEqual(r["tested_revision"], self.REV)
        self.assertEqual(sorted(r["passed_acceptance_ids"]), self.TASK["acceptance_ids"])
        self.assertEqual(r["pending_acceptance_ids"], [])
        self.assertEqual([c["id"] for c in r["phase_cases"]], list(self.TASK["phase_acceptance"]))
        for c in r["phase_cases"]:
            self.assertEqual((c["status"], c["exit_code"]), ("pass", 0))
            self.assertEqual(c["acceptance_ids"], self.TASK["phase_acceptance"][c["id"]])
            self.assertTrue(c["command"].startswith("TOGAVSU_ACCEPTANCE_IDS=" + ",".join(c["acceptance_ids"]) + " sh "))

    def test_a_failing_phase_keeps_the_report_pending(self):
        self.write_all(**{"backup-restore": {"failing": ["AC-018"]}, "candidate-acceptance": {"failing": ["AC-018"]}})
        r = self.writer.build(self.TASK, self.dir)
        self.assertEqual(r["status"], "pending")
        self.assertEqual(r["exit_code"], 1)
        self.assertEqual(r["pending_acceptance_ids"], ["AC-018"])
        self.assertEqual(r["pending_reasons"], {"AC-018": ["chk fail"]})

    def test_refuses_runs_that_are_not_exact_subsets_or_mix_revisions(self):
        bad = [{"backup-restore": {"mode": "full"}},
               {"signed-images-updater-load": {"rev": "b" * 40}}]
        for over in bad:
            self.write_all(**over)
            with self.assertRaises(SystemExit, msg=repr(over)):
                self.writer.build(self.TASK, self.dir)
        self.write_all()
        self.write_phase("backup-restore", ["AC-018", "AC-040"])
        with self.assertRaises(SystemExit):
            self.writer.build(self.TASK, self.dir)


if __name__ == "__main__":
    unittest.main()
