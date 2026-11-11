package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// IdentityAPI is the identity service as the account screens call it, always as the
// signed-in user: admin checks, password and second-factor proofs, token ceilings and
// owner rules stay in identity. *identity.Service satisfies it.
type IdentityAPI interface {
	CreateUser(ctx context.Context, caller identity.Caller, in identity.NewUser) (domain.User, error)
	ChangePassword(ctx context.Context, caller identity.Caller, current, next string) error
	ResetTOTP(ctx context.Context, caller identity.Caller, factor identity.Factor) (identity.Enrollment, error)
	ConfirmReset(ctx context.Context, caller identity.Caller, enrollmentToken, code string) error
	RegenerateRecovery(ctx context.Context, caller identity.Caller, factor identity.Factor) ([]string, error)
	IssueToken(ctx context.Context, caller identity.Caller, in identity.Issue) (identity.IssuedToken, error)
	ListTokens(ctx context.Context, caller identity.Caller) ([]identity.TokenView, error)
	GetToken(ctx context.Context, caller identity.Caller, id domain.ID) (identity.TokenView, error)
	RevokeToken(ctx context.Context, caller identity.Caller, id domain.ID) error
}

// UserWriter is the identity user repository: read one user, write one back.
type UserWriter interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
	Update(ctx context.Context, row domain.User) error
}

// Recorder is the audit service: one row per changed field, committed with the change.
type Recorder interface {
	Record(ctx context.Context, who domain.Principal, change audit.Change) (domain.AuditEntry, error)
	Atomic(ctx context.Context, fn func(ctx context.Context) error) error
}

// WallService is practice's matter wall API, called as the admin. Practice decides which
// matters the admin can see and who may change a wall.
type WallService interface {
	ListMatters(ctx context.Context, p domain.Principal, status string, area domain.ID) ([]practice.MatterView, error)
	MatterWall(ctx context.Context, p domain.Principal, id domain.ID) ([]domain.MatterWall, error)
	SetWall(ctx context.Context, p domain.Principal, matter domain.ID, users []domain.ID) error
}

// Profile is the part of a user's profile the users table has no column for: phone,
// chosen initials and the profile picture (PRD 8.17, contract MeProfileWrite).
type Profile struct {
	UserID      domain.ID
	Phone       string
	Initials    string
	PictureType string
	Picture     []byte
	UpdatedAt   time.Time
}

// ProfileStore keeps Profile rows. Writes join the audit transaction in ctx.
type ProfileStore interface {
	Profile(ctx context.Context, user domain.ID) (Profile, bool, error)
	PutProfile(ctx context.Context, row Profile) error
}

// Accounts is the admin page's Users and roles and Agents and API panels and the My
// profile screen (PRD 8.7, 8.9, 8.17). Every call runs as the signed-in user.
//
// ponytail: ceiling: user edit and deactivate are written here on the identity user
// repository and the audit service, because identity.Service has CreateUser but no
// update or deactivate; phone, initials and the picture live in the ui table
// user_profiles, because the section 5 users table has no columns for them. Upgrade
// path: identity.Service gains UpdateUser/DeactivateUser and the users table gains
// phone, initials and profile_file_id (the picture then lives in files); Accounts calls
// those, and the last-admin check moves under identity's per-user lock (mu below holds
// only within one core process). Trigger: the integration lead mounts PATCH
// /api/v1/users/{id} and PATCH /api/v1/me, or a second core process serves the admin page.
type Accounts struct {
	Identity IdentityAPI
	Users    UserWriter
	// List returns every user, active or not.
	List     func(ctx context.Context) ([]domain.User, error)
	Audit    Recorder
	WallSvc  WallService
	Profiles ProfileStore
	Now      func() time.Time

	// mu serializes this process's role and active changes, so two admins cannot each
	// remove the other as the last admin at the same moment.
	mu sync.Mutex
}

func caller(p domain.Principal) identity.Caller { return identity.Caller{Principal: p} }

