package trust

import (
	"context"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Money sign rules. A debit is a negative amount, a credit a positive one, and a balance
// below zero flags (PRD 8.4). These are fixed ledger rules, not settings.

func negative(v int64) bool { return v < int64(0) }

func positive(v int64) bool { return v > int64(0) }

func isDebit(c domain.Cents) bool { return negative(int64(c)) }

// intSetting reads an integer setting through the registry. The registry supplies the
// default, so there is no second default here.
func (s *Service) intSetting(ctx context.Context, key string) (int, error) {
	raw, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(raw)
}
