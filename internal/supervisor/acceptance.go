package supervisor

// Acceptance is the platform view of a product criterion. Scaffolding does
// not mark these passed.
type Acceptance struct {
	ID        string
	Status    string
	BlockedBy string
}

// PlatformAcceptance reports the criteria this assignment touches. Full
// behavior waits on core, identity, and signed images.
func PlatformAcceptance() []Acceptance {
	return []Acceptance{
		{ID: "AC-001", Status: "pending", BlockedBy: "core first-run setup is not in this assignment"},
		{ID: "AC-016", Status: "pending", BlockedBy: "signed core images and the zero-downtime load test are not runnable"},
		{ID: "AC-018", Status: "pending", BlockedBy: "the product audit log is identity; a full firm restore waits on the core schema"},
	}
}
