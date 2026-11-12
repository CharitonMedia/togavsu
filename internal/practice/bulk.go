package practice

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// bulkRoutes mounts POST /api/v1/{record}:bulk for the practice records (PRD 7: a bulk
// endpoint per record type validates and writes the batch in one transaction and rejects
// the whole batch when any item fails). Each item runs through the record's own create
// handler, so it gets the same access, wall, validation, audit and event rules, inside
// one practice unit of work: the first failing item discards every earlier item.
func (h *Handler) bulkRoutes(mux *http.ServeMux) {
	for path, one := range map[string]http.HandlerFunc{
		"/api/v1/contacts":          h.createContactJSON,
		"/api/v1/contact-phones":    h.createPhoneJSON,
		"/api/v1/contact-emails":    h.createEmailJSON,
		"/api/v1/contact-addresses": h.createAddressJSON,
		"/api/v1/contact-tags":      h.createTagJSON,
		"/api/v1/matters":           h.createMatterJSON,
		"/api/v1/matter-markers":    h.createMarkerJSON,
		"/api/v1/matter-parties":    h.createPartyJSON,
		"/api/v1/notes":             h.createNoteJSON,
		"/api/v1/conflict-records":  h.createConflictJSON,
	} {
		mux.HandleFunc("POST "+path+":bulk", h.bulk(one))
	}
}

// itemResult records one item's response.
type itemResult struct {
	head http.Header
	code int
	body bytes.Buffer
}

func (r *itemResult) Header() http.Header { return r.head }
func (r *itemResult) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(b)
}
func (r *itemResult) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

func (h *Handler) bulk(one http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Authenticate before reading the body, so an anonymous caller learns nothing.
		if _, ok := h.caller(w, r, true); !ok {
			return
		}
		var in struct {
			Items []json.RawMessage `json:"items"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeErr(w, domain.Invalid("items", "request body must be {\"items\": [...]}"))
			return
		}
		if len(in.Items) == 0 {
			writeErr(w, domain.Invalid("items", "items needs at least one record"))
			return
		}
		if max := h.Svc.intSetting(r.Context(), "api.bulk_batch_max", 500); len(in.Items) > max {
			writeErr(w, domain.Invalid("items", "batch is larger than api.bulk_batch_max ("+strconv.Itoa(max)+")"))
			return
		}
		ctx, done := h.Svc.unit(r.Context())
		out := make([]json.RawMessage, 0, len(in.Items))
		var failed error
		for i, item := range in.Items {
			req := r.Clone(ctx)
			req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(item)), int64(len(item))
			req.Header.Set("Content-Type", "application/json")
			// One key for the whole batch would replay item 0 for every item.
			req.Header.Del("Idempotency-Key")
			res := &itemResult{head: http.Header{}}
			one(res, req)
			if res.code < 200 || res.code > 299 {
				failed = itemErr(i, res)
				break
			}
			out = append(out, json.RawMessage(bytes.Clone(res.body.Bytes())))
		}
		if err := done(failed); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": out})
	}
}

// itemErr is the failing item's own error, naming its index.
func itemErr(i int, res *itemResult) error {
	var api domain.APIError
	if err := json.Unmarshal(res.body.Bytes(), &api); err != nil || api.Code == "" {
		api = domain.APIError{Code: domain.CodeValidation, Message: http.StatusText(res.code)}
		if res.code >= 500 {
			api.Code = "internal"
		}
	}
	prefix := "items[" + strconv.Itoa(i) + "]"
	api.Message = prefix + ": " + api.Message
	if api.Field != "" {
		api.Field = prefix + "." + api.Field
	} else {
		api.Field = prefix
	}
	return &api
}
