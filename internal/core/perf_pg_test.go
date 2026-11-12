package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// TestReleasePerfP95 is AC-017: p95 page and API response under 300 ms on seed data of
// 50 users, 20,000 matters and 1,000,000 time entries.
//
// It runs only with TOGAVSU_PERF=1 (and then fails, not skips, without a database). It
// seeds a database named togavsu_perf once and keeps it for later runs
// (TOGAVSU_PERF_RESEED=1 starts over), starts the composed core behind an httptest
// server, signs in a staff user and an admin over HTTP, and times every listed page and
// API call through the whole handler stack: TOGAVSU_PERF_N requests each (default 200)
// after a warm-up, TOGAVSU_PERF_CONCURRENCY at a time (default 4). It prints p50, p95 and
// max per endpoint and fails when any endpoint's p95, or the overall p95, is 300 ms or more.
func TestReleasePerfP95(t *testing.T) {
	if os.Getenv("TOGAVSU_PERF") != "1" {
		t.Skip("set TOGAVSU_PERF=1 to run the AC-017 performance test")
	}
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TOGAVSU_PERF=1 needs TOGAVSU_TEST_DATABASE_URL")
	}
	dsn, fresh := perfDatabase(t, raw)
	cfg := Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", MailEvery: -1, DataDir: perfDataDir(t), NoBackground: true}
	var handler atomic.Pointer[http.Handler]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*handler.Load()).ServeHTTP(w, r) }))
	defer srv.Close()
	open := func() *App {
		start := time.Now()
		app, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		h := app.Handler()
		handler.Store(&h)
		t.Logf("core start: %s", time.Since(start).Round(time.Millisecond))
		return app
	}
	app := open()
	if fresh {
		start := time.Now()
		seed := seedPerf(t, app, srv.URL)
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
		app = open() // practice loads the documents and projects matters and contacts
		seedPerfTables(t, app.Pool)
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
		app = open() // trust rebuilds balances from the seeded ledger
		perfStoreAccounts(t, app, seed)
		if _, err := app.Pool.Exec(`ANALYZE`); err != nil {
			t.Fatal(err)
		}
		t.Logf("seed total: %s", time.Since(start).Round(time.Second))
	}
	defer func() { _ = app.Close() }()
	perfCheckVolume(t, app)

	admin, staff := perfSignIn(t, app, srv.URL, "admin"), perfSignIn(t, app, srv.URL, "staff")
	staffID := str(t, staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	m := string(seedID(1, 10)) // staff-visible, billed, not walled
	inv := string(seedID(20, 10))
	walled := string(seedID(1, 51)) // walled away from the staff user
	if r := staff.do("GET", "/api/v1/matters/"+walled, nil); r.code < 400 {
		t.Fatalf("staff read a walled matter: %d", r.code)
	}
	if r := staff.do("GET", "/api/v1/time-entries?matter_id="+walled, nil); r.code == http.StatusOK && strings.Contains(r.body, walled) {
		t.Fatalf("staff listed time on a walled matter")
	}
	type ep struct {
		name, path string
		adminOnly  bool
	}
	eps := []ep{
		{"page home", "/", false},
		{"page matters", "/practice/matters", false},
		{"page matter", "/practice/matters/" + m, false},
		{"page activity", "/practice/activity", false},
		{"page notes", "/practice/notes", false},
		{"page search", "/practice/search?q=2025-00011", false},
		{"page time", "/billing/time", false},
		{"page time matter", "/billing/time?matter_id=" + m, false},
		{"page invoices", "/billing/invoices", false},
		{"page invoice", "/billing/invoices/" + inv, false},
		{"page unbilled", "/billing/reports/unbilled-time", false},
		{"page ar aging", "/billing/reports/ar-aging", false},
		{"page trust ledger", "/trust/ledger", false},
		{"page trust balances", "/trust/balances", false},
		{"page trust matter", "/trust/matters/" + m, false},
		{"page calendar", "/calendar", false},
		{"page admin", "/admin", true},
		{"api matters", "/api/v1/matters", false},
		{"api matter", "/api/v1/matters/" + m, false},
		{"api matter timeline", "/api/v1/matters/" + m + "/timeline", false},
		{"api time", "/api/v1/time-entries", false},
		{"api time matter", "/api/v1/time-entries?matter_id=" + m, false},
		{"api time user+dates", "/api/v1/time-entries?user_id=" + staffID + "&from_date=2026-01-01&to_date=2026-03-31", false},
		{"api search number", "/api/v1/search?q=2025-00011", false},
		{"api search phone", "/api/v1/search?q=" + url.QueryEscape("+15550000010"), false},
		{"api search invoice", "/api/v1/search?q=S-000011", false},
		{"api unbilled", "/api/v1/reports/unbilled-time", false},
		{"api ar aging", "/api/v1/reports/ar-aging", false},
		{"api trust balances", "/api/v1/trust/balances", false},
		{"api trust ledger", "/api/v1/trust-ledger?matter_id=" + m, false},
		{"api nav context", "/api/v1/nav/context?entity_type=matter&entity_id=" + m, false},
		{"api invoices", "/api/v1/invoices", false},
		{"api expenses matter", "/api/v1/expenses?matter_id=" + m, false},
		{"api notes matter", "/api/v1/notes?matter_id=" + m, false},
		{"api calendar", "/api/v1/calendar-events?from=2026-03-01T00:00:00Z&to=2026-04-01T00:00:00Z", false},
		{"api widget open matters", "/api/v1/widgets/my-open-matters", false},
	}
	n, conc := perfEnvInt("TOGAVSU_PERF_N", 200), perfEnvInt("TOGAVSU_PERF_CONCURRENCY", 4)
	if path := os.Getenv("TOGAVSU_PERF_CPUPROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); _ = f.Close() }()
	}
	only := os.Getenv("TOGAVSU_PERF_ONLY") // a substring of endpoint names, for diagnosis
	const limit = 300 * time.Millisecond
	var all []time.Duration
	var failed []string
	var out strings.Builder
	fmt.Fprintf(&out, "\nAC-017 n=%d concurrency=%d\n%-8s %-26s %5s %9s %9s %9s %s\n", n, conc, "as", "endpoint", "n", "p50", "p95", "max", "result")
	for _, who := range []struct {
		name string
		c    *client
	}{{"staff", staff}, {"admin", admin}} {
		for _, e := range eps {
			if e.adminOnly && who.name != "admin" || only != "" && !strings.Contains(e.name, only) {
				continue
			}
			lat, status, err := perfRun(who.c, e.path, n, conc)
			res := "ok"
			switch {
			case err != nil:
				res = "error: " + err.Error()
			case status != http.StatusOK:
				res = fmt.Sprintf("status %d", status)
			}
			p50, p95, mx := perfPct(lat, 50), perfPct(lat, 95), perfPct(lat, 100)
			if res == "ok" && p95 >= limit {
				res = "p95 >= 300ms"
			}
			if res != "ok" {
				failed = append(failed, who.name+" "+e.name+": "+res)
			}
			all = append(all, lat...)
			line := fmt.Sprintf("%-8s %-26s %5d %9s %9s %9s %s\n", who.name, e.name, len(lat), perfMs(p50), perfMs(p95), perfMs(mx), res)
			out.WriteString(line)
			fmt.Print(line)
		}
	}
	overall := perfPct(all, 95)
	fmt.Fprintf(&out, "overall  %-26s %5d %9s %9s %9s\n", "all requests", len(all), perfMs(perfPct(all, 50)), perfMs(overall), perfMs(perfPct(all, 100)))
	t.Log(out.String())
	fmt.Println(out.String())
	if only != "" {
		failed = append(failed, "TOGAVSU_PERF_ONLY ran a subset; this is a diagnostic run, not an AC-017 result")
	}
	if overall >= limit {
		failed = append(failed, "overall p95 "+perfMs(overall))
	}
	if len(failed) > 0 {
		t.Fatalf("AC-017 failed:\n%s", strings.Join(failed, "\n"))
	}
}

func perfEnvInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func perfMs(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }

// perfPct is the nearest-rank percentile; 100 is the max.
func perfPct(lat []time.Duration, p int) time.Duration {
	if len(lat) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), lat...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	k := (p*len(s) + 99) / 100
	if k < 1 {
		k = 1
	}
	return s[k-1]
}

// perfRun warms path up, then times n full requests (headers and body read) with conc
// workers. A path whose warm-up takes over five seconds is sampled 8 times instead of n,
// so a hopeless endpoint is still measured and reported without stalling the run.
func perfRun(c *client, path string, n, conc int) ([]time.Duration, int, error) {
	one := func() (time.Duration, int, error) {
		req, err := http.NewRequest("GET", c.base+path, nil)
		if err != nil {
			return 0, 0, err
		}
		start := time.Now()
		res, err := c.http.Do(req)
		if err != nil {
			return 0, 0, err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return time.Since(start), res.StatusCode, nil
	}
	var slow bool
	for i := 0; i < perfEnvInt("TOGAVSU_PERF_WARMUP", 5); i++ {
		d, code, err := one()
		if err == nil && code != http.StatusOK {
			// Say why: the first part of the error body.
			if r, gerr := c.http.Get(c.base + path); gerr == nil {
				b, _ := io.ReadAll(io.LimitReader(r.Body, 200))
				_ = r.Body.Close()
				err = fmt.Errorf("%s", strings.Join(strings.Fields(string(b)), " "))
			}
		}
		if err != nil || code != http.StatusOK {
			return nil, code, err
		}
		if d > 5*time.Second {
			slow = true
			break
		}
	}
	if slow {
		n = 8
	}
	lat := make([]time.Duration, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	status, var1 := http.StatusOK, error(nil)
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				d, code, err := one()
				lat[i] = d
				if err != nil || code != http.StatusOK {
					mu.Lock()
					status, var1 = code, err
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return lat, status, var1
}

// perfDatabase returns the kept perf database and whether it must be seeded.
func perfDatabase(t *testing.T, raw string) (string, bool) {
	t.Helper()
	ctx := context.Background()
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	const name = "togavsu_perf"
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	var exists bool
	if err := admin.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists && os.Getenv("TOGAVSU_PERF_RESEED") != "1" {
		pool, err := db.OpenPostgres(u.String(), "")
		if err != nil {
			t.Fatal(err)
		}
		var version string
		err = pool.QueryRowContext(ctx, `SELECT value FROM perf.harness WHERE key = 'seed_version'`).Scan(&version)
		_ = pool.Close()
		if err == nil && version == perfSeedVersion {
			return u.String(), false
		}
	}
	_, _ = admin.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	return u.String(), true
}

func perfDataDir(t *testing.T) string {
	d := os.Getenv("TOGAVSU_PERF_DATA_DIR")
	if d == "" {
		d = os.TempDir() + "/togavsu-perf-data"
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// perfStoreAccounts keeps the harness users' synthetic sign-in secrets beside the seed,
// in a schema of the harness's own, and marks the seed complete.
func perfStoreAccounts(t *testing.T, app *App, seed perfSeed) {
	t.Helper()
	for _, q := range []string{`CREATE SCHEMA IF NOT EXISTS perf`, `CREATE TABLE IF NOT EXISTS perf.harness (key TEXT PRIMARY KEY, value TEXT NOT NULL)`} {
		if _, err := app.Pool.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{
		"admin_email": seed.Admin.Email, "admin_password": seed.Admin.Password, "admin_secret": seed.Admin.Secret,
		"staff_email": seed.Staff.Email, "staff_password": seed.Staff.Password, "staff_secret": seed.Staff.Secret,
		"seed_version": perfSeedVersion,
	} {
		if _, err := app.Pool.Exec(`INSERT INTO perf.harness (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func perfCheckVolume(t *testing.T, app *App) {
	t.Helper()
	var users, matters, times, invoices, trust, events int
	for q, dst := range map[string]*int{
		`SELECT COUNT(*) FROM users WHERE id <> '` + string(domain.SystemUserID) + `'`: &users,
		`SELECT COUNT(*) FROM matters`:                                   &matters,
		`SELECT COUNT(*) FROM billing_records WHERE kind = 'time_entry'`: &times,
		`SELECT COUNT(*) FROM billing_records WHERE kind = 'invoice'`:    &invoices,
		`SELECT COUNT(*) FROM trust_ledger`:                              &trust,
		`SELECT COUNT(*) FROM calendar_events`:                           &events,
	} {
		if err := app.Pool.QueryRow(q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("volume: %d users, %d matters, %d time entries, %d invoices, %d trust entries, %d calendar events", users, matters, times, invoices, trust, events)
	if users != perfUsers || matters < perfMatters || times < perfMatters*perfTimePer {
		t.Fatalf("seed volume below AC-017: %d users, %d matters, %d time entries", users, matters, times)
	}
}

// perfSignIn signs a harness user in over the JSON API with password and TOTP.
func perfSignIn(t *testing.T, app *App, base, who string) *client {
	t.Helper()
	get := func(k string) string {
		var v string
		if err := app.Pool.QueryRow(`SELECT value FROM perf.harness WHERE key = $1`, who+"_"+k).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	email, password, secret := get("email"), get("password"), get("secret")
	c := newClient(t, base)
	for step := 0; step < 3; step++ {
		login := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
		r := c.do("POST", "/api/v1/auth/totp", map[string]string{"totp_token": str(t, login.json, "totp_token"), "code": totp(t, secret, time.Now().Add(time.Duration(step)*30*time.Second))})
		if r.code == http.StatusOK {
			c.csrf, _ = r.json["csrf_token"].(string)
			return c
		}
	}
	t.Fatalf("%s could not sign in", who)
	return nil
}

// enrollUserSecret is enrollUser that also returns the TOTP secret.
func enrollUserSecret(t *testing.T, app *App, base string, admin *client, name, email string, secret *string, roles ...domain.Role) (*client, domain.ID) {
	t.Helper()
	ctx := context.Background()
	u, _ := url.Parse(base)
	var session string
	for _, ck := range admin.http.Jar.Cookies(u) {
		if ck.Name == sessionCookie {
			session = ck.Value
		}
	}
	caller, err := app.Identity.Authenticate(ctx, identity.Presented{SessionToken: session})
	if err != nil {
		t.Fatal(err)
	}
	user, err := app.Identity.CreateUser(ctx, caller, identity.NewUser{Name: name, Email: email, Password: "synthetic-password-" + name, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, base)
	login := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "synthetic-password-" + name})
	en := c.must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": str(t, login.json, "enrollment_token")})
	*secret = str(t, en.json, "secret")
	c.must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{
		"enrollment_token": str(t, en.json, "enrollment_token"), "code": totp(t, *secret, time.Now()),
	})
	return c, user.ID
}
