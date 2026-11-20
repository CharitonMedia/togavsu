package modules

// RuntimeCriteria lists granular behaviors built and tested by this core.
func RuntimeCriteria() []string {
	return []string{
		"module.service_reads", "module.contact_mail",
		"module.own_settings", "contract.forwarded_context", "contract.targeted_events",
		"contract.too_large", "webhook.timestamp", "webhook.forward_authorization",
		"file.purge_restrict", "module.named_activity", "migration.up_only",
		"coretest.converter", "contact.phone_row_item",
		"module.permission_reasons", "module.form_proxy", "file.trashed_delivery",
		"calendar.busy_across_walls", "conflict.named_people", "module.county_read",
		"file.service_status", "file.publication_author", "contract.document_generated",
		"coretest.doc_tender", "contract.processor_payment",
	}
}

// RuntimeSupported is the behavior registry used by pending.yaml.
func RuntimeSupported(criterion string) bool {
	for _, built := range RuntimeCriteria() {
		if built == criterion {
			return true
		}
	}
	return false
}
