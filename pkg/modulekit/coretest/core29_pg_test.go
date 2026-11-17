package coretest_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/supervisor"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// TestCORE29MailStandInAndNewRoutes: coretest isolates supervisor mail to a temp outbox,
// points SMTP at the stand-in, and reaches representative CORE routes through the harness
// (firm profile, module context-token) so a module suite can observe DeliverMail.
func TestCORE29MailStandInAndNewRoutes(t *testing.T) {
	c := coretest.New(t)
	if c.Mail == nil || c.SupervisorMailOutbox == "" {
		t.Fatal("coretest mail stand-ins missing")
	}
	if !filepath.IsAbs(c.SupervisorMailOutbox) {
		t.Fatalf("SupervisorMailOutbox should be absolute temp path: %q", c.SupervisorMailOutbox)
	}

	firm := c.Admin.Must(http.StatusOK, "GET", "/api/v1/firm-profile", nil)
	if !strings.Contains(firm.Body, "name") && firm.JSON["name"] == nil {
		t.Fatalf("firm profile: %s", firm.Body)
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
	c.TrustKey("Matter Clock coretest key", pub)
	c.Install(signed)
	c.ApproveAll(matterclock.ID)

	tok := c.ContextFor(coretest.Staff, matterclock.ID, c.Open)
	mod := c.ModuleClient(matterclock.ID)
	if r := mod.Do("POST", "/api/v1/modules/"+matterclock.ID+"/context-tokens/verify", nil, coretest.ContextHeader, tok); r.Code != http.StatusOK {
		t.Fatalf("context verify: %d %s", r.Code, r.Body)
	}

	box := supervisor.OpenOutbox(c.SupervisorMailOutbox)
	if err := box.Notify(context.Background(), "Update failed", "coretest supervisor notice"); err != nil {
		t.Fatal(err)
	}
	mailBefore := len(c.Mail.To(c.Admin.Email))
	sent := c.DeliverMail()
	if sent < 1 {
		t.Fatalf("DeliverMail sent %d, want supervisor notice", sent)
	}
	if left, err := box.List(); err != nil || len(left) != 0 {
		t.Fatalf("outbox left %+v err=%v", left, err)
	}
	if len(c.Mail.To(c.Admin.Email)) <= mailBefore {
		t.Fatalf("supervisor notice did not reach mail stand-in; all=%d", len(c.Mail.Messages()))
	}
}