func (a *Accounts) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// UserInput is the add and edit form for one user. Nil fields are left as they are.
type UserInput struct {
	Name      *string
	Email     *string
	Password  string
	Roles     *[]domain.Role
	RateCents *int64
	BarNumber *string
	BarState  *string
	ContactID *domain.ID
	Phone     *string
}

// UserRow is one user on the Users and roles tab.
type UserRow struct {
	User  domain.User
	Roles []domain.Role
	Phone string
}

// AllRoles is every role in the order the forms show them.
var AllRoles = []domain.Role{domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant, domain.RoleClient}

// ParseRoleList reads role names, refusing unknown ones and an empty list.
func ParseRoleList(names []string) ([]domain.Role, error) {
	var out []domain.Role
	seen := map[domain.Role]bool{}
	for _, n := range names {
		r := domain.Role(strings.TrimSpace(n))
		known := false
		for _, k := range AllRoles {
			if r == k {
				known = true
			}
		}
		if !known {
			return nil, domain.Invalid("roles", "unknown role "+n)
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, domain.Invalid("roles", "choose at least one role")
	}
	return out, nil
}

func hasRole(roles []domain.Role, want domain.Role) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// UsersFor lists every user for the admin, with phone numbers.
func (a *Accounts) UsersFor(ctx context.Context, p domain.Principal) ([]UserRow, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	all, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserRow, 0, len(all))
	for _, u := range all {
		roles, _ := domain.ParseRoles(u.Roles)
		row := UserRow{User: u, Roles: roles}
		if pr, ok, err := a.Profiles.Profile(ctx, u.ID); err != nil {
			return nil, err
		} else if ok {
			row.Phone = pr.Phone
		}
		out = append(out, row)
	}
	return out, nil
}

// User is one user for the admin's edit screen.
func (a *Accounts) User(ctx context.Context, p domain.Principal, id domain.ID) (UserRow, error) {
	if err := admin(p); err != nil {
		return UserRow{}, err
	}
	u, err := a.Users.Get(ctx, id)
	if err != nil {
		return UserRow{}, missingUser(err)
	}
	roles, _ := domain.ParseRoles(u.Roles)
	row := UserRow{User: u, Roles: roles}
	if pr, ok, err := a.Profiles.Profile(ctx, id); err != nil {
		return UserRow{}, err
	} else if ok {
		row.Phone = pr.Phone
	}
	return row, nil
}

func missingUser(err error) error {
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return domain.NotFound("user")
	}
	return err
}

// CreateUser adds a user through identity (admin only, password set, audited), then
// sets the fields identity's create does not take.
func (a *Accounts) CreateUser(ctx context.Context, p domain.Principal, in UserInput) (domain.User, error) {
	if err := admin(p); err != nil {
		return domain.User{}, err
	}
	if in.Name == nil || in.Email == nil || in.Roles == nil {
		return domain.User{}, domain.Invalid("name", "name, email and roles are required")
	}
	if err := checkUserFields(*in.Roles, in); err != nil {
		return domain.User{}, err
	}
	if utf8.RuneCountInString(in.Password) < fixed.PasswordMin {
		return domain.User{}, domain.Invalid("password", "the password needs at least "+strconv.Itoa(fixed.PasswordMin)+" characters")
	}
	nu := identity.NewUser{Name: strings.TrimSpace(*in.Name), Email: *in.Email, Password: in.Password, Roles: *in.Roles}
	u, err := a.Identity.CreateUser(ctx, caller(p), nu)
	if err != nil {
		return domain.User{}, err
	}
	rest := UserInput{RateCents: in.RateCents, BarNumber: in.BarNumber, BarState: in.BarState, ContactID: in.ContactID, Phone: in.Phone}
	if rest != (UserInput{}) {
		return a.UpdateUser(ctx, p, u.ID, rest)
	}
	return u, nil
}

