package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// moduleReadResponses adds presentation restrictions after services authorize the
// read. It confers no access. The same HTTP operation backs REST and MCP.
func (a *App) moduleReadResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/") || bearerOf(r) == "" {
			next.ServeHTTP(w, r)
			return
		}
		t, ok := a.toolFor(r.Method, r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		// Only JSON record operations, never file bytes, streams or arbitrary module data.
		switch t.Name {
		case "getMatter", "listMatter", "getCalendarEvent", "listCalendarEvent", "getDeadline", "listDeadline", "getMatterParty", "listMatterParty", "getNote", "listNote", "getFile", "listFile", "listMatterTimeline", "getContact", "listContact", "getContactAddress", "listContactAddress", "getContactPhone", "listContactPhone", "getContactEmail", "listContactEmail", "listUserDirectory":
		default:
			next.ServeHTTP(w, r)
			return
		}
		c, err := a.caller(r)
		if err != nil || c.Principal.ModuleID == "" {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		if rec.Code == http.StatusOK && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			var value map[string]any
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			if dec.Decode(&value) == nil {
				annotate := func(row map[string]any) {
					matter, _ := row["matter_id"].(string)
					if matter == "" && (t.Name == "getMatter" || t.Name == "listMatter") {
						if _, yes := row["number"]; yes {
							matter, _ = row["id"].(string)
						}
					}
					var members []string
					if matter != "" {
						members = a.wallMembers(domain.ID(matter))
					} else {
						contact, _ := row["contact_id"].(string)
						if t.Name == "getContact" || t.Name == "listContact" {
							contact, _ = row["id"].(string)
						}
						if contact != "" {
							members = a.contactWallMembers(domain.ID(contact))
						}
					}
					if len(members) > 0 {
						row["walled"] = true
						row["wall_member_user_ids"] = members
					}
				}
				annotate(value)
				if t.Name == "listUserDirectory" {
					if members := a.wallMembers(domain.ID(r.URL.Query().Get("matter_id"))); len(members) > 0 {
						value["walled"] = true
						value["wall_member_user_ids"] = members
					}
				} else if rows, ok := value["data"].([]any); ok {
					for _, v := range rows {
						if row, ok := v.(map[string]any); ok {
							annotate(row)
						}
					}
				}
				// Never inspect included module columns. An arbitrary matter_id inside module
				// data is not authorization to disclose that matter's wall membership.
				if encoded, err := json.Marshal(value); err == nil {
					body = encoded
				}
			}
		}
		for k, values := range rec.Header() {
			w.Header()[k] = values
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	})
}

func (a *App) wallMembers(matter domain.ID) []string {
	if matter.IsZero() {
		return nil
	}
	walls := a.Store.Walls(matter)
	out := make([]string, 0, len(walls))
	for _, wall := range walls {
		out = append(out, string(wall.UserID))
	}
	sort.Strings(out)
	return out
}

// Contacts are visible through any readable matter. If every associated matter is
// walled, the union of wall members may read the contact. An open matter removes
// this presentation restriction, just as the contact service's access rule does.
func (a *App) contactWallMembers(contact domain.ID) []string {
	seen := map[string]bool{}
	for _, party := range a.Store.Parties("", contact) {
		members := a.wallMembers(party.MatterID)
		if len(members) == 0 {
			return nil
		}
		for _, user := range members {
			seen[user] = true
		}
	}
	out := make([]string, 0, len(seen))
	for user := range seen {
		out = append(out, user)
	}
	sort.Strings(out)
	return out
}
