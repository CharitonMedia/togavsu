package repository

import (
	"reflect"
	"strings"
	"testing"
)

// TestAppendOnlyInterfaces checks the Go interfaces only. It does not prove a
// database rejects UPDATE or DELETE. That persistence proof is pending; see
// db.TestAppendOnlyNotEnforcedAtDatabase.
func TestAppendOnlyInterfaces(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf((*TrustLedger)(nil)).Elem(),
		reflect.TypeOf((*AuditLog)(nil)).Elem(),
		reflect.TypeOf((*Events)(nil)).Elem(),
		reflect.TypeOf((*FileVersions)(nil)).Elem(),
		reflect.TypeOf((*FileRenditions)(nil)).Elem(),
		reflect.TypeOf((*AICalls)(nil)).Elem(),
	}
	for _, typ := range types {
		if typ.NumMethod() == 0 {
			t.Fatal(typ)
		}
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			for _, bad := range []string{"Update", "Delete", "Edit", "Remove"} {
				if strings.Contains(name, bad) {
					t.Fatalf("%s has %s", typ.Name(), name)
				}
			}
		}
	}
}
