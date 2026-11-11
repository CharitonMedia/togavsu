package marketplace

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"time"
)

// limits are the marketplace's input-safety bounds and polling periods (fixed.json).
// They are not firm settings; the firm's choices are modules.marketplace_address and
// modules.alert_check in the settings registry.
type limits struct {
	ClientTimeoutSeconds int            `json:"client_timeout_seconds"`
	MaxPackageBytes      int64          `json:"max_package_bytes"`
	MaxManifestBytes     int64          `json:"max_manifest_bytes"`
	MaxExpandedBytes     int64          `json:"max_expanded_bytes"`
	MaxPackageEntries    int            `json:"max_package_entries"`
	MaxResponseBytes     int64          `json:"max_response_bytes"`
	MaxNoteRunes         int            `json:"max_note_runes"`
	MaxQueryModuleIDs    int            `json:"max_query_module_ids"`
	AlertCheckHours      map[string]int `json:"alert_check_hours"`
	RetryAfterFailureMin int            `json:"retry_after_failure_minutes"`
	DuePollSeconds       int            `json:"due_poll_seconds"`
	WidgetRows           int            `json:"widget_rows"`
	CheckOnOpenSeconds   int            `json:"check_on_open_seconds"`
}

//go:embed fixed.json
var fixedJSON []byte

var fixed = func() limits {
	var l limits
	if err := json.Unmarshal(fixedJSON, &l); err != nil {
		panic(fmt.Sprintf("marketplace: fixed.json: %v", err))
	}
	return l
}()

// MaxPackageBytes is the largest signed package the client or the service accepts.
func MaxPackageBytes() int64 { return fixed.MaxPackageBytes }

// MaxNoteRunes is the longest operator flag or pull note.
func MaxNoteRunes() int { return fixed.MaxNoteRunes }

func seconds(n int) time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%ds", n))
	return d
}

func minutes(n int) time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%dm", n))
	return d
}

func hoursOf(n int) time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%dh", n))
	return d
}

// clientTimeout bounds one marketplace request.
func clientTimeout() time.Duration { return seconds(fixed.ClientTimeoutSeconds) }

// AlertPeriod is how often the core polls for the modules.alert_check value. An unknown
// value falls back to the registry default, daily.
func AlertPeriod(value string) time.Duration {
	if n, ok := fixed.AlertCheckHours[value]; ok {
		return hoursOf(n)
	}
	return hoursOf(fixed.AlertCheckHours["daily"])
}
