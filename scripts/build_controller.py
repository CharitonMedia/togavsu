"""Togavsu control plane: cloud work only, exact-revision review, durable decisions.

Installed from a reviewed commit; never imports or executes candidate application code.
Run once from a persistent systemd timer. STOP pauses all mutations, including merges.
"""
import argparse
import base64
import datetime as dt
import email.message
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import re
import smtplib
import ssl
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile
import zlib

REPO = "CharitonMedia/togavsu"
RUN_ACTIVE = {"CREATING", "RUNNING", "WAITING_FOR_BACKGROUND_WORK"}
ACTIVE = RUN_ACTIVE | {"ACTIVE"}  # Agent lifecycle ACTIVE is only a conservative busy signal.
TERMINAL = {"FINISHED", "ERROR", "CANCELLED", "EXPIRED"}  # Cursor v1 Get A Run outcomes.
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
    ".github/workflows/codex-checkpoint.yml", ".github/workflows/setup-checks.yml", ".github/workflows/lane-agent.yml",
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


class ObservationError(ValueError):
    """The response cannot establish worker execution state; observe again later."""


def validate_snapshot(snapshot):
    if not isinstance(snapshot, dict):
        raise ObservationError("Cursor snapshot is not an object")
    status = snapshot.get("status")
    if not isinstance(status, str) or status not in ACTIVE | TERMINAL:
        raise ObservationError("Cursor run status is missing or unrecognized")
    run_id = snapshot.get("run_id")
    if run_id is not None and (not isinstance(run_id, str) or (run_id and not run_id.strip())):
        raise ObservationError("Cursor run identity is malformed")
    if status in TERMINAL and not run_id:
        raise ObservationError("Terminal Cursor observation lacks a run identity")


def observation_error(error):
    return ("Cursor observation invalid: " + str(error) if isinstance(error, ObservationError)
        else "Cursor observation HTTP " + str(error.code) if isinstance(error, urllib.error.HTTPError)
        else type(error).__name__ + ": agent observation unavailable")


def parse_time(value):
    try:
        stamp = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except (AttributeError, TypeError, ValueError):
        return None
    return stamp if stamp.tzinfo else stamp.replace(tzinfo=dt.timezone.utc)


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


# Fixed problem classes keep one fingerprint across runs and heads, so they count
# only consecutive repeats and reset on evidence of progress.
FIXED_PROBLEMS = ("interrupted", "missing-pr", "draft")
DEFAULT_CORRECTION_ATTENTION = 10
DEFAULT_MAX_CORRECTIONS = 30
# CI that cannot reach a server it needs (Ubuntu's package servers, a lost runner) is not a code
# problem. Such a failure gets one rerun and then an emailed hold, never an agent correction.
INFRA_FAILURE = re.compile(r"Temporary failure resolving|Could not resolve host|Unable to fetch some archives"
                           r"|^(?:\S+\s+)?[EW]: Failed to fetch |lost communication with the server"
                           r"|The runner has received a shutdown signal", re.M)
JOB_URL = re.compile(r"/actions/runs/(\d+)/job/(\d+)")
# Stall alerts: a stopped, held or repeating step emails the plan's alert_email instead of
# retrying silently. Each condition is emailed once; it can alert again only after it clears.
MERGE_REFUSED = re.compile(r"HTTP (405|409|422)")  # GitHub refused the merge; nothing was merged.
MERGE_REFUSAL_LIMIT = 2  # More than one controller cycle refused: hold merge retries.
MERGE_HOLD_STATES = {"blocked", "unknown", None}  # Retry a held merge only once GitHub reports another state.
ERROR_ALERT_TICKS = 3  # The same control error on this many consecutive ticks is a repeating failure.
DEFAULT_IDLE_ALERT_SECONDS = 3600  # No state change for an hour while no agent run is active.
ALERT_RETRY_SECONDS = 900  # After a failed alert email, wait this long before trying again.
PROGRESS_FIELDS = ("agent_status", "pr", "last_submission", "last_dispatched_run", "waiting", "repair_count",
                   "correction_volume", "merge_intent", "completed")


class Mailer:
    """Sends alerts the way Render-1's Doc Tender report does: SMTP over SSL, SMTP_* from the environment."""
    def send(self, to, subject, body):
        host, user, password = (os.environ.get(k, "") for k in ("SMTP_HOST", "SMTP_USER", "SMTP_PASS"))
        if not (host and user and password):
            raise RuntimeError("SMTP_HOST, SMTP_USER and SMTP_PASS must be set to send controller alerts")
        message = email.message.EmailMessage()
        message["Subject"], message["From"], message["To"] = subject, user, to
        message.set_content(body)
        with smtplib.SMTP_SSL(host, int(os.environ.get("SMTP_PORT", "465")), context=ssl.create_default_context(),
                              timeout=45) as server:
            server.login(user, password)
            refused = server.send_message(message)
        if refused:
            raise RuntimeError("Mail server refused " + ", ".join(refused))


def problem_fingerprints(problem):
    # Keep singleton fingerprints stable, but never give A a fresh budget just
    # because the next report changes from [A, B] to [A, C].
    # ponytail: exact normalized text, so a reworded finding is a new problem; ceiling: per-assignment correction volume (correction_attention_threshold, max_corrections_per_assignment); trigger: an assignment reaches correction_ceiling while its findings name the same file and defect.
    parts = [problem]
    if isinstance(problem, dict):
        for kind in ("review_findings", "failed_checks", "missing_checks"):
            if problem.get(kind):
                parts = [{kind: [item]} for item in problem[kind]]
                break
    return sorted({hashlib.sha256(json.dumps(part, sort_keys=True).encode()).hexdigest() for part in parts})


def observe_dispatch(record, snapshot):
    # Persist what a validated snapshot proves about the acknowledged run: that
    # it was seen, or (until then) which other run IDs were still being reported.
    expected = record.get("last_dispatched_run")
    run_id = snapshot.get("run_id")
    if not isinstance(expected, str) or not expected or record.get("last_dispatched_run_observed"):
        return
    if run_id == expected:
        record["last_dispatched_run_observed"] = True
    elif isinstance(run_id, str) and run_id:
        stale = record.setdefault("last_dispatch_stale_runs", [])
        if run_id not in stale:
            stale.append(run_id)


def unobserved_run(record, snapshot):
    # A successful POST reserves a slot until its run becomes visible. A stale
    # run, even one other than the immediate predecessor, proves nothing about it.
    # Once the exact run was seen, only a different identified run that was not
    # the predecessor or reported before it is a newer run of the same agent.
    if "last_dispatched_run" not in record:
        return False
    expected = record.get("last_dispatched_run")
    run_id = snapshot.get("run_id")
    if not isinstance(expected, str) or not expected:
        return True
    if run_id == expected:
        return False
    if not record.get("last_dispatched_run_observed") or not isinstance(run_id, str) or not run_id:
        return True
    return run_id == record.get("last_dispatch_source_run") or run_id in record.get("last_dispatch_stale_runs", [])


