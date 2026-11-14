package domain

// Named product defaults. Settings and seed read these so the literal lives in one place.
const (
	MatterNumberFormat    = "YYYY-NNNN"
	LawCraftCommit        = "770a8fab12392485c0c2789fb254da7d9296df4d"
	AvailableDatesTitle   = "AVAILABLE DATES FOR HEARING"
	AvailableDatesPurpose = "one day trial"
	LabelingModel         = "gpt-6.1-sol"
	StaffPortalLabel      = "Staff portal"
	NamingPattern         = "<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>"
	// MarketplaceAddress is a sentinel until the marketplace task publishes a hostname.
	// ponytail: sentinel is not a URL. Upgrade path: replace it with the marketplace origin. trigger: marketplace task publishes the service URL.
	MarketplaceAddress = "togavsu-marketplace"
)

// Registry fallbacks. Practice uses these only when the settings registry is absent.
// The catalog defaults in the settings package are these same values.
const (
	ConflictScoreMinimum        = 60
	BillingTimeIncrementMinutes = 6
	SearchMinChars              = 2
	SearchHitsAll               = 5
	SearchHitsScope             = 8
	ActivityWindowDays          = 30
	ActivityPageSize            = 100
	NotesPageSize               = 25
)

// Fixed API bounds, not firm settings: the contract's Idempotency-Key maxLength, and the
// shortest provider key whose last four characters the AI tab shows.
const (
	IdempotencyKeyMaxChars = 200
	KeyHintMinChars        = 8
)

// DocCategory is one default filing category.
type DocCategory struct {
	Meta
	Name        string `db:"name"`
	Description string `db:"description"`
	Tier        string `db:"tier"`
	Folder      string `db:"folder"`
	Active      bool   `db:"active"`
}

func (DocCategory) TableName() string { return "doc_categories" }

// DefaultDocCategories is the PRD category list. High-tier names match section 8.21.
func DefaultDocCategories() []DocCategory {
	return []DocCategory{
		{Name: "court_filing", Description: "Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing", Tier: "high", Folder: "Pleadings", Active: true},
		{Name: "court_notice", Description: "Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link", Tier: "high", Folder: "Pleadings", Active: true},
		{Name: "discovery", Description: "Interrogatories, requests for production or admission, responses, depositions", Tier: "high", Folder: "Discovery", Active: true},
		{Name: "correspondence", Description: "Letter or email to or from the client, counsel or a third party, including plea offers", Tier: "standard", Folder: "Correspondence", Active: true},
		{Name: "agency_notice", Description: "Letter from a government agency, such as child support, IRS, DOR, Children's Division or MSPD", Tier: "high", Folder: "Correspondence", Active: true},
		{Name: "estate_planning", Description: "The will, trust, power of attorney, directive or beneficiary deed itself", Tier: "high", Folder: "Client documents", Active: true},
		{Name: "property_title", Description: "Deed, deed of trust, mortgage release, secured note, survey, title", Tier: "standard", Folder: "Client documents", Active: true},
		{Name: "financial_record", Description: "Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form", Tier: "standard", Folder: "Client documents", Active: true},
		{Name: "personal_record", Description: "ID, birth, death or dissolution certificate, driving record, completion certificate", Tier: "standard", Folder: "Client documents", Active: true},
		{Name: "medical_insurance", Description: "Medical record or evaluation, health or vision insurance document", Tier: "standard", Folder: "Client documents", Active: true},
		{Name: "contracts_agreements", Description: "Contract between parties, prenup, articles, bylaws or operating agreement", Tier: "high", Folder: "Client documents", Active: true},
		{Name: "police_criminal", Description: "Police report, probable cause statement, citation, bodycam report, restitution", Tier: "high", Folder: "Evidence", Active: true},
		{Name: "evidence_media", Description: "Photo, video, screenshot, text messages or call log, including phone message exports", Tier: "standard", Folder: "Evidence", Active: true},
		{Name: "call_meeting", Description: "Recording, transcript or notes of a phone call, voicemail or meeting", Tier: "standard", Folder: "Notes", Active: true},
		{Name: "engagement_intake", Description: "This firm's fee agreement, invoice, intake or consultation notes, MSPD forms", Tier: "standard", Folder: "Billing", Active: true},
		{Name: "firm_work_product", Description: "This firm's memo, research, hearing or trial prep, case notes", Tier: "standard", Folder: "Notes", Active: true},
		{Name: "reference", Description: "Article, statute, template, logo or blank form; not about a client matter", Tier: "standard", Folder: "Notes", Active: true},
	}
}

// DefaultPartyRoles is the matter-party role list from section 5.
func DefaultPartyRoles() []string {
	return []string{
		"client", "opposing party", "opposing counsel", "prosecutor", "judge", "expert",
		"witness", "co-counsel", "guardian ad litem", "referrer", "officer", "authorized user",
	}
}

// DefaultFolders is the per-practice-area folder list.
func DefaultFolders() []string {
	return []string{"Correspondence", "Pleadings", "Discovery", "Evidence", "Client documents", "Billing", "Notes"}
}

// DefaultMarkers is the core matter-marker list.
func DefaultMarkers() []string {
	return []string{"Pro bono", "Zero fee", "Also asked about"}
}

// DefaultRetrySchedule is six attempts: two immediately, then 15 minutes, 2 hours, 12 hours, and 24 hours.
const DefaultRetrySchedule = `["0s","0s","15m0s","2h0m0s","12h0m0s","24h0m0s"]`

// DefaultNotificationPrefs is the firm channel grid: every kind in-app, and email for the named kinds.
// The trust-flag email cannot be turned off.
const DefaultNotificationPrefs = `{"email":["trust_flag","deadline_reminder","update_failure","module_alert"],"email_locked":["trust_flag"],"in_app":["trust_flag","deadline_reminder","replenishment","matter_assignment","update_failure","module_alert"]}`

// HighTierCategories are the categories that need two agreeing models.
func HighTierCategories() []string {
	return []string{
		"court_filing", "court_notice", "discovery", "agency_notice",
		"police_criminal", "estate_planning", "contracts_agreements",
	}
}
