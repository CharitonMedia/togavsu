"""Backup and restore over the running Compose stack (AC-018, AC-040, AC-050).

usage: python3 -m tests.e2e.backup_e2e STEP BASE_URL STATE_FILE [ARGS...]

Steps, run in order by scripts/ci/test-backup-e2e.sh. Each step reads and updates
STATE_FILE and prints "PASS: ..." only after its assertions ran.

  setup               first-run setup, a staff user, tokens, synthetic records and files
  denied              the staff session and token get 403 on every backup route
  hashes OUT          SHA-256 of every file version and rendition, downloaded via the API
  destinations        add network path and S3 destinations and test them
  backup NET S3 OUT   back up, download to OUT, compare with the destination copies
  mcp                 the backup tools are offered on /mcp to an admin token
  resetup             first-run on the wiped install with the same backup passphrase
  restore ARCHIVE     upload the downloaded archive and restore from it
  signin              the original admin signs in again (password and TOTP)

Synthetic data only. The local Caddy CA is not trusted, so TLS is not verified.
"""
import hashlib
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from tests.e2e.first_run import Client, totp

PASSPHRASE = "backup-e2e-synthetic-passphrase"
ADMIN = {"email": "avery@firm.example", "password": "backup-e2e-admin-password", "name": "Avery Admin"}
SECOND = {"email": "second@firm.example", "password": "backup-e2e-second-password", "name": "Sam Second"}
STAFF = {"email": "staff@firm.example", "password": "backup-e2e-staff-password", "name": "Riley Staff"}


def fail(msg):
    raise SystemExit("FAIL: " + msg)


def load(path):
    with open(path) as f:
        return json.load(f)


def save(path, state):
    with open(path, "w") as f:
        json.dump(state, f, indent=1)


def raw(c, method, path, data=None, headers=None, bearer=None, timeout=120):
    """A request with a raw body; returns (status, bytes)."""
    req = urllib.request.Request(c.base + path, data=data, method=method)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    if bearer:
        req.add_header("Authorization", "Bearer " + bearer)
    else:
        if c.csrf and method not in ("GET", "HEAD"):
            req.add_header("X-CSRF-Token", c.csrf)
        if c.cookies:
            req.add_header("Cookie", "; ".join("%s=%s" % kv for kv in c.cookies.items()))
    try:
        with c.opener.open(req, timeout=timeout) as res:
            return res.status, res.read()
    except urllib.error.HTTPError as err:
        return err.code, err.read()


def jcall(c, method, path, body=None, bearer=None):
    data = None if body is None else json.dumps(body).encode()
    hdr = {"Content-Type": "application/json"} if data is not None else {}
    status, out = raw(c, method, path, data, hdr, bearer)
    try:
        doc = json.loads(out or b"null")
    except ValueError:
        doc = None
    return status, doc, out


def must(c, want, method, path, body=None, bearer=None):
    status, doc, out = jcall(c, method, path, body, bearer)
    if status != want:
        fail("%s %s: want %d, got %d: %s" % (method, path, want, status, out[:400]))
    return doc


def client(base, state, who="admin"):
    s = state[who]
    c = Client(base, dict(s["cookies"]))
    c.csrf = s["csrf"]
    return c


def sign_in(base, user, secret=None):
    """Password then TOTP; a user without two-factor enrolls. Returns (client, secret)."""
    c = Client(base)
    res = c.must(200, "POST", "/api/v1/auth/login", {"email": user["email"], "password": user["password"]})
    if res.get("enrollment_token"):
        e = c.must(200, "POST", "/api/v1/auth/totp/enroll", {"enrollment_token": res["enrollment_token"]})
        secret = e["secret"]
        res = c.must(200, "POST", "/api/v1/auth/totp/confirm", {"enrollment_token": e["enrollment_token"], "code": totp(secret)})
    elif res.get("totp_token"):
        if not secret:
            fail("%s needs a TOTP code and no secret is saved" % user["email"])
        res = c.must(200, "POST", "/api/v1/auth/totp", {"totp_token": res["totp_token"], "code": totp(secret)})
    if res.get("status") != "authenticated":
        fail("sign-in of %s did not authenticate: %s" % (user["email"], res))
    c.csrf = res["csrf_token"]
    return c, secret


