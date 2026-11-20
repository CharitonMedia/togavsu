package modules

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestCoreUpdate2GVisibilityErrorsDropDelivery(t *testing.T) {
	failure := errors.New("synthetic visibility failure")
	for _, kind := range []string{"payment", "file", "books"} {
		t.Run(kind, func(t *testing.T) {
			ev := domain.Event{Meta: domain.Meta{ID: "synthetic-event"}, Action: "payment.completed", RecordType: "payment", RecordID: "synthetic-record"}
			approved := map[string]bool{"events:read": true, "files:read": true, "books:read": true}
			s := &Service{d: Deps{Now: time.Now}}
			switch kind {
			case "payment":
				s.d.PaymentReadable = func(context.Context, string, domain.Event) (bool, error) { return false, failure }
			case "file":
				ev.Action, ev.RecordType = "file.created", "file"
				s.d.FileReadable = func(context.Context, string, domain.ID) (bool, error) { return false, failure }
			case "books":
				ev.Action, ev.RecordType = "contact.updated", "contact"
				s.d.BooksContactOK = func(context.Context, string, domain.ID) (bool, error) { return false, failure }
			}
			mod := &Installed{ID: "synthetic-reader", Approved: approved}
			mod.Manifest.Events = []string{ev.Action}
			if n, err := s.enqueue(context.Background(), []*Installed{mod}, ev); err != nil || n != 0 {
				t.Fatalf("visibility error queued=%d error=%v; want a dropped delivery", n, err)
			}
		})
	}
}
