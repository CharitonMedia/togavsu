"""Launch bounded Cursor-hosted agents; durable IDs prevent duplicate submissions."""
import argparse
import base64
import fcntl
import json
import os
import subprocess
import urllib.error
import urllib.request
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / ".build-state"
REPO = "https://github.com/CharitonMedia/togavsu"


def api(method, path, payload=None):
    key = os.environ.get("CURSOR_API_KEY")
    if not key:
        raise RuntimeError("Set CURSOR_API_KEY in the control environment; never commit it")
    request = urllib.request.Request(
        "https://api.cursor.com/v1" + path,
        data=None if payload is None else json.dumps(payload).encode(),
        headers={"Authorization": "Basic " + base64.b64encode((key + ":").encode()).decode(),
                 "Content-Type": "application/json"}, method=method)
    with urllib.request.urlopen(request, timeout=40) as response:
        return json.load(response)


def save(path, data):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(data, indent=2) + "\n")
    temp.replace(path)


def load(path):
    return json.loads(path.read_text()) if path.exists() else {}


def snapshot(record):
    agent = api("GET", "/agents/" + record["agent_id"])
    run_id = agent.get("latestRunId")
    run = api("GET", "/agents/" + agent["id"] + "/runs/" + run_id) if run_id else {}
    return {"agent_id":agent["id"], "url":agent.get("url"), "status":run.get("status", agent.get("status")),
            "run_id":run_id, "git":run.get("git", {}), "result":run.get("result", "")}


def dependencies_pass(task, completed):
    for dep in task["depends_on"]:
        evidence = completed.get(dep, {})
        if not evidence.get("pr") or not evidence.get("merge_sha"):
            raise RuntimeError("Dependency " + dep + " needs verified merged PR and phase/test evidence")
        raw = subprocess.check_output(
            ["gh","pr","view",str(evidence["pr"]),"--repo","CharitonMedia/togavsu",
             "--json","mergedAt,mergeCommit"],text=True)
        pr = json.loads(raw)
        if not pr.get("mergedAt") or pr.get("mergeCommit",{}).get("oid") != evidence["merge_sha"]:
            raise RuntimeError("Dependency merge evidence is stale: " + dep)
        if not evidence.get("report"):
            raise RuntimeError("Dependency acceptance report missing: " + dep)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command",choices=["launch","status"])
    parser.add_argument("wave",nargs="?",default="bootstrap")
    args = parser.parse_args()
    STATE.mkdir(exist_ok=True)
    with open(STATE/"launcher.lock","w") as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        records = load(STATE/"agents.json")
        plan = json.loads((ROOT/"docs/build/tasks.json").read_text())
        if args.command == "status":
            for task_id, record in records.items():
                try:
                    value = snapshot(record)
                    record.update(value)
                    print(json.dumps({"task":task_id, **{k:v for k,v in value.items() if k != "result"}}))
                except urllib.error.HTTPError as exc:
                    print(json.dumps({"task":task_id,"http_error":exc.code,"agent_id":record["agent_id"]}))
            save(STATE/"agents.json",records)
            return
        if subprocess.check_output(["git","status","--porcelain"],cwd=ROOT,text=True).strip():
            raise RuntimeError("Commit and push the control checkout before launching")
        sha = subprocess.check_output(["git","rev-parse","HEAD"],cwd=ROOT,text=True).strip()
        remote = subprocess.check_output(["git","ls-remote","origin","refs/heads/main"],cwd=ROOT,text=True).split()[0]
        if sha != remote:
            raise RuntimeError("Launch from the exact pushed main revision")
        selected = [t for t in plan["tasks"] if t["wave"] == args.wave]
        if not selected:
            raise RuntimeError("Unknown wave")
        completed = load(STATE/"completed.json")
        for task in selected:
            dependencies_pass(task,completed)
        active = 0
        for record in records.values():
            try:
                if snapshot(record)["status"] in ["CREATING","RUNNING","WAITING_FOR_BACKGROUND_WORK","ACTIVE"]:
                    active += 1
            except urllib.error.HTTPError as exc:
                if exc.code != 404:
                    raise
        fresh = [t for t in selected if t["id"] not in records]
        if active + len(fresh) > plan["max_implementation_agents"]:
            raise RuntimeError("Four-agent concurrency limit reached")
        for task in selected:
            if task["id"] in records and records[task["id"]].get("submitted"):
                print(task["id"] + ": already submitted " + records[task["id"]]["agent_id"])
                continue
            record = records.setdefault(task["id"],{"agent_id":"bc-"+str(uuid.uuid4()),"starting_sha":sha})
            if record["starting_sha"] != sha:
                raise RuntimeError("Uncertain submission belongs to another revision; reconcile before relaunch")
            save(STATE/"agents.json",records)
            prompt = (
                "Implement Togavsu assignment " + task["id"] + ". Read AGENTS.md, the full togavsu-core-prd.md, "
                "docs/build/WORKFLOW.md and your task in docs/build/tasks.json. "
                "Work only inside this Cursor-hosted VM on your own branch. No client data or production access. "
                "Owned paths: " + ", ".join(task["owned_paths"]) + ". "
                "PRD sections: " + ", ".join(task["prd_sections"]) + ". "
                "Acceptance IDs (full behavior may span later integration): " + ", ".join(task["acceptance_ids"]) + ". "
                + task["prompt"] + " Do the work, run appropriate checks, commit and push explicit paths, "
                "and open a small pull request. Do not merge, push main, change the PRD or review gate, "
                "claim pending product acceptance, or stop to ask Patrick about settled decisions. "
                "Include task ID, actual results, pending dependencies and simplification/debt count in the PR.")
            payload = {"agentId":record["agent_id"],"name":"Togavsu "+task["id"],
                       "prompt":{"text":prompt},"repos":[{"url":REPO,"startingRef":sha}],
                       "env":{"type":"cloud"},"autoCreatePR":True,"workOnCurrentBranch":False}
            try:
                result = api("POST","/agents",payload)
                agent = result["agent"]
                record.update(submitted=True,url=agent.get("url"),run_id=result.get("run",{}).get("id"))
            except urllib.error.HTTPError as exc:
                if exc.code == 409:
                    record.update(snapshot(record),submitted=True)
                else:
                    record["submission_http_error"] = exc.code
                    save(STATE/"agents.json",records)
                    raise RuntimeError("Cursor rejected " + task["id"] + " with HTTP " + str(exc.code)) from None
            save(STATE/"agents.json",records)
            print(json.dumps({"task":task["id"],**record}))


if __name__ == "__main__":
    main()
