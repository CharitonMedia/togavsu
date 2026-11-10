"""Drive first-run setup on a running install over HTTPS (AC-001).

usage: python3 -m tests.e2e.first_run setup BASE_URL STATE_FILE
       python3 -m tests.e2e.first_run verify BASE_URL STATE_FILE

setup  checks that a fresh install serves only setup, completes setup with two-factor,
       creates a practice area and a matter, and saves the session to STATE_FILE.
verify reuses that session after a core restart and checks the matter survived.
Synthetic data only. The local Caddy CA is not trusted, so TLS is not verified.
"""
import base64
import hashlib
import hmac
import http.cookiejar
import json
import ssl
import struct
import sys
import time
import urllib.error
import urllib.request


def totp(secret, at=None):
    key = base64.b32decode(secret.upper() + "=" * (-len(secret) % 8))
    counter = int((at or time.time()) // 30)
    mac = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    off = mac[-1] & 0x0F
    code = (struct.unpack(">I", mac[off:off + 4])[0] & 0x7FFFFFFF) % 1000000
    return "%06d" % code


class Client:
    def __init__(self, base, cookies=None):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ctx), urllib.request.HTTPCookieProcessor(self.jar), NoRedirect())
        self.csrf = ""
        self.cookies = cookies or {}

    def call(self, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if self.csrf:
            req.add_header("X-CSRF-Token", self.csrf)
        if self.cookies:
            req.add_header("Cookie", "; ".join("%s=%s" % kv for kv in self.cookies.items()))
        try:
            with self.opener.open(req, timeout=30) as res:
                status, raw, loc = res.status, res.read(), res.headers.get("Location", "")
        except urllib.error.HTTPError as err:
            status, raw, loc = err.code, err.read(), err.headers.get("Location", "")
        for c in self.jar:
            self.cookies[c.name] = c.value
        try:
            doc = json.loads(raw or b"null")
        except ValueError:
            doc = None
        return status, doc, raw.decode("utf-8", "replace"), loc

    def must(self, want, method, path, body=None):
        status, doc, raw, _ = self.call(method, path, body)
        if status != want:
            raise SystemExit("%s %s: want %d, got %d: %s" % (method, path, want, status, raw[:400]))
        return doc


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def setup(base, state_file):
    c = Client(base)
    if c.must(200, "GET", "/api/v1/setup") != {"ready": False}:
        raise SystemExit("fresh install is not waiting for setup")
    status, _, _, loc = c.call("GET", "/")
    if status != 303 or not loc.endswith("/setup"):
        raise SystemExit("home did not send a fresh install to setup: %d %s" % (status, loc))
    status, _, raw, _ = c.call("GET", "/setup")
    if status != 200 or 'name="firm_name"' not in raw:
        raise SystemExit("setup screen did not render")
    status, _, _, _ = c.call("GET", "/api/v1/matters")
    if status != 409:
        raise SystemExit("API before setup answered %d, want 409" % status)
    enroll = c.must(201, "POST", "/api/v1/setup", {
        "firm_name": "Compose Synthetic Firm", "time_zone": "America/Chicago", "admin_name": "Casey Compose",
        "admin_email": "casey@firm.example", "password": "compose-synthetic-password",
        "backup_passphrase": "compose-synthetic-backup"})
    done = c.must(200, "POST", "/api/v1/auth/totp/confirm",
                  {"enrollment_token": enroll["enrollment_token"], "code": totp(enroll["secret"])})
    if done.get("status") != "authenticated" or not done.get("recovery_codes"):
        raise SystemExit("two-factor confirm did not sign in: %s" % done)
    c.csrf = done["csrf_token"]
    if c.must(200, "GET", "/api/v1/setup") != {"ready": True}:
        raise SystemExit("install is not ready after setup")
    area = c.must(201, "POST", "/api/v1/practice-areas", {"name": "Probate", "stages": ["Intake"]})
    matter = c.must(201, "POST", "/api/v1/matters", {"name": "Compose Estate", "practice_area_id": area["id"], "billing_type": "hourly"})
    rows = c.must(200, "GET", "/api/v1/audit-log?entity_type=matter&entity_id=" + matter["id"])["data"]
    if not rows or any(r["source"] != "ui" or not r["actor_id"] for r in rows):
        raise SystemExit("matter writes are not audited with actor and source: %s" % rows)
    with open(state_file, "w") as f:
        json.dump({"cookies": c.cookies, "csrf": c.csrf, "matter": matter["id"]}, f)
    print("first-run setup passed: matter %s, %d audit rows" % (matter["number"], len(rows)))


def verify(base, state_file):
    with open(state_file) as f:
        state = json.load(f)
    c = Client(base, state["cookies"])
    c.csrf = state["csrf"]
    got = c.must(200, "GET", "/api/v1/matters/" + state["matter"])
    if got.get("name") != "Compose Estate":
        raise SystemExit("matter did not survive the core restart: %s" % got)
    if c.must(200, "GET", "/api/v1/setup") != {"ready": True}:
        raise SystemExit("setup state lost on restart")
    print("restart check passed: session and matter survived")


if __name__ == "__main__":
    if len(sys.argv) != 4 or sys.argv[1] not in ("setup", "verify"):
        raise SystemExit(__doc__)
    {"setup": setup, "verify": verify}[sys.argv[1]](sys.argv[2], sys.argv[3])
