package core

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// Codex PR #41 findings 1 and 2: the admin's PATCH /api/v1/users/{id} saves initials
// with PATCH /me's validation and an audit row, and an explicit null clears
// linked_contact_id and default_hourly_rate_cents while an omitted field leaves them.
func TestAdminUserPatchInitialsAndNullClears(t *testing.T) {
	f := newFin(t)
	_, contact := f.matter("Initials Estate", "", nil)
	user := f.admin.must(http.StatusCreated, "POST", "/api/v1/users", map[string]any{
		"name": "Cal Client", "email": "cal@client.example", "roles": []string{"client"},
		"password": "synthetic-password-cal", "linked_contact_id": contact, "default_hourly_rate_cents": 12500,
	})
	id := str(t, user.json, "id")
	if user.json["linked_contact_id"] != contact || user.json["default_hourly_rate_cents"] != float64(12500) {
		t.Fatalf("created: %s", user.body)
	}
	path := "/api/v1/users/" + id

	// Initials: saved upper case, audited, and refused with PATCH /me's rule.
	f.admin.must(http.StatusOK, "PATCH", path, map[string]any{"initials": "cc"})
	if n := f.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1 AND initials = 'CC'`, id); n != 1 {
		t.Fatalf("admin initials not stored: %d rows", n)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=user&entity_id="+id, nil); !strings.Contains(r.body, `"initials"`) || !strings.Contains(r.body, "CC") {
		t.Fatalf("initials change not audited: %s", r.body)
	}
	if r := f.admin.do("PATCH", path, map[string]any{"initials": "C3"}); r.code != http.StatusBadRequest {
		t.Fatalf("bad initials: %d %s", r.code, r.body)
	}
	if n := f.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1 AND initials = 'CC'`, id); n != 1 {
		t.Fatal("refused initials changed the stored value")
	}

	// Omitted: both values stay.
	got := f.admin.must(http.StatusOK, "PATCH", path, map[string]any{"name": "Cal Q. Client"}).json
	if got["linked_contact_id"] != contact || got["default_hourly_rate_cents"] != float64(12500) {
		t.Fatalf("omitted fields changed: %v", got)
	}
	// Set: new values replace them.
	_, other := f.matter("Other Estate", "", nil)
	got = f.admin.must(http.StatusOK, "PATCH", path, map[string]any{"linked_contact_id": other, "default_hourly_rate_cents": 9000}).json
	if got["linked_contact_id"] != other || got["default_hourly_rate_cents"] != float64(9000) {
		t.Fatalf("set fields: %v", got)
	}
	// Null: each clears on its own, and the other stays.
	got = f.admin.must(http.StatusOK, "PATCH", path, map[string]any{"linked_contact_id": nil}).json
	if got["linked_contact_id"] != nil || got["default_hourly_rate_cents"] != float64(9000) {
		t.Fatalf("null linked_contact_id: %v", got)
	}
	got = f.admin.must(http.StatusOK, "PATCH", path, map[string]any{"default_hourly_rate_cents": nil}).json
	if got["default_hourly_rate_cents"] != float64(0) || got["linked_contact_id"] != nil {
		t.Fatalf("null default_hourly_rate_cents: %v", got)
	}
	hist := f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=user&entity_id="+id, nil).body
	if !strings.Contains(hist, "contact_id") || !strings.Contains(hist, "default_hourly_rate_cents") {
		t.Fatalf("clears not audited: %s", hist)
	}
}

// Codex PR #41 finding 3: PATCH /api/v1/me takes profile_file_id. A file the caller can
// read becomes their picture through the files service's checks, null removes it, and a
// walled file is refused without changing the picture.
func TestMeProfileFileID(t *testing.T) {
	d := newDocs(t)
	open, _ := d.matter("Picture Estate", "", nil)
	walled, _ := d.matter("Sealed Picture", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	mine := str(t, d.upload(d.staff, open, "me.png", "image/png", buf.Bytes(), nil), "id")
	sealed := str(t, d.upload(d.atty, walled, "sealed.png", "image/png", buf.Bytes(), nil), "id")
	text := str(t, d.upload(d.staff, open, "notes.txt", "text/plain", []byte("not a picture"), nil), "id")
	pictures := func(ctype string) int {
		return d.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1 AND picture_type = $2`, d.staffID, ctype)
	}

	d.staff.must(http.StatusOK, "PATCH", "/api/v1/me", map[string]any{"profile_file_id": mine})
	if pictures("image/png") != 1 {
		t.Fatal("profile_file_id did not set the picture")
	}
	if r := d.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=user&entity_id="+d.staffID, nil); !strings.Contains(r.body, "profile_picture") {
		t.Fatalf("picture change not audited: %s", r.body)
	}

	// A walled file is not found for staff, and a file that is not an image is refused;
	// neither touches the picture or the other fields in the same request.
	if r := d.staff.do("PATCH", "/api/v1/me", map[string]any{"profile_file_id": sealed, "initials": "ZZ"}); r.code != http.StatusNotFound {
		t.Fatalf("walled file as picture: %d %s", r.code, r.body)
	}
	if r := d.staff.do("PATCH", "/api/v1/me", map[string]any{"profile_file_id": text}); r.code != http.StatusBadRequest {
		t.Fatalf("text file as picture: %d %s", r.code, r.body)
	}
	if pictures("image/png") != 1 || d.count(`SELECT count(*) FROM user_profiles WHERE user_id = $1 AND initials = 'ZZ'`, d.staffID) != 0 {
		t.Fatal("a refused profile_file_id changed the profile")
	}

	// Omitted leaves the picture; null removes it.
	d.staff.must(http.StatusOK, "PATCH", "/api/v1/me", map[string]any{"initials": "SS"})
	if pictures("image/png") != 1 {
		t.Fatal("an omitted profile_file_id removed the picture")
	}
	d.staff.must(http.StatusOK, "PATCH", "/api/v1/me", map[string]any{"profile_file_id": nil})
	if pictures("") != 1 {
		t.Fatal("null profile_file_id did not remove the picture")
	}
}
