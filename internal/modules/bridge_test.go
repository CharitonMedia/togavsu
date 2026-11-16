package modules

import "testing"

func TestBridgeNavigateURL(t *testing.T) {
	mod := "matter-clock"
	okCases := []struct {
		page   string
		params map[string]string
		want   string
	}{
		{"/modules/matter-clock/tabs/hours", nil, "/modules/matter-clock/tabs/hours"},
		{"/modules/matter-clock/tabs/hours", map[string]string{"matter_id": "m1"}, "/modules/matter-clock/tabs/hours?matter_id=m1"},
		{"/modules/matter-clock/widgets/today", nil, "/modules/matter-clock/widgets/today"},
		{"/modules/matter-clock/matter-items/panel", map[string]string{"matter_id": "m1"}, "/modules/matter-clock/matter-items/panel?matter_id=m1"},
		{"/practice/matters/m1", nil, "/practice/matters/m1"},
		{"/billing/invoices", nil, "/billing/invoices"},
		{"/files/f1/edit", nil, "/files/f1/edit"},
		{"/", nil, "/"},
	}
	for _, c := range okCases {
		got, ok := BridgeNavigateURL(mod, c.page, c.params)
		if !ok || got != c.want {
			t.Fatalf("allowed %q params %v: got %q ok=%v want %q", c.page, c.params, got, ok, c.want)
		}
	}
	deny := []string{
		"https://evil.example/",
		"http://evil.example/practice/matters",
		"//evil.example/practice",
		"/modules/other-mod/tabs/hours",
		"/modules/matter-clock/../other/tabs/x",
		"",
		"practice/matters",
	}
	for _, page := range deny {
		if got, ok := BridgeNavigateURL(mod, page, nil); ok {
			t.Fatalf("denied %q but got %q", page, got)
		}
	}
}
