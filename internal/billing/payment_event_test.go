package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type paymentEventErrorStore struct {
	Store
	err error
}

func (s paymentEventErrorStore) Get(context.Context, string, string, any) error { return s.err }

func TestCoreUpdate2GPaymentEventMissingOrForbidden(t *testing.T) {
	failure := errors.New("synthetic storage failure")
	for _, recordType := range []string{"payment", "payment_refund", "processor_initialization"} {
		for _, err := range []error{ErrNotFound, domain.NotFound("record"), domain.Forbidden("record"), failure} {
			t.Run(recordType+"/"+err.Error(), func(t *testing.T) {
				s := &Service{d: Deps{Store: paymentEventErrorStore{Store: NewMemory(), err: err}}}
				visible, got := s.PaymentEventReadable(context.Background(), domain.Principal{}, domain.Event{RecordType: recordType, RecordID: "missing"})
				if visible || (err == failure && !errors.Is(got, failure)) || (err != failure && got != nil) {
					t.Fatalf("visible=%v error=%v for lookup error %v", visible, got, err)
				}
			})
		}
	}
}
