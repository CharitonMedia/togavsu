package practice

import "github.com/CharitonMedia/togavsu/internal/domain"

// PhoneRow holds a phone and module actions beside it, after core access checks.
type PhoneRow struct {
	Phone domain.ContactPhone
	Items []PhoneAction
}
type PhoneAction struct{ Title, Name, Path string }
