package domain

// EventRead is a verified, expiring core grant for the one record an event names.
// Request handlers never populate it from unsigned input. A grant does not confer
// roles, scopes, attorney-only access, writes, or access to related records.
type EventRead struct {
	Module     string `json:"module"`
	EventID    string `json:"event_id"`
	RecordType string `json:"record_type"`
	RecordID   ID     `json:"record_id"`
	MatterID   ID     `json:"matter_id"`
	ExpiresAt  int64  `json:"exp"`
}

// EventRecord permits only the named record, as the module's service principal.
func (p Principal) EventRecord(record string, id, matter ID) bool {
	g := p.EventRead
	return g != nil && p.Source == SourceService && p.UserID.IsZero() && p.ModuleID == g.Module &&
		record == g.RecordType && id != "" && id == g.RecordID && matter == g.MatterID
}
