package modules

import (
	"testing"
)

func TestProcessorResultBinding(t *testing.T) {
	bound := ProcessorPaymentResult{IntentID: "intent-1", AmountCents: 100, Destination: "operating", Status: "pending"}
	for _, tc := range []struct {
		name string
		edit func(*ProcessorPaymentResult)
		want bool
	}{
		{"pending", func(*ProcessorPaymentResult) {}, true},
		{"approved", func(r *ProcessorPaymentResult) { r.Status, r.ReferenceNumber = "approved", "synthetic-approval" }, true},
		{"declined", func(r *ProcessorPaymentResult) { r.Status, r.DeclineCode = "declined", "card_declined" }, true},
		{"other intent", func(r *ProcessorPaymentResult) { r.IntentID = "intent-2" }, false},
		{"other amount", func(r *ProcessorPaymentResult) { r.AmountCents++ }, false},
		{"trust", func(r *ProcessorPaymentResult) { r.Destination = "trust" }, false},
		{"unknown", func(r *ProcessorPaymentResult) { r.Status = "completed" }, false},
		{"approval missing reference", func(r *ProcessorPaymentResult) { r.Status = "approved" }, false},
		{"decline with reference", func(r *ProcessorPaymentResult) { r.Status, r.ReferenceNumber = "declined", "claim" }, false},
		{"pending with reference", func(r *ProcessorPaymentResult) { r.ReferenceNumber = "claim" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bound
			tc.edit(&r)
			if got := validProcessorResult(r, bound); got != tc.want {
				t.Fatalf("valid=%v want=%v for %#v", got, tc.want, r)
			}
		})
	}
}