def first_run(base, admin):
    c = Client(base)
    if c.must(200, "GET", "/api/v1/setup") != {"ready": False}:
        fail("the install is not waiting for first-run setup")
    enroll = c.must(201, "POST", "/api/v1/setup", {
        "firm_name": "Backup Synthetic Firm", "time_zone": "America/Chicago", "admin_name": admin["name"],
        "admin_email": admin["email"], "password": admin["password"], "backup_passphrase": PASSPHRASE})
    done = c.must(200, "POST", "/api/v1/auth/totp/confirm",
                  {"enrollment_token": enroll["enrollment_token"], "code": totp(enroll["secret"])})
    if done.get("status") != "authenticated":
        fail("two-factor confirm did not sign in: %s" % done)
    c.csrf = done["csrf_token"]
    if c.must(200, "GET", "/api/v1/setup") != {"ready": True}:
        fail("install is not ready after setup")
    return c, enroll["secret"]


def upload(c, matter, name, media, body):
    sha = hashlib.sha256(body).hexdigest()
    up = must(c, 201, "POST", "/api/v1/file-uploads",
              {"file_name": name, "matter_id": matter, "media_type": media, "size_bytes": len(body), "sha256": sha})
    chunk = int(up.get("chunk_size_bytes") or len(body)) or len(body)
    off = 0
    while off < len(body):
        part = body[off:off + chunk]
        status, out = raw(c, "PUT", "/api/v1/file-uploads/" + up["id"], part,
                          {"Content-Type": "application/octet-stream",
                           "Content-Range": "bytes %d-%d/%d" % (off, off + len(part) - 1, len(body))})
        if status != 200:
            fail("upload chunk of %s: %d %s" % (name, status, out[:300]))
        off += len(part)
    must(c, 200, "POST", "/api/v1/file-uploads/%s/complete" % up["id"])
    f = must(c, 201, "POST", "/api/v1/files", {"upload_id": up["id"], "matter_id": matter, "name": name})
    return f["id"], sha


def step_setup(base, path):
    admin, secret = first_run(base, ADMIN)
    state = {"admin_user": ADMIN, "admin_secret": secret}
    # A staff user, created on the admin page, who enrolls two-factor at first sign-in.
    form = urllib.parse.urlencode({"name": STAFF["name"], "email": STAFF["email"], "password": STAFF["password"],
                                   "roles_sent": "1", "roles": "staff", "csrf": admin.csrf}).encode()
    status, out = raw(admin, "POST", "/admin/users", form, {"Content-Type": "application/x-www-form-urlencoded"})
    if status != 303:
        fail("create staff user: %d %s" % (status, out[:300]))
    me = must(admin, 200, "GET", "/api/v1/me")
    staff, _ = sign_in(base, STAFF)
    staff_me = must(staff, 200, "GET", "/api/v1/me")
    staff_id = (staff_me.get("user") or staff_me).get("id")
    tok = must(admin, 201, "POST", "/api/v1/api-tokens",
               {"name": "staff synthetic", "owner_user_id": staff_id, "scopes": ["matters:read"]})
    atok = must(admin, 201, "POST", "/api/v1/api-tokens", {"name": "admin backups", "scopes": ["backups:admin"]})
    # Synthetic records.
    area = must(admin, 201, "POST", "/api/v1/practice-areas", {"name": "Probate", "stages": ["Intake", "Open"]})
    matters = []
    for i in range(3):
        m = must(admin, 201, "POST", "/api/v1/matters",
                 {"name": "Backup Estate %d" % i, "practice_area_id": area["id"], "billing_type": "hourly"})
        matters.append(m["id"])
    for i in range(2):
        must(admin, 201, "POST", "/api/v1/contacts", {"kind": "person", "given_name": "Synthetic%d" % i, "family_name": "Client"})
    # Doc Tender on a synthetic OpenAI-compatible server (tests/e2e/fake_model.py), so the
    # text files get Markdown copies.
    for key, value in (("ai.compatible_base_url", "http://fakeai:8000/v1"), ("ai.compatible_api_key", "synthetic-key"),
                       ("ai.primary_provider", "compatible"), ("ai.primary_fast_model", "synthetic-fast"),
                       ("ai.primary_strong_model", "synthetic-strong")):
        must(admin, 200, "PATCH", "/api/v1/settings/" + key, {"value": value})
    files = {}
    blobs = [("will-draft.txt", "text/plain", b"Last will of a synthetic testator.\n" * 40),
             ("inventory.md", "text/markdown", b"# Inventory\n\n- one synthetic chair\n- one synthetic table\n"),
             ("scan.bin", "application/octet-stream", os.urandom(300000))]
    for name, media, body in blobs:
        fid, sha = upload(admin, matters[0], name, media, body)
        files[fid] = name
    end = time.time() + 240
    want = [fid for fid, name in files.items() if not name.endswith(".bin")]
    while True:
        have = [fid for fid in want if any(r["kind"] == "markdown" for r in
                must(admin, 200, "GET", "/api/v1/files/%s/renditions" % fid)["data"])]
        if len(have) == len(want):
            break
        if time.time() > end:
            fail("Doc Tender made Markdown copies of %d of %d text files" % (len(have), len(want)))
        time.sleep(3)
    print("PASS: Doc Tender made a Markdown copy of each of the %d text files" % len(want))
    state.update({"admin": {"cookies": admin.cookies, "csrf": admin.csrf, "id": (me.get("user") or me).get("id")},
                  "staff": {"cookies": staff.cookies, "csrf": staff.csrf},
                  "staff_token": tok["secret"], "admin_token": atok["secret"],
                  "matters": matters, "files": files})
    save(path, state)
    print("PASS: setup (admin, staff, %d matters, %d files)" % (len(matters), len(files)))


