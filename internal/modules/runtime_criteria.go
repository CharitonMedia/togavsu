package modules

// RuntimeCriteria lists granular behaviors built and tested by this core.
func RuntimeCriteria() []string {
	return []string{
		"module.service_reads", "module.event_record_reads", "module.contact_mail",
		"module.own_settings", "contract.forwarded_context", "contract.targeted_events",
		"contract.too_large", "webhook.timestamp", "webhook.forward_authorization",
		"file.purge_restrict", "module.named_activity", "migration.up_only",
		"coretest.converter", "contact.phone_row_item",
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