def dispatched_run_seen(record, snapshot):
    return "last_dispatched_run" in record and not unobserved_run(record, snapshot)


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
        if not isinstance(agent, dict) or agent.get("id") != record["agent_id"]:
            raise ObservationError("Cursor agent response does not match the requested identity")
        run_id = agent.get("latestRunId")
        if run_id is None or run_id == "":
            # Agent lifecycle IDLE/ARCHIVED is not a terminal run result.
            snapshot = {"status": "ACTIVE" if agent.get("status") == "ACTIVE" else None,
                "run_id": None, "git": {}, "updated_at": agent.get("updatedAt")}
        else:
            if not isinstance(run_id, str) or not run_id.strip():
                raise ObservationError("Cursor latest run identity is malformed")
            snapshot = self.run(record, run_id)
        validate_snapshot(snapshot)
        return snapshot

    def run(self, record, run_id):
        # One run by ID: its identity, agent and status must all match exactly.
        run = self.cursor("GET", "/agents/" + record["agent_id"] + "/runs/" + run_id)
        if (not isinstance(run, dict) or run.get("id") != run_id
                or run.get("agentId") != record["agent_id"]):
            raise ObservationError("Cursor run response does not match the requested agent and run")
        status = run.get("status")
        if not isinstance(status, str) or status not in RUN_ACTIVE | TERMINAL:
            raise ObservationError("Cursor run status is missing or unrecognized")
        return {"status": status, "run_id": run_id, "git": run.get("git", {}),
            "updated_at": run.get("updatedAt"), "created_at": run.get("createdAt")}

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

    def infrastructure_failures(self, failures):
        # {check name: (run id, first matching log line)} for failures whose job log shows CI could not
        # reach a server. An unreadable log counts as an ordinary failure.
        found = {}
        for failure in failures:
            match = JOB_URL.search(failure.get("url") or "")
            if not match:
                continue
            try:
                log = self.gh("actions/jobs/" + match.group(2) + "/logs", binary=True)
            except (RuntimeError, OSError, subprocess.SubprocessError):
                continue
            text = log.decode(errors="replace") if isinstance(log, bytes) else ""
            hit = INFRA_FAILURE.search(text)
            if hit:
                end = text.find("\n", hit.end())
                line = text[text.rfind("\n", 0, hit.start()) + 1:end if end >= 0 else len(text)]
                found[failure["name"]] = (match.group(1), line.strip()[-300:])
        return found


LANE_WORKFLOW = ".github/workflows/lane-agent.yml"
LANE_PROMPT_LIMIT = 60_000  # GitHub caps a workflow_dispatch payload at 65,535 characters.
LANE_ARTIFACT_LIMIT = 300_000_000
LANE_DISPATCH_GRACE = dt.timedelta(minutes=15)  # A dispatched run appears within seconds, even while queued.
LANE_QUEUED = {"queued", "waiting", "requested", "pending"}
LANE_OUTCOME = {"success": "FINISHED", "cancelled": "CANCELLED", "timed_out": "EXPIRED"}


class LaneRejected(ValueError):
    """The lane run finished but its result cannot be published; report the run as ERROR."""


