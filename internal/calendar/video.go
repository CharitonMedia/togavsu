package calendar

import (
	"encoding/json"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// VideoPatch merges only supplied fields. Nil pointers preserve fields; pointers
// to nil clear nullable fields. A null requested resets the boolean to false.
type VideoPatch struct {
	Requested         *bool       `json:"requested,omitempty"`
	Provider          **string    `json:"provider,omitempty"`
	HostUserID        **domain.ID `json:"host_user_id,omitempty"`
	ProviderMeetingID **string    `json:"provider_meeting_id,omitempty"`
}

// UnmarshalJSON preserves the distinction between omitted and explicitly null fields.
func (v *VideoPatch) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return invalidVideo("video", "invalid video object")
	}
	if fields == nil {
		return invalidVideo("video", "invalid video object")
	}
	*v = VideoPatch{}
	for key, raw := range fields {
		var dst any
		switch key {
		case "requested":
			v.Requested = new(bool)
			dst = v.Requested
		case "provider":
			v.Provider = new(*string)
			dst = v.Provider
		case "host_user_id":
			v.HostUserID = new(*domain.ID)
			dst = v.HostUserID
		case "provider_meeting_id":
			v.ProviderMeetingID = new(*string)
			dst = v.ProviderMeetingID
		default:
			return invalidVideo("video", "unknown video field")
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return invalidVideo("video."+key, "invalid video field")
		}
	}
	return nil
}

func (v *VideoPatch) merge(stored *domain.CalendarVideo) *domain.CalendarVideo {
	if v == nil {
		return nil
	}
	next := stored.Clone()
	if next == nil {
		next = &domain.CalendarVideo{}
	}
	if v.Requested != nil {
		next.Requested = *v.Requested
	}
	if v.Provider != nil {
		next.Provider = *v.Provider
	}
	if v.HostUserID != nil {
		next.HostUserID = *v.HostUserID
	}
	if v.ProviderMeetingID != nil {
		next.ProviderMeetingID = *v.ProviderMeetingID
	}
	return next.Clone()
}
