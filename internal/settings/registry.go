package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const Redacted = "[redacted]"

const (
	KindString   = "string"
	KindInt      = "int"
	KindBool     = "bool"
	KindCents    = "cents"
	KindDuration = "duration"
	KindEnum     = "enum"
	KindJSON     = "json"
)

const (
	TabFirm       = "firm_profile"
	TabSecurity   = "security"
	TabPractice   = "practice"
	TabBilling    = "time_billing"
	TabTrust      = "trust"
	TabCalendar   = "calendar"
	TabFiles      = "files_storage"
	TabEditor     = "document_editor"
	TabDocTender  = "doc_tender"
	TabHome       = "home_screen"
	TabSearch     = "lists_search"
	TabMail       = "notifications_mail"
	TabAppearance = "appearance"
	TabAI         = "ai"
	TabModules    = "modules"
	TabUpdates    = "updates"
	TabBackups    = "backups"
	TabAPI        = "agents_api"
)

var (
	ErrUnknown  = errors.New("settings: unknown key")
	ErrInvalid  = errors.New("settings: invalid value")
	ErrReadOnly = errors.New("settings: read only")
	ErrActor    = errors.New("settings: actor required")
)

// Definition is one registry entry: key, type, default, range, tab, and help.
type Definition struct {
	Key      string
	Kind     string
	Default  string
	Min      int64
	Max      int64
	HasMin   bool
	HasMax   bool
	Allowed  []string
	Tab      string
	Help     string
	Locked   bool
	Secret   bool
	Optional bool
	PRD      bool
	Special  string
}

// Change is the audit record for one update or reset. Secret values are redacted.
type Change struct {
	Key      string
	Old      string
	New      string
	Actor    domain.ID
	At       time.Time
	Redacted bool
}

// Entry is a setting plus the value the next request will see.
type Entry struct {
	Definition
	Value      string
	Overridden bool
	SecretSet  bool
}

// Store persists overrides. A missing key means the registry default.
type Store interface {
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	Put(ctx context.Context, key, value string, at time.Time, actor domain.ID) error
	Delete(ctx context.Context, key string) error
}

// Auditor records old and new values. Implementations must not expect secrets in the clear.
type Auditor interface {
	SettingChanged(ctx context.Context, change Change) error
}

// Registry reads and writes settings. Get always reads the store, so a change applies on the next request.
type Registry struct {
	store Store
	audit Auditor
	now   func() time.Time
	byKey map[string]Definition
}

// New builds a registry. audit and store are required.
func New(store Store, audit Auditor) (*Registry, error) {
	return NewWithClock(store, audit, time.Now)
}

// NewWithClock is New with an injectable clock.
func NewWithClock(store Store, audit Auditor, now func() time.Time) (*Registry, error) {
	if store == nil || audit == nil || now == nil {
		return nil, errors.New("settings: store, auditor, and clock are required")
	}
	if err := ValidateCatalog(); err != nil {
		return nil, err
	}
	by := make(map[string]Definition, len(definitions))
	for _, def := range definitions {
		by[def.Key] = def
	}
	return &Registry{store: store, audit: audit, now: now, byKey: by}, nil
}

// Definitions returns a copy of the catalog.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

// ValidateCatalog checks keys, help, and that every default is a legal value.
func ValidateCatalog() error {
	seen := map[string]bool{}
	for _, def := range definitions {
		if def.Key == "" || seen[def.Key] {
			return fmt.Errorf("%w: duplicate or empty key %q", ErrInvalid, def.Key)
		}
		seen[def.Key] = true
		if def.Tab == "" || def.Help == "" || def.Kind == "" {
			return fmt.Errorf("%w: %s missing kind, tab, or help", ErrInvalid, def.Key)
		}
		if err := validate(def, def.Default); err != nil {
			return fmt.Errorf("%s default: %w", def.Key, err)
		}
	}
	return nil
}

// Get returns the stored value or the default.
func (r *Registry) Get(ctx context.Context, key string) (string, error) {
	def, err := r.definition(key)
	if err != nil {
		return "", err
	}
	return r.current(ctx, def)
}

// List returns every setting. Secret values are redacted.
func (r *Registry) List(ctx context.Context) ([]Entry, error) {
	out := make([]Entry, 0, len(definitions))
	for _, def := range definitions {
		value, err := r.current(ctx, def)
		if err != nil {
			return nil, err
		}
		over, ok, err := r.store.Get(ctx, def.Key)
		if err != nil {
			return nil, err
		}
		entry := Entry{Definition: def, Value: value, Overridden: ok}
		if def.Secret {
			if value != "" {
				entry.SecretSet = true
			}
			entry.Value = redactedIfSecret(def, value)
			_ = over
		}
		out = append(out, entry)
	}
	return out, nil
}

