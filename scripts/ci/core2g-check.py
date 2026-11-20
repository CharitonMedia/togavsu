#!/usr/bin/env python3
"""Run one local 2g check without a deadline; redact supplied secrets in its log.

Usage: python3 scripts/ci/core2g-check.py NAME A|B COMMAND [ARG ...]
Logs and JSON evidence go to /out/core2g-checks (or TOGAVSU_CHECK_OUT).
The command runs unchanged; its exit status is returned to the caller.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    name, server, *command = sys.argv[1:]
    if not re.fullmatch(r"[a-zA-Z0-9_-]+", name) or server not in ("A", "B") or not command:
        raise SystemExit("expected NAME A|B COMMAND [ARG ...]")
    env = os.environ.copy()
    if server == "B":
        env["TOGAVSU_TEST_DATABASE_URL"], env["TOGAVSU_TEST_DATABASE_URL_B"] = (
            env["TOGAVSU_TEST_DATABASE_URL_B"], env["TOGAVSU_TEST_DATABASE_URL"])
    out = Path(env.get("TOGAVSU_CHECK_OUT", "/out/core2g-checks"))
    out.mkdir(parents=True, exist_ok=True)
    temp = out / (name + "-tmp")
    temp.mkdir(exist_ok=True)
    env["TMPDIR"] = str(temp)
    secrets = sorted({v for k, v in env.items() if len(v) >= 4 and
                      re.search(r"PASSWORD|SECRET|TOKEN|DATABASE_URL|API_KEY", k)}, key=len, reverse=True)

    def redact(text):
        for secret in secrets:
            text = text.replace(secret, "[redacted]")
        return re.sub(r"(postgres(?:ql)?://)[^\s\"']+", r"\1[redacted]", text)

    counts = {"pass": 0, "fail": 0, "skip": 0}
    cases = {key: [] for key in counts}
    with (out / (name + ".log")).open("w") as log:
        process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, text=True)
        for line in process.stdout:
            log.write(redact(line))
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Action") in counts and "Test" in event and "/" not in event["Test"]:
                action = event["Action"]
                counts[action] += 1
                cases[action].append(event["Package"] + "." + event["Test"])
        code = process.wait()
    result = dict(name=name, database=server, command=command, exit_code=code,
                  top_level_tests=counts, cases=cases)
    (out / (name + ".json")).write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k != "cases"}))
    return code


if __name__ == "__main__":
    sys.exit(main())