class ActionsRemote(Remote):
    """Claude Code lanes in GitHub Actions on the Max subscription, behind the Cursor-shaped interface.

    The controller keeps calling cursor()/agent()/run(); this class answers them from a durable lane
    registry and the Lane agent workflow. A lane job holds a read-only token and returns a git bundle;
    this control process verifies the bundle and pushes it as the repository owner, without force, so
    protected CI and the review gate run on every revision. Nothing here builds or runs candidate code.
    """

    push_url = "https://github.com/" + REPO + ".git"  # Owner credentials via gh's git credential helper.

    def __init__(self, state_dir, repo_dir, dry=False):
        self.registry_path = Path(state_dir) / "lane-agents.json"
        self.work_dir = Path(state_dir)
        self.repo_dir = Path(repo_dir)
        self.dry = dry

    def load(self):
        return json.loads(self.registry_path.read_text()) if self.registry_path.exists() else {}

    def store(self, registry):
        if not self.dry:
            atomic(self.registry_path, registry)

    def git(self, *args, timeout=120):
        proc = subprocess.run(["git", "-C", str(self.repo_dir)] + list(args), capture_output=True, timeout=timeout)
        if proc.returncode:
            raise RuntimeError("git " + args[0] + " failed: " + proc.stderr.decode(errors="replace")[-1200:])
        return proc.stdout.decode().strip()

    @staticmethod
    def missing(path, reason):
        return urllib.error.HTTPError("lane:" + path, 404, reason, {}, None)

    def legacy(self, agent_id):
        # Agents launched on Cursor before the switch stay on Cursor until they finish; new ones are lanes.
        return agent_id not in self.load() and bool(os.environ.get("CURSOR_API_KEY"))

    def cursor(self, method, path, body=None):
        parts = path.strip("/").split("/")
        if method == "POST" and parts == ["agents"]:
            return self.create(body)
        if len(parts) >= 2 and parts[0] == "agents" and self.legacy(parts[1]):
            return Remote.cursor(self, method, path, body)
        if method == "GET" and len(parts) == 2 and parts[0] == "agents":
            agent = self.load().get(parts[1])
            if not agent:
                raise self.missing(path, "lane agent unknown")
            return {"id": parts[1], "status": "ACTIVE", "repos": [{"url": "https://github.com/" + REPO}],
                    "latestRunId": agent["runs"][-1]["id"] if agent["runs"] else None}
        if method == "POST" and len(parts) == 3 and parts[0] == "agents" and parts[2] == "runs":
            registry = self.load()
            if parts[1] not in registry:
                raise self.missing(path, "lane agent unknown")
            agent = registry[parts[1]]
            prompt = ("Your original assignment follows, then the controller's new instruction for the same "
                "assignment, branch and pull request.\n\n## Original assignment\n\n" + agent["assignment"]
                + "\n\n## New instruction\n\n" + body["prompt"]["text"])
            return {"run": {"id": self.dispatch(registry, parts[1], prompt)}}
        raise ValueError("Unsupported lane control request: " + method + " " + path)

    def create(self, body):
        agent_id = body["agentId"]
        registry = self.load()
        if agent_id in registry:
            # Same contract as Cursor: a conflicting create is verified by GET, never re-sent.
            raise urllib.error.HTTPError("lane:/agents", 409, "lane agent exists", {}, None)
        if not re.fullmatch(r"bc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", agent_id):
            raise ValueError("Lane agent ID is malformed")
        task = body["name"].removeprefix("Togavsu ")
        if not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,40}", task):
            raise ValueError("Lane task ID is malformed")
        starting = body["repos"][0]["startingRef"]
        if not re.fullmatch(r"[0-9a-f]{40}", starting):
            raise ValueError("Lane starting revision is malformed")
        registry[agent_id] = {"task": task, "branch": "claude/togavsu-" + task + "-" + agent_id[3:11],
            "starting_sha": starting, "assignment": body["prompt"]["text"], "created_at": now(), "runs": []}
        run_id = self.dispatch(registry, agent_id, body["prompt"]["text"])
        return {"agent": {"id": agent_id}, "run": {"id": run_id}}

    def check_paths(self, base, ref):
        # Pushing with owner credentials runs the branch's workflow files with repository secrets and any token
        # scope they request, before the controller's later protected-file halt sees the PR. So lane commits may
        # not touch .github/ at all, control files, links or submodules. CI changes go through a control PR.
        # -z: paths arrive verbatim, never C-quoted, so a quoted name cannot slip past a prefix check.
        raw = subprocess.run(["git", "-C", str(self.repo_dir), "log", "--format=", "--raw", "-z", "-m", "--no-renames",
                              "--no-abbrev", base + ".." + ref], capture_output=True, timeout=120)
        if raw.returncode:
            raise RuntimeError("git log failed: " + raw.stderr.decode(errors="replace")[-1200:])
        fields = raw.stdout.split(b"\0")
        if fields and fields[-1] == b"":
            fields.pop()
        if len(fields) % 2:
            raise LaneRejected("Lane change list is malformed")
        for meta, name in zip(fields[0::2], fields[1::2]):
            meta = meta.strip(b"\n").decode("ascii", "replace")
            path = name.decode("utf-8", "surrogateescape")
            if not meta.startswith(":") or not path:
                raise LaneRejected("Lane change list is malformed")
            modes = meta.lstrip(":").split()[:2]
            if any(m in {"120000", "160000"} for m in modes):
                raise LaneRejected("Lane commits add a symlink or submodule: " + path)
            if path in PROTECTED or path.startswith(("deploy/controller/", ".git/")) or path == ".gitmodules":
                raise LaneRejected("Lane commits change a protected control-plane file: " + path)
            if path.startswith(".github/"):
                raise LaneRejected("Lane commits change a GitHub workflow or setting file; propose it in the PR instead: " + path)

    def branch_head(self, branch):
        try:
            return self.gh("git/ref/heads/" + urllib.parse.quote(branch, safe="/"))["object"]["sha"]
        except RuntimeError as error:
            if "HTTP 404" in str(error):
                return None
            raise

    def dispatch(self, registry, agent_id, prompt):
        agent = registry[agent_id]
        packed = base64.b64encode(zlib.compress(prompt.encode(), 9)).decode()
        if len(packed) > LANE_PROMPT_LIMIT:
            raise ValueError("Lane prompt exceeds the workflow dispatch limit; nothing was sent")
        base = self.branch_head(agent["branch"]) or agent["starting_sha"]
        run_id = "run-" + str(uuid.uuid4())
        # Persist before the non-idempotent POST: a lost response leaves a findable run ID.
        agent["runs"].append({"id": run_id, "base": base, "dispatched_at": now(), "state": "dispatching"})
        self.store(registry)
        try:
            self.gh("actions/workflows/" + Path(LANE_WORKFLOW).name + "/dispatches", "POST", {"ref": "main", "inputs": {
                "agent_id": agent_id, "run_id": run_id, "task": agent["task"], "branch": agent["branch"],
                "base": base, "prompt_z": packed}})
        except RuntimeError as error:
            if not re.search(r"HTTP 4\d\d", str(error)):
                raise  # Ambiguous: the run may exist; run() finds it or reports it lost after the grace period.
            agent["runs"][-1]["state"] = "dispatch_failed"
            agent["runs"][-1]["error"] = str(error)[-300:]
            if len(agent["runs"]) == 1:
                registry.pop(agent_id)  # A refused create leaves no agent, like Cursor; the next launch retries.
            self.store(registry)
            raise
        agent["runs"][-1]["state"] = "dispatched"
        self.store(registry)
        return run_id

    def find_github_run(self, entry):
        since = entry["dispatched_at"][:10]
        runs, page = [], 1
        while page <= 5:
            rows = self.gh("actions/workflows/" + Path(LANE_WORKFLOW).name + "/runs?event=workflow_dispatch&per_page=100"
                "&created=%3E%3D" + since + "&page=" + str(page)).get("workflow_runs", [])
            runs += [r for r in rows if r.get("display_title") == "Lane " + entry["id"]]
            if len(rows) < 100:
                break
            page += 1
        if len(runs) > 1:
            raise ObservationError("More than one workflow run claims lane run " + entry["id"])
        return runs[0] if runs else None

    def run(self, record, run_id):
        if self.legacy(record["agent_id"]):
            return Remote.run(self, record, run_id)
        registry = self.load()
        agent = registry.get(record["agent_id"])
        entry = next((r for r in (agent or {}).get("runs", []) if r["id"] == run_id), None)
        if not entry:
            raise self.missing("/agents/" + record["agent_id"] + "/runs/" + run_id, "lane run unknown")
        if entry.get("github_run_id"):
            github = self.gh("actions/runs/" + str(entry["github_run_id"]))
        else:
            github = self.find_github_run(entry)
            if github:
                entry["github_run_id"] = github["id"]
                self.store(registry)
        if github and (github.get("path") != LANE_WORKFLOW or github.get("event") != "workflow_dispatch"
                or github.get("head_branch") != "main" or github.get("display_title") != "Lane " + run_id):
            raise ObservationError("Workflow run does not belong to the protected Lane agent workflow")
        if not github and (entry.get("state") == "dispatch_failed" or entry.get("lost") or
                (parse_time(entry["dispatched_at"]) or dt.datetime.now(dt.timezone.utc))
                < dt.datetime.now(dt.timezone.utc) - LANE_DISPATCH_GRACE):
            if not entry.get("lost") and not self.dry:
                entry["lost"] = now()  # GitHub never created the run; report it rather than wait forever.
                self.store(registry)
            status = "ERROR"
        elif not github or github.get("status") in LANE_QUEUED:
            status = "CREATING"
        elif github.get("status") != "completed":
            status = "RUNNING"
        else:
            status = LANE_OUTCOME.get(github.get("conclusion"), "ERROR")
            if not entry.get("published") and not entry.get("rejected"):
                if self.dry:
                    status = "RUNNING"  # A dry run never pushes, so the result is not yet published.
                else:
                    try:
                        entry["published"] = self.publish(record["agent_id"], agent, entry, github)
                    except LaneRejected as error:
                        entry["rejected"] = {"reason": str(error)[:600], "time": now()}
                    self.store(registry)
            if entry.get("rejected"):
                status = "ERROR"
        branch = {"branch": agent["branch"], "repoUrl": "https://github.com/" + REPO}
        if agent.get("pr_url"):
            branch["prUrl"] = agent["pr_url"]
        return {"status": status, "run_id": run_id, "git": {"branches": [branch]},
                "updated_at": (github or {}).get("updated_at"), "created_at": entry["dispatched_at"]}

    def publish(self, agent_id, agent, entry, github):
        artifacts = self.gh("actions/runs/" + str(github["id"]) + "/artifacts?per_page=100")["artifacts"]
        found = [a for a in artifacts if a["name"] == "lane-" + entry["id"] and not a["expired"]]
        if len(found) != 1:
            if github.get("conclusion") != "success":
                return {"head": None, "time": now(), "note": "no result artifact"}
            raise LaneRejected("Lane result artifact missing")
        if found[0].get("size_in_bytes", 0) > LANE_ARTIFACT_LIMIT:
            raise LaneRejected("Lane result artifact is oversized")
        blob = self.gh("actions/artifacts/" + str(found[0]["id"]) + "/zip", binary=True)
        with zipfile.ZipFile(io.BytesIO(blob)) as archive:
            names = {Path(i.filename).name: i for i in archive.infolist()}
            if "lane-result.json" not in names or names["lane-result.json"].file_size > 2_000_000:
                raise LaneRejected("Lane result is missing or oversized")
            result = json.loads(archive.read(names["lane-result.json"]))
            bundle = archive.read(names["lane.bundle"]) if "lane.bundle" in names else None
        expected = {"agent_id": agent_id, "run_id": entry["id"], "task": agent["task"], "branch": agent["branch"], "base": entry["base"]}
        for key, value in expected.items():
            if result.get(key) != value:
                raise LaneRejected("Lane result " + key + " does not match the dispatched run")
        head = result.get("head")
        if not isinstance(head, str) or not re.fullmatch(r"[0-9a-f]{40}", head):
            raise LaneRejected("Lane result head is malformed")
        published = {"head": head, "time": now()}
        if head != entry["base"]:
            if bundle is None:
                raise LaneRejected("Lane reported new commits without a bundle")
            current = self.branch_head(agent["branch"])
        if head != entry["base"] and current != head:  # Equal only when an earlier attempt already pushed.
            if current not in {None, entry["base"]} or (current is None and entry["base"] != agent["starting_sha"]):
                raise LaneRejected("Branch moved during the lane run; its result would not fast-forward")
            path = self.work_dir / (".lane-" + entry["id"] + ".bundle")
            ref = "refs/lane-import/" + entry["id"]
            try:
                path.write_bytes(bundle)
                # The base is often a squash merge made through the API, absent from this checkout.
                self.git("fetch", "--no-tags", "--quiet", self.push_url, entry["base"], timeout=300)
                self.git("bundle", "verify", str(path))
                self.git("fetch", "--no-tags", "--quiet", str(path), "refs/heads/lane:" + ref)
                if self.git("rev-parse", ref) != head:
                    raise LaneRejected("Lane bundle head does not match its result")
                proc = subprocess.run(["git", "-C", str(self.repo_dir), "merge-base", "--is-ancestor", entry["base"], head],
                                      capture_output=True, timeout=60)
                if proc.returncode:
                    raise LaneRejected("Lane commits do not descend from the dispatched base")
                self.check_paths(entry["base"], ref)
                self.git("push", "--quiet", self.push_url, ref + ":refs/heads/" + agent["branch"], timeout=300)
            finally:
                path.unlink(missing_ok=True)
                subprocess.run(["git", "-C", str(self.repo_dir), "update-ref", "-d", ref], capture_output=True, timeout=60)
        title = str(result.get("pr_title") or "").strip()[:200] or "Togavsu " + agent["task"]
        body = str(result.get("pr_body") or "")[:60_000]
        if self.branch_head(agent["branch"]) is None:
            return dict(published, note="no commits; no branch")
        if not agent.get("pr"):
            prs = self.gh("pulls?state=all&head=CharitonMedia:" + urllib.parse.quote(agent["branch"], safe=""))
            pr = prs[0] if len(prs) == 1 else self.gh("pulls", "POST", {"title": title, "head": agent["branch"],
                "base": "main", "body": body, "draft": False})
            agent["pr"], agent["pr_url"] = pr["number"], pr["html_url"]
        elif body.strip():
            pull = self.gh("pulls/" + str(agent["pr"]))
            if pull.get("body") != body and pull.get("state") == "open":
                self.gh("pulls/" + str(agent["pr"]), "PATCH", {"body": body})
        return published


