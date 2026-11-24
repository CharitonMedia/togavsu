package coretest_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2ILinkedNavigation(t *testing.T) {
	c := coretest.New(t)
	const provider = "navigation-phone"
	const consumer = "navigation-intake"
	const contract = "phone.provider.v1"
	install2d(t, c, provider, []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: call\n        path: /call\n")
	install2d(t, c, consumer, []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n", func(files map[string][]byte) {
		// Add a second home tab with an explicit param declaration.
		text := string(files[modulekit.ManifestFile])
		text = strings.Replace(text, "tabs:\n", "tabs:\n  - id: start\n    title: Start intake\n    placement: home\n    path: /start\n    params: [phone]\n  - id: client-only\n    title: Client only\n    placement: client\n    path: /client-only\n", 1)
		files[modulekit.ManifestFile] = []byte(text)
	})
	nav := func(who *coretest.User, source, page string, params map[string]string, status int) coretest.Resp {
		t.Helper()
		b, _ := json.Marshal(params)
		q := url.Values{"page": {page}, "params": {string(b)}}
		return who.Must(status, "GET", "/modules/"+source+"/navigate?"+q.Encode(), nil)
	}
	target := "/modules/" + consumer + "/tabs/start"
	params := map[string]string{"phone": "+15555550123", "secret": "drop", "matter_id": c.Walled}
	nav(c.Staff, provider, target, params, 403)
	link := c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": provider, "consumer_id": consumer})
	result := nav(c.Staff, provider, target+"?bad=drop&phone=old", params, 200)
	if result.JSON["url"] != target+"?phone=%2B15555550123" {
		t.Fatalf("params not filtered: %v", result.JSON)
	}
	nav(c.Staff, consumer, "/modules/"+provider+"/tabs/hours", nil, 200)
	nav(c.Client, provider, target, params, 403)
	nav(c.Accountant, provider, target, params, 403)
	for _, page := range []string{
		"/modules/" + consumer + "/widgets/hours", "/modules/" + consumer + "/tabs/missing", "/modules/" + consumer + "/tabs/client-only",
		"/modules/" + consumer + "/tabs/start/../../other", "/modules/" + consumer + "/tabs/%73tart",
		"/modules/" + consumer + "/tabs/start#fragment", "//evil.example/path", "https://evil.example/path",
		"/modules/" + consumer + "/tabs/start\\evil",
	} {
		nav(c.Staff, provider, page, params, 403)
	}
	frame := c.Staff.Must(200, "GET", "/modules/"+provider+"/tabs/hours", nil)
	if !strings.Contains(frame.Body, "/navigate?") {
		t.Fatal("frame bridge does not use live navigation check")
	}
	c.Admin.Must(200, "POST", "/api/v1/modules/"+consumer+"/disable", nil)
	nav(c.Staff, provider, target, params, 403)
	c.Admin.Must(200, "POST", "/api/v1/modules/"+consumer+"/enable", nil)
	nav(c.Staff, provider, target, params, 200)
	c.Admin.Must(204, "DELETE", "/api/v1/modules/contract-links/"+str(t, link.JSON, "id"), nil)
	nav(c.Staff, provider, target, params, 403)
}