// checkUserFields applies the form rules: bar fields are for attorneys, the linked
// contact is for client users, and the rate is zero or more.
func checkUserFields(roles []domain.Role, in UserInput) error {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return domain.Invalid("name", "name is required")
	}
	if in.Email != nil && !strings.Contains(*in.Email, "@") {
		return domain.Invalid("email", "enter an email address")
	}
	if in.RateCents != nil && *in.RateCents < noCents {
		return domain.Invalid("default_hourly_rate_cents", "the hourly rate can't be below zero")
	}
	attorney := hasRole(roles, domain.RoleAttorney)
	if !attorney && ((in.BarNumber != nil && strings.TrimSpace(*in.BarNumber) != "") || (in.BarState != nil && strings.TrimSpace(*in.BarState) != "")) {
		return domain.Invalid("bar_number", "bar number and state are for attorneys")
	}
	if in.BarState != nil {
		s := strings.TrimSpace(*in.BarState)
		if s != "" && (len(s) != fixed.BarStateLen || strings.ToUpper(s) != s || strings.ContainsFunc(s, func(r rune) bool { return r < 'A' || r > 'Z' })) {
			return domain.Invalid("bar_state", "bar state is a two-letter state code, such as MO")
		}
	}
	if in.ContactID != nil && !in.ContactID.IsZero() && !hasRole(roles, domain.RoleClient) {
		return domain.Invalid("linked_contact_id", "a linked contact is for client users")
	}
	for _, f := range []*string{in.Name, in.Email, in.BarNumber, in.Phone} {
		if f != nil && utf8.RuneCountInString(*f) > fixed.FieldMax {
			return domain.Invalid("name", "a field is too long")
		}
	}
	return nil
}

// noCents is zero money, for the rate's lower bound.
var noCents int64