// Set validates and stores a value, then audits the old and new values.
func (r *Registry) Set(ctx context.Context, key, value string, actor domain.ID) (Change, error) {
	def, err := r.definition(key)
	if err != nil {
		return Change{}, err
	}
	if actor.IsZero() {
		return Change{}, ErrActor
	}
	if def.Locked && value != def.Default {
		return Change{}, fmt.Errorf("%w: %s", ErrReadOnly, key)
	}
	if err := validate(def, value); err != nil {
		return Change{}, err
	}
	return r.persist(ctx, def, value, actor)
}

// Reset restores the default and audits the change.
func (r *Registry) Reset(ctx context.Context, key string, actor domain.ID) (Change, error) {
	def, err := r.definition(key)
	if err != nil {
		return Change{}, err
	}
	return r.Set(ctx, key, def.Default, actor)
}

func (r *Registry) definition(key string) (Definition, error) {
	def, ok := r.byKey[key]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	return def, nil
}

func (r *Registry) current(ctx context.Context, def Definition) (string, error) {
	value, ok, err := r.store.Get(ctx, def.Key)
	if err != nil {
		return "", err
	}
	if !ok {
		return def.Default, nil
	}
	return value, nil
}

func (r *Registry) persist(ctx context.Context, def Definition, value string, actor domain.ID) (Change, error) {
	prev, ok, err := r.store.Get(ctx, def.Key)
	if err != nil {
		return Change{}, err
	}
	current := def.Default
	if ok {
		current = prev
	}
	at := r.now().UTC()
	change := Change{Key: def.Key, Actor: actor, At: at}
	change.Old = display(def, current)
	change.New = display(def, value)
	change.Redacted = def.Secret
	if value == current {
		return change, nil
	}
	if value == def.Default {
		if err := r.store.Delete(ctx, def.Key); err != nil {
			return Change{}, err
		}
	} else if err := r.store.Put(ctx, def.Key, value, at, actor); err != nil {
		return Change{}, err
	}
	if err := r.audit.SettingChanged(ctx, change); err != nil {
		if ok {
			_ = r.store.Put(ctx, def.Key, prev, at, actor)
		} else {
			_ = r.store.Delete(ctx, def.Key)
		}
		return Change{}, err
	}
	return change, nil
}

func display(def Definition, value string) string {
	if def.Secret {
		return Redacted
	}
	return value
}

func redactedIfSecret(def Definition, value string) string {
	if def.Secret && value != "" {
		return Redacted
	}
	return value
}

func validate(def Definition, value string) error {
	if def.Optional && value == "" && def.Kind != KindBool {
		return nil
	}
	switch def.Kind {
	case KindBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("%w: %s wants true or false", ErrInvalid, def.Key)
		}
	case KindInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		if def.HasMin && n < def.Min || def.HasMax && n > def.Max {
			return fmt.Errorf("%w: %s out of range", ErrInvalid, def.Key)
		}
	case KindCents:
		if def.Optional && value == "" {
			return nil
		}
		n, err := domain.ParseCents(value)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		if def.HasMin && int64(n) < def.Min || def.HasMax && int64(n) > def.Max {
			return fmt.Errorf("%w: %s out of range", ErrInvalid, def.Key)
		}
	case KindDuration:
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		if def.HasMin && int64(d) < def.Min || def.HasMax && int64(d) > def.Max {
			return fmt.Errorf("%w: %s out of range", ErrInvalid, def.Key)
		}
	case KindEnum:
		if !contains(def.Allowed, value) {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case KindJSON:
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case KindString:
		if len(def.Allowed) > 0 && value != "" && !contains(def.Allowed, value) {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		if len(def.Allowed) > 0 && value == "" && !def.Optional {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	default:
		return fmt.Errorf("%w: %s kind", ErrInvalid, def.Key)
	}
	return validateSpecial(def, value)
}

func validateSpecial(def Definition, value string) error {
	if def.Optional && value == "" {
		return nil
	}
	switch def.Special {
	case "":
		return nil
	case "naming_pattern":
		if err := domain.ValidateNamingPattern(value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
	case "time_of_day":
		if !timeOfDay.MatchString(value) {
			return fmt.Errorf("%w: %s time of day", ErrInvalid, def.Key)
		}
	case "day_list":
		parts := strings.Split(value, ",")
		if len(parts) == 0 {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		for _, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || n > 365 {
				return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
			}
		}
	case "regexp_list":
		var patterns []string
		if err := json.Unmarshal([]byte(value), &patterns); err != nil || len(patterns) == 0 {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
		for _, pattern := range patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
			}
		}
	case "confidence":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || n < 0 || n > 1 {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case "backup_scope":
		if value == "all" {
			return nil
		}
		var ids []string
		if err := json.Unmarshal([]byte(value), &ids); err != nil || len(ids) == 0 {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case "zone":
		if _, err := domain.LoadZone(value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case "file_id":
		if _, err := domain.ParseID(value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case "email":
		if !strings.Contains(value, "@") || strings.Contains(value, " ") {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	case "phone":
		if _, err := domain.ParseE164(value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, def.Key)
		}
	default:
		return fmt.Errorf("%w: unknown special %s", ErrInvalid, def.Special)
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

var timeOfDay = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
