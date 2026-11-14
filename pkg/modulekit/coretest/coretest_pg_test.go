package coretest_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// TestMatterClockThroughHarness is the kit's proof on the core's own sample module, the
// way a module repository's acceptance suite runs: the composed core, a signed package
// from pkg/modulekit, install and approval as the admin, a tab call as a viewer inside
// the walls, the module's own database login, and uninstall with its data deleted.
func TestMatterClockThroughHarness(t *testing.T) {
	c := coretest.New(t)
	for _, role := range []string{coretest.Admin, coretest.Attorney, coretest.Staff, coretest.ClientRole, coretest.Accountant} {
		u := c.As(role)
		if me := u.Must(http.StatusOK, "GET", "/api/v1/me", nil); !strings.Contains(me.Body, u.ID) {
			t.Fatalf("%s signed in as someone else: %s", role, me.Body)
		}
	}
	c.Serve(matterclock.ID, func(m coretest.Module) http.Handler {
		return matterclock.New(matterclock.Config{Core: m.CoreURL, Token: m.Token, WebhookSecret: m.WebhookSecret, DB: m.DB, SkewSeconds: 300}).Handler()
	})

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := matterclock.Files()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := modulekit.Build(priv, files)
	if err != nil {
		t.Fatal(err)
	}
	if r := c.TryInstall(signed); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an untrusted signer was accepted: %d %s", r.Code, r.Body)
	}
	c.TrustKey("Matter Clock sample key", pub)
	c.Install(signed)
	if len(c.Runtime.Deployments()) != 0 || c.SchemaExists(matterclock.ID) {
		t.Fatal("the module ran before approval")
	}
	c.ApproveAll(matterclock.ID)
	dep := c.Last(matterclock.ID)
	if dep.Version != "1.0.0" || dep.Secrets["togavsu_api_token"] == "" || !c.SchemaExists(matterclock.ID) {
		t.Fatalf("deployment: %+v", dep)
	}

	// The module's own login writes its schema and can't read core contacts.
	ctx := context.Background()
	mdb := c.ModuleDB(matterclock.ID)
	for _, m := range []string{c.Open, c.Walled} {
		if err := matterclock.SetTarget(ctx, mdb, m, 120, "partner note", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mdb.ExecContext(ctx, `SELECT 1 FROM core.contacts LIMIT 1`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("module login read core contacts: %v", err)
	}

	// A tab call as a viewer: staff see the open matter's hours, not the walled one; the
	// attorney inside the wall sees the walled matter's target and the attorney-only note.
	c.LogTime(coretest.Staff, c.Open, 5)
	c.LogTime(coretest.Attorney, c.Walled, 3)
	home := c.Tab(coretest.Staff, matterclock.ID, "hours", "")
	if home.Code != http.StatusOK || !strings.Contains(home.Body, `data-matter="`+c.Open+`"`) || strings.Contains(home.Body, c.Walled) {
		t.Fatalf("staff home tab: %d %s", home.Code, home.Body)
	}
	if r := c.Tab(coretest.Attorney, matterclock.ID, "matter", c.Walled); r.Code != http.StatusOK || !strings.Contains(r.Body, "partner note") {
		t.Fatalf("attorney's walled matter tab: %d %s", r.Code, r.Body)
	}

	// Context and agent tokens: the module verifies a viewer's context with its own token;
	// an agent token acts within its owner's access.
	ctxTok := c.ContextFor(coretest.Staff, matterclock.ID, "")
	mod := c.ModuleClient(matterclock.ID)
	if r := mod.Do("POST", "/api/v1/modules/"+matterclock.ID+"/context-tokens/verify", nil, coretest.ContextHeader, ctxTok); r.Code != http.StatusOK || !strings.Contains(r.Body, c.Staff.ID) {
		t.Fatalf("context verify: %d %s", r.Code, r.Body)
	}
	agent := c.AgentToken(coretest.Staff, "matters:read")
	if r := agent.Do("GET", "/api/v1/matters/"+c.Walled, nil); r.Code != http.StatusNotFound {
		t.Fatalf("staff's agent reached the walled matter: %d", r.Code)
	}
	agent.Must(http.StatusOK, "GET", "/api/v1/matters/"+c.Open, nil)

	c.Uninstall(matterclock.ID, true)
	if c.SchemaExists(matterclock.ID) || len(c.Backups.Made()) != 1 {
		t.Fatalf("uninstall left the schema or took %d backups", len(c.Backups.Made()))
	}
	if r := c.Staff.Do("GET", "/modules/"+matterclock.ID+"/tabs/hours", nil); r.Code != http.StatusNotFound {
		t.Fatalf("the tab outlived uninstall: %d", r.Code)
	}
}
