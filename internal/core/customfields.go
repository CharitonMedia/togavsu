package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// customRecordScopes is the write scope a token needs for each item of
// custom-field-values:bulk (record_type_scopes); a token needs only the scopes of the
// record types its batch names.
var customRecordScopes = map[string]string{
	practice.RecordMatter: "matters:write", practice.RecordContact: "contacts:write",
	practice.RecordTimeEntry: "time:write", practice.RecordExpense: "expenses:write",
}

type customBulkItem struct {
	RecordType string           `json:"record_type"`
	RecordID   domain.ID        `json:"record_id"`
	Values     []map[string]any `json:"values"`
}

// bulkCustomFields is POST /api/v1/custom-field-values:bulk. Every item is authorized,
// validated and routed to its domain before anything is written: matter and contact items
// stay on the core; time_entry and expense items follow the time domain, so while a module
// replaces time they go to the module's putTimeEntryCustomFields and
// putExpenseCustomFields and the core writes none of them. One inaccessible record is
// not_found and one invalid item rejects the batch; either way nothing is written. The
// core's items then commit in one practice unit.
// ponytail: ceiling: with time replaced, the module's writes go first and the core's items
// commit after them, so a core commit failure (only a database fault; every core item was
// already validated) leaves the module's values in place. Upgrade path: a two-phase
// prepare on the module contract. Trigger: a replacement module with its own custom fields.
func (a *App) bulkCustomFields(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Items []customBulkItem `json:"items"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, domain.Invalid("items", "request body must be {\"items\": [...]}"))
		return
	}
	if len(body.Items) == 0 {
		writeErr(w, domain.Invalid("items", "items needs at least one record"))
		return
	}
	if raw, err := a.Settings.Get(r.Context(), "api.bulk_batch_max"); err == nil {
		if max, err := strconv.Atoi(raw); err == nil && len(body.Items) > max {
			writeErr(w, domain.Invalid("items", "batch is larger than api.bulk_batch_max ("+raw+")"))
			return
		}
	}
	replaced := ""
	if a.ext != nil {
		if replaced, err = a.ext.Modules.Replacement(r.Context(), modules.DomainTime); err != nil {
			writeErr(w, err)
			return
		}
	}
	out, err := a.Practice.Idempotent(r.Context(), p, r.Header.Get("Idempotency-Key"), "bulk_custom_field_values", body, func() (any, error) {
		return a.writeCustomBatch(r, p, body.Items, replaced)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(out)
}

func rejected(i int, err error) error {
	var api *domain.APIError
	if !errors.As(err, &api) || api.Code == domain.CodeNotFound || api.Code == domain.CodeForbidden || api.Code == domain.CodeUnauthenticated {
		return err
	}
	return &domain.APIError{Code: domain.CodeValidation, Field: fmt.Sprintf("items[%d]", i), Message: "batch rejected: " + api.Message}
}

func (a *App) writeCustomBatch(r *http.Request, p domain.Principal, items []customBulkItem, replaced string) (any, error) {
	ctx := r.Context()
	var core []practice.CustomWrite
	var forward []int
	for i, it := range items {
		scope, ok := customRecordScopes[it.RecordType]
		if !ok {
			return nil, rejected(i, domain.Invalid("record_type", "matter, contact, time_entry or expense"))
		}
		if _, err := domain.ParseID(string(it.RecordID)); err != nil {
			return nil, rejected(i, domain.Invalid("record_id", "record_id is a UUID"))
		}
		if it.Values == nil {
			return nil, rejected(i, domain.Invalid("values", "values is required"))
		}
		if err := a.scopes(r, scope); err != nil {
			return nil, shared(err)
		}
		values, err := practice.CustomInputs(it.Values)
		if err != nil {
			return nil, rejected(i, err)
		}
		var t practice.CustomTarget
		switch it.RecordType {
		case practice.RecordTimeEntry, practice.RecordExpense:
			if replaced != "" {
				forward = append(forward, i)
				continue
			}
			t, err = a.Billing.CustomTarget(ctx, p, it.RecordType, it.RecordID)
			if err == nil {
				t.Action = ""
			}
		default:
			t, err = a.Practice.CustomTargetFor(ctx, p, it.RecordType, it.RecordID)
		}
		if err != nil {
			return nil, rejected(i, err)
		}
		if err := a.Practice.CheckCustom(ctx, p, t, values); err != nil {
			return nil, rejected(i, err)
		}
		core = append(core, practice.CustomWrite{Target: t, Values: values})
	}
	for _, i := range forward {
		if err := a.forwardCustom(r, replaced, items[i]); err != nil {
			return nil, rejected(i, err)
		}
	}
	if err := a.Practice.SetRecordsCustom(ctx, p, core); err != nil {
		return nil, err
	}
	return map[string]any{"updated": len(items)}, nil
}

// forwardCustom sends one time_entry or expense item to the module that replaces time,
// through the core's own routing with the caller's credentials, as /mcp dispatches.
func (a *App) forwardCustom(r *http.Request, module string, it customBulkItem) error {
	path := "/api/v1/time-entries/"
	if it.RecordType == practice.RecordExpense {
		path = "/api/v1/expenses/"
	}
	raw, err := json.Marshal(map[string]any{"values": it.Values})
	if err != nil {
		return err
	}
	inner, err := http.NewRequestWithContext(r.Context(), http.MethodPut, path+url.PathEscape(string(it.RecordID))+"/custom-fields", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for _, h := range []string{"Authorization", "Cookie", "X-CSRF-Token"} {
		if v := r.Header.Get(h); v != "" {
			inner.Header.Set(h, v)
		}
	}
	inner.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, inner)
	if rec.Code == http.StatusNotFound {
		return domain.NotFound(it.RecordType)
	}
	if rec.Code >= http.StatusMultipleChoices {
		return domain.Invalid("record_id", "module "+module+", which replaces time, refused the values: "+strings.TrimSpace(rec.Body.String()))
	}
	return nil
}
