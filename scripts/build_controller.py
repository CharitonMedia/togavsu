"""Togavsu control plane: cloud work only, exact-revision review, durable decisions.

Installed from a reviewed commit; never imports or executes candidate application code.
Run once from a persistent systemd timer. STOP pauses all mutations, including merges.
"""
import argparse
import base64
import datetime as dt
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile

REPO = "CharitonMedia/togavsu"
ACTIVE = {"CREATING", "RUNNING", "WAITING_FOR_BACKGROUND_WORK", "ACTIVE"}
REQUIRED = {"Setup checks", "Codex checkpoint"}
APP_ID = 15368  # GitHub Actions, verified against this repository's check runs.
LEGACY_REPAIR_HALT = "Three correction cycles exhausted; independent work may continue"
CI_OWNERSHIP = (
    "The controller and integration lead own GitHub CI monitoring, review feedback, and future dispatch. "
    "Do not create background CI subscriptions (including github:ci:branch) or CI-wait continuation events, "
    "and do not start asynchronous background waits. Read-only synchronous status checks are allowed. "
    "Return the exact pushed commit SHA, local test commands and results, and links to pending CI checks. "
    "Report pending CI honestly, then finish this run; the controller will monitor CI and dispatch any further correction. "
    "Do not start another run or subscription to continue waiting."
)
CHECK_WORKFLOWS = {"Codex checkpoint": ".github/workflows/codex-checkpoint.yml",
                   "Setup checks": ".github/workflows/setup-checks.yml"}
for _name in ("Core tests", "API contract tests", "QA harness tests", "Supervisor tests", "Backup integration", "Supervisor image", "Full acceptance"):
    CHECK_WORKFLOWS[_name] = ".github/workflows/product-ci.yml"
PROTECTED = {
    "AGENTS.md", "togavsu-core-prd.md", ".cursor/rules/togavsu.mdc",
    ".github/workflows/codex-checkpoint.yml", ".github/workflows/setup-checks.yml",
    "scripts/build_controller.py", "scripts/test_build_controller.py", "scripts/cursor_agents.py",
    "scripts/check_review.py", "scripts/check_setup.py", "scripts/test_review_gate.py",
    "docs/build/controller-plan.json", "docs/build/tasks.json", "docs/build/review.schema.json",
    "docs/build/prompts/codex-review.md",
}


class ReviewPending(Exception):
    pass


class ReviewSuperseded(Exception):
    pass


class AgentNotFound(Exception):
    """The agent endpoint itself returned 404, not its latest-run endpoint."""


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def age(timestamp):
    return (dt.datetime.fromisoformat(now()) - dt.datetime.fromisoformat(timestamp)).total_seconds()


def review_problem(findings):
    # Review identity/summary/ordering/priority are metadata, not defect progress.
    normalized = []
    for finding in findings:
        file = finding.get("file")
        location = r"(?::\d+(?::\d+)?(?:-\d+(?::\d+)?)?|#L\d+(?:-L?\d+)?)"
        path = re.sub(location + "$", "", file) if file else file
        detail = " ".join(finding.get("detail", "").split())
        if path:
            detail = re.sub(re.escape(path) + location + r"(?![\w:])", lambda _: path, detail)
        detail = re.sub(r"\blines?\s+\d+(?::\d+)?(?:\s*[-–]\s*\d+(?::\d+)?)?\b",
            "line <location>", detail, flags=re.I)
        normalized.append({"file": path, "detail": detail})
    return {"review_findings": sorted(normalized, key=lambda f: json.dumps(f, sort_keys=True))}


def problem_fingerprints(problem):
    # Keep singleton fingerprints stable, but never give A a fresh budget just
    # because the next report changes from [A, B] to [A, C].
    parts = [problem]
    if isinstance(problem, dict):
        for kind in ("review_findings", "failed_checks", "missing_checks"):
            if problem.get(kind):
                parts = [{kind: [item]} for item in problem[kind]]
                break
    return sorted({hashlib.sha256(json.dumps(part, sort_keys=True).encode()).hexdigest() for part in parts})


def unobserved_run(record, snapshot):
    # A successful POST reserves a slot until its run becomes visible. A stale
    # run, even one other than the immediate predecessor, proves nothing about it.
    if "last_dispatched_run" not in record:
        return False
    expected = record.get("last_dispatched_run")
    return not isinstance(expected, str) or not expected or snapshot.get("run_id") != expected


def atomic(path, obj):
    temp = path.with_suffix(".tmp")
    with open(temp, "w") as out:
        json.dump(obj, out, indent=2)
        out.write("\n")
        out.flush()
        os.fsync(out.fileno())
    os.replace(temp, path)
    directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def latest_checks(checks):
    result = {}
    for check in checks:
        if check.get("app", {}).get("id") != APP_ID:
            continue
        old = result.get(check["name"])
        if old is None or check["id"] > old["id"]:
            result[check["name"]] = check
    return result


def validate_review(report, context, pr, main, expected_task=None):
    if not isinstance(report, dict) or not isinstance(context, dict):
        raise ValueError("Review and context must be JSON objects")
    if expected_task is not None and (report.get("task_id") != expected_task or context.get("task_id") != expected_task):
        raise ValueError("Independent review does not cover the expected assignment")
    if context.get("body") != pr.get("body"):
        raise ReviewSuperseded("PR evidence changed after the reviewed event")
    if expected_task is not None and [label["name"] for label in pr.get("labels", [])
            if label["name"].startswith("togavsu-task:")] != ["togavsu-task:" + expected_task]:
        raise ReviewSuperseded("PR task identity changed after review")
    if context.get("head") != pr["head"]["sha"] or context.get("base") != main:
        raise ValueError("Stale review context")
    if context.get("pr") != pr["number"]:
        raise ValueError("Review belongs to another PR")
    if report.get("reviewed_head") != context["head"] or report.get("reviewed_base") != main:
        raise ValueError("Stale review verdict")
    if not isinstance(report.get("verdict"), str) or report["verdict"] not in {"pass", "fail"} or not isinstance(report.get("findings"), list):
        raise ValueError("Malformed verdict")
    if not isinstance(report.get("summary"), str) or not report["summary"].strip():
        raise ValueError("Missing review summary")
    if report["verdict"] == "pass" and report["findings"]:
        raise ValueError("Passing review has unresolved findings")


