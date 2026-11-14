package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Updates (PRD 6, 8.7 Updates tab): GET /api/v1/updates is the update history from the
// supervisor's append-only ledger, and POST /api/v1/updates is the admin's Update now.
// Both are admin only (authenticate requires updates:admin of a token).

// supEvent is supervisor.Event.
type supEvent struct {
	Time   time.Time         `json:"time"`
	Kind   string            `json:"kind"`
	Actor  string            `json:"actor"`
	Fields map[string]string `json:"fields"`
}

func (b *backups) mountUpdates(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/updates", b.listUpdates)
	m.HandleFunc("POST /api/v1/updates", b.startUpdate)
}

// eventID is a UUIDv7 for a ledger line: its time in the first 48 bits and a hash of the
// line in the rest, so the same line always has the same id.
func eventID(e supEvent, seq int) string {
	h := sha256.New()
	_, _ = h.Write([]byte(e.Time.UTC().Format(time.RFC3339Nano) + "|" + e.Kind + "|" + e.Fields["status"] + "|" + e.Fields["to"] + "|" + e.Fields["image"]))
	_, _ = h.Write([]byte{byte(seq >> 8), byte(seq)})
	sum := h.Sum(nil)
	ms := uint64(e.Time.UnixMilli())
	var u [16]byte
	for i := 0; i < 6; i++ {
		u[i] = byte(ms >> (40 - 8*i))
	}
	copy(u[6:], sum[:10])
	u[6] = 0x70 | (u[6] & 0x0f)
	u[8] = 0x80 | (u[8] & 0x3f)
	s := hex.EncodeToString(u[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// updateRecord maps one ledger line to the contract's UpdateRecord.
func updateRecord(e supEvent, seq int) map[string]any {
	status := "aborted"
	switch e.Fields["status"] {
	case "applied":
		status = "completed"
	case "running", "started":
		status = "running"
	}
	reason := e.Fields["reason"]
	if reason == "" && status == "aborted" {
		reason = e.Fields["status"]
	}
	to := e.Fields["to"]
	if to == "" {
		to = e.Fields["image"]
	}
	return map[string]any{
		"id": eventID(e, seq), "created_at": e.Time.UTC().Format(time.RFC3339), "created_by": string(domain.SystemUserID),
		"status": status, "from_image": e.Fields["from"], "to_image": to, "reason": reason,
	}
}

func (b *backups) listUpdates(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	var out struct {
		Updates []supEvent `json:"updates"`
	}
	if err := b.call(r.Context(), http.MethodGet, "/v1/updates", "", nil, &out); err != nil {
		fail(w, err)
		return
	}
	data := make([]map[string]any, 0, len(out.Updates))
	for i, e := range out.Updates {
		data = append(data, updateRecord(e, i))
	}
	sort.SliceStable(data, func(i, j int) bool { return data[i]["created_at"].(string) > data[j]["created_at"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

// startUpdate asks the supervisor for an update now and answers at once: the update
// replaces this very core, so the answer can't wait for it. The outcome lands in the
// update history (and the supervisor mails the admins on a failure).
func (b *backups) startUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	if b.secret == "" {
		fail(w, errNoSupervisor)
		return
	}
	id, err := domain.NewID()
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := b.record(r.Context(), p, "update", string(id), "requested", "update now"); err != nil {
		fail(w, err)
		return
	}
	go func(ctx context.Context) {
		if err := b.call(ctx, http.MethodPost, "/v1/updates", "application/json", jsonBody(map[string]bool{"force": true}), nil); err != nil {
			log.Printf("core: update now: %v", err)
		}
	}(context.WithoutCancel(r.Context()))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "created_at": b.now().UTC().Format(time.RFC3339),
		"created_by": p.UserID, "status": "running", "reason": "update now"})
}
