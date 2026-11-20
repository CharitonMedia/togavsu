package identity

import (
	"context"
	"testing"
)

func TestFrameCSRFSessionRotation(t *testing.T) {
	b := newDefaultBox(t)
	ctx := context.Background()
	if _, err := b.svc.Setup(ctx, SetupInput{FirmName: "Synthetic", TimeZone: "UTC", AdminName: "Synthetic", AdminEmail: "synthetic@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase"}); err != nil {
		t.Fatal(err)
	}
	first, err := b.svc.Login(ctx, "synthetic@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.svc.Login(ctx, "synthetic@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	token := b.svc.FrameCSRF(first.SessionID, "frame-one")
	for _, tc := range []struct {
		session, module, csrf string
		want                  bool
	}{
		{first.SessionToken, "frame-one", token, true},
		{first.SessionToken, "frame-two", token, false},
		{first.SessionToken, "", token, false},
		{second.SessionToken, "frame-one", token, false},
		{first.SessionToken, "frame-one", first.CSRFToken, false},
	} {
		_, err := b.svc.Authenticate(ctx, Presented{SessionToken: tc.session, FrameModule: tc.module, CSRF: tc.csrf, Write: true})
		if (err == nil) != tc.want {
			t.Errorf("session rotation/module/core boundary: allowed=%v want=%v", err == nil, tc.want)
		}
	}
}
