package core

import (
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestSystemMailRefusesEmptyWebhookSecret(t *testing.T) {
	m := &systemMailer{}
	if err := m.postModule(context.Background(), modules.SystemMailRoute{}, systemMail{}); err == nil || !strings.Contains(err.Error(), "empty webhook secret") {
		t.Fatalf("unsigned mail: %v", err)
	}
}
