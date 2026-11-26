# Settings labels (desk fix 1)

Patrick's report from testing 1.0 on Desk: the admin Settings page leads every row with the code key
(`firm.address`) instead of a name a person reads ("Firm Mailing Address"). Every setting gets a plain
label, shown first. The key stays as the setting's identifier everywhere else (URLs, forms, API, audit log).

Use these labels exactly, in Title Case as written.

| Key | Label |
|---|---|
| firm.name | Firm Name |
| firm.short_name | Short Name |
| firm.address | Firm Mailing Address |
| firm.phone | Firm Phone |
| firm.email | Firm Email |
| firm.website | Firm Website |
| firm.logo_file_id | Firm Logo |
| firm.time_zone | Time Zone |
| firm.matter_number_format | Matter Number Format |
| security.totp_required.admin | Require Two-Factor for Admins |
| security.totp_required.attorney | Require Two-Factor for Attorneys |
| security.totp_required.staff | Require Two-Factor for Staff |
| security.totp_required.accountant | Require Two-Factor for Accountants |
| security.totp_required.client | Require Two-Factor for Clients |
| security.password_optional | Sign In Without a Password |
| security.idle_timeout | Idle Sign-Out Time |
| security.lockout_attempts | Failed Sign-Ins Before Lockout |
| security.lockout_length | Lockout Length |
| security.api_rate_per_minute | API Requests per Minute |
| practice.case_number_patterns | Case Number Patterns |
| practice.conflict_score_minimum | Lowest Conflict Score Shown |
| practice.party_roles | Party Roles |
| practice.markers | Matter Markers |
| billing.time_increment_minutes | Time Increment |
| billing.default_hourly_rate_cents | Default Hourly Rate |
| billing.expense_markup_percent | Expense Markup |
| billing.payment_terms_days | Payment Terms |
| billing.processor_authorization_lifetime | Payment Authorization Lifetime |
| billing.grace_period_days | Grace Period |
| billing.late_interest_enabled | Charge Late Interest |
| billing.late_interest_annual_percent | Late Interest Rate |
| billing.invoice_number_format | Invoice Number Format |
| billing.default_client_message | Default Invoice Message |
| billing.activity_types | Activity Types |
| billing.expense_types | Expense Types |
| trust.evergreen_minimum_cents | Default Evergreen Minimum |
| trust.reconciliation_reminder_day | Reconciliation Reminder Day |
| trust.flag_email_extra | Extra Trust Flag Recipients |
| calendar.deadline_reminder_days | Deadline Reminders |
| calendar.event_reminder_days | Event Reminder |
| calendar.ical_enabled | iCal Feeds |
| calendar.available_dates_range_months | Available Dates Range |
| calendar.available_dates_purpose | Available Dates Purpose |
| calendar.available_dates_title | Available Dates Title |
| files.default_backend | Default Storage |
| files.encrypt_local_disk | Encrypt Local Disk |
| files.encrypt_network | Encrypt Network Path |
| files.encrypt_s3 | Encrypt S3 Storage |
| files.encrypt_dropbox | Encrypt Dropbox |
| files.backup_scope | Storage Included in Backups |
| files.max_upload_bytes | Largest Upload |
| files.upload_chunk_bytes | Upload Chunk Size |
| files.default_folders | Default Matter Folders |
| files.drop_steady_seconds | Drop Folder Wait |
| files.publication_grant_minutes | Module File Access Window |
| editor.lawcraft_commit | LawCraft Version |
| editor.lock_timeout | Edit Lock Timeout |
| editor.max_runs | Editor Runs at Once |
| editor.run_timeout | Editor Run Time Limit |
| editor.run_memory_bytes | Editor Run Memory Limit |
| editor.pdf_previews | PDF Previews |
| doctender.enabled | Doc Tender On |
| doctender.naming_pattern | File Naming Pattern |
| doctender.review_confidence | Review Confidence Threshold |
| doctender.photo_quiet | Photo Grouping Wait |
| doctender.retry_schedule | Retry Schedule |
| doctender.review_recipients | Review Queue Recipients |
| doctender.daily_summary_enabled | Daily Summary Email |
| doctender.daily_summary_time | Daily Summary Time |
| doctender.exception_email_interval | Exception Email Spacing |
| doctender.lease_timeout | Worker Lease Length |
| doctender.max_attempts | Attempts Before Exception |
| doctender.audio_max_gap | Largest Transcript Gap |
| doctender.audio_check_window | Transcript Check Window |
| doctender.video_frame_interval | Video Frame Interval |
| doctender.provider_down_threshold | Provider Outage Threshold |
| home.max_saved_layouts | Saved Layouts per User |
| search.min_chars | Search Start |
| search.delay | Search Delay |
| search.hits_all | Results per Scope on All |
| search.hits_scope | Results in One Scope |
| search.excerpt_chars | File Excerpt Length |
| activity.window_days | Activities Feed Window |
| activity.page_size | Activities Feed Page Size |
| notes.page_size | Notes Page Size |
| mail.smtp_host | Mail Server |
| mail.smtp_port | Mail Server Port |
| mail.smtp_timeout_seconds | Mail Send Time Limit |
| mail.smtp_username | Mail Username |
| mail.smtp_password | Mail Password |
| mail.smtp_starttls | Require STARTTLS |
| mail.smtp_from | System Mail From Address |
| mail.route_through_email_module | Send Through Email Module |
| mail.problem_recipients | Problem Report Recipients |
| notifications.defaults | Default Notifications |
| appearance.preset | Color Preset |
| appearance.heading_font | Heading Font |
| appearance.body_font | Body Font |
| appearance.text_size | Text Size |
| appearance.staff_portal_label | Staff Portal Label |
| appearance.client_portal_label | Client Portal Label |
| appearance.mode | Color Mode |
| appearance.colors | Custom Colors |
| ai.timeout | AI Wait Before Fallback |
| ai.primary_provider | Primary AI Provider |
| ai.secondary_provider | Secondary AI Provider |
| ai.primary_fast_model | Primary Fast Model |
| ai.primary_strong_model | Primary Strong Model |
| ai.secondary_fast_model | Secondary Fast Model |
| ai.secondary_strong_model | Secondary Strong Model |
| ai.openai_api_key | OpenAI API Key |
| ai.anthropic_api_key | Anthropic API Key |
| ai.compatible_api_key | Compatible Endpoint API Key |
| ai.local_api_key | Local Endpoint API Key |
| ai.compatible_base_url | Compatible Endpoint URL |
| ai.local_base_url | Local Model URL |
| ai.speech_to_text_model | Speech-to-Text Model |
| ai.labeling_model | Labeling Model |
| ai.categorizing_model | Categorizing Model |
| ai.labeling_allow_local | Allow Local Labeling |
| ai.categorizing_allow_local | Allow Local Categorizing |
| ai.model_prices | Model Prices |
| ai.task_overrides | Per-Task Model Overrides |
| modules.marketplace_address | Marketplace Address |
| modules.alert_check | Module Check Frequency |
| modules.replace_time | Time and Billing Replacement Module |
| modules.replace_trust | Trust Replacement Module |
| modules.replace_calendar | Calendar Replacement Module |
| updates.window_start | Update Window Start |
| updates.window_end | Update Window End |
| updates.paused | Pause Automatic Updates |
| backups.nightly_time | Nightly Backup Time |
| backups.local_retention_days | Local Backup Retention |
| backups.test_restore_day | Test Restore Day |
| backups.log_retention_days | Log Retention |
| backups.passphrase | Backup Passphrase |
| api.page_size | API Page Size |
| api.max_page_size | API Maximum Page Size |
| api.idempotency_retention_hours | Idempotency Key Retention |
| api.bulk_batch_max | Bulk Request Limit |
| api.webhook_attempts | Webhook Attempts |
| api.webhook_window_hours | Webhook Retry Window |
| llms.show_firm_name | Show Firm Name on llms.txt |
| llms.firm_paragraph | llms.txt Firm Paragraph |