class Controller:
    def __init__(self, remote, plan, state_dir, dry=False, mailer=None):
        self.remote, self.plan, self.dir, self.dry, self.mailer = remote, plan, Path(state_dir), dry, mailer
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
                        if not dispatched_run_seen(record, snapshot):
                            record["waiting"] = "Await observation of previously delivered correction"
                            return
                        window.update(attempts=1, last_dispatch_at=now())
                    windows[fingerprint] = window
            current = [windows[fingerprint] for fingerprint in fingerprints]
            window = record["repair_window"] = max(current, key=lambda w: w["attempts"])
            if record.get("stalled", {}).get("kind") == "no_progress":
                record.pop("stalled")
            ceiling = self.plan.get("max_corrections_per_assignment", DEFAULT_MAX_CORRECTIONS)
            if record.get("correction_volume", 0) - record.get("correction_base", 0) >= ceiling:
                record["stalled"] = {"kind": "correction_ceiling", "reason":
                    "Assignment reached " + str(ceiling) + " fix passes; further corrections are held. Integration lead must "
                    "inspect the existing agent, PR and review findings for a reworded repeat; observation and merge gates continue"}
                record.pop("waiting", None)
                self.save()
                return
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
            if dispatched_run_seen(record, snapshot):
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
            record["correction_volume"] = record.get("correction_volume", 0) + 1
            self.flag_volume(record)
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
        record.pop("last_dispatched_run_observed", None)
        record.pop("last_dispatch_stale_runs", None)
        record["last_dispatch_source_run"] = intent["previous_run"]
        record["last_dispatch_at"] = intent["time"]
        if record.get("halted") in {"Delivery outcome uncertain; no automatic replay",
                "Unreconciled delivery intent; refusing duplicate agent prompt"}:
            record.pop("halted")
        record.pop("waiting", None)
        self.save()

    def verify_dispatched_run(self, task, record, snapshot):
        # A newer latest run can hide the acknowledged run between ticks. Fetch
        # that exact run; any failure leaves the reservation in place.
        expected = record.get("last_dispatched_run")
        latest = snapshot.get("run_id")
        if (not isinstance(expected, str) or not expected or record.get("last_dispatched_run_observed")
                or not isinstance(latest, str) or not latest or latest == expected):
            return
        run = self.remote.run(record, expected)
        stale = record.get("last_dispatch_stale_runs", [])
        if latest in stale:
            # Reported earlier while the acknowledged run was unseen; newer only if creation times prove it.
            latest_at, expected_at = parse_time(snapshot.get("created_at")), parse_time(run.get("created_at"))
            if latest_at and expected_at and latest_at > expected_at:
                stale.remove(latest)
        record["last_dispatched_run_observed"] = True
        self.event(task, "Acknowledged run " + expected + " verified directly; later latest run " + latest + " is newer")
        self.save()

    def reconcile_intent(self, task, run_id, operator):
        # Operator path for an uncertain delivery: bind the persisted intent to a
        # run verified by ID. Never posts a prompt.
        record = self.tasks.get(task)
        if not record or not record.get("intent"):
            raise ValueError("Task " + str(task) + " has no outstanding delivery intent")
        if not isinstance(operator, str) or not operator.strip():
            raise ValueError("Operator name is required for an audited reconciliation")
        if not isinstance(run_id, str) or not run_id.strip():
            raise ValueError("Run ID is required")
        intent = record["intent"]
        if run_id in {intent.get("previous_run"), record.get("last_dispatched_run")}:
            raise ValueError("Run " + run_id + " predates this intent and cannot be its delivery")
        run = self.remote.run(record, run_id)
        created, after = run.get("created_at"), intent.get("time")
        if created and after:
            stamp = parse_time(created)
            if not stamp:
                raise ValueError("Run " + run_id + " has an unreadable creation time")
            if stamp < parse_time(after):
                raise ValueError("Run " + run_id + " was created before the intent was persisted")
            timing = "created " + created + " after intent " + after
        else:
            timing = "creation time unavailable; ID and agent verified only"
        if self.dry:
            print("WOULD reconcile " + task + " intent " + intent["key"] + " to run " + run_id, flush=True)
            return
        self.acknowledge(record, run_id)
        record["last_dispatched_run_observed"] = True  # Verified by ID just now.
        record.setdefault("reconciled", []).append({"time": now(), "operator": operator, "key": intent["key"],
            "run_id": run_id, "evidence": timing})
        self.event(task, "Operator " + operator + " reconciled delivery intent " + intent["key"] + " to run "
            + run_id + " (" + timing + "); no prompt sent")
        self.save()

    def rehome(self, task, operator):
        # Operator path: retire a stopped Cursor record so the next tick launches the task as a fresh lane.
        record = self.tasks.get(task)
        if self.plan.get("worker_backend") != "claude-actions" or not isinstance(self.remote, ActionsRemote):
            raise ValueError("Rehoming needs the claude-actions backend")
        if not record or record.get("completed"):
            raise ValueError("Task " + str(task) + " has no unfinished record")
        if not isinstance(operator, str) or not operator.strip():
            raise ValueError("Operator name is required for an audited rehome")
        if record["agent_id"] in self.remote.load():
            raise ValueError("Task " + task + " already runs as a lane")
        if record.get("pr") or record.get("merge_intent"):
            raise ValueError("Task " + task + " has a pull request; finish or close it first")
        if record.get("agent_status") not in {"ERROR", "EXPIRED", "CANCELLED"}:
            # A halt alone is not enough: a halted agent may still be running and could push untracked work.
            raise ValueError("Task " + task + " has no terminal Cursor status; rehome only a stopped agent")
        if self.dry:
            print("WOULD rehome " + task + " from Cursor agent " + record["agent_id"], flush=True)
            return
        self.state.setdefault("retired_records", {}).setdefault(task, []).append(
            {"time": now(), "operator": operator, "reason": "rehomed to claude-actions lane", "record": record})
        del self.tasks[task]
        self.event(task, "Operator " + operator + " retired Cursor agent " + record["agent_id"]
            + " for a fresh Claude Code lane launch")
        self.save()

    def flag_volume(self, record):
        # Soft supervision signal only: corrections and observation continue.
        threshold = self.plan.get("correction_attention_threshold", DEFAULT_CORRECTION_ATTENTION)
        if record.get("correction_volume", 0) >= threshold and not record.get("stalled"):
            record["stalled"] = {"kind": "high_correction_volume", "reason": "Assignment has had "
                + str(record["correction_volume"]) + " corrections; integration lead should check for a reworded repeated "
                "finding. Corrections continue until the per-assignment ceiling"}

    def reset_fixed_problems(self, task, record, kinds, evidence):
        # Fixed-key budgets count consecutive repeats; progress starts a new streak.
        windows = record.get("repair_problems", {})
        reset = []
        for kind in kinds:
            fingerprint = problem_fingerprints(kind)[0]
            found = windows.pop(fingerprint, None) is not None
            if record.get("repair_window", {}).get("fingerprint") == fingerprint:
                record.pop("repair_window")
                found = True
            if record.get("stalled", {}).get("kind") == "no_progress" and record["stalled"].get("fingerprint") == fingerprint:
                record.pop("stalled")
            if found:
                reset.append(kind)
        if reset:
            self.event(task, "Progress (" + evidence + ") reset consecutive budget for " + ", ".join(reset))

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
        validate_snapshot(snapshot)  # Direct callers cannot bypass the observation boundary.
        observe_dispatch(record, snapshot)
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
        self.flag_volume(record)
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
                    and record.get("stalled", {}).get("kind") in {None, "active_run", "high_correction_volume"}):
                record["stalled"] = {"kind": "active_run", "reason":
                    "Cursor run exceeded observation deadline; integration lead must inspect this run before any replacement",
                    "run_id": watch["run_id"], "since": watch["since"]}
            return False
        record.pop("active_watch", None)
        if record.get("stalled", {}).get("kind") == "active_run":
            record.pop("stalled")
        if snapshot["status"] == "FINISHED":
            self.reset_fixed_problems(task, record, ["interrupted"], "run finished")
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
        self.reset_fixed_problems(task, record, ["missing-pr"], "PR found")
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
            if record.get("last_submission"):
                self.reset_fixed_problems(task, record, FIXED_PROBLEMS, "new PR head")
            record.pop("merge_blocked", None)  # A held merge belongs to one head only.
            if record.get("stalled", {}).get("kind") == "merge_blocked":
                record.pop("stalled")
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
        self.reset_fixed_problems(task, record, ["draft"], "PR ready")
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
        infra = self.remote.infrastructure_failures(failures) if failures else {}
        if failures and {f["name"] for f in failures} <= set(infra):
            # Every failure is CI infrastructure: no fix pass can help.
            self.hold_infrastructure(task, record, head, infra)
            return False
        if record.pop("infra_hold", None) and record.get("stalled", {}).get("kind") == "infrastructure":
            record.pop("stalled")
            self.event(task, "Infrastructure hold cleared on " + head)
        if failures:
            code = [f for f in failures if f["name"] not in infra]
            aside = ("" if not infra else " These checks also failed because CI could not reach a server; they are not "
                     "yours to fix and will rerun: " + ", ".join(sorted(infra)) + ".")
            self.followup(task, record, snapshot, "tests:" + head,
                "Codex review passed, but CI failed on your existing PR #" + str(number) + ". Read the failing Actions logs, "
                "fix the causes with regression tests, rerun all applicable tests and update original PR evidence. "
                "Do not bypass checks or merge. Failures: " + json.dumps(code) + aside,
                problem={"failed_checks": sorted(c["name"] for c in code)})
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
        blocked = record.get("merge_blocked")
        if blocked and blocked["head"] != head:
            record.pop("merge_blocked")  # A new head is a new merge candidate.
            if record.get("stalled", {}).get("kind") == "merge_blocked":
                record.pop("stalled")
            blocked = None
        if blocked and blocked.get("held"):
            seen = [fresh.get("mergeable_state"), fresh.get("updated_at")]
            if seen[0] in MERGE_HOLD_STATES or seen == blocked.get("held_on"):
                record["waiting"] = "Merge held after repeated GitHub refusals; await a change to the pull request"
                return False
            # The pull request changed since the hold (threads resolved, review given): one more attempt.
            blocked.pop("held")
            blocked["attempts"] = MERGE_REFUSAL_LIMIT - 1
            if record.get("stalled", {}).get("kind") == "merge_blocked":
                record.pop("stalled")
            self.event(task, "Pull request state changed to " + str(fresh.get("mergeable_state")) + "; retrying held merge once")
        if self.dry:
            self.effect("protected merge PR " + str(number), lambda: None)
            return True
        record["merge_intent"] = {"head": head, "base": main, "time": now()}
        self.save()
        try:
            result = self.remote.gh("pulls/" + str(number) + "/merge", "PUT", {"sha": head, "merge_method": "squash"})
        except RuntimeError as error:
            if not MERGE_REFUSED.search(str(error)):
                raise
            result = {"merged": False, "message": " ".join(str(error).split())[-300:]}
        if not result.get("merged"):
            self.merge_refused(task, record, head, number, result.get("message") or "GitHub did not permit merge", fresh)
            return False
        record["merge_intent"]["result_sha"] = result["sha"]
        self.save()
        return self.verify_merge(task, record, self.remote.gh("pulls/" + str(number)))

    def hold_infrastructure(self, task, record, head, found):
        # Every failed check is CI infrastructure: rerun the failed jobs once, then hold and email.
        hold = record.get("infra_hold")
        if not hold or hold.get("head") != head:
            hold = record["infra_hold"] = {"head": head, "reruns": 0}
        names = ", ".join(sorted(found))
        if hold["reruns"] < 1:
            hold["reruns"] += 1
            self.save()
            for run_id in sorted({run for run, _ in found.values()}):
                self.effect("rerun failed jobs " + run_id, lambda run_id=run_id: self.remote.gh(
                    "actions/runs/" + run_id + "/rerun-failed-jobs", "POST"))
            self.event(task, "CI could not reach a server it needs (" + names + "); reran once, no correction sent")
        else:
            line = next(iter(found.values()))[1]
            record["stalled"] = {"kind": "infrastructure", "head": head, "reason":
                "CI could not reach a server it needs, twice on the same revision (" + names + "): " + line
                + ". The code is not the cause, so no fix pass is sent."}
            record.pop("waiting", None)
            self.save()

    def merge_refused(self, task, record, head, number, reason, fresh):
        # Protections stay in force; a refusal on a second cycle holds retries and alerts.
        blocked = record.get("merge_blocked")
        if not blocked or blocked["head"] != head:
            blocked = record["merge_blocked"] = {"head": head, "since": now(), "attempts": 0}
        blocked.update(attempts=blocked["attempts"] + 1, reason=reason, last=now())
        self.event(task, "GitHub refused merge of PR #" + str(number) + " (attempt " + str(blocked["attempts"]) + "): " + reason)
        if blocked["attempts"] >= MERGE_REFUSAL_LIMIT:
            # Remember what GitHub showed at the hold, so the same state never releases it twice.
            blocked.update(held=True, held_on=[fresh.get("mergeable_state"), fresh.get("updated_at")],
                           holds=blocked.get("holds", 0) + 1)
            record["stalled"] = {"kind": "merge_blocked", "head": head, "hold": blocked["holds"], "reason": "GitHub refused to merge PR #"
                + str(number) + " on " + str(blocked["attempts"]) + " controller cycles: " + reason
                + ". Merge retries are held until the pull request changes"}
        self.save()

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
        record.pop("merge_blocked", None)
        self.reset_fixed_problems(task, record, FIXED_PROBLEMS, "verified merge")
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
            "Work in your isolated hosted workspace (a Cursor-hosted VM or a GitHub-hosted lane runner) on your own branch using synthetic data only. "
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
            threshold = self.plan.get("correction_attention_threshold", DEFAULT_CORRECTION_ATTENTION)
            attention = [task for task, rec in self.tasks.items() if not rec.get("completed")
                and task not in self.plan.get("held_tasks", [])
                and (any(rec.get(field) for field in ("halted", "stalled", "last_error", "launch_uncertain", "intent"))
                    or rec.get("correction_volume", 0) >= threshold)]
            self.state["health"] = "needs_attention" if attention or self.state.get("tick_error") else "observing"
            if attention or self.state.get("tick_error"):
                self.state["last_result"] = "Needs supervision: " + (", ".join(attention) or "controller request failed")
            self.save()
            self.alert()
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
        held = set(self.plan.get("held_tasks", []))
        snapshots, unverified = {}, {}
        # Observe ALL workers before any dispatch; a later dictionary entry may already occupy a slot.
        # A held task is frozen: its record is never changed and it is not corrected, refreshed or
        # merged. A held worker that may still be running keeps its slot, so the cap stays real.
        for task, record in list(self.tasks.items()):
            if task not in specs or record.get("completed"):
                continue
            if task in held:
                try:
                    snapshot = self.remote.agent(record)
                    validate_snapshot(snapshot)
                except AgentNotFound:
                    # As for an unheld task: only a never-submitted, never-dispatched create frees the slot.
                    if (record.get("submitted") or record.get("halted") or record.get("intent")
                            or "last_dispatched_run" in record or record.get("launch_uncertain")):
                        self.occupied += 1
                    continue
                except Exception:
                    self.occupied += 1  # An unreadable held worker may still be running.
                    continue
                if snapshot["status"] in ACTIVE or record.get("intent") or unobserved_run(record, snapshot):
                    self.occupied += 1
                continue
            try:
                snapshot = self.remote.agent(record)
                validate_snapshot(snapshot)
                snapshots[task] = snapshot
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
                record["last_error"] = observation_error(error)
                continue
            snapshot = snapshots[task]
            try:
                self.verify_dispatched_run(task, record, snapshot)
            except Exception as error:
                # The record stays unobserved: it keeps its slot and its PR stays blocked.
                unverified[task] = observation_error(error)
            observe_dispatch(record, snapshot)
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
                if task in unverified:
                    record["last_error"] = unverified[task]
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
            if task in self.plan.get("held_tasks", []):
                continue  # Operator hold: launch only after Patrick approves this assignment.
            try:
                self.launch(spec, main)
            except Exception as error:
                self.tasks[task]["last_error"] = "Launch outcome unconfirmed: " + type(error).__name__
                self.event(task, "Launch not confirmed; durable agent ID retained: " + type(error).__name__)
                self.save()
        self.state["last_result"] = "Waiting for agents/checks or ready work"
        self.save()

    def watch_progress(self):
        # Track repeating control errors and unchanged task state between ticks.
        held = set(self.plan.get("held_tasks", []))
        for task, rec in self.tasks.items():
            if rec.get("completed") or task in held:
                rec.pop("error_watch", None)
                rec.pop("idle_watch", None)
                continue
            error = rec.get("last_error")
            watch = rec.get("error_watch")
            if not error:
                rec.pop("error_watch", None)
            elif watch and watch["text"] == error:
                watch["ticks"] += 1
            else:
                rec["error_watch"] = {"text": error, "since": now(), "ticks": 1}
            digest = hashlib.sha256(json.dumps({f: rec.get(f) for f in PROGRESS_FIELDS}, sort_keys=True).encode()).hexdigest()
            if rec.get("idle_watch", {}).get("digest") != digest:
                rec["idle_watch"] = {"digest": digest, "since": now()}
        error = self.state.get("tick_error")
        watch = self.state.get("tick_error_watch")
        if not error:
            self.state.pop("tick_error_watch", None)
        elif watch and watch["text"] == error:
            watch["ticks"] += 1
        else:
            self.state["tick_error_watch"] = {"text": error, "since": now(), "ticks": 1}

    def alert_conditions(self):
        # Returns {condition key: (task, headline, detail, what the controller does now)}.
        idle_limit = self.plan.get("idle_alert_seconds", DEFAULT_IDLE_ALERT_SECONDS)
        found = {}
        held = set(self.plan.get("held_tasks", []))
        for task, rec in self.tasks.items():
            if rec.get("completed") or task in held:
                continue  # A held task is frozen on purpose; it is not a stall.
            if rec.get("halted"):
                found["halted:" + task + ":" + rec["halted"]] = (task, "Stopped", rec["halted"],
                    "It stays stopped until the integration lead clears it.")
            stalled = rec.get("stalled")
            if stalled:
                marker = stalled.get("fingerprint") or stalled.get("head") or stalled.get("run_id") or ""
                if stalled.get("hold"):
                    marker += ":" + str(stalled["hold"])  # A merge held again after its one retry alerts again.
                action = {"merge_blocked": "Merge retries are held until the pull request changes, for example when its "
                              "review threads are resolved or a new commit lands.",
                          "no_progress": "No more agent runs for this problem until the integration lead resumes it.",
                          "correction_ceiling": "No more corrections until the integration lead inspects it.",
                          "infrastructure": "No fix passes are sent while CI cannot reach its servers; the "
                              "controller clears this when the checks pass."}.get(
                              stalled["kind"], "Flagged for inspection; the controller keeps watching.")
                found["stalled:" + task + ":" + stalled["kind"] + ":" + marker] = (
                    task, "Stalled (" + stalled["kind"] + ")", stalled["reason"], action)
            watch = rec.get("error_watch")
            if watch and watch["ticks"] >= ERROR_ALERT_TICKS:
                found["error:" + task + ":" + hashlib.sha256(watch["text"].encode()).hexdigest()] = (
                    task, "Same control error on " + str(watch["ticks"]) + " ticks", watch["text"],
                    "The controller keeps checking; nothing is pushed or merged while this error repeats.")
            idle = rec.get("idle_watch")
            if (idle and not rec.get("halted") and not stalled and rec.get("agent_status") not in ACTIVE
                    and age(idle["since"]) >= idle_limit):
                found["idle:" + task + ":" + idle["digest"]] = (
                    task, "No progress for " + str(int(age(idle["since"]) // 60)) + " minutes",
                    rec.get("waiting") or "No change in this assignment's state while no agent run was active.",
                    "The controller keeps watching; nothing has changed since " + idle["since"] + ".")
        watch = self.state.get("tick_error_watch")
        if watch and watch["ticks"] >= ERROR_ALERT_TICKS:
            found["tick-error:" + hashlib.sha256(watch["text"].encode()).hexdigest()] = (
                "controller", "Controller tick failed " + str(watch["ticks"]) + " times in a row", watch["text"],
                "Each tick stops at this error before doing any work.")
        return found

    def alert(self):
        # Never raises: a failed alert is recorded and shown as needs_attention, then retried later.
        to = self.plan.get("alert_email")
        if not to or self.dry:
            return
        try:
            self.watch_progress()
            found = {hashlib.sha256(key.encode()).hexdigest()[:20]: item for key, item in self.alert_conditions().items()}
            sent = self.state.setdefault("alerts_sent", {})
            for key in [k for k in sent if k not in found]:
                sent.pop(key)  # Cleared; a recurrence alerts again.
            new = [(key, item) for key, item in found.items() if key not in sent]
            if not new:
                self.state.pop("alert_failure", None)
                return
            failure = self.state.get("alert_failure")
            if failure and age(failure["time"]) < ALERT_RETRY_SECONDS:
                self.state["health"] = "needs_attention"
                return
            if self.mailer is None:
                raise RuntimeError("No mailer configured for alert_email")
            tasks = sorted({item[0] for _, item in new})
            subject = "Togavsu build needs you: " + (tasks[0] if len(tasks) == 1 else str(len(tasks)) + " assignments")
            body = "The Togavsu build controller stopped or flagged the work below and won't keep retrying it silently.\n\n"
            for _, (task, headline, detail, action) in new:
                rec = self.tasks.get(task, {})
                body += task + (" (PR #" + str(rec["pr"]) + ")" if rec.get("pr") else "") + ": " + headline + "\n"
                body += detail.strip() + "\n" + action + "\n"
                if rec.get("pr"):
                    body += "https://github.com/" + REPO + "/pull/" + str(rec["pr"]) + "\n"
                body += "\n"
            body += "Controller status: https://github.com/" + REPO + "/issues/1\n"
            self.mailer.send(to, subject, body)
            for key, _ in new:
                sent[key] = now()
            self.state.pop("alert_failure", None)
            self.event("controller", "Alert emailed: " + ", ".join(tasks))
        except Exception as error:
            self.state["alert_failure"] = {"time": now(), "error": type(error).__name__ + ": " + str(error)[:300]}
            self.state["health"] = "needs_attention"
            self.event("controller", "Alert email failed: " + self.state["alert_failure"]["error"])
        finally:
            self.save()

    def resume(self, task, operator):
        # Operator path: after a fix lands, give a stalled assignment one fresh attempt. Halts are not touched.
        record = self.tasks.get(task)
        if not isinstance(operator, str) or not operator.strip():
            raise ValueError("Operator name is required for an audited resume")
        if not record or record.get("completed"):
            raise ValueError("Task " + str(task) + " has no unfinished record")
        stalled = record.get("stalled") or {}
        if stalled.get("kind") not in {"no_progress", "merge_blocked", "correction_ceiling", "infrastructure"}:
            raise ValueError("Task " + task + " is not stalled on a repeated problem, a fix-pass limit, CI "
                             "infrastructure or a held merge")
        if self.dry:
            print("WOULD resume " + task + " from " + stalled["kind"], flush=True)
            return
        if stalled["kind"] == "no_progress":
            # Every exhausted problem gets the fresh attempt: a correction for [A, B] spends both budgets.
            for window in record.get("repair_problems", {}).values():
                if window.get("attempts", 0) >= self.plan["max_repairs"]:
                    window["attempts"] = 0
            if record.get("repair_window", {}).get("attempts", 0) >= self.plan["max_repairs"]:
                record["repair_window"]["attempts"] = 0
        elif stalled["kind"] == "correction_ceiling":
            # A fresh allowance of fix passes; the lifetime count stays in correction_volume.
            record["correction_base"] = record.get("correction_volume", 0)
        elif stalled["kind"] == "infrastructure":
            record.pop("infra_hold", None)
        else:
            record.pop("merge_blocked", None)
        record.pop("stalled")
        self.state.setdefault("operator_resumes", []).append({"time": now(), "task": task, "operator": operator,
            "stalled": stalled})
        self.event(task, "Operator " + operator + " resumed " + stalled["kind"] + " stall")
        self.save()

    def write_status(self):
        if self.dry:
            return
        summary = {"updated": now(), "tick": self.state["tick"], "health": self.state.get("health", "unknown"),
            "last_productive_progress": self.state.get("last_productive_progress"),
            "tick_error": self.state.get("tick_error"), "publication_error": self.state.get("publication_error"),
            "alert_failure": self.state.get("alert_failure"), "tasks": {
            k: {field: v[field] for field in ("pr", "agent_status", "repair_count", "correction_volume", "repair_window", "last_submission", "active_watch",
                "completed", "halted", "stalled", "last_error", "waiting", "launch_uncertain", "intent") if field in v}
            for k, v in self.tasks.items()}}
        for task in self.plan.get("held_tasks", []):
            if task in summary["tasks"] and not summary["tasks"][task].get("completed"):
                summary["tasks"][task]["held"] = True
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
            if summary["alert_failure"]:
                body += "Alert email failed: " + summary["alert_failure"]["error"] + "\n\n"
            body += "| Assignment | PR | State | Corrections |\n|---|---|---|---|\n"
            held = set(self.plan.get("held_tasks", []))
            for task, rec in self.tasks.items():
                status = ("merged" if rec.get("completed") else "HELD: frozen by the plan until the hold is released"
                    if task in held else "STOPPED: " + rec["halted"] if rec.get("halted")
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
    parser.add_argument("--reconcile-intent", nargs=2, metavar=("TASK", "RUN_ID"),
        help="bind TASK's outstanding delivery intent to RUN_ID after verifying it; runs no tick")
    parser.add_argument("--rehome", metavar="TASK",
        help="retire TASK's stopped Cursor record so the next tick launches it as a lane; runs no tick")
    parser.add_argument("--resume", metavar="TASK",
        help="give TASK's repeated-problem, fix-pass-limit, CI-infrastructure or held-merge stall a fresh attempt; runs no tick")
    parser.add_argument("--test-alert", action="store_true", help="email a test alert to the plan's alert_email; runs no tick")
    parser.add_argument("--operator", help="name recorded with --reconcile-intent, --rehome or --resume")
    args = parser.parse_args()
    if (args.reconcile_intent or args.rehome or args.resume) and not args.operator:
        parser.error("--reconcile-intent, --rehome and --resume require --operator")
    plan = json.loads(Path(args.plan).read_text())
    state = Path(args.state)
    state.mkdir(parents=True, exist_ok=True)
    with open(state / "controller.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("Another controller tick owns the lock")
            return
        # worker_backend selects who runs assignments; every gate after the PR is unchanged.
        remote = (ActionsRemote(state, state.resolve().parent, args.dry_run)
                  if plan.get("worker_backend") == "claude-actions" else Remote())
        controller = Controller(remote, plan, state, args.dry_run, Mailer())
        if args.test_alert:
            if not plan.get("alert_email"):
                raise SystemExit("Plan has no alert_email")
            if args.dry_run:
                print("WOULD email a test alert to " + plan["alert_email"], flush=True)
                return
            controller.mailer.send(plan["alert_email"], "Togavsu build controller: test alert",
                "This is a test of the Togavsu build controller's stall alerts. No action is needed.\n")
        elif args.reconcile_intent:
            controller.reconcile_intent(*args.reconcile_intent, args.operator)
        elif args.rehome:
            controller.rehome(args.rehome, args.operator)
        elif args.resume:
            controller.resume(args.resume, args.operator)
        else:
            controller.tick()


if __name__ == "__main__":
    main()