// otherAdmin reports whether an active admin other than except exists.
func (a *Accounts) otherAdmin(ctx context.Context, except domain.ID) (bool, error) {
	all, err := a.List(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range all {
		roles, _ := domain.ParseRoles(u.Roles)
		if u.Active && u.ID != except && hasRole(roles, domain.RoleAdmin) {
			return true, nil
		}
	}
	return false, nil
}

type fieldChange struct{ field, old, new string }

// UpdateUser edits a user as the admin. Each changed field is audited with its old and
// new value in the same transaction as the write. The last active admin keeps the role.
func (a *Accounts) UpdateUser(ctx context.Context, p domain.Principal, id domain.ID, in UserInput) (domain.User, error) {
	if err := admin(p); err != nil {
		return domain.User{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, err := a.Users.Get(ctx, id)
	if err != nil {
		return domain.User{}, missingUser(err)
	}
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil {
		return domain.User{}, err
	}
	next := u
	nextRoles := roles
	if in.Roles != nil {
		nextRoles = *in.Roles
	}
	if err := checkUserFields(nextRoles, in); err != nil {
		return domain.User{}, err
	}
	if hasRole(roles, domain.RoleAdmin) && !hasRole(nextRoles, domain.RoleAdmin) && u.Active {
		ok, err := a.otherAdmin(ctx, u.ID)
		if err != nil {
			return domain.User{}, err
		}
		if !ok {
			return domain.User{}, domain.Conflict("the firm needs at least one active admin")
		}
	}
	var changes []fieldChange
	set := func(field string, cur *string, v *string) {
		if v == nil {
			return
		}
		nv := strings.TrimSpace(*v)
		if field == "email" {
			nv = strings.ToLower(nv)
		}
		if nv != *cur {
			changes = append(changes, fieldChange{field, *cur, nv})
			*cur = nv
		}
	}
	set("name", &next.Name, in.Name)
	set("email", &next.Email, in.Email)
	set("bar_number", &next.BarNumber, in.BarNumber)
	set("bar_state", &next.BarState, in.BarState)
	if in.Roles != nil {
		enc, err := domain.MarshalRoles(nextRoles)
		if err != nil {
			return domain.User{}, domain.Invalid("roles", "roles are invalid")
		}
		if enc != next.Roles {
			changes = append(changes, fieldChange{"roles", next.Roles, enc})
			next.Roles = enc
		}
	}
	if in.RateCents != nil && domain.Cents(*in.RateCents) != next.DefaultHourlyRateCents {
		changes = append(changes, fieldChange{"default_hourly_rate_cents", strconv.Itoa(int(next.DefaultHourlyRateCents)), strconv.Itoa(int(*in.RateCents))})
		next.DefaultHourlyRateCents = domain.Cents(*in.RateCents)
	}
	if in.ContactID != nil && *in.ContactID != next.ContactID {
		changes = append(changes, fieldChange{"contact_id", string(next.ContactID), string(*in.ContactID)})
		next.ContactID = *in.ContactID
	}
	// A user who loses the attorney or client role loses the fields that go with it.
	if !hasRole(nextRoles, domain.RoleAttorney) {
		if next.BarNumber != "" {
			changes = append(changes, fieldChange{"bar_number", next.BarNumber, ""})
			next.BarNumber = ""
		}
		if next.BarState != "" {
			changes = append(changes, fieldChange{"bar_state", next.BarState, ""})
			next.BarState = ""
		}
	}
	if !hasRole(nextRoles, domain.RoleClient) && !next.ContactID.IsZero() {
		changes = append(changes, fieldChange{"contact_id", string(next.ContactID), ""})
		next.ContactID = ""
	}
	var prof Profile
	var profChanged bool
	if in.Phone != nil {
		cur, ok, err := a.Profiles.Profile(ctx, id)
		if err != nil {
			return domain.User{}, err
		}
		if !ok {
			cur = Profile{UserID: id}
		}
		if ph := strings.TrimSpace(*in.Phone); ph != cur.Phone {
			if err := checkPhone(ph); err != nil {
				return domain.User{}, err
			}
			changes = append(changes, fieldChange{"phone", cur.Phone, ph})
			cur.Phone = ph
			prof, profChanged = cur, true
		}
	}
	if len(changes) == 0 {
		return u, nil
	}
	next.UpdatedAt = a.now()
	err = a.Audit.Atomic(ctx, func(ctx context.Context) error {
		if next != u {
			if err := a.Users.Update(ctx, next); err != nil {
				return userWriteErr(err)
			}
		}
		if profChanged {
			prof.UpdatedAt = next.UpdatedAt
			if err := a.Profiles.PutProfile(ctx, prof); err != nil {
				return err
			}
		}
		for _, c := range changes {
			if _, err := a.Audit.Record(ctx, p, audit.Change{Entity: "user", EntityID: id, Field: c.field, Old: c.old, New: c.new, Reason: "user.updated"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return next, nil
}

// userWriteErr turns a refused users row into the form's message.
func userWriteErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return domain.Conflict("email is already in use")
		case "23503":
			return domain.Invalid("linked_contact_id", "unknown contact")
		}
	}
	if errors.Is(err, repository.ErrConflict) {
		return domain.Conflict("email is already in use")
	}
	return err
}

// DeactivateUser turns a user off. Their sessions and bearer tokens stop authenticating
// on the next request, since identity refuses inactive users. An admin can't deactivate
// themselves or the last active admin.
func (a *Accounts) DeactivateUser(ctx context.Context, p domain.Principal, id domain.ID) error {
	if err := admin(p); err != nil {
		return err
	}
	if id == p.UserID {
		return domain.Invalid("user_id", "you can't deactivate yourself")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, err := a.Users.Get(ctx, id)
	if err != nil {
		return missingUser(err)
	}
	if !u.Active {
		return nil
	}
	roles, _ := domain.ParseRoles(u.Roles)
	if hasRole(roles, domain.RoleAdmin) {
		ok, err := a.otherAdmin(ctx, u.ID)
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("the firm needs at least one active admin")
		}
	}
	u.Active = false
	u.UpdatedAt = a.now()
	return a.Audit.Atomic(ctx, func(ctx context.Context) error {
		if err := a.Users.Update(ctx, u); err != nil {
			return err
		}
		_, err := a.Audit.Record(ctx, p, audit.Change{Entity: "user", EntityID: id, Field: "active", Old: "true", New: "false", Reason: "user.deactivated"})
		return err
	})
}

// WallRow is one walled matter on a user's edit screen.
type WallRow struct {
	MatterID domain.ID
	Number   string
	Name     string
	Members  int
	OnWall   bool
}

// Walls lists the walled matters the admin can see, marking those the user is on.
// Matters without a wall are open to every staff role and are not listed.
func (a *Accounts) Walls(ctx context.Context, p domain.Principal, user domain.ID) ([]WallRow, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	if a.WallSvc == nil {
		return nil, nil
	}
	ms, err := a.WallSvc.ListMatters(ctx, p, "", "")
	if err != nil {
		return nil, err
	}
	var out []WallRow
	for _, m := range ms {
		ws, err := a.WallSvc.MatterWall(ctx, p, m.Matter.ID)
		if err != nil || len(ws) == 0 {
			continue
		}
		row := WallRow{MatterID: m.Matter.ID, Number: m.Matter.Number, Name: m.Matter.Name, Members: len(ws)}
		for _, w := range ws {
			if w.UserID == user {
				row.OnWall = true
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// SetOnWall adds the user to, or removes them from, an existing matter wall through
// practice (admin only, audited there). A matter's wall is created on the matter
// itself; removing the last person is refused here, since an empty wall opens the matter.
func (a *Accounts) SetOnWall(ctx context.Context, p domain.Principal, user, matter domain.ID, on bool) error {
	if err := admin(p); err != nil {
		return err
	}
	if a.WallSvc == nil {
		return domain.Invalid("matter_id", "matter walls are not available")
	}
	if _, err := a.Users.Get(ctx, user); err != nil {
		return missingUser(err)
	}
	ws, err := a.WallSvc.MatterWall(ctx, p, matter)
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		return domain.Invalid("matter_id", "this matter has no wall; set one on the matter first")
	}
	var ids []domain.ID
	found := false
	for _, w := range ws {
		if w.UserID == user {
			found = true
			if !on {
				continue
			}
		}
		ids = append(ids, w.UserID)
	}
	if on == found {
		return nil
	}
	if on {
		ids = append(ids, user)
	}
	if len(ids) == 0 {
		return domain.Invalid("matter_id", "this is the last person on the wall; remove the wall on the matter instead")
	}
	return a.WallSvc.SetWall(ctx, p, matter, ids)
}

// MyProfile is the signed-in user's profile.
type MyProfile struct {
	User       domain.User
	Phone      string
	Initials   string
	HasPicture bool
	PictureAt  time.Time
}

// Me is p's own profile.
func (a *Accounts) Me(ctx context.Context, p domain.Principal) (MyProfile, error) {
	if err := staff(p); err != nil {
		return MyProfile{}, err
	}
	u, err := a.Users.Get(ctx, p.UserID)
	if err != nil {
		return MyProfile{}, missingUser(err)
	}
	out := MyProfile{User: u, Initials: initials(u.Name)}
	pr, ok, err := a.Profiles.Profile(ctx, p.UserID)
	if err != nil {
		return MyProfile{}, err
	}
	if ok {
		out.Phone = pr.Phone
		if pr.Initials != "" {
			out.Initials = pr.Initials
		}
		out.HasPicture = len(pr.Picture) > 0
		out.PictureAt = pr.UpdatedAt
	}
	return out, nil
}

func checkPhone(ph string) error {
	if utf8.RuneCountInString(ph) > fixed.PhoneMax {
		return domain.Invalid("phone", "the phone number is too long")
	}
	for _, r := range ph {
		if !strings.ContainsRune("0123456789+-(). x", r) {
			return domain.Invalid("phone", "a phone number has digits, spaces and + - ( ) . x only")
		}
	}
	return nil
}

func checkInitials(s string) error {
	if utf8.RuneCountInString(s) > fixed.InitialsMax {
		return domain.Invalid("initials", "initials are at most "+strconv.Itoa(fixed.InitialsMax)+" letters")
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') {
			return domain.Invalid("initials", "initials are letters only")
		}
	}
	return nil
}

// UpdateMe changes p's own name, phone and initials (contract PATCH /api/v1/me: email,
// roles and the rest are the admin's). Each change is audited with old and new values.
func (a *Accounts) UpdateMe(ctx context.Context, p domain.Principal, name, phone, inits *string) (MyProfile, error) {
	if err := staff(p); err != nil {
		return MyProfile{}, err
	}
	if p.Source != domain.SourceUI {
		return MyProfile{}, domain.Forbidden(domain.ReasonRole)
	}
	u, err := a.Users.Get(ctx, p.UserID)
	if err != nil {
		return MyProfile{}, missingUser(err)
	}
	pr, ok, err := a.Profiles.Profile(ctx, p.UserID)
	if err != nil {
		return MyProfile{}, err
	}
	if !ok {
		pr = Profile{UserID: p.UserID}
	}
	next, nextPr := u, pr
	var changes []fieldChange
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || utf8.RuneCountInString(n) > fixed.FieldMax {
			return MyProfile{}, domain.Invalid("name", "name is required")
		}
		if n != u.Name {
			changes = append(changes, fieldChange{"name", u.Name, n})
			next.Name = n
		}
	}
	if phone != nil {
		ph := strings.TrimSpace(*phone)
		if err := checkPhone(ph); err != nil {
			return MyProfile{}, err
		}
		if ph != pr.Phone {
			changes = append(changes, fieldChange{"phone", pr.Phone, ph})
			nextPr.Phone = ph
		}
	}
	if inits != nil {
		in := strings.ToUpper(strings.TrimSpace(*inits))
		if err := checkInitials(in); err != nil {
			return MyProfile{}, err
		}
		if in != pr.Initials {
			changes = append(changes, fieldChange{"initials", pr.Initials, in})
			nextPr.Initials = in
		}
	}
	if len(changes) > 0 {
		at := a.now()
		err = a.Audit.Atomic(ctx, func(ctx context.Context) error {
			if next.Name != u.Name {
				next.UpdatedAt = at
				if err := a.Users.Update(ctx, next); err != nil {
					return userWriteErr(err)
				}
			}
			if nextPr.Phone != pr.Phone || nextPr.Initials != pr.Initials {
				nextPr.UpdatedAt = at
				if err := a.Profiles.PutProfile(ctx, nextPr); err != nil {
					return err
				}
			}
			for _, c := range changes {
				if _, err := a.Audit.Record(ctx, p, audit.Change{Entity: "user", EntityID: p.UserID, Field: c.field, Old: c.old, New: c.new, Reason: "user.profile_updated"}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return MyProfile{}, err
		}
	}
	return a.Me(ctx, p)
}

// pictureTypes are the image formats a profile picture may be; the bytes are sniffed,
// not trusted from the upload's name or header.
var pictureTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// SetPicture stores p's profile picture, or removes it when data is empty. The audit
// row records the change and the type, not the image.
func (a *Accounts) SetPicture(ctx context.Context, p domain.Principal, data []byte) error {
	if err := staff(p); err != nil {
		return err
	}
	ctype := ""
	if len(data) > 0 {
		if len(data) > fixed.PictureMaxBytes {
			return domain.Invalid("picture", "the picture is larger than "+strconv.Itoa(fixed.PictureMaxBytes/1024)+" KB")
		}
		ctype = http.DetectContentType(data)
		if !pictureTypes[ctype] {
			return domain.Invalid("picture", "the picture must be a PNG, JPEG or WebP image")
		}
	}
	pr, ok, err := a.Profiles.Profile(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !ok {
		pr = Profile{UserID: p.UserID}
	}
	old := pr.PictureType
	if old == "" && ctype == "" {
		return nil
	}
	pr.Picture, pr.PictureType, pr.UpdatedAt = data, ctype, a.now()
	return a.Audit.Atomic(ctx, func(ctx context.Context) error {
		if err := a.Profiles.PutProfile(ctx, pr); err != nil {
			return err
		}
		nv := "removed"
		if ctype != "" {
			nv = ctype
		}
		_, err := a.Audit.Record(ctx, p, audit.Change{Entity: "user", EntityID: p.UserID, Field: "profile_picture", Old: old, New: nv, Reason: "user.profile_updated"})
		return err
	})
}

// Picture is a user's picture as p may see it: staff see staff pictures; nobody else does.
func (a *Accounts) Picture(ctx context.Context, p domain.Principal, user domain.ID) ([]byte, string, error) {
	if err := staff(p); err != nil {
		return nil, "", err
	}
	pr, ok, err := a.Profiles.Profile(ctx, user)
	if err != nil {
		return nil, "", err
	}
	if !ok || len(pr.Picture) == 0 {
		return nil, "", domain.NotFound("picture")
	}
	if user != p.UserID {
		u, err := a.Users.Get(ctx, user)
		if err != nil {
			return nil, "", domain.NotFound("picture")
		}
		roles, _ := domain.ParseRoles(u.Roles)
		if err := staff(domain.Principal{UserID: u.ID, Roles: roles}); err != nil {
			return nil, "", domain.NotFound("picture")
		}
	}
	return pr.Picture, pr.PictureType, nil
}

// ChangePassword checks the new password here, then lets identity check the current one
// (a wrong one counts toward lockout) and revoke the user's other sessions.
func (a *Accounts) ChangePassword(ctx context.Context, p domain.Principal, current, next, confirm string) error {
	if err := staff(p); err != nil {
		return err
	}
	if next != confirm {
		return domain.Invalid("new_password", "the new passwords don't match")
	}
	if utf8.RuneCountInString(next) < fixed.PasswordMin {
		return domain.Invalid("new_password", "the new password needs at least "+strconv.Itoa(fixed.PasswordMin)+" characters")
	}
	if err := a.Identity.ChangePassword(ctx, caller(p), current, next); err != nil {
		if isCode(err, domain.CodeUnauthenticated) || isCode(err, domain.CodeForbidden) {
			return domain.Forbidden("the current password is not correct")
		}
		return err
	}
	return nil
}

// StartTOTPReset proves the password and returns the new secret, shown once. The current
// secret keeps working until ConfirmTOTPReset succeeds.
func (a *Accounts) StartTOTPReset(ctx context.Context, p domain.Principal, password string) (identity.Enrollment, error) {
	if err := staff(p); err != nil {
		return identity.Enrollment{}, err
	}
	en, err := a.Identity.ResetTOTP(ctx, caller(p), identity.Factor{Password: password})
	if err != nil {
		return identity.Enrollment{}, factorErr(err)
	}
	return en, nil
}

// ConfirmTOTPReset activates the new secret with a code from it.
func (a *Accounts) ConfirmTOTPReset(ctx context.Context, p domain.Principal, token, code string) error {
	if err := staff(p); err != nil {
		return err
	}
	if err := a.Identity.ConfirmReset(ctx, caller(p), token, strings.TrimSpace(code)); err != nil {
		return factorErr(err)
	}
	return nil
}

// NewRecoveryCodes proves the password and replaces the unused recovery codes.
func (a *Accounts) NewRecoveryCodes(ctx context.Context, p domain.Principal, password string) ([]string, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	codes, err := a.Identity.RegenerateRecovery(ctx, caller(p), identity.Factor{Password: password})
	if err != nil {
		return nil, factorErr(err)
	}
	return codes, nil
}

func factorErr(err error) error {
	var api *domain.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case domain.CodeUnauthenticated, domain.CodeForbidden, "reauth_required", "factor_invalid":
			return domain.Forbidden("that didn't match; check it and try again")
		}
		return err
	}
	return err
}

// TokenRow is one API token on a list, with its owner's name.
type TokenRow struct {
	identity.TokenView
	Owner string
}

func (a *Accounts) tokenRows(ctx context.Context, views []identity.TokenView) []TokenRow {
	names := map[domain.ID]string{}
	out := make([]TokenRow, 0, len(views))
	for _, v := range views {
		row := TokenRow{TokenView: v}
		if !v.UserID.IsZero() {
			if n, ok := names[v.UserID]; ok {
				row.Owner = n
			} else if u, err := a.Users.Get(ctx, v.UserID); err == nil {
				names[v.UserID], row.Owner = u.Name, u.Name
			}
		} else {
			row.Owner = "Module " + v.ModuleID
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// MyTokens are the tokens p owns. Identity shows an admin every token; this list keeps
// only p's own, as PRD 8.17 asks.
func (a *Accounts) MyTokens(ctx context.Context, p domain.Principal) ([]TokenRow, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	views, err := a.Identity.ListTokens(ctx, caller(p))
	if err != nil {
		return nil, err
	}
	var own []identity.TokenView
	for _, v := range views {
		if v.UserID == p.UserID {
			own = append(own, v)
		}
	}
	return a.tokenRows(ctx, own), nil
}

// AllTokens are every token, for the Agents and API tab.
func (a *Accounts) AllTokens(ctx context.Context, p domain.Principal) ([]TokenRow, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	views, err := a.Identity.ListTokens(ctx, caller(p))
	if err != nil {
		return nil, err
	}
	return a.tokenRows(ctx, views), nil
}

// IssueToken mints a user-bound token through identity (admin only; the scopes can't
// exceed the admin's or the owner's roles). The secret is returned once and not stored.
// Module tokens are issued by the module runtime at install, against the manifest.
func (a *Accounts) IssueToken(ctx context.Context, p domain.Principal, name string, owner domain.ID, scopes []string) (identity.IssuedToken, error) {
	if err := admin(p); err != nil {
		return identity.IssuedToken{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > fixed.FieldMax {
		return identity.IssuedToken{}, domain.Invalid("name", "give the token a name")
	}
	if owner.IsZero() {
		return identity.IssuedToken{}, domain.Invalid("owner_user_id", "choose the user the token acts as")
	}
	if len(scopes) == 0 {
		return identity.IssuedToken{}, domain.Invalid("scopes", "choose at least one scope")
	}
	u, err := a.Users.Get(ctx, owner)
	if err != nil {
		return identity.IssuedToken{}, domain.Invalid("owner_user_id", "unknown user")
	}
	if !u.Active {
		return identity.IssuedToken{}, domain.Invalid("owner_user_id", "that user is deactivated")
	}
	return a.Identity.IssueToken(ctx, caller(p), identity.Issue{Name: name, UserID: owner, Scopes: scopes})
}

// RevokeToken spends a token. With own set, only a token p owns may be revoked (My
// profile); otherwise identity's rule applies (an admin may revoke any token).
func (a *Accounts) RevokeToken(ctx context.Context, p domain.Principal, id domain.ID, own bool) error {
	if err := staff(p); err != nil {
		return err
	}
	if !own {
		if err := admin(p); err != nil {
			return err
		}
	}
	v, err := a.Identity.GetToken(ctx, caller(p), id)
	if err != nil {
		return err
	}
	if own && v.UserID != p.UserID {
		return domain.NotFound("api token")
	}
	return a.Identity.RevokeToken(ctx, caller(p), id)
}
