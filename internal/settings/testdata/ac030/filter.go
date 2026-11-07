package sample

// Negative fixture: search.hits_scope and api.webhook_attempts are 8.
type Filter struct{ Limit int }

func one() Filter { return Filter{Limit: 8} }

func many() []Filter { return []Filter{{Limit: 8}} }