DENIED = [("GET", "/api/v1/backups", None), ("POST", "/api/v1/backups", None),
          ("GET", "/api/v1/backup-destinations", None),
          ("POST", "/api/v1/backup-destinations", {"kind": "network_path", "name": "x", "retention_days": 7, "settings": {"path": "/tmp"}}),
          ("POST", "/api/v1/restores", {"backup_id": "20260101T000000Z-0000000000000000", "passphrase": "x"}),
          ("GET", "/api/v1/backups/20260101T000000Z-0000000000000000/download", None)]


def step_denied(base, path):
    state = load(path)
    staff = client(base, state, "staff")
    for method, p, body in DENIED:
        for how, bearer in (("session", None), ("token", state["staff_token"])):
            status, _, out = jcall(staff, method, p, body, bearer)
            if status != 403:
                fail("staff %s %s %s: want 403, got %d: %s" % (how, method, p, status, out[:200]))
    print("PASS: staff session and staff token get 403 on %d backup routes" % len(DENIED))


def file_objects(c, state):
    """Every version and rendition of the synthetic files with its downloaded SHA-256."""
    out = {}
    for fid in state["files"]:
        vs = must(c, 200, "GET", "/api/v1/files/%s/versions" % fid)["data"]
        for v in vs:
            status, body = raw(c, "GET", "/api/v1/files/%s/versions/%s/download" % (fid, v["id"]))
            if status != 200:
                fail("download version %s: %d %s" % (v["id"], status, body[:200]))
            got = hashlib.sha256(body).hexdigest()
            if got != v["sha256"]:
                fail("version %s downloads as %s, recorded %s" % (v["id"], got, v["sha256"]))
            out["version:" + v["id"]] = got
        rs = must(c, 200, "GET", "/api/v1/files/%s/renditions" % fid)["data"]
        for r in rs:
            status, body = raw(c, "GET", "/api/v1/files/%s/renditions/%s" % (fid, r["id"]))
            if status != 200:
                fail("download rendition %s: %d" % (r["id"], status))
            out["%s:%s" % (r["kind"], r["id"])] = hashlib.sha256(body).hexdigest()
    return out


def step_hashes(base, path, out_file):
    state = load(path)
    c = client(base, state)
    got = file_objects(c, state)
    if len([k for k in got if k.startswith("version:")]) < len(state["files"]):
        fail("fewer versions than files: %s" % got)
    with open(out_file, "w") as f:
        json.dump(got, f, indent=1, sort_keys=True)
    md = len([k for k in got if k.startswith("markdown:")])
    print("PASS: downloaded %d objects (%d Markdown copies) and recorded SHA-256" % (len(got), md))


