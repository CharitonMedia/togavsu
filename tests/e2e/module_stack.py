"""AC-012 on a running Compose stack: the sample module installs from a local file, asks
for its permissions, shows its widget and tab, receives events and uninstalls, all
while the core keeps running, with the module in a supervisor-run container.

usage: python3 -m tests.e2e.module_stack BASE_URL STATE_FILE PACKAGE PUBKEY_PEM

STATE_FILE is the signed-in admin from `tests.e2e.first_run setup`. The caller checks
the module container and the core container's start time with docker. Synthetic data only.
"""
import json
import sys
import time
import urllib.parse
import uuid

from tests.e2e.first_run import Client


def multipart(path, data):
    boundary = "togavsu-" + uuid.uuid4().hex
    body = (("--%s\r\nContent-Disposition: form-data; name=\"package\"; filename=\"%s\"\r\n"
             "Content-Type: application/json\r\n\r\n") % (boundary, path.rsplit("/", 1)[-1])).encode()
    body += data + ("\r\n--%s--\r\n" % boundary).encode()
    return body, "multipart/form-data; boundary=" + boundary


def upload(c, path, pkg_path):
    import urllib.request
    import urllib.error
    with open(pkg_path, "rb") as f:
        body, ctype = multipart(pkg_path, f.read())
    req = urllib.request.Request(c.base + path, data=body, method="POST")
    req.add_header("Content-Type", ctype)
    req.add_header("X-CSRF-Token", c.csrf)
    req.add_header("Cookie", "; ".join("%s=%s" % kv for kv in c.cookies.items()))
    try:
        with c.opener.open(req, timeout=120) as res:
            return res.status, json.loads(res.read() or b"null")
    except urllib.error.HTTPError as err:
        raw = err.read()
        try:
            return err.code, json.loads(raw)
        except ValueError:
            return err.code, raw.decode("utf-8", "replace")


def wait(what, fn, seconds=120):
    end = time.time() + seconds
    while True:
        got = fn()
        if got:
            return got
        if time.time() > end:
            raise SystemExit("timed out waiting for " + what)
        time.sleep(2)


def main(base, state_file, pkg, pub):
    with open(state_file) as f:
        state = json.load(f)
    c = Client(base, state["cookies"])
    c.csrf = state["csrf"]
    with open(pub) as f:
        c.must(201, "POST", "/api/v1/trusted-signing-keys", {"name": "Compose sample key", "public_key_pem": f.read()})
    status, doc = upload(c, "/api/v1/modules", pkg)
    if status not in (200, 201, 202):
        raise SystemExit("install from a local file: %d %s" % (status, doc))
    mod = c.must(200, "GET", "/api/v1/modules/matter-clock")
    pending = [p["scope"] for p in mod.get("permissions", []) if not p.get("approved")]
    if not pending:
        raise SystemExit("the install did not ask for its permissions: %s" % mod)
    print("install asked for: " + ", ".join(sorted(pending)))
    c.must(200, "POST", "/api/v1/modules/matter-clock/permissions",
           {"decisions": [{"scope": s, "decision": "approve"} for s in pending]})
    wait("the module to be installed", lambda: c.must(200, "GET", "/api/v1/modules/matter-clock").get("status") == "installed")
    widgets = c.must(200, "GET", "/api/v1/widgets")
    if '"matter-clock.hours"' not in json.dumps(widgets):
        raise SystemExit("the module's widget is not offered: %s" % json.dumps(widgets)[:400])
    nav = json.dumps(c.must(200, "GET", "/api/v1/nav/context"))
    if "matter-clock" not in nav:
        raise SystemExit("the module is not in the sidebar context: %s" % nav[:400])

    last = {}

    def framed(page):
        # The shell frames a module page with a short-lived context token for the viewer.
        tok = c.must(201, "POST", "/api/v1/modules/matter-clock/context-tokens", {})["token"]
        status, _, raw, _ = c.call("GET", "/m/matter-clock" + page + "?context=" + urllib.parse.quote(tok))
        last[page] = "%d %s" % (status, raw[:200])
        return raw if status == 200 and "<" in raw else None
    try:
        widget = wait("the module container to serve its widget through the core", lambda: framed("/widget"))
        tab = wait("the module container to serve its tab through the core", lambda: framed("/tab"))
    except SystemExit:
        print("last frame answers: %s" % last)
        raise
    print("widget and tab served by the container (%d and %d bytes)" % (len(widget), len(tab)))

    activity = c.must(201, "POST", "/api/v1/activity-types", {"name": "Compose drafting"})
    c.must(201, "POST", "/api/v1/time-entries", {
        "matter_id": state["matter"], "worked_on": time.strftime("%Y-%m-%d", time.gmtime()), "increments": 3,
        "description": "Compose synthetic time", "activity_type_id": activity["id"], "billable": True, "rate_cents": 10000})

    def delivered():
        rows = c.must(200, "GET", "/api/v1/webhook-deliveries?module_id=matter-clock").get("data", [])
        return [r for r in rows if str(r.get("action", r.get("event", ""))).startswith("time_entry.") and r.get("status") in ("delivered", "succeeded", "ok")] or None
    rows = wait("the module container to receive the time entry event", delivered, 180)
    print("events delivered to the container: %d" % len(rows))

    c.must(200, "POST", "/api/v1/modules/matter-clock/uninstall", {"data": "keep", "confirmation": "matter-clock"})
    status, _, _, _ = c.call("GET", "/api/v1/modules/matter-clock")
    gone = c.must(200, "GET", "/api/v1/widgets")
    if '"matter-clock.hours"' in json.dumps(gone):
        raise SystemExit("the widget stayed after uninstall")
    print("uninstalled with data kept; module status answer %d" % status)


if __name__ == "__main__":
    if len(sys.argv) != 5:
        raise SystemExit(__doc__)
    main(*sys.argv[1:])
