package supervisor

import (
	"errors"
	"testing"
)

func TestRestoreOwner(t *testing.T) {
	for _, c := range []struct {
		admin, app, want string
		bad              bool
	}{
		{"postgres://togavsu:a@db/postgres", "postgres://togavsu:a@db/togavsu", "", false},
		{"postgres://togavsu:a@db/postgres", "postgres://togavsu_app:b@db/togavsu", "togavsu_app", false},
		{"postgres://togavsu:a@db/postgres", "postgres://db/togavsu", "", false},
		{"postgres://togavsu:a@db/postgres", "postgres://Evil%3BDROP:b@db/togavsu", "", true},
	} {
		got, err := restoreOwner(c.admin, c.app)
		if c.bad {
			if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("%s: want ErrBadRequest, got %q %v", c.app, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%s: got %q %v, want %q", c.app, got, err, c.want)
		}
	}
}