def sha_file(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


def find(root, name):
    hits = []
    for d, _, fs in os.walk(root):
        hits += [os.path.join(d, f) for f in fs if f == name]
    return hits


def step_destinations(base, path):
    state = load(path)
    c = client(base, state)
    net = must(c, 201, "POST", "/api/v1/backup-destinations",
               {"kind": "network_path", "name": "Office NAS", "retention_days": 30, "settings": {"path": "/mnt/netpath"}})
    s3 = must(c, 201, "POST", "/api/v1/backup-destinations",
              {"kind": "s3", "name": "Offsite S3", "retention_days": 30,
               "settings": {"endpoint": "http://s3:9000", "region": "us-east-1", "bucket": "togavsu-backups",
                            "prefix": "firm", "access_key": "bkaccesskey", "secret_key": "bksecretkey-synthetic"}})
    for d in (net, s3):
        must(c, 200, "POST", "/api/v1/backup-destinations/%s/test" % d["id"])
    names = sorted(d["name"] for d in must(c, 200, "GET", "/api/v1/backup-destinations")["data"])
    if names != ["Office NAS", "Offsite S3"]:
        fail("destination list: %s" % names)
    print("PASS: network path and S3 destinations added and tested")


def step_backup(base, path, netpath_host, s3_host, out_dir):
    state = load(path)
    c = client(base, state)
    b = must(c, 202, "POST", "/api/v1/backups")
    if b.get("status") != "completed" or not b.get("checksum"):
        fail("backup did not complete: %s" % b)
    ids = [x["id"] for x in must(c, 200, "GET", "/api/v1/backups")["data"]]
    if b["id"] not in ids:
        fail("backup %s is not listed: %s" % (b["id"], ids))
    os.makedirs(out_dir, exist_ok=True)
    archive = os.path.join(out_dir, b["id"] + ".tgbk")
    status, body = raw(c, "GET", "/api/v1/backups/%s/download" % b["id"], timeout=600)
    if status != 200:
        fail("download backup: %d %s" % (status, body[:200]))
    with open(archive, "wb") as f:
        f.write(body)
    local = sha_file(archive)
    if local != b["checksum"] or len(body) != b.get("bytes", len(body)):
        fail("downloaded archive sha256 %s (%d bytes), backup says %s (%s bytes)" % (local, len(body), b["checksum"], b.get("bytes")))
    print("PASS: backup %s downloaded to the admin's computer (%d bytes, sha256 matches)" % (b["id"], len(body)))
    # The supervisor writes the network path as root with private modes; open it up for
    # this host-side check (a throwaway root container, the same image the stack uses).
    subprocess.run([os.environ.get("DOCKER", "docker"), "run", "--rm", "-v", os.path.abspath(netpath_host) + ":/n",
                    "postgres:17", "chmod", "-R", "a+rX", "/n"], check=True, stdout=subprocess.DEVNULL)
    for label, root in (("network path", netpath_host), ("S3", s3_host)):
        hits = find(root, b["id"] + ".tgbk")
        if len(hits) != 1:
            fail("%s holds %d copies of %s.tgbk" % (label, len(hits), b["id"]))
        if sha_file(hits[0]) != local:
            fail("%s copy %s differs from the download" % (label, hits[0]))
        side = find(root, b["id"] + ".sha256")
        if len(side) != 1 or open(side[0]).read().split()[0] != local:
            fail("%s checksum sidecar is missing or wrong" % label)
        print("PASS: %s copy %s matches the download" % (label, os.path.relpath(hits[0], root)))
    state["backup"] = b
    state["archive"] = archive
    save(path, state)


def step_mcp(base, path):
    state = load(path)
    c = Client(base)
    status, doc, out = jcall(c, "POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, state["admin_token"])
    if status != 200 or not doc or "result" not in doc:
        fail("mcp tools/list: %d %s" % (status, out[:300]))
    names = {t["name"] for t in doc["result"]["tools"]}
    want = {"createBackup", "listBackup", "downloadBackup", "deleteBackup", "createBackupDestination", "updateBackupDestination",
            "testBackupDestination", "deleteBackupDestination", "createRestore", "getRestore", "createBackupUpload"}
    if want - names:
        fail("mcp does not offer %s" % sorted(want - names))
    status, doc, out = jcall(c, "POST", "/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
                                                "params": {"name": "listBackup", "arguments": {}}}, state["admin_token"])
    if status != 200 or not doc or "result" not in doc or doc["result"].get("isError"):
        fail("mcp listBackup: %d %s" % (status, out[:300]))
    # tools/list is the same for every caller; the call itself is refused for staff.
    for name in ("listBackup", "createBackup"):
        status, doc, out = jcall(c, "POST", "/mcp", {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
                                                    "params": {"name": name, "arguments": {}}}, state["staff_token"])
        out = out.decode("utf-8", "replace")
        refused = status in (401, 403) or (doc or {}).get("error") or ((doc or {}).get("result") or {}).get("isError")
        if not refused or "403" not in out and "forbidden" not in out.lower():
            fail("mcp %s by a staff token was not refused with 403: %d %s" % (name, status, out[:300]))
    print("PASS: /mcp offers %d backup tools; an admin token lists backups and a staff token is refused" % len(want))


def step_resetup(base, path):
    state = load(path)
    c, secret = first_run(base, SECOND)
    status, doc, _ = jcall(c, "GET", "/api/v1/matters")
    if status != 200 or (doc or {}).get("data"):
        fail("the wiped install still lists matters: %d %s" % (status, doc))
    state["second"] = {"cookies": c.cookies, "csrf": c.csrf}
    save(path, state)
    print("PASS: wiped install has no matters; first-run done again with the same backup passphrase")


def wait_health(base, timeout=300):
    c = Client(base)
    end = time.time() + timeout
    while time.time() < end:
        try:
            status, _, _, _ = c.call("GET", "/api/v1/health")
            if status == 200:
                return True
        except Exception:
            pass
        time.sleep(2)
    return False


def step_restore(base, path, archive):
    state = load(path)
    c = client(base, state, "second")
    if os.environ.get("BK_RESTORE_BY_ID"):
        # Debug only: restore the stored backup by id instead of the uploaded download.
        rec = must(c, 201, "POST", "/api/v1/restores", {"backup_id": state["backup"]["id"], "passphrase": PASSPHRASE})
        print("NOTE: restore %s started by backup_id (BK_RESTORE_BY_ID), not from the upload" % rec["id"])
        return wait_restore(base, state, rec)
    with open(archive, "rb") as f:
        body = f.read()
    status, out = raw(c, "POST", "/api/v1/backup-uploads", body, {"Content-Type": "application/octet-stream"}, timeout=600)
    if status != 201:
        fail("upload archive: %d %s" % (status, out[:300]))
    up = json.loads(out)
    if up["checksum"] != hashlib.sha256(body).hexdigest() or up["bytes"] != len(body):
        fail("upload receipt does not match the archive: %s" % up)
    status, doc, out = jcall(c, "POST", "/api/v1/restores", {"upload_token": up["upload_token"], "passphrase": "wrong-passphrase"})
    if status < 400 or status >= 500:
        fail("restore with a wrong passphrase answered %d: %s" % (status, out[:200]))
    rec = must(c, 201, "POST", "/api/v1/restores", {"upload_token": up["upload_token"], "passphrase": PASSPHRASE})
    if rec.get("interrupts_users") is not True:
        fail("restore record: %s" % rec)
    print("PASS: archive uploaded (sha256 matches) and restore %s started; a wrong passphrase was refused" % rec["id"])
    wait_restore(base, state, rec)


def wait_restore(base, state, rec):
    # The core stops and restarts. The restored database has the original admin, so the
    # status is read with the admin token restored with it.
    time.sleep(3)
    end = time.time() + 600
    last = None
    while time.time() < end:
        if not wait_health(base, 30):
            continue
        status, doc, out = jcall(Client(base), "GET", "/api/v1/restores/" + rec["id"], None, state["admin_token"])
        if status == 200:
            last = doc
            if doc["status"] in ("completed", "failed"):
                break
        time.sleep(3)
    if not last or last["status"] != "completed":
        fail("restore did not complete: %s" % last)
    if not wait_health(base, 60):
        fail("core is not healthy after the restore")
    print("PASS: restore %s completed and the core is healthy" % rec["id"])


def step_signin(base, path, hashes_before):
    state = load(path)
    c, _ = sign_in(base, state["admin_user"], state["admin_secret"])
    status, doc, _ = jcall(c, "GET", "/api/v1/matters")
    ids = {m["id"] for m in (doc or {}).get("data", [])}
    if status != 200 or not set(state["matters"]) <= ids:
        fail("restored matters missing: %d %s" % (status, ids))
    got = file_objects(c, state)
    with open(hashes_before) as f:
        want = json.load(f)
    if got != want:
        missing = sorted(set(want) - set(got))
        diff = sorted(k for k in want if k in got and got[k] != want[k])
        fail("restored objects differ: missing %s, changed %s, extra %s" % (missing, diff, sorted(set(got) - set(want))))
    print("PASS: original admin signed in; %d restored versions and renditions match their SHA-256" % len(got))


if __name__ == "__main__":
    steps = {"setup": (step_setup, 0), "denied": (step_denied, 0), "hashes": (step_hashes, 1), "destinations": (step_destinations, 0), "backup": (step_backup, 3),
             "mcp": (step_mcp, 0), "resetup": (step_resetup, 0), "restore": (step_restore, 1), "signin": (step_signin, 1)}
    if len(sys.argv) < 4 or sys.argv[1] not in steps or len(sys.argv) != 4 + steps[sys.argv[1]][1]:
        raise SystemExit(__doc__)
    steps[sys.argv[1]][0](*sys.argv[2:])
