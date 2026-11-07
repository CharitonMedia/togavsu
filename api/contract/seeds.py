"""Seed lists the core contract expects the data layer to load. Not a migration."""

DOC_CATEGORIES = [
    {"key": "court_filing", "tier": "high", "folder_name": "Pleadings", "description": "Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing."},
    {"key": "court_notice", "tier": "high", "folder_name": "Pleadings", "description": "Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link."},
    {"key": "discovery", "tier": "high", "folder_name": "Discovery", "description": "Interrogatories, requests for production or admission, responses, depositions."},
    {"key": "correspondence", "tier": "standard", "folder_name": "Correspondence", "description": "Letter or email to or from the client, counsel, or a third party, including plea offers."},
    {"key": "agency_notice", "tier": "high", "folder_name": "Correspondence", "description": "Letter from a government agency, such as child support, IRS, DOR, Children's Division, or MSPD."},
    {"key": "estate_planning", "tier": "high", "folder_name": "Client documents", "description": "The will, trust, power of attorney, directive, or beneficiary deed itself."},
    {"key": "property_title", "tier": "standard", "folder_name": "Client documents", "description": "Deed, deed of trust, mortgage release, secured note, survey, title."},
    {"key": "financial_record", "tier": "standard", "folder_name": "Client documents", "description": "Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form."},
    {"key": "personal_record", "tier": "standard", "folder_name": "Client documents", "description": "ID, birth, death or dissolution certificate, driving record, completion certificate."},
    {"key": "medical_insurance", "tier": "standard", "folder_name": "Client documents", "description": "Medical record or evaluation, health or vision insurance document."},
    {"key": "contracts_agreements", "tier": "high", "folder_name": "Client documents", "description": "Contract between parties, prenup, articles, bylaws, or operating agreement."},
    {"key": "police_criminal", "tier": "high", "folder_name": "Evidence", "description": "Police report, probable cause statement, citation, bodycam report, restitution."},
    {"key": "evidence_media", "tier": "standard", "folder_name": "Evidence", "description": "Photo, video, screenshot, text messages, or call log, including phone message exports."},
    {"key": "call_meeting", "tier": "standard", "folder_name": "Notes", "description": "Recording, transcript, or notes of a phone call, voicemail, or meeting."},
    {"key": "engagement_intake", "tier": "standard", "folder_name": "Billing", "description": "This firm's fee agreement, invoice, intake or consultation notes, MSPD forms."},
    {"key": "firm_work_product", "tier": "standard", "folder_name": "Notes", "description": "This firm's memo, research, hearing or trial prep, case notes."},
    {"key": "reference", "tier": "standard", "folder_name": "Notes", "description": "Article, statute, template, logo, or blank form; not about a client matter."},
]

PARTY_ROLES = [
    "client",
    "opposing_party",
    "opposing_counsel",
    "prosecutor",
    "judge",
    "expert",
    "witness",
    "co_counsel",
    "guardian_ad_litem",
    "referrer",
    "officer",
    "authorized_user",
]

NAMING_TOKENS = [
    "clientlast",
    "clientfirst",
    "matter#",
    "mattername",
    "what-it-is",
    "doc-date",
    "received-date",
    "category",
]

LAWCRAFT_COMMIT = "770a8fab12392485c0c2789fb254da7d9296df4d"
