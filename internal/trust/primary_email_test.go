package trust

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestCurrentPrimaryEmailEffectiveToToday: effective_to == today is expired (end-exclusive),
// matching domain.EffectiveOn / practice contact readers; tomorrow still counts.
func TestCurrentPrimaryEmailEffectiveToToday(t *testing.T) {
	today := "2026-11-17"
	endingToday := []domain.ContactEmail{{
		Primary: true, Email: "ends-today@client.example", EffectiveTo: today,
	}}
	if got := currentPrimaryEmail(endingToday, today); got != "" {
		t.Fatalf("effective_to == today must be expired, got %q", got)
	}
	endingTomorrow := []domain.ContactEmail{{
		Primary: true, Email: "ends-tomorrow@client.example", EffectiveTo: "2026-11-18",
	}}
	if got := currentPrimaryEmail(endingTomorrow, today); got != "ends-tomorrow@client.example" {
		t.Fatalf("effective_to tomorrow: got %q", got)
	}
	openEnded := []domain.ContactEmail{{
		Primary: true, Email: "open@client.example",
	}}
	if got := currentPrimaryEmail(openEnded, today); got != "open@client.example" {
		t.Fatalf("open-ended: got %q", got)
	}
}
