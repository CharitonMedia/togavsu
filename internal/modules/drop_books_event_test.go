package modules

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type dropWalls struct {
	walled map[domain.ID]bool
}

func (d dropWalls) View(_ context.Context, _ domain.Principal, matter domain.ID) (domain.WallView, error) {
	if d.walled[matter] {
		return domain.WallView{Walled: true, Allows: false}, nil
	}
	return domain.WallView{}, nil
}

func TestDropBooksEvent(t *testing.T) {
	walled := domain.ID("walled-matter")
	open := domain.ID("open-matter")
	s := &Service{d: Deps{
		Walls: dropWalls{walled: map[domain.ID]bool{walled: true}},
		BooksContactOK: func(_ context.Context, id domain.ID) (bool, error) {
			return id != "hidden-contact", nil
		},
	}}
	cases := []struct {
		ev   domain.Event
		drop bool
	}{
		{domain.Event{Action: "invoice.sent", MatterID: walled}, true},
		{domain.Event{Action: "payment.completed", MatterID: walled}, true},
		{domain.Event{Action: "expense.created", MatterID: walled}, true},
		{domain.Event{Action: "trust.entry_posted", MatterID: walled}, true},
		{domain.Event{Action: "invoice.sent", MatterID: open}, false},
		{domain.Event{Action: "contact.updated", RecordID: "hidden-contact"}, true},
		{domain.Event{Action: "contact.updated", RecordID: "visible-contact"}, false},
		{domain.Event{Action: "matter.updated", MatterID: walled}, false},
	}
	for _, c := range cases {
		got, err := s.dropBooksEvent(context.Background(), c.ev)
		if err != nil || got != c.drop {
			t.Fatalf("%s matter=%s record=%s: drop=%v err=%v want %v", c.ev.Action, c.ev.MatterID, c.ev.RecordID, got, err, c.drop)
		}
	}
}
