package calendar

import (
	"context"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// BulkEvents validates every item first and writes none if any item fails. The writes
// run in one audit.Service.Atomic, so on Postgres a failure on a later item (a dropped
// connection, an audit or activity row) rolls back the earlier ones and a retry starts
// clean. The Memory store has no write that can fail after validation.
func (s *Service) BulkEvents(ctx context.Context, p domain.Principal, ins []EventInput) ([]Event, error) {
	if err := s.gate(ctx, p, domain.ActionWrite, ""); err != nil {
		return nil, err
	}
	if err := batchSize(s, ctx, ins); err != nil {
		return nil, err
	}
	for i := range ins {
		cp := ins[i]
		cp.Attendees = append([]domain.ID(nil), ins[i].Attendees...)
		if err := s.validateEvent(ctx, p, &cp, nil); err != nil {
			return nil, indexed(i, err)
		}
	}
	var out []Event
	err := s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		out = make([]Event, 0, len(ins))
		for _, in := range ins {
			e, err := s.CreateEvent(ctx, p, in)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BulkDeadlines validates every item first and writes none if any item fails, in one
// transaction like BulkEvents.
func (s *Service) BulkDeadlines(ctx context.Context, p domain.Principal, ins []DeadlineInput) ([]Deadline, error) {
	if err := s.gate(ctx, p, domain.ActionWrite, ""); err != nil {
		return nil, err
	}
	if err := batchSize(s, ctx, ins); err != nil {
		return nil, err
	}
	defaults, err := s.DefaultDeadlineOffsets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ins {
		cp := ins[i]
		if len(cp.Offsets) == 0 {
			cp.Offsets = defaults
		}
		if err := s.validateDeadline(ctx, p, &cp, ""); err != nil {
			return nil, indexed(i, err)
		}
	}
	var out []Deadline
	err = s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		out = make([]Deadline, 0, len(ins))
		for _, in := range ins {
			d, err := s.CreateDeadline(ctx, p, in)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// batchSize bounds one bulk request by api.max_page_size, the most rows one API call
// returns, so a batch is never larger than a page.
func batchSize[T any](s *Service, ctx context.Context, items []T) error {
	_, most, err := s.pageLimits(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 || len(items) > most {
		return domain.Invalid("items", fmt.Sprintf("a batch holds 1 to %d items", most))
	}
	return nil
}