def extract_review(blob):
    if len(blob) > 10_000_000:
        raise ValueError("Oversized review artifact")
    with zipfile.ZipFile(io.BytesIO(blob)) as archive:
        results = {}
        for name in ("review-result.json", "review-context.json"):
            matches = [i for i in archive.infolist() if Path(i.filename).name == name]
            if len(matches) != 1 or matches[0].file_size > 2_000_000:
                raise ValueError("Missing, duplicate or oversized review data: " + name)
            results[name] = json.loads(archive.read(matches[0]))
    return results["review-result.json"], results["review-context.json"]


class Remote:
    """Only GitHub metadata/artifacts and Cursor's public control API are accessed."""
    def gh(self, path, method="GET", payload=None, binary=False):
        args = ["gh", "api", "--method", method, "repos/" + REPO + "/" + path]
        if payload is not None:
            args += ["--input", "-"]
        proc = subprocess.run(args, input=None if payload is None else json.dumps(payload).encode(),
                              capture_output=True, timeout=55)
        if proc.returncode:
            # gh's diagnostic contains status/path, not the authentication token.
            raise RuntimeError(proc.stderr.decode(errors="replace")[-1800:])
        return proc.stdout if binary else json.loads(proc.stdout or b"{}")

    def cursor(self, method, path, body=None):
        key = os.environ["CURSOR_API_KEY"]
        request = urllib.request.Request("https://api.cursor.com/v1" + path,
            data=None if body is None else json.dumps(body).encode(), method=method,
            headers={"Authorization": "Basic " + base64.b64encode((key + ":").encode()).decode(),
                     "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=45) as response:
            return json.load(response)

    def agent(self, record):
        try:
            agent = self.cursor("GET", "/agents/" + record["agent_id"])
        except urllib.error.HTTPError as error:
            if error.code == 404:
                raise AgentNotFound("Cursor agent endpoint returned HTTP 404") from error
            raise
        run_id = agent.get("latestRunId")
        run = self.cursor("GET", "/agents/" + record["agent_id"] + "/runs/" + run_id) if run_id else {}
        return {"status": run.get("status", agent.get("status")), "run_id": run_id,
                "git": run.get("git", {}), "updated_at": run.get("updatedAt", agent.get("updatedAt"))}

    def review(self, check):
        if not self.authentic_checkpoint(check):
            raise ValueError("Checkpoint is not the protected workflow's actual check run")
        match = re.search(r"/actions/runs/(\d+)/", check.get("details_url", ""))
        if not match:
            raise ValueError("Review check has no workflow run")
        run = match.group(1)
        provenance = self.gh("actions/runs/" + run)
        if (provenance.get("path") != ".github/workflows/codex-checkpoint.yml"
                or provenance.get("event") != "pull_request"
                or provenance.get("head_sha") != check["head_sha"]):
            raise ValueError("Check did not originate from the protected Codex workflow at this head")
        if provenance.get("status") != "completed":
            raise ReviewPending("Workflow attempt is still queued or running")
        if provenance.get("conclusion") == "cancelled":
            # A newer PR event normally owns the concurrency group. Replaying
            # this old event would cancel its replacement and restore stale evidence.
            raise ReviewSuperseded("Cancelled checkpoint; await the replacement review event")
        if provenance.get("conclusion") != check.get("conclusion"):
            raise ValueError("Check result contradicts the protected workflow result")
        artifacts = self.gh("actions/runs/" + run + "/artifacts?per_page=100")
        expected = "codex-review-" + check["head_sha"] + "-attempt-" + str(provenance["run_attempt"])
        found = [a for a in artifacts["artifacts"] if a["name"] == expected and not a["expired"]]
        if len(found) != 1:
            raise ValueError("Review artifact unavailable")
        return extract_review(self.gh("actions/artifacts/" + str(found[0]["id"]) + "/zip", binary=True))

    def checks(self, head):
        checks, page = [], 1
        while True:
            rows = self.gh("commits/" + head + "/check-runs?per_page=100&page=" + str(page))["check_runs"]
            checks.extend(rows)
            if len(rows) < 100:
                native = [self.native_check(c) for c in checks]
                return latest_checks([c for c in native if c is not None])
            page += 1

    def authentic_checkpoint(self, check):
        return check.get("name") == "Codex checkpoint" and self.native_check(check) is not None

    def native_check(self, check):
        if check.get("app", {}).get("id") != APP_ID:
            return None
        match = re.fullmatch(r"https://github.com/CharitonMedia/togavsu/actions/runs/(\d+)/job/(\d+)", check.get("details_url", ""))
        if not match:
            return None
        run_id, job_id = map(int, match.groups())
        try:
            job = self.gh("actions/jobs/" + str(job_id))
            run = self.gh("actions/runs/" + str(run_id))
        except RuntimeError as error:
            if "HTTP 404" in str(error):
                return None
            raise
        identity = (job.get("id") == job_id and job.get("run_id") == run_id
                and job.get("check_run_url") == "https://api.github.com/repos/" + REPO + "/check-runs/" + str(check["id"])
                and job.get("head_sha") == check.get("head_sha") and job.get("name") == check.get("name")
                and run.get("head_sha") == check.get("head_sha"))
        if not identity or run.get("path") != CHECK_WORKFLOWS.get(check["name"], run.get("path")):
            return None
        if check["name"] == "Codex checkpoint" and run.get("event") != "pull_request":
            return None
        if job.get("status") not in {"queued", "in_progress", "completed", "waiting", "pending", "requested"}:
            return None
        native = dict(check, status=job["status"], conclusion=job.get("conclusion"))
        if run.get("status") and run["status"] != "completed" and native["status"] == "completed":
            native.update(status="in_progress", conclusion=None)
        if run.get("status") == "completed" and run.get("conclusion") != "success" and native["conclusion"] == "success":
            native["conclusion"] = run.get("conclusion")  # Never let a synthetic success mask a failed genuine workflow.
        return native

    def changed_files(self, number):
        pr = self.gh("pulls/" + str(number))
        count = pr.get("changed_files")
        if type(count) is not int or not 0 <= count < 3000:
            raise ValueError("PR file inventory is missing or reaches GitHub's 3000-file cap")
        files, page = [], 1
        while True:
            rows = self.gh("pulls/" + str(number) + "/files?per_page=100&page=" + str(page))
            files.extend(rows)
            if len(rows) < 100:
                fresh = self.gh("pulls/" + str(number))
                if (len(files) != count or len({f["filename"] for f in files}) != count
                        or fresh.get("changed_files") != count or fresh["head"]["sha"] != pr["head"]["sha"]):
                    raise ValueError("Incomplete or changed PR file inventory; protected-file scan unavailable")
                return files
            page += 1

    def test_failures(self, checks):
        return [{"name": c["name"], "conclusion": c.get("conclusion"), "url": c.get("details_url")}
                for c in checks.values() if c["status"] == "completed" and c.get("conclusion") != "success"]


class Controller:
    def __init__(self, remote, plan, state_dir, dry=False):
        self.remote, self.plan, self.dir, self.dry = remote, plan, Path(state_dir), dry
        self.dir.mkdir(parents=True, exist_ok=True)
        path = self.dir / "controller.json"
        self.state = json.loads(path.read_text()) if path.exists() else {"tasks": {}, "events": [], "tick": 0}
        self.tasks = self.state["tasks"]
        self.actions = []
        self.occupied = 0

    def save(self):
        if not self.dry:
            atomic(self.dir / "controller.json", self.state)

    def event(self, task, message):
        self.state["events"].append({"time": now(), "task": task, "message": message})
        self.state["events"] = self.state["events"][-200:]
        print(task + ": " + message, flush=True)

    def halt(self, task, record, reason):
        if record.get("halted") != reason:
            record["halted"] = reason
            self.event(task, "STOPPED: " + reason)
        self.save()

    def adopt(self):
        path = self.dir / "agents.json"
        if not path.exists():
            return
        for task, old in json.loads(path.read_text()).items():
            if task not in self.tasks:
                self.tasks[task] = {"agent_id": old["agent_id"], "starting_sha": old.get("starting_sha"),
                    "repair_count": old.get("repair_cycle", 0), "submitted": bool(old.get("submitted", False))}
                branches = old.get("git", {}).get("branches", [])
                if len(branches) == 1:
                    self.tasks[task]["expected_branch"] = branches[0]["branch"]
                self.event(task, "Adopted existing agent; prior correction count " + str(old.get("repair_cycle", 0)))
        self.save()

    def effect(self, label, function):
        self.actions.append(label)
        if self.dry:
            print("WOULD " + label, flush=True)
            return None
        return function()

    def followup(self, task, record, snapshot, key, prompt, repair=True, problem=None):
        # Persist intent BEFORE non-idempotent POST; ambiguous replies never cause a replay.
        if record.get("intent"):
            self.halt(task, record, "Unreconciled delivery intent; refusing duplicate agent prompt")
            return
        if repair:
            # A new SHA/run ID alone is not evidence that a repeated defect improved.
            fingerprints = problem_fingerprints(problem if problem is not None else key.split(":")[0])
            windows = record.setdefault("repair_problems", {})
            previous = record.get("repair_window", {})
            if previous:
                windows.setdefault(previous["fingerprint"], previous)
            for fingerprint in fingerprints:
                if fingerprint not in windows:
                    window = {"fingerprint": fingerprint, "attempts": 0}
                    # Preserve old delivered keys without replaying an uncertain run.
                    if key in record.get("delivered", []):
                        if snapshot.get("run_id") != record.get("last_dispatched_run"):
                            record["waiting"] = "Await observation of previously delivered correction"
                            return
                        window.update(attempts=1, last_dispatch_at=now())
                    windows[fingerprint] = window
            current = [windows[fingerprint] for fingerprint in fingerprints]
            window = record["repair_window"] = max(current, key=lambda w: w["attempts"])
            if record.get("stalled", {}).get("kind") == "no_progress":
                record.pop("stalled")
            if window["attempts"] >= self.plan["max_repairs"]:
                record["stalled"] = {"kind": "no_progress", "reason":
                    "Repeated problem exhausted recovery budget; integration lead must inspect the existing agent and PR",
                    "fingerprint": window["fingerprint"]}
                record.pop("waiting", None)
                self.save()
                return
            retry_at = max((dt.datetime.fromisoformat(w["last_dispatch_at"]) + dt.timedelta(
                seconds=min(1800, 300 * 2 ** (w["attempts"] - 1))) for w in current if w["attempts"]), default=None)
            if retry_at and dt.datetime.fromisoformat(now()) < retry_at:
                record["waiting"] = "Repeated problem backoff until " + retry_at.isoformat()
                return
            attempt = [(w["fingerprint"], w["attempts"] + 1) for w in current]
            key += ":recovery:" + hashlib.sha256(json.dumps(attempt).encode()).hexdigest()
            if window["attempts"]:
                prompt = ("The same problem remains after a completed correction. Diagnose why the prior approach "
                    "made no progress, use a different approach within this assignment, and report concrete blockers.\n\n" + prompt)
        elif key in record.get("delivered", []):
            if snapshot.get("run_id") == record.get("last_dispatched_run"):
                self.halt(task, record, "Integration correction finished without advancing its revision")
            return
        if not repair and record.get("sync_count", 0) >= 3:
            self.halt(task, record, "Three integration conflict attempts exhausted")
            return
        if self.occupied >= self.plan["max_implementation_agents"]:
            record["waiting"] = "Correction queued: all Cursor worker slots occupied"
            return
        self.occupied += 1  # Reserve before POST, including uncertain delivery outcomes.
        if self.dry:
            self.effect("followup " + task + " " + key, lambda: None)
            return
        record["intent"] = {"key": key, "previous_run": snapshot.get("run_id"), "time": now(), "repair": repair}
        if repair:
            record["intent"]["problems"] = fingerprints
        self.save()
        try:
            result = self.remote.cursor("POST", "/agents/" + record["agent_id"] + "/runs",
                {"prompt": {"text": prompt + "\n\n" + CI_OWNERSHIP}})
        except Exception:
            self.event(task, "Delivery uncertain; integration lead must verify the dispatched run before reconciliation")
            raise
        self.acknowledge(record, result["run"]["id"])
        self.event(task, ("Cursor correction dispatched (cycle " + str(record.get("repair_count", 0)) + ")")
                   if repair else "Cursor integration conflict resolution dispatched")

    def acknowledge(self, record, run_id):
        # Only a dispatch response (or independently verified delivery) binds an
        # intent to a run. A different latestRunId alone is not that evidence.
        if not isinstance(run_id, str) or not run_id:
            raise ValueError("Dispatch response missing a valid run ID; delivery remains uncertain")
        intent = record.pop("intent")
        record.setdefault("delivered", []).append(intent["key"])
        if intent["repair"]:
            record["repair_count"] = record.get("repair_count", 0) + 1
            if intent.get("problems"):
                current = [record["repair_problems"][fingerprint] for fingerprint in intent["problems"]]
                for window in current:
                    window["attempts"] += 1
                    window["last_dispatch_at"] = intent["time"]
                record["repair_window"] = max(current, key=lambda w: w["attempts"])
            elif intent.get("problem"):
                # Reconcile an already-persisted single-window intent once.
                window = record["repair_window"]
                window["attempts"] += 1
                window["last_dispatch_at"] = intent["time"]
                record["repair_problems"][intent["problem"]] = window
        else:
            record["sync_count"] = record.get("sync_count", 0) + 1
        record["last_dispatched_run"] = run_id
        record["last_dispatch_source_run"] = intent["previous_run"]
        record["last_dispatch_at"] = intent["time"]
        if record.get("halted") in {"Delivery outcome uncertain; no automatic replay",
                "Unreconciled delivery intent; refusing duplicate agent prompt"}:
            record.pop("halted")
        record.pop("waiting", None)
        self.save()

    def pr_for(self, record, snapshot):
        if record.get("pr"):
            return self.remote.gh("pulls/" + str(record["pr"]))
        for branch in snapshot.get("git", {}).get("branches", []):
            url = branch.get("prUrl", "")
            match = re.fullmatch(r"https://github.com/CharitonMedia/togavsu/pull/(\d+)", url)
            if match:
                record["pr"] = int(match.group(1))
                self.save()
                return self.remote.gh("pulls/" + str(record["pr"]))
            if branch.get("branch"):
                query = "pulls?state=all&head=CharitonMedia:" + urllib.parse.quote(branch["branch"], safe="")
                prs = self.remote.gh(query)
                if len(prs) == 1:
                    record["pr"] = prs[0]["number"]
                    self.save()
                    return prs[0]
        return None

    def process(self, spec, record, snapshot, main):
        task = spec["id"]
        if record.get("halted") == LEGACY_REPAIR_HALT:
            record.setdefault("recovered_halts", []).append({"reason": record.pop("halted"), "time": now()})
            self.event(task, "Recovered obsolete lifetime correction cap; history retained, bounded recovery policy applies")
            self.save()
        if record.get("intent"):
            # No acknowledgement survived. The snapshot may be any older run,
            # not just previous_run, so it cannot establish delivery identity.
            record["waiting"] = "Integration lead must verify delivery and reconcile the existing intent; no replay"
            if not record.get("halted"):
                self.halt(task, record, "Delivery outcome uncertain; no automatic replay")
            return False
        if unobserved_run(record, snapshot):
            record["waiting"] = "Await observation of acknowledged Cursor run; no duplicate prompt"
            # Legacy records have no dispatch time. Start an honest inspection
            # clock on first uncertainty instead of inventing a historical time.
            since = record.get("last_dispatch_at") or record.setdefault("dispatch_visibility_since", now())
            if age(since) >= 900:
                record["stalled"] = {"kind": "run_observation", "reason":
                    "Acknowledged Cursor run not observed within 15 minutes; inspect existing run, do not replay"}
            return False
        record.pop("dispatch_visibility_since", None)
        if record.get("stalled", {}).get("kind") == "run_observation":
            record.pop("stalled")
            record.pop("waiting", None)
        if record.get("halted") or record.get("completed"):
            return False
        record["agent_status"] = snapshot["status"]
        if not record.get("expected_branch"):
            branches = snapshot.get("git", {}).get("branches", [])
            if len(branches) == 1 and branches[0].get("repoUrl", "").removeprefix("https://").rstrip("/") == "github.com/" + REPO:
                record["expected_branch"] = branches[0]["branch"]
                self.save()
        if snapshot["status"] in ACTIVE:
            run_id = snapshot.get("run_id")
            if not isinstance(run_id, str) or not run_id:
                run_id = None
            record["waiting"] = ("Cursor run active; awaiting completion" if run_id else
                "Cursor run active; run ID unavailable; original observation deadline retained")
            watch = record.setdefault("active_watch", {"run_id": run_id, "since": now()})
            if run_id and watch.get("run_id") and watch["run_id"] != run_id:
                watch = record["active_watch"] = {"run_id": run_id, "since": now()}
                if record.get("stalled", {}).get("kind") == "active_run":
                    record.pop("stalled")
            elif run_id and not watch.get("run_id"):
                watch["run_id"] = run_id  # Binding an unknown run does not prove a new run started.
            watch["observed_run_id"] = run_id
            if (age(watch["since"]) >= self.plan.get("active_run_timeout_seconds", 1800)
                    and record.get("stalled", {}).get("kind") in {None, "active_run"}):
                record["stalled"] = {"kind": "active_run", "reason":
                    "Cursor run exceeded observation deadline; integration lead must inspect this run before any replacement",
                    "run_id": watch["run_id"], "since": watch["since"]}
            return False
        record.pop("active_watch", None)
        if record.get("stalled", {}).get("kind") == "active_run":
            record.pop("stalled")
        if snapshot["status"] != "FINISHED":
            self.followup(task, record, snapshot, "interrupted:" + str(snapshot.get("run_id")),
                "Your previous run ended with status " + str(snapshot["status"]) + ". Resume the same assignment and branch, "
                "diagnose the interruption, finish tests and PR evidence, and push. Do not merge or bypass gates.")
            return False
        pr = self.pr_for(record, snapshot)
        if not pr:
            self.followup(task, record, snapshot, "missing-pr:" + str(snapshot.get("run_id")),
                "Your run finished without a discoverable pull request. Complete your assigned work, tests and push, "
                "then open the PR on the same branch. Report genuine blockers. Do not merge or bypass checks.")
            return False
        if not record.get("expected_branch") or pr["head"].get("ref") != record["expected_branch"]:
            self.halt(task, record, "PR branch does not match the branch pinned to this Cursor assignment")
            return False
        if any(other != task and value.get("pr") == pr["number"] for other, value in self.tasks.items()):
            self.halt(task, record, "PR is already assigned to another task")
            return False
        if pr["head"]["repo"]["full_name"] != REPO or pr["base"]["ref"] != "main":
            self.halt(task, record, "Unexpected PR repository or target branch")
            return False
        head, number = pr["head"]["sha"], pr["number"]
        if record.get("last_submission", {}).get("head") != head:
            record["last_submission"] = {"head": head, "observed_at": now()}
        if record.get("merge_intent", {}).get("result_sha"):
            return self.verify_merge(task, record, pr)  # Reconcile, never resubmit an accepted merge.
        if pr.get("merged"):
            # Merged work is a bounded dependency, never a declaration of product/phase acceptance.
            if not record.get("merge_intent") or record["merge_intent"]["head"] != head:
                self.halt(task, record, "PR merged outside this controller; verify evidence before adopting completion")
                return False
            return self.verify_merge(task, record, pr)
        if pr["state"] != "open":
            self.halt(task, record, "Assignment PR closed without merge")
            return False
        expected_label = "togavsu-task:" + task
        task_labels = [label["name"] for label in pr.get("labels", []) if label["name"].startswith("togavsu-task:")]
        if task_labels and task_labels != [expected_label]:
            self.halt(task, record, "PR assignment label contradicts pinned task identity")
            return False
        if not task_labels:
            def bind_label():
                try:
                    self.remote.gh("labels/" + urllib.parse.quote(expected_label, safe=""))
                except RuntimeError as error:
                    if "HTTP 404" not in str(error):
                        raise
                    self.remote.gh("labels", "POST", {"name": expected_label, "color": "1d76db"})
                self.remote.gh("issues/" + str(number) + "/labels", "POST", {"labels": [expected_label]})
            self.effect("bind task label on PR " + str(number), bind_label)
            record["waiting"] = "Assignment label added; await task-bound Codex review"
            return False
        try:
            changed_files = self.remote.changed_files(number)
        except ValueError as error:
            self.halt(task, record, str(error))
            return False
        for file in changed_files:
            paths = {file["filename"], file.get("previous_filename", "")}
            if paths & PROTECTED or any(p.startswith("deploy/controller/") for p in paths):
                self.halt(task, record, "Worker changed protected control-plane file; requires independent control-plane review")
                return False
        if pr.get("draft"):
            self.followup(task, record, snapshot, "draft:" + head,
                "Your assignment is still a draft PR. Resolve its remaining bounded blockers, run tests and mark it ready "
                "when complete. Do not claim product acceptance or merge. Report an actual external blocker explicitly.")
            return False
        comparison = self.remote.gh("compare/" + main + "..." + head)
        if comparison["behind_by"]:
            if record.get("updated_for") == [head, main]:
                record.setdefault("update_requested_at", now())
                elapsed = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(record["update_requested_at"])).total_seconds()
                if elapsed > 900:
                    self.halt(task, record, "Accepted branch update did not advance the head within 15 minutes")
                    return False
            if pr.get("mergeable") is None:
                return False
            if pr.get("mergeable") is False:
                self.followup(task, record, snapshot, "conflict:" + head + ":" + main,
                    "Merge origin/main into your existing PR branch and resolve integration conflicts, preserving both sides' "
                    "requirements, tests and debt entries. You may edit shared files solely to reconcile this integration. "
                    "Run complete applicable tests, refresh the ORIGINAL PR body with current revision/results, commit and push. "
                    "Do not force-push, merge the PR, weaken checks or discard another domain's work.", repair=False)
            elif record.get("updated_for") != [head, main]:
                if not self.dry:
                    record["updated_for"] = [head, main]
                    record["update_requested_at"] = now()
                    self.save()  # A lost update response must still have a bounded deadline.
                self.effect("update branch PR " + str(number), lambda: self.remote.gh("pulls/" + str(number) + "/update-branch",
                    "PUT", {"expected_head_sha": head}))
                self.event(task, "Refresh branch against main; await fresh tests and Codex")
            return False
        checks = self.remote.checks(head)
        existing_protection = self.remote.gh("branches/main/protection")
        required = REQUIRED | set(spec.get("required_checks", [])) | set(
            (existing_protection.get("required_status_checks") or {}).get("contexts", []))
        if not required <= checks.keys() or any(c["status"] != "completed" for c in checks.values()):
            record["waiting"] = "Checks pending for " + head
            if record.get("check_wait", {}).get("head") != head:
                record["check_wait"] = {"head": head, "since": now()}
            elapsed = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(record["check_wait"]["since"])).total_seconds()
            missing = sorted(required - checks.keys())
            if missing and elapsed > 900 and checks and all(c["status"] == "completed" for c in checks.values()):
                self.followup(task, record, snapshot, "missing-checks:" + head,
                    "Your PR is missing required executable CI jobs: " + ", ".join(missing) + ". Wire the actual tests into CI, "
                    "run them and push on the same branch. Do not add placeholder success jobs or weaken required checks.",
                    problem={"missing_checks": missing})
            elif elapsed > 5400:
                self.halt(task, record, "Required CI missing or stalled for 90 minutes; no merge or duplicate job launch")
            return False
        record.pop("waiting", None)
        record.pop("check_wait", None)
        codex = checks["Codex checkpoint"]
        try:
            report, context = self.remote.review(codex)
            validate_review(report, context, pr, main, task)
        except ReviewPending:
            record["waiting"] = "Codex workflow attempt queued or running"
            return False
        except ReviewSuperseded as error:
            self.wait_for_review(task, record, pr, str(error))
            return False
        except (ValueError, KeyError, zipfile.BadZipFile) as error:
            # Stale base can result from a workflow event predating branch refresh. Rerun once.
            key = head + ":" + main
            run_match = re.search(r"/actions/runs/(\d+)/", codex.get("details_url", ""))
            if run_match and key not in record.get("infra_retried", []):
                if self.dry:
                    self.effect("rerun unavailable review " + task, lambda: None)
                else:
                    record.setdefault("infra_retried", []).append(key)
                    self.save()
                    self.remote.gh("actions/runs/" + run_match.group(1) + "/rerun", "POST")
            else:
                self.halt(task, record, "Review unavailable after retry: " + str(error))
            return False
        if report["verdict"] == "fail":
            self.followup(task, record, snapshot, "review:" + head,
                "Independent Codex checkpoint failed for your existing PR #" + str(number) + ". Resolve ALL findings below "
                "with regression tests on the SAME branch. Preserve PRD scope. Do not merge or weaken gates. "
                "Rerun the complete applicable test suite and REWRITE THE ORIGINAL PR BODY with the tested revision, current "
                "commands/results and per-finding resolutions. New pushes automatically rerun review.\n\n" + json.dumps(report),
                problem=review_problem(report["findings"]))
            return False
        failures = self.remote.test_failures(checks)
        if failures:
            self.followup(task, record, snapshot, "tests:" + head,
                "Codex review passed, but CI failed on your existing PR #" + str(number) + ". Read the failing Actions logs, "
                "fix the causes with regression tests, rerun all applicable tests and update original PR evidence. "
                "Do not bypass checks or merge. Failures: " + json.dumps(failures),
                problem={"failed_checks": sorted(c["name"] for c in failures)})
            return False
        if codex.get("conclusion") != "success":
            self.halt(task, record, "Passing review artifact conflicts with failed checkpoint status")
            return False
        if spec.get("report_path"):
            try:
                data = self.remote.gh("contents/" + spec["report_path"] + "?ref=" + head)
                evidence = json.loads(base64.b64decode(data["content"]))
                self.validate_evidence(spec, evidence, head)
            except (ValueError, KeyError, TypeError, RuntimeError) as error:
                if isinstance(error, RuntimeError) and "HTTP 404" not in str(error):
                    raise  # Transient control failures are not implementation defects.
                self.followup(task, record, snapshot, "phase-evidence:" + head,
                    "Your required integration report " + spec["report_path"] + " is invalid: " + str(error) +
                    ". Complete the assigned integration and actual tests, record honest results and push. "
                    "Never fabricate acceptance or change a failed result to pass. Report external blockers explicitly.",
                    problem={"phase_evidence": str(error)})
                return False
        protection = self.remote.gh("branches/main/protection")
        required_config = protection.get("required_status_checks") or {}
        if not (required_config.get("strict") and REQUIRED <= set(required_config.get("contexts", []))
                and protection.get("enforce_admins", {}).get("enabled")
                and protection.get("required_pull_request_reviews") is not None):
            self.halt(task, record, "Branch protection changed or no longer enforces required checkpoints")
            return False
        # Required product jobs must be enforced by GitHub, not just a stale local snapshot.
        enforced = set(required_config.get("contexts", []))
        required |= enforced
        if not required <= enforced:
            prior = {c["context"]: c for c in required_config.get("checks", [])}
            bindings = [dict(prior.get(name, {"context": name, "app_id": -1})) for name in sorted(enforced | required)]
            for binding in bindings:
                if binding["context"] in REQUIRED | set(spec.get("required_checks", [])):
                    binding["app_id"] = APP_ID
            if self.dry:
                self.effect("enforce product checks " + task, lambda: None)
            else:
                self.remote.gh("branches/main/protection/required_status_checks", "PATCH", {"strict": True, "checks": bindings})
                protection = self.remote.gh("branches/main/protection")
                if not required <= set(protection["required_status_checks"].get("contexts", [])):
                    self.halt(task, record, "GitHub did not enforce task-required product checks")
                    return False
        current_checks = self.remote.checks(head)
        if (not required <= current_checks.keys() or any(c["status"] != "completed" or c.get("conclusion") != "success"
                for c in current_checks.values()) or current_checks["Codex checkpoint"]["id"] != codex["id"]):
            record["waiting"] = "Checks changed during final refresh; await current results"
            return False
        # Final metadata refresh follows product-check enforcement and observation.
        fresh = self.remote.gh("pulls/" + str(number))
        current_main = self.remote.gh("git/ref/heads/main")["object"]["sha"]
        if (fresh.get("base", {}).get("ref") != "main"
                or fresh.get("head", {}).get("ref") != record["expected_branch"]):
            self.halt(task, record, "PR was retargeted or its pinned branch changed during final refresh")
            return False
        if fresh["head"]["sha"] != head or current_main != main:
            return False
        try:
            validate_review(report, context, fresh, main, task)
        except ReviewSuperseded as error:
            self.wait_for_review(task, record, fresh, str(error))
            return False
        if self.dry:
            self.effect("protected merge PR " + str(number), lambda: None)
            return True
        record["merge_intent"] = {"head": head, "base": main, "time": now()}
        self.save()
        result = self.remote.gh("pulls/" + str(number) + "/merge", "PUT", {"sha": head, "merge_method": "squash"})
        if not result.get("merged"):
            self.event(task, "GitHub did not permit merge; protections retained")
            return False
        record["merge_intent"]["result_sha"] = result["sha"]
        self.save()
        return self.verify_merge(task, record, self.remote.gh("pulls/" + str(number)))

    def verify_merge(self, task, record, pr):
        if pr.get("base", {}).get("ref") != "main":
            self.halt(task, record, "Merge target changed; refusing dependency completion outside main")
            return False
        intent = record["merge_intent"]
        merged = intent.get("result_sha", pr.get("merge_commit_sha"))
        visible = pr.get("merged") and pr.get("merge_commit_sha") == merged and pr["head"]["sha"] == intent["head"]
        current_main = self.remote.gh("git/ref/heads/main")["object"]["sha"]
        on_main = merged == current_main
        if visible and not on_main and isinstance(merged, str) and re.fullmatch(r"[0-9a-f]{40}", merged):
            graph = self.remote.gh("compare/" + merged + "..." + current_main)
            on_main = graph.get("status") in {"ahead", "identical"} and graph.get("behind_by") == 0
        if not visible or not on_main:
            record.setdefault("merge_verify_since", now())
            elapsed = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(record["merge_verify_since"])).total_seconds()
            record["waiting"] = "Await verified merge commit on main; dependencies remain blocked"
            if elapsed > 900:
                self.halt(task, record, "Merge response could not be verified on main within 15 minutes")
            self.save()
            return False
        record["completed"] = {"pr": pr["number"], "merge_sha": merged, "head": intent["head"], "report": pr["html_url"]}
        record.pop("stalled", None)
        self.state["last_productive_progress"] = {"time": now(), "task": task, "merge_sha": merged}
        self.event(task, "Verified protected merge on main: " + merged)
        self.save()
        return True

    def wait_for_review(self, task, record, pr, reason):
        signature = hashlib.sha256(json.dumps({"head": pr["head"]["sha"], "body": pr.get("body"),
            "labels": pr.get("labels", [])}, sort_keys=True).encode()).hexdigest()
        if record.get("evidence_wait", {}).get("signature") != signature:
            record["evidence_wait"] = {"signature": signature, "since": now()}
        record["waiting"] = reason + "; await fresh matching Codex review"
        elapsed = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(record["evidence_wait"]["since"])).total_seconds()
        if elapsed > 5400:
            self.halt(task, record, "Fresh review of current evidence did not arrive within 90 minutes")

    def validate_evidence(self, spec, evidence, head):
        if not isinstance(evidence, dict):
            raise ValueError("Integration evidence must be a JSON object")
        if evidence.get("task_id") != spec["id"] or evidence.get("status") != "pass":
            raise ValueError("Integration report absent or not passing")
        tests = evidence.get("tests")
        if not isinstance(tests, list) or not tests or any(
                not isinstance(t, dict) or type(t.get("exit_code")) is not int or t["exit_code"] != 0
                or not isinstance(t.get("command"), str) or not t["command"].strip() for t in tests):
            raise ValueError("Missing or failed actual test commands")
        passed, pending = evidence.get("passed_acceptance_ids"), evidence.get("pending_acceptance_ids")
        if not isinstance(passed, list) or not isinstance(pending, list) or any(
                not isinstance(x, str) for x in passed + pending):
            raise ValueError("Acceptance inventory missing")
        if (len(set(passed)) != len(passed) or len(set(pending)) != len(pending)
                or set(passed) & set(pending) or set(passed) | set(pending) != set(spec["acceptance_ids"])):
            raise ValueError("Acceptance inventory is incomplete, overlapping or unknown")
        if spec.get("full_acceptance") and (pending or set(passed) != set(spec["acceptance_ids"])):
            raise ValueError("Full release acceptance still pending")
        required_cases = spec.get("phase_acceptance", {})
        cases = evidence.get("phase_cases")
        if not required_cases or not isinstance(cases, list) or len(cases) != len(required_cases):
            raise ValueError("Assigned phase case coverage missing")
        seen = set()
        for case in cases:
            if not isinstance(case, dict):
                raise ValueError("Phase case must be a JSON object")
            key = case.get("id")
            if key not in required_cases or key in seen:
                raise ValueError("Unknown or duplicate phase case")
            seen.add(key)
            if (case.get("status") != "pass" or type(case.get("exit_code")) is not int or case["exit_code"] != 0
                    or not isinstance(case.get("command"), str) or not case["command"].strip()
                    or set(case.get("acceptance_ids", [])) != set(required_cases[key])):
                raise ValueError("Phase case not passed with its assigned acceptance mapping")
        tested = evidence.get("tested_revision", "")
        if not isinstance(tested, str) or not re.fullmatch(r"[0-9a-f]{40}", tested):
            raise ValueError("Tested code revision missing")
        if tested != head:
            comparison = self.remote.gh("compare/" + tested + "..." + head)
            files = comparison.get("files", [])
            if ("files" not in comparison or comparison.get("status") not in {"ahead", "identical"} or comparison.get("behind_by") != 0
                    or len(files) >= 300 or any(not f["filename"].startswith("docs/build/reports/")
                    or (f.get("previous_filename") and not f["previous_filename"].startswith("docs/build/reports/")) for f in files)):
                raise ValueError("Tested revision is stale: code changed after the tests")

    def launch(self, spec, main):
        task = spec["id"]
        if self.occupied >= self.plan["max_implementation_agents"]:
            return
        self.occupied += 1
        record = self.tasks.setdefault(task, {"agent_id": "bc-" + str(uuid.uuid4()), "starting_sha": main, "repair_count": 0})
        self.save()
        prompt = ("Implement Togavsu assignment " + task + ". Read AGENTS.md, the full togavsu-core-prd.md, "
            "docs/build/WORKFLOW.md and docs/build/AUTOMATION.md. Your exact approved assignment follows. "
            "Work in this isolated Cursor-hosted VM on your own branch using synthetic data only. "
            "Implement working behavior with meaningful success/failure tests and honest pending criteria. "
            "Do not merge, push main, force-push or alter controller/gates/acceptance criteria. "
            "Commit/push explicit paths and open a ready PR when your bounded assignment is complete. "
            "Keep original PR body current with tested code revision and results.\n\n" + json.dumps(spec)
            + "\n\n" + CI_OWNERSHIP)
        try:
            result = self.effect("launch " + task, lambda: self.remote.cursor("POST", "/agents", {
                "agentId": record["agent_id"], "name": "Togavsu " + task, "prompt": {"text": prompt},
                "repos": [{"url": "https://github.com/" + REPO, "startingRef": record["starting_sha"]}],
                "env": {"type": "cloud"}, "autoCreatePR": True, "workOnCurrentBranch": False}))
        except urllib.error.HTTPError as error:
            if error.code != 409:
                raise
            agent = self.remote.cursor("GET", "/agents/" + record["agent_id"])
            if agent.get("id") != record["agent_id"] or not any(
                    r.get("url", "").rstrip("/") == "https://github.com/" + REPO for r in agent.get("repos", [])):
                raise ValueError("Conflicting agent ID does not identify this assignment repository")
            result = {"agent": agent}
        if not self.dry:
            record["submitted"] = True
            record["url"] = "https://cursor.com/agents/" + record["agent_id"]
            self.save()
        self.event(task, "Cloud assignment launched")

    def tick(self):
        if (self.dir / "STOP").exists():
            print("Paused by STOP file; no mutations", flush=True)
            return
        self.occupied, self.actions = 0, []
        self.state["tick"] += 1
        self.state["last_tick"] = now()
        self.state.pop("tick_error", None)
        self.state.pop("publication_error", None)
        try:
            self._tick()
        except Exception as error:
            self.state["tick_error"] = type(error).__name__ + ": " + str(error)[:1800]
            raise
        finally:
            attention = [task for task, rec in self.tasks.items() if not rec.get("completed")
                and any(rec.get(field) for field in ("halted", "stalled", "last_error", "launch_uncertain", "intent"))]
            self.state["health"] = "needs_attention" if attention or self.state.get("tick_error") else "observing"
            if attention or self.state.get("tick_error"):
                self.state["last_result"] = "Needs supervision: " + (", ".join(attention) or "controller request failed")
            self.save()
            try:
                self.publish()
            except Exception as error:
                self.state["publication_error"] = type(error).__name__ + ": " + str(error)[:1800]
                self.state["health"] = "needs_attention"
                self.state["last_result"] += "; status publication failed"
                self.save()
                self.write_status()  # Local only. Never recursively retry the remote mutation.
                if not self.state.get("tick_error"):
                    raise  # Preserve an already-raised observation error, if any.
            else:
                if not self.state.get("tick_error") and not any(r.get("last_error") or r.get("launch_uncertain")
                        for r in self.tasks.values() if not r.get("completed")):
                    self.state["last_successful_tick"] = now()  # Control heartbeat only, never product progress.
                    self.save()

    def _tick(self):
        self.adopt()
        main = self.remote.gh("git/ref/heads/main")["object"]["sha"]
        specs = {t["id"]: t for t in self.plan["tasks"]}
        snapshots = {}
        # Observe ALL workers before any dispatch; a later dictionary entry may already occupy a slot.
        for task, record in list(self.tasks.items()):
            if task not in specs or record.get("completed"):
                continue
            try:
                snapshots[task] = self.remote.agent(record)
                if not record.get("submitted"):
                    record["submitted"] = True  # Reconcile an uncertain idempotent create.
                    record.pop("launch_uncertain", None)
            except AgentNotFound as error:
                if (not record.get("submitted") and not record.get("halted") and not record.get("intent")
                        and "last_dispatched_run" not in record):
                    # Only the agent endpoint can establish this case. Retry the
                    # original idempotent create with its existing durable ID.
                    record.pop("launch_uncertain", None)
                    continue
                self.occupied += 1  # A 404 does not prove a submitted worker stopped.
                record["last_error"] = str(error) + "; existing worker requires inspection"
                if not record.get("submitted"):
                    record["launch_uncertain"] = True
                if not record.get("halted"):
                    self.halt(task, record, "Cursor agent unavailable; verify existing worker before recovery")
                continue
            except Exception as error:
                self.occupied += 1  # Includes run-endpoint 404 and every unknown observation outcome.
                if not record.get("submitted"):
                    record["launch_uncertain"] = True
                record["last_error"] = ("Cursor observation HTTP " + str(error.code) if isinstance(error, urllib.error.HTTPError)
                    else type(error).__name__ + ": agent observation unavailable")
                continue
            snapshot = snapshots[task]
            if snapshot["status"] in ACTIVE or record.get("intent") or unobserved_run(record, snapshot):
                self.occupied += 1
        for task, snapshot in snapshots.items():
            record = self.tasks[task]
            try:
                if self.process(specs[task], record, snapshot, main):
                    # Main changed: avoid acting against stale base on other PRs this tick.
                    record.pop("last_error", None)
                    self.state["last_result"] = "Merged " + task + "; re-evaluate next tick"
                    self.save()
                    return
                record.pop("last_error", None)
            except Exception as error:
                record["last_error"] = type(error).__name__ + ": " + str(error)[:1800]
                self.event(task, "Control error; no bypass: " + record["last_error"])
            self.save()
        for task, spec in specs.items():
            if self.occupied >= self.plan["max_implementation_agents"]:
                break
            record = self.tasks.get(task, {})
            if (record.get("submitted") or record.get("halted") or record.get("launch_uncertain")
                    or record.get("intent") or "last_dispatched_run" in record):
                continue
            if not all(self.tasks.get(dep, {}).get("completed") for dep in spec["depends_on"]):
                continue
            try:
                self.launch(spec, main)
            except Exception as error:
                self.tasks[task]["last_error"] = "Launch outcome unconfirmed: " + type(error).__name__
                self.event(task, "Launch not confirmed; durable agent ID retained: " + type(error).__name__)
                self.save()
        self.state["last_result"] = "Waiting for agents/checks or ready work"
        self.save()

    def write_status(self):
        if self.dry:
            return
        summary = {"updated": now(), "tick": self.state["tick"], "health": self.state.get("health", "unknown"),
            "last_productive_progress": self.state.get("last_productive_progress"),
            "tick_error": self.state.get("tick_error"), "publication_error": self.state.get("publication_error"), "tasks": {
            k: {field: v[field] for field in ("pr", "agent_status", "repair_count", "repair_window", "last_submission", "active_watch",
                "completed", "halted", "stalled", "last_error", "waiting", "launch_uncertain", "intent") if field in v}
            for k, v in self.tasks.items()}}
        atomic(self.dir / "status.json", summary)
        return summary

    def publish(self):
        if self.dry:
            return
        summary = self.write_status()
        # Only publish a changed state, not every timer heartbeat.
        public = dict(summary)
        public.pop("updated")
        public.pop("tick")
        digest = hashlib.sha256(json.dumps(public, sort_keys=True).encode()).hexdigest()
        if digest != self.state.get("published_digest"):
            body = "## Automatic build controller\n\nUpdated: " + summary["updated"] + "\n\n"
            body += "Health: " + summary["health"] + ". Timer heartbeat is not product progress.\n\n"
            progress = summary["last_productive_progress"]
            if progress:
                body += "Last verified protected merge: " + progress["task"] + " at " + progress["time"] + " (" + progress["merge_sha"] + ").\n\n"
            if summary["tick_error"]:
                body += "Control error: " + summary["tick_error"] + "\n\n"
            body += "| Assignment | PR | State | Corrections |\n|---|---|---|---|\n"
            for task, rec in self.tasks.items():
                status = ("merged" if rec.get("completed") else "STOPPED: " + rec["halted"] if rec.get("halted")
                    else "STALLED: " + rec["stalled"]["reason"] if rec.get("stalled")
                    else "CONTROL ERROR: " + rec["last_error"] if rec.get("last_error")
                    else "Launch outcome uncertain; inspect durable agent ID" if rec.get("launch_uncertain")
                    else rec.get("waiting", rec.get("agent_status", "submitted")))
                body += "| " + task + " | " + ("#" + str(rec["pr"]) if rec.get("pr") else "pending") + " | " + status.replace("|", "/") + " | " + str(rec.get("repair_count", 0)) + " |\n"
            body += "\nProtected checks stay enforced. Merged assignments are bounded work, not completed product acceptance.\n"
            if self.state.get("status_comment"):
                self.remote.gh("issues/comments/" + str(self.state["status_comment"]), "PATCH", {"body": body})
            else:
                comment = self.remote.gh("issues/1/comments", "POST", {"body": body})
                self.state["status_comment"] = comment["id"]
            self.state["published_digest"] = digest
            self.save()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--plan", required=True)
    parser.add_argument("--state", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    plan = json.loads(Path(args.plan).read_text())
    state = Path(args.state)
    state.mkdir(parents=True, exist_ok=True)
    with open(state / "controller.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("Another controller tick owns the lock")
            return
        Controller(Remote(), plan, state, args.dry_run).tick()


if __name__ == "__main__":
    main()
