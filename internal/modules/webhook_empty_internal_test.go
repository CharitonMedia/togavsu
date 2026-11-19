package modules

import (
	"context"
	"strings"
	"testing"
)

func TestEventDeliveryRefusesEmptySecret(t *testing.T) {
	// Refused before any runtime lookup or outbound request.
	s := &Service{}
	if err := s.post(context.Background(), "synthetic", "", "", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "empty webhook secret") {
		t.Fatalf("unsigned event: %v", err)
	}
}