## Batch 2 settings

These names extend the same catalog requirement without changing keys or defaults.

| Key | Label |
| --- | --- |
| `client_access.days_after_close` | Client Access After a Matter Closes |
| `practice.conflict_additional_names_max` | Additional Names per Conflict Check |
| `practice.conflict_name_max_chars` | Conflict Name Length Limit |
| `payments.processor_pending_grant_days` | Days a Payment Service Can Update a Pending Payment |
| `billing.invoice_number_start` | Starting Invoice Number |
| `modules.inbound_webhook_max_bytes` | Incoming Module Message Size Limit |
| `modules.inbound_webhook_rate_per_minute` | Incoming Module Messages per Minute |
| `modules.service_call_limit.createConflictRecord` | Module Calls per Minute: Add Conflict Records |
| `modules.service_call_limit.getCounty` | Module Calls per Minute: Read Counties |
| `modules.service_call_limit.markFileStatus` | Module Calls per Minute: Change File Status |
| `modules.service_call_limit.getNote` | Module Calls per Minute: Read a Note |
| `modules.service_call_limit.listNote` | Module Calls per Minute: List Notes |
| `modules.service_call_limit.listFile` | Module Calls per Minute: List Files |
| `modules.service_call_limit.getDeadline` | Module Calls per Minute: Read a Deadline |
| `modules.service_call_limit.listDeadline` | Module Calls per Minute: List Deadlines |
| `modules.service_call_limit.getCalendarEvent` | Module Calls per Minute: Read a Calendar Event |
| `modules.service_call_limit.listMatterParty` | Module Calls per Minute: List Matter Parties |
| `modules.service_call_limit.getMatterParty` | Module Calls per Minute: Read a Matter Party |
| `modules.service_call_limit.getFirmProfile` | Module Calls per Minute: Read Firm Details |
| `modules.service_call_limit.listUserDirectory` | Module Calls per Minute: List Staff |
| `modules.service_call_limit.listContactAddress` | Module Calls per Minute: List Contact Addresses |
| `modules.service_call_limit.getContactAddress` | Module Calls per Minute: Read a Contact Address |
| `modules.service_call_limit.listPartyRole` | Module Calls per Minute: List Party Roles |
| `modules.service_call_limit.getPartyRole` | Module Calls per Minute: Read a Party Role |
| `modules.service_call_limit.listCustomFieldDefinition` | Module Calls per Minute: List Custom Fields |
| `modules.service_call_limit.getCustomFieldDefinition` | Module Calls per Minute: Read a Custom Field |
| `modules.service_call_limit.listSettings` | Module Calls per Minute: List Settings |
| `modules.service_call_limit.getSetting` | Module Calls per Minute: Read a Setting |
| `modules.service_call_limit.createContact` | Module Calls per Minute: Add Contacts |
| `modules.service_call_limit.getContact` | Module Calls per Minute: Read a Contact |
| `modules.service_call_limit.listContact` | Module Calls per Minute: List Contacts |
| `modules.service_call_limit.createContactEmail` | Module Calls per Minute: Add Contact Emails |
| `modules.service_call_limit.updateContactEmail` | Module Calls per Minute: Change Contact Emails |
| `modules.service_call_limit.getContactEmail` | Module Calls per Minute: Read a Contact Email |
| `modules.service_call_limit.listContactEmail` | Module Calls per Minute: List Contact Emails |
| `modules.service_call_limit.createContactPhone` | Module Calls per Minute: Add Contact Phones |
| `modules.service_call_limit.updateContactPhone` | Module Calls per Minute: Change Contact Phones |
| `modules.service_call_limit.getContactPhone` | Module Calls per Minute: Read a Contact Phone |
| `modules.service_call_limit.listContactPhone` | Module Calls per Minute: List Contact Phones |
| `modules.service_call_limit.createMatter` | Module Calls per Minute: Add Matters |
| `modules.service_call_limit.createMatterParty` | Module Calls per Minute: Add Matter Parties |
| `modules.service_call_limit.getMatter` | Module Calls per Minute: Read a Matter |
| `modules.service_call_limit.listMatter` | Module Calls per Minute: List Matters |
| `modules.service_call_limit.runConflictCheck` | Module Calls per Minute: Run Conflict Checks |
| `modules.service_call_limit.getConflictCheck` | Module Calls per Minute: Read a Conflict Check |
| `modules.service_call_limit.createCalendarEvent` | Module Calls per Minute: Add Calendar Events |
| `modules.service_call_limit.updateCalendarEvent` | Module Calls per Minute: Change Calendar Events |
| `modules.service_call_limit.deleteCalendarEvent` | Module Calls per Minute: Delete Calendar Events |
| `modules.service_call_limit.listCalendarEvent` | Module Calls per Minute: List Calendar Events |
| `modules.service_call_limit.previewAvailableDates` | Module Calls per Minute: Preview Available Dates |
| `modules.service_call_limit.createDeadline` | Module Calls per Minute: Add Deadlines |
| `modules.service_call_limit.updateDeadline` | Module Calls per Minute: Change Deadlines |
| `modules.service_call_limit.createNote` | Module Calls per Minute: Add Notes |
| `modules.service_call_limit.listMatterTimeline` | Module Calls per Minute: Read Matter Timelines |
| `modules.service_call_limit.listFileRenditions` | Module Calls per Minute: List File Copies |
| `modules.service_call_limit.downloadFileRendition` | Module Calls per Minute: Download File Copies |
| `modules.public_page_rate_per_minute` | Public Module Page Requests per Minute |
| `modules.public_page_max_bytes` | Public Module Page Request Size Limit |
| `modules.contract_call_max_bytes` | Module to Module Request Size Limit |
| `modules.staff_banner_order` | Staff Banner Order |
