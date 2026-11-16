package settings_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestUIErrorNamesSettingByLabel(t *testing.T) {
	cases := []struct {
		key  string
		err  error
		want string
	}{
		{
			key:  "billing.time_increment_minutes",
			err:  fmt.Errorf("%w: %s out of range", settings.ErrInvalid, "billing.time_increment_minutes"),
			want: "invalid value: Time Increment out of range",
		},
		{
			key:  "firm.phone",
			err:  fmt.Errorf("%w: %s", settings.ErrInvalid, "firm.phone"),
			want: "That value is not allowed for Firm Phone.",
		},
		{
			key:  "firm.name",
			err:  fmt.Errorf("%w: %s is required", settings.ErrInvalid, "firm.name"),
			want: "invalid value: Firm Name is required",
		},
		{
			key:  "firm.name",
			err:  fmt.Errorf("%w: %s", settings.ErrReadOnly, "firm.name"),
			want: "Firm Name is read only",
		},
		{
			key:  "doctender.naming_pattern",
			err:  fmt.Errorf("%w: invalid naming pattern: needs <ext>", settings.ErrInvalid),
			want: "That value is not allowed for File Naming Pattern: invalid naming pattern: needs <ext>.",
		},
		{
			key:  "doctender.naming_pattern",
			err:  fmt.Errorf("%w: invalid naming pattern: status token is refused", settings.ErrInvalid),
			want: "That value is not allowed for File Naming Pattern: invalid naming pattern: status token is refused.",
		},
		{
			key:  "ai.model_prices",
			err:  fmt.Errorf("%w: model prices are a JSON object of model to {\"input\", \"output\"} cents per million tokens", settings.ErrInvalid),
			want: "That value is not allowed for Model Prices: model prices are a JSON object of model to {\"input\", \"output\"} cents per million tokens.",
		},
	}
	for _, tc := range cases {
		got := settings.UIError(tc.key, tc.err)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.key, got, tc.want)
		}
		if strings.Contains(got, tc.key) {
			t.Errorf("%s: UI message still names the key: %q", tc.key, got)
		}
	}
}
