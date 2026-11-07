# API and MCP coverage

Generated from `api/openapi/v1/openapi.json`. This is the contract matrix for AC-002.
It does not claim the handlers or the MCP server are implemented.

| Feature | Method | Path | Operation | MCP | Roles | Scopes |
| --- | --- | --- | --- | --- | --- | --- |
| openapi | `GET` | `/api/v1/openapi.json` | `getOpenAPI` | required:getOpenAPI | public | none |
| llms | `GET` | `/llms.txt` | `getLlmsTxt` | required:getLlmsTxt | public | none |
| llms | `GET` | `/api/v1/llms.txt` | `getLlmsTxtV1` | alias:getLlmsTxt | public | none |
| health | `GET` | `/healthz` | `getHealthz` | required:getHealthz | public | none |
| health | `GET` | `/api/v1/health` | `getHealth` | alias:getHealthz | public | none |
| mcp | `POST` | `/mcp` | `postMcp` | transport:transport | admin, attorney, staff, accountant, client | none |
| setup | `GET` | `/api/v1/setup` | `getSetup` | required:getSetup | public | none |
| setup | `POST` | `/api/v1/setup` | `createSetup` | required:createSetup | public | none |
| auth | `POST` | `/api/v1/auth/login` | `login` | required:login | public | none |
| auth | `POST` | `/api/v1/auth/totp` | `verifyTotp` | required:verifyTotp | public | none |
| auth | `POST` | `/api/v1/auth/recovery` | `verifyRecoveryCode` | required:verifyRecoveryCode | public | none |
| auth | `POST` | `/api/v1/auth/logout` | `logout` | required:logout | admin, attorney, staff, accountant, client | sessions:write |
| auth | `POST` | `/api/v1/auth/password-resets` | `requestPasswordReset` | required:requestPasswordReset | public | none |
| auth | `POST` | `/api/v1/auth/password-resets/confirm` | `confirmPasswordReset` | required:confirmPasswordReset | public | none |
| profile | `GET` | `/api/v1/me` | `getMe` | required:getMe | admin, attorney, staff, accountant, client | profile:read |
| profile | `PATCH` | `/api/v1/me` | `updateMe` | required:updateMe | admin, attorney, staff, accountant, client | profile:write |
| profile | `POST` | `/api/v1/me/password` | `changePassword` | required:changePassword | admin, attorney, staff, accountant, client | profile:write |
| auth | `POST` | `/api/v1/me/totp/reset` | `resetTotp` | required:resetTotp | admin, attorney, staff, accountant, client | profile:write |
| auth | `POST` | `/api/v1/me/recovery-codes` | `regenerateRecoveryCodes` | required:regenerateRecoveryCodes | admin, attorney, staff, accountant, client | profile:write |
| me-settings | `GET` | `/api/v1/me/settings` | `getMeSettings` | required:getMeSettings | admin, attorney, staff, accountant, client | profile:read |
| me-settings | `PATCH` | `/api/v1/me/settings` | `updateMeSettings` | required:updateMeSettings | admin, attorney, staff, accountant, client | profile:write |
| notification-preferences | `GET` | `/api/v1/me/notification-preferences` | `getNotificationPreferences` | required:getNotificationPreferences | admin, attorney, staff, accountant | notifications:read |
| notification-preferences | `PUT` | `/api/v1/me/notification-preferences` | `putNotificationPreferences` | required:putNotificationPreferences | admin, attorney, staff, accountant | notifications:write |
| sidebar | `GET` | `/api/v1/me/sidebar` | `getSidebarState` | required:getSidebarState | admin, attorney, staff, accountant | profile:read |
| sidebar | `PUT` | `/api/v1/me/sidebar` | `putSidebarState` | required:putSidebarState | admin, attorney, staff, accountant | profile:write |
| auth | `GET` | `/api/v1/sessions` | `listSessions` | required:listSessions | admin, attorney, staff, accountant, client | sessions:read |
| auth | `DELETE` | `/api/v1/sessions/{session_id}` | `deleteSession` | required:deleteSession | admin, attorney, staff, accountant, client | sessions:write |
| users | `GET` | `/api/v1/users` | `listUser` | required:listUser | admin | users:read |
| users | `POST` | `/api/v1/users` | `createUser` | required:createUser | admin | users:write |
| users | `GET` | `/api/v1/users/{user_id}` | `getUser` | required:getUser | admin | users:read |
| users | `PATCH` | `/api/v1/users/{user_id}` | `updateUser` | required:updateUser | admin | users:write |
| users | `POST` | `/api/v1/users/{user_id}/deactivate` | `deactivateUser` | required:deactivateUser | admin | users:write |
| api-tokens, profile | `GET` | `/api/v1/api-tokens` | `listApiToken` | required:listApiToken | admin, attorney, staff, accountant, client | tokens:read |
| api-tokens | `POST` | `/api/v1/api-tokens` | `createApiToken` | required:createApiToken | admin, attorney, staff, accountant, client | tokens:write |
| api-tokens, profile | `GET` | `/api/v1/api-tokens/{token_id}` | `getApiToken` | required:getApiToken | admin, attorney, staff, accountant, client | tokens:read |
| api-tokens | `DELETE` | `/api/v1/api-tokens/{token_id}` | `revokeApiToken` | required:revokeApiToken | admin, attorney, staff, accountant, client | tokens:write |
| contacts | `GET` | `/api/v1/contacts` | `listContact` | required:listContact | admin, attorney, staff | contacts:read |
| contacts | `POST` | `/api/v1/contacts` | `createContact` | required:createContact | admin, attorney, staff | contacts:write |
| contacts | `GET` | `/api/v1/contacts/{contact_id}` | `getContact` | required:getContact | admin, attorney, staff | contacts:read |
| contacts | `PATCH` | `/api/v1/contacts/{contact_id}` | `updateContact` | required:updateContact | admin, attorney, staff | contacts:write |
| contacts | `POST` | `/api/v1/contacts/{contact_id}/archive` | `archiveContact` | required:archiveContact | admin, attorney, staff | contacts:write |
| contacts, edit-history | `GET` | `/api/v1/contacts/{contact_id}/history` | `historyContact` | required:historyContact | admin, attorney, staff | contacts:read |
| contacts, bulk | `POST` | `/api/v1/contacts:bulk` | `bulkContact` | required:bulkContact | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-addresses` | `listContactAddress` | required:listContactAddress | admin, attorney, staff | contacts:read |
| contact-channels | `POST` | `/api/v1/contact-addresses` | `createContactAddress` | required:createContactAddress | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-addresses/{address_id}` | `getContactAddress` | required:getContactAddress | admin, attorney, staff | contacts:read |
| contact-channels | `PATCH` | `/api/v1/contact-addresses/{address_id}` | `updateContactAddress` | required:updateContactAddress | admin, attorney, staff | contacts:write |
| contact-channels, bulk | `POST` | `/api/v1/contact-addresses:bulk` | `bulkContactAddress` | required:bulkContactAddress | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-emails` | `listContactEmail` | required:listContactEmail | admin, attorney, staff | contacts:read |
| contact-channels | `POST` | `/api/v1/contact-emails` | `createContactEmail` | required:createContactEmail | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-emails/{email_id}` | `getContactEmail` | required:getContactEmail | admin, attorney, staff | contacts:read |
| contact-channels | `PATCH` | `/api/v1/contact-emails/{email_id}` | `updateContactEmail` | required:updateContactEmail | admin, attorney, staff | contacts:write |
| contact-channels, bulk | `POST` | `/api/v1/contact-emails:bulk` | `bulkContactEmail` | required:bulkContactEmail | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-phones` | `listContactPhone` | required:listContactPhone | admin, attorney, staff | contacts:read |
| contact-channels | `POST` | `/api/v1/contact-phones` | `createContactPhone` | required:createContactPhone | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-phones/{phone_id}` | `getContactPhone` | required:getContactPhone | admin, attorney, staff | contacts:read |
| contact-channels | `PATCH` | `/api/v1/contact-phones/{phone_id}` | `updateContactPhone` | required:updateContactPhone | admin, attorney, staff | contacts:write |
| contact-channels, bulk | `POST` | `/api/v1/contact-phones:bulk` | `bulkContactPhone` | required:bulkContactPhone | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-tags` | `listContactTag` | required:listContactTag | admin, attorney, staff | contacts:read |
| contact-channels | `POST` | `/api/v1/contact-tags` | `createContactTag` | required:createContactTag | admin, attorney, staff | contacts:write |
| contact-channels | `GET` | `/api/v1/contact-tags/{tag_id}` | `getContactTag` | required:getContactTag | admin, attorney, staff | contacts:read |
| contact-channels | `PATCH` | `/api/v1/contact-tags/{tag_id}` | `updateContactTag` | required:updateContactTag | admin, attorney, staff | contacts:write |
| contact-channels, bulk | `POST` | `/api/v1/contact-tags:bulk` | `bulkContactTag` | required:bulkContactTag | admin, attorney, staff | contacts:write |
| contacts, merge | `POST` | `/api/v1/contacts/{contact_id}/merge` | `mergeContact` | required:mergeContact | admin, attorney | contacts:write |
| matters, matter-glance, include-extensions | `GET` | `/api/v1/matters` | `listMatter` | required:listMatter | admin, attorney, staff | matters:read |
| matters, matter-glance, include-extensions | `POST` | `/api/v1/matters` | `createMatter` | required:createMatter | admin, attorney, staff | matters:write |
| matters, matter-glance, include-extensions | `GET` | `/api/v1/matters/{matter_id}` | `getMatter` | required:getMatter | admin, attorney, staff | matters:read |
| matters, matter-glance, include-extensions | `PATCH` | `/api/v1/matters/{matter_id}` | `updateMatter` | required:updateMatter | admin, attorney, staff | matters:write |
| matters, matter-glance, include-extensions, edit-history | `GET` | `/api/v1/matters/{matter_id}/history` | `historyMatter` | required:historyMatter | admin, attorney, staff | matters:read |
| matters, matter-glance, include-extensions, bulk | `POST` | `/api/v1/matters:bulk` | `bulkMatter` | required:bulkMatter | admin, attorney, staff | matters:write |
| matter-close | `GET` | `/api/v1/matters/{matter_id}/close-preview` | `previewMatterClose` | required:previewMatterClose | admin, attorney, staff | matters:read |
| matter-close | `POST` | `/api/v1/matters/{matter_id}/close` | `closeMatter` | required:closeMatter | admin, attorney, staff | matters:write |
| matter-reopen | `POST` | `/api/v1/matters/{matter_id}/reopen` | `reopenMatter` | required:reopenMatter | admin, attorney | matters:write |
| matters, merge | `POST` | `/api/v1/matters/{matter_id}/merge` | `mergeMatter` | required:mergeMatter | admin, attorney | matters:write |
| missing-case-number | `GET` | `/api/v1/worklists/matters-missing-case-number` | `listMattersMissingCaseNumber` | required:listMattersMissingCaseNumber | admin, attorney, staff | matters:read |
| activity | `GET` | `/api/v1/matters/{matter_id}/timeline` | `listMatterTimeline` | required:listMatterTimeline | admin, attorney, staff | events:read |
| matter-parties | `GET` | `/api/v1/matter-parties` | `listMatterParty` | required:listMatterParty | admin, attorney, staff | matters:read |
| matter-parties | `POST` | `/api/v1/matter-parties` | `createMatterParty` | required:createMatterParty | admin, attorney, staff | matters:write |
| matter-parties | `GET` | `/api/v1/matter-parties/{party_id}` | `getMatterParty` | required:getMatterParty | admin, attorney, staff | matters:read |
| matter-parties | `PATCH` | `/api/v1/matter-parties/{party_id}` | `updateMatterParty` | required:updateMatterParty | admin, attorney, staff | matters:write |
| matter-parties, bulk | `POST` | `/api/v1/matter-parties:bulk` | `bulkMatterParty` | required:bulkMatterParty | admin, attorney, staff | matters:write |
| matter-walls | `PUT` | `/api/v1/matters/{matter_id}/wall` | `putMatterWall` | required:putMatterWall | admin | matters:write |
| matter-walls | `GET` | `/api/v1/matters/{matter_id}/wall` | `getMatterWall` | required:getMatterWall | admin | matters:read |
| matter-markers | `GET` | `/api/v1/matter-markers` | `listMatterMarker` | required:listMatterMarker | admin, attorney, staff | matters:read |
| matter-markers | `POST` | `/api/v1/matter-markers` | `createMatterMarker` | required:createMatterMarker | admin, attorney, staff | matters:write |
| matter-markers | `GET` | `/api/v1/matter-markers/{matter_marker_id}` | `getMatterMarker` | required:getMatterMarker | admin, attorney, staff | matters:read |
| matter-markers | `PATCH` | `/api/v1/matter-markers/{matter_marker_id}` | `updateMatterMarker` | required:updateMatterMarker | admin, attorney, staff | matters:write |
| matter-markers | `DELETE` | `/api/v1/matter-markers/{matter_marker_id}` | `deleteMatterMarker` | required:deleteMatterMarker | admin, attorney, staff | matters:write |
| matter-markers | `GET` | `/api/v1/marker-definitions` | `listMarkerDefinition` | required:listMarkerDefinition | admin, attorney, staff | settings:read |
| matter-markers | `POST` | `/api/v1/marker-definitions` | `createMarkerDefinition` | required:createMarkerDefinition | admin | settings:write |
| matter-markers | `GET` | `/api/v1/marker-definitions/{marker_id}` | `getMarkerDefinition` | required:getMarkerDefinition | admin, attorney, staff | settings:read |
| matter-markers | `PATCH` | `/api/v1/marker-definitions/{marker_id}` | `updateMarkerDefinition` | required:updateMarkerDefinition | admin | settings:write |
| fee-agreements | `GET` | `/api/v1/fee-agreements` | `listFeeAgreement` | required:listFeeAgreement | admin, attorney, staff | matters:read |
| fee-agreements | `POST` | `/api/v1/fee-agreements` | `createFeeAgreement` | required:createFeeAgreement | admin, attorney, staff | matters:write |
| fee-agreements | `GET` | `/api/v1/fee-agreements/{fee_agreement_id}` | `getFeeAgreement` | required:getFeeAgreement | admin, attorney, staff | matters:read |
| fee-agreements | `PATCH` | `/api/v1/fee-agreements/{fee_agreement_id}` | `updateFeeAgreement` | required:updateFeeAgreement | admin, attorney, staff | matters:write |
| fee-agreements, edit-history | `GET` | `/api/v1/fee-agreements/{fee_agreement_id}/history` | `historyFeeAgreement` | required:historyFeeAgreement | admin, attorney, staff | matters:read |
| fee-agreements, bulk | `POST` | `/api/v1/fee-agreements:bulk` | `bulkFeeAgreement` | required:bulkFeeAgreement | admin, attorney, staff | matters:write |
| notes | `GET` | `/api/v1/notes` | `listNote` | required:listNote | admin, attorney, staff | notes:read |
| notes | `POST` | `/api/v1/notes` | `createNote` | required:createNote | admin, attorney, staff | notes:write |
| notes | `GET` | `/api/v1/notes/{note_id}` | `getNote` | required:getNote | admin, attorney, staff | notes:read |
| notes | `PATCH` | `/api/v1/notes/{note_id}` | `updateNote` | required:updateNote | admin, attorney, staff | notes:write |
| notes | `POST` | `/api/v1/notes/{note_id}/archive` | `archiveNote` | required:archiveNote | admin, attorney, staff | notes:write |
| notes, bulk | `POST` | `/api/v1/notes:bulk` | `bulkNote` | required:bulkNote | admin, attorney, staff | notes:write |
| conflicts | `GET` | `/api/v1/conflict-records` | `listConflictRecord` | required:listConflictRecord | admin, attorney, staff | conflicts:read |
| conflicts | `POST` | `/api/v1/conflict-records` | `createConflictRecord` | required:createConflictRecord | admin, attorney, staff | conflicts:write |
| conflicts | `GET` | `/api/v1/conflict-records/{conflict_record_id}` | `getConflictRecord` | required:getConflictRecord | admin, attorney, staff | conflicts:read |
| conflicts | `PATCH` | `/api/v1/conflict-records/{conflict_record_id}` | `updateConflictRecord` | required:updateConflictRecord | admin, attorney, staff | conflicts:write |
| conflicts, bulk | `POST` | `/api/v1/conflict-records:bulk` | `bulkConflictRecord` | required:bulkConflictRecord | admin, attorney, staff | conflicts:write |
| conflicts | `POST` | `/api/v1/matters/{matter_id}/conflict-checks` | `runConflictCheck` | required:runConflictCheck | admin, attorney, staff | conflicts:read |
| conflicts | `GET` | `/api/v1/conflict-checks` | `listConflictChecks` | required:listConflictChecks | admin, attorney, staff | conflicts:read |
| conflicts | `GET` | `/api/v1/conflict-checks/{conflict_check_id}` | `getConflictCheck` | required:getConflictCheck | admin, attorney, staff | conflicts:read |
| conflicts | `POST` | `/api/v1/conflict-checks/{conflict_check_id}/decisions` | `decideConflictHit` | required:decideConflictHit | admin, attorney | conflicts:decide |
| practice-areas | `GET` | `/api/v1/practice-areas` | `listPracticeArea` | required:listPracticeArea | admin, attorney, staff | settings:read |
| practice-areas | `POST` | `/api/v1/practice-areas` | `createPracticeArea` | required:createPracticeArea | admin | settings:write |
| practice-areas | `GET` | `/api/v1/practice-areas/{practice_area_id}` | `getPracticeArea` | required:getPracticeArea | admin, attorney, staff | settings:read |
| practice-areas | `PATCH` | `/api/v1/practice-areas/{practice_area_id}` | `updatePracticeArea` | required:updatePracticeArea | admin | settings:write |
| counties | `GET` | `/api/v1/counties` | `listCounty` | required:listCounty | admin, attorney, staff | settings:read |
| counties | `POST` | `/api/v1/counties` | `createCounty` | required:createCounty | admin | settings:write |
| counties | `GET` | `/api/v1/counties/{county_id}` | `getCounty` | required:getCounty | admin, attorney, staff | settings:read |
| counties | `PATCH` | `/api/v1/counties/{county_id}` | `updateCounty` | required:updateCounty | admin | settings:write |
| referral-sources | `GET` | `/api/v1/referral-sources` | `listReferralSource` | required:listReferralSource | admin, attorney, staff | settings:read |
| referral-sources | `POST` | `/api/v1/referral-sources` | `createReferralSource` | required:createReferralSource | admin | settings:write |
| referral-sources | `GET` | `/api/v1/referral-sources/{referral_source_id}` | `getReferralSource` | required:getReferralSource | admin, attorney, staff | settings:read |
| referral-sources | `PATCH` | `/api/v1/referral-sources/{referral_source_id}` | `updateReferralSource` | required:updateReferralSource | admin | settings:write |
| party-roles | `GET` | `/api/v1/party-roles` | `listPartyRole` | required:listPartyRole | admin, attorney, staff | settings:read |
| party-roles | `POST` | `/api/v1/party-roles` | `createPartyRole` | required:createPartyRole | admin | settings:write |
| party-roles | `GET` | `/api/v1/party-roles/{party_role_id}` | `getPartyRole` | required:getPartyRole | admin, attorney, staff | settings:read |
| party-roles | `PATCH` | `/api/v1/party-roles/{party_role_id}` | `updatePartyRole` | required:updatePartyRole | admin | settings:write |
| fee-items | `GET` | `/api/v1/fee-items` | `listFeeItem` | required:listFeeItem | admin, attorney, staff | settings:read |
| fee-items | `POST` | `/api/v1/fee-items` | `createFeeItem` | required:createFeeItem | admin | settings:write |
| fee-items | `GET` | `/api/v1/fee-items/{fee_item_id}` | `getFeeItem` | required:getFeeItem | admin, attorney, staff | settings:read |
| fee-items | `PATCH` | `/api/v1/fee-items/{fee_item_id}` | `updateFeeItem` | required:updateFeeItem | admin | settings:write |
| activity-types | `GET` | `/api/v1/activity-types` | `listActivityType` | required:listActivityType | admin, attorney, staff | settings:read |
| activity-types | `POST` | `/api/v1/activity-types` | `createActivityType` | required:createActivityType | admin | settings:write |
| activity-types | `GET` | `/api/v1/activity-types/{activity_type_id}` | `getActivityType` | required:getActivityType | admin, attorney, staff | settings:read |
| activity-types | `PATCH` | `/api/v1/activity-types/{activity_type_id}` | `updateActivityType` | required:updateActivityType | admin | settings:write |
| expense-types | `GET` | `/api/v1/expense-types` | `listExpenseType` | required:listExpenseType | admin, attorney, staff | settings:read |
| expense-types | `POST` | `/api/v1/expense-types` | `createExpenseType` | required:createExpenseType | admin | settings:write |
| expense-types | `GET` | `/api/v1/expense-types/{expense_type_id}` | `getExpenseType` | required:getExpenseType | admin, attorney, staff | settings:read |
| expense-types | `PATCH` | `/api/v1/expense-types/{expense_type_id}` | `updateExpenseType` | required:updateExpenseType | admin | settings:write |
| custom-fields | `GET` | `/api/v1/custom-field-definitions` | `listCustomFieldDefinition` | required:listCustomFieldDefinition | admin, attorney, staff | settings:read |
| custom-fields | `POST` | `/api/v1/custom-field-definitions` | `createCustomFieldDefinition` | required:createCustomFieldDefinition | admin | settings:write |
| custom-fields | `GET` | `/api/v1/custom-field-definitions/{definition_id}` | `getCustomFieldDefinition` | required:getCustomFieldDefinition | admin, attorney, staff | settings:read |
| custom-fields | `PATCH` | `/api/v1/custom-field-definitions/{definition_id}` | `updateCustomFieldDefinition` | required:updateCustomFieldDefinition | admin | settings:write |
| custom-fields | `POST` | `/api/v1/custom-field-definitions/{definition_id}/retire` | `retireCustomFieldDefinition` | required:retireCustomFieldDefinition | admin | settings:write |
| custom-fields | `PUT` | `/api/v1/matters/{matter_id}/custom-fields` | `putMatterCustomFields` | required:putMatterCustomFields | admin, attorney, staff | matters:write |
| custom-fields | `PUT` | `/api/v1/time-entries/{time_entry_id}/custom-fields` | `putTimeEntryCustomFields` | required:putTimeEntryCustomFields | admin, attorney, staff | time:write |
| custom-fields | `PUT` | `/api/v1/expenses/{expense_id}/custom-fields` | `putExpenseCustomFields` | required:putExpenseCustomFields | admin, attorney, staff | expenses:write |
| custom-fields, bulk | `POST` | `/api/v1/custom-field-values:bulk` | `bulkCustomFieldValue` | required:bulkCustomFieldValue | admin, attorney, staff | contacts:write, matters:write, time:write, expenses:write |
| custom-fields | `PUT` | `/api/v1/contacts/{contact_id}/custom-fields` | `putContactCustomFields` | required:putContactCustomFields | admin, attorney, staff | contacts:write |
| time-entries | `GET` | `/api/v1/time-entries` | `listTimeEntry` | required:listTimeEntry | admin, attorney, staff, accountant | time:read |
| time-entries | `POST` | `/api/v1/time-entries` | `createTimeEntry` | required:createTimeEntry | admin, attorney, staff | time:write |
| time-entries | `GET` | `/api/v1/time-entries/{time_entry_id}` | `getTimeEntry` | required:getTimeEntry | admin, attorney, staff, accountant | time:read |
| time-entries | `PATCH` | `/api/v1/time-entries/{time_entry_id}` | `updateTimeEntry` | required:updateTimeEntry | admin, attorney, staff | time:write |
| time-entries, edit-history | `GET` | `/api/v1/time-entries/{time_entry_id}/history` | `historyTimeEntry` | required:historyTimeEntry | admin, attorney, staff, accountant | time:read |
| time-entries, bulk | `POST` | `/api/v1/time-entries:bulk` | `bulkTimeEntry` | required:bulkTimeEntry | admin, attorney, staff | time:write |
| timers | `GET` | `/api/v1/timers` | `listTimer` | required:listTimer | admin, attorney, staff | time:read |
| timers | `POST` | `/api/v1/timers` | `createTimer` | required:createTimer | admin, attorney, staff | time:write |
| timers | `GET` | `/api/v1/timers/{timer_id}` | `getTimer` | required:getTimer | admin, attorney, staff | time:read |
| timers | `POST` | `/api/v1/timers/{timer_id}/pause` | `pauseTimer` | required:pauseTimer | admin, attorney, staff | time:write |
| timers | `POST` | `/api/v1/timers/{timer_id}/stop` | `stopTimer` | required:stopTimer | admin, attorney, staff | time:write |
| expenses | `GET` | `/api/v1/expenses` | `listExpense` | required:listExpense | admin, attorney, staff, accountant | expenses:read |
| expenses | `POST` | `/api/v1/expenses` | `createExpense` | required:createExpense | admin, attorney, staff | expenses:write |
| expenses | `GET` | `/api/v1/expenses/{expense_id}` | `getExpense` | required:getExpense | admin, attorney, staff, accountant | expenses:read |
| expenses | `PATCH` | `/api/v1/expenses/{expense_id}` | `updateExpense` | required:updateExpense | admin, attorney, staff | expenses:write |
| expenses, edit-history | `GET` | `/api/v1/expenses/{expense_id}/history` | `historyExpense` | required:historyExpense | admin, attorney, staff, accountant | expenses:read |
| expenses, bulk | `POST` | `/api/v1/expenses:bulk` | `bulkExpense` | required:bulkExpense | admin, attorney, staff | expenses:write |
| invoices | `GET` | `/api/v1/invoices` | `listInvoice` | required:listInvoice | admin, attorney, staff, accountant, client | invoices:read |
| invoices | `GET` | `/api/v1/invoices/{invoice_id}` | `getInvoice` | required:getInvoice | admin, attorney, staff, accountant, client | invoices:read |
| invoices | `PATCH` | `/api/v1/invoices/{invoice_id}` | `updateInvoice` | required:updateInvoice | admin, attorney, staff | invoices:write |
| invoices, edit-history | `GET` | `/api/v1/invoices/{invoice_id}/history` | `historyInvoice` | required:historyInvoice | admin, attorney, staff, accountant, client | invoices:read |
| invoices, billing-worklist | `POST` | `/api/v1/invoice-drafts` | `createInvoiceDrafts` | required:createInvoiceDrafts | admin, attorney, staff | invoices:write |
| invoices | `POST` | `/api/v1/invoices/{invoice_id}/approve` | `approveInvoice` | required:approveInvoice | admin, attorney | invoices:approve |
| invoices | `POST` | `/api/v1/invoices/{invoice_id}/send` | `sendInvoice` | required:sendInvoice | admin, attorney | invoices:send |
| invoices | `GET` | `/api/v1/invoices/{invoice_id}/pdf` | `getInvoicePdf` | required:getInvoicePdf | admin, attorney, staff, accountant, client | invoices:read |
| invoices | `POST` | `/api/v1/invoices/{invoice_id}/void` | `voidInvoice` | required:voidInvoice | admin, attorney | invoices:write |
| collections, trust-ledger | `POST` | `/api/v1/invoices/{invoice_id}/apply-trust` | `applyTrustToInvoice` | required:applyTrustToInvoice | admin, attorney | trust:withdraw |
| invoices, collections | `POST` | `/api/v1/invoices/{invoice_id}/write-off` | `writeOffInvoice` | required:writeOffInvoice | admin, attorney | invoices:write |
| collections, trust-replenishment | `POST` | `/api/v1/invoices/{invoice_id}/replenishment-requests` | `requestInvoiceReplenishment` | required:requestInvoiceReplenishment | admin, attorney, staff | trust:write |
| payments, collections | `GET` | `/api/v1/payments` | `listPayment` | required:listPayment | admin, attorney, staff, accountant | payments:read |
| payments, collections | `POST` | `/api/v1/payments` | `createPayment` | required:createPayment | admin, attorney, staff | payments:write |
| payments, collections | `GET` | `/api/v1/payments/{payment_id}` | `getPayment` | required:getPayment | admin, attorney, staff, accountant | payments:read |
| payments, collections, bulk | `POST` | `/api/v1/payments:bulk` | `bulkPayment` | required:bulkPayment | admin, attorney, staff | payments:write |
| payments | `POST` | `/api/v1/payments/{payment_id}/status` | `updatePaymentStatus` | required:updatePaymentStatus | admin, attorney, staff | payments:write |
| payments | `POST` | `/api/v1/payments/{payment_id}/reverse` | `reversePayment` | required:reversePayment | admin, attorney | payments:write |
| billing-worklist | `GET` | `/api/v1/billing/worklist` | `getBillingWorklist` | required:getBillingWorklist | admin, attorney, staff, accountant | invoices:read |
| billing-worklist, trust-ledger | `POST` | `/api/v1/billing/flat-fees/{matter_id}/apply` | `applyFlatFee` | required:applyFlatFee | admin, attorney | trust:withdraw |
| reports | `GET` | `/api/v1/reports/unbilled-time` | `reportUnbilledTime` | required:reportUnbilledTime | admin, attorney, staff, accountant | reports:read |
| reports | `GET` | `/api/v1/reports/time-by-user` | `reportTimeByUser` | required:reportTimeByUser | admin, attorney, staff, accountant | reports:read |
| reports | `GET` | `/api/v1/reports/ar-aging` | `reportArAging` | required:reportArAging | admin, attorney, staff, accountant | reports:read |
| reports | `GET` | `/api/v1/reports/payments-received` | `reportPaymentsReceived` | required:reportPaymentsReceived | admin, attorney, staff, accountant | reports:read |
| reports | `GET` | `/api/v1/reports/write-offs` | `reportWriteOffs` | required:reportWriteOffs | admin, attorney, staff, accountant | reports:read |
| reports, trust-reports | `GET` | `/api/v1/reports/trust-balances` | `reportTrustBalances` | required:reportTrustBalances | admin, attorney, staff, accountant | trust:read |
| reports, trust-reports | `GET` | `/api/v1/reports/trust-client-ledger` | `reportTrustClientLedger` | required:reportTrustClientLedger | admin, attorney, staff, accountant | trust:read |
| reports, trust-reports | `GET` | `/api/v1/reports/trust-account-ledger` | `reportTrustAccountLedger` | required:reportTrustAccountLedger | admin, attorney, staff, accountant | trust:read |
| reports, trust-reports | `GET` | `/api/v1/reports/reconciliations` | `reportReconciliations` | required:reportReconciliations | admin, attorney, staff, accountant | trust:read |
| reports | `GET` | `/api/v1/reports/doc-tender-spend` | `reportDocTenderSpend` | required:reportDocTenderSpend | admin, attorney, staff, accountant | reports:read |
| trust-accounts | `GET` | `/api/v1/trust-accounts` | `listTrustAccount` | required:listTrustAccount | admin, attorney, staff, accountant | trust:read |
| trust-accounts | `POST` | `/api/v1/trust-accounts` | `createTrustAccount` | required:createTrustAccount | admin | trust:write |
| trust-accounts | `GET` | `/api/v1/trust-accounts/{trust_account_id}` | `getTrustAccount` | required:getTrustAccount | admin, attorney, staff, accountant | trust:read |
| trust-accounts | `PATCH` | `/api/v1/trust-accounts/{trust_account_id}` | `updateTrustAccount` | required:updateTrustAccount | admin | trust:write |
| trust-ledger, trust-below-zero | `GET` | `/api/v1/trust-ledger` | `listTrustEntry` | required:listTrustEntry | admin, attorney, staff, accountant | trust:read |
| trust-ledger, trust-below-zero | `POST` | `/api/v1/trust-ledger` | `postTrustEntry` | required:postTrustEntry | admin, attorney, staff | trust:write |
| trust-ledger, trust-below-zero | `GET` | `/api/v1/trust-ledger/{trust_entry_id}` | `getTrustEntry` | required:getTrustEntry | admin, attorney, staff, accountant | trust:read |
| trust-ledger, trust-below-zero, bulk | `POST` | `/api/v1/trust-ledger:bulk` | `bulkTrustEntry` | required:bulkTrustEntry | admin, attorney, staff | trust:write |
| trust-ledger | `POST` | `/api/v1/trust-ledger/{trust_entry_id}/reversals` | `reverseTrustEntry` | required:reverseTrustEntry | admin, attorney | trust:withdraw |
| trust-ledger | `GET` | `/api/v1/trust/balances` | `getTrustBalances` | required:getTrustBalances | admin, attorney, staff, accountant | trust:read |
| trust-flags | `GET` | `/api/v1/trust-flags` | `listTrustFlag` | required:listTrustFlag | admin, attorney, staff, accountant | trust:read |
| trust-flags | `GET` | `/api/v1/trust-flags/{trust_flag_id}` | `getTrustFlag` | required:getTrustFlag | admin, attorney, staff, accountant | trust:read |
| trust-flags | `POST` | `/api/v1/trust-flags/{trust_flag_id}/resolve` | `resolveTrustFlag` | required:resolveTrustFlag | admin, attorney | trust:write |
| trust-replenishment | `GET` | `/api/v1/trust-replenishment-requests` | `listTrustReplenishment` | required:listTrustReplenishment | admin, attorney, staff, accountant | trust:read |
| trust-replenishment | `POST` | `/api/v1/trust-replenishment-requests` | `createTrustReplenishment` | required:createTrustReplenishment | admin, attorney, staff | trust:write |
| trust-replenishment | `GET` | `/api/v1/trust-replenishment-requests/{replenishment_id}` | `getTrustReplenishment` | required:getTrustReplenishment | admin, attorney, staff, accountant | trust:read |
| trust-replenishment | `POST` | `/api/v1/trust-replenishment-requests/{replenishment_id}/cancel` | `cancelTrustReplenishment` | required:cancelTrustReplenishment | admin, attorney, staff | trust:write |
| trust-replenishment | `POST` | `/api/v1/trust-replenishment-requests/{replenishment_id}/satisfy` | `satisfyTrustReplenishment` | required:satisfyTrustReplenishment | admin, attorney, staff | trust:write |
| trust-reconciliation | `GET` | `/api/v1/trust-reconciliations` | `listTrustReconciliation` | required:listTrustReconciliation | admin, attorney, staff, accountant | trust:read |
| trust-reconciliation | `POST` | `/api/v1/trust-reconciliations` | `createTrustReconciliation` | required:createTrustReconciliation | admin, attorney, staff | trust:write |
| trust-reconciliation | `GET` | `/api/v1/trust-reconciliations/{reconciliation_id}` | `getTrustReconciliation` | required:getTrustReconciliation | admin, attorney, staff, accountant | trust:read |
| calendar | `GET` | `/api/v1/calendar-events` | `listCalendarEvent` | required:listCalendarEvent | admin, attorney, staff | calendar:read |
| calendar | `POST` | `/api/v1/calendar-events` | `createCalendarEvent` | required:createCalendarEvent | admin, attorney, staff | calendar:write |
| calendar | `GET` | `/api/v1/calendar-events/{calendar_event_id}` | `getCalendarEvent` | required:getCalendarEvent | admin, attorney, staff | calendar:read |
| calendar | `PATCH` | `/api/v1/calendar-events/{calendar_event_id}` | `updateCalendarEvent` | required:updateCalendarEvent | admin, attorney, staff | calendar:write |
| calendar | `DELETE` | `/api/v1/calendar-events/{calendar_event_id}` | `deleteCalendarEvent` | required:deleteCalendarEvent | admin, attorney, staff | calendar:write |
| calendar, bulk | `POST` | `/api/v1/calendar-events:bulk` | `bulkCalendarEvent` | required:bulkCalendarEvent | admin, attorney, staff | calendar:write |
| deadlines | `GET` | `/api/v1/deadlines` | `listDeadline` | required:listDeadline | admin, attorney, staff | deadlines:read |
| deadlines | `POST` | `/api/v1/deadlines` | `createDeadline` | required:createDeadline | admin, attorney, staff | deadlines:write |
| deadlines | `GET` | `/api/v1/deadlines/{deadline_id}` | `getDeadline` | required:getDeadline | admin, attorney, staff | deadlines:read |
| deadlines | `PATCH` | `/api/v1/deadlines/{deadline_id}` | `updateDeadline` | required:updateDeadline | admin, attorney, staff | deadlines:write |
| deadlines, bulk | `POST` | `/api/v1/deadlines:bulk` | `bulkDeadline` | required:bulkDeadline | admin, attorney, staff | deadlines:write |
| available-dates | `POST` | `/api/v1/calendar/available-dates` | `previewAvailableDates` | required:previewAvailableDates | admin, attorney, staff | calendar:read |
| ical | `GET` | `/api/v1/ical-feeds` | `listIcalFeed` | required:listIcalFeed | admin, attorney, staff | calendar:read |
| ical | `POST` | `/api/v1/ical-feeds` | `createIcalFeed` | required:createIcalFeed | admin, attorney, staff | calendar:write |
| ical | `GET` | `/api/v1/ical-feeds/{feed_id}` | `getIcalFeed` | required:getIcalFeed | admin, attorney, staff | calendar:read |
| ical | `POST` | `/api/v1/ical-feeds/{feed_id}/revoke` | `revokeIcalFeed` | required:revokeIcalFeed | admin, attorney, staff | calendar:write |
| ical | `GET` | `/ical/{secret}.ics` | `downloadIcal` | required:downloadIcal | public | none |
| files | `GET` | `/api/v1/files` | `listFile` | required:listFile | admin, attorney, staff, accountant, client | files:read |
| files | `GET` | `/api/v1/files/{file_id}` | `getFile` | required:getFile | admin, attorney, staff, accountant, client | files:read |
| files, file-uploads, bulk | `POST` | `/api/v1/files:bulk` | `bulkFile` | required:bulkFile | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/rename` | `renameFile` | required:renameFile | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/move` | `moveFile` | required:moveFile | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/status` | `markFileStatus` | required:markFileStatus | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/trash` | `trashFile` | required:trashFile | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/restore` | `restoreFile` | required:restoreFile | admin, attorney, staff | files:write |
| files | `POST` | `/api/v1/files/{file_id}/purge` | `purgeFile` | required:purgeFile | admin | files:write |
| file-versions | `GET` | `/api/v1/files/{file_id}/versions` | `listFileVersions` | required:listFileVersions | admin, attorney, staff, accountant, client | files:read |
| file-versions, file-uploads | `POST` | `/api/v1/files/{file_id}/versions` | `addFileVersion` | required:addFileVersion | admin, attorney, staff | files:write |
| file-versions, quarantine | `GET` | `/api/v1/files/{file_id}/versions/{version_id}/download` | `downloadFileVersion` | required:downloadFileVersion | admin, attorney, staff, accountant, client | files:read |
| file-versions | `POST` | `/api/v1/files/{file_id}/versions/{version_id}/restore` | `restoreFileVersion` | required:restoreFileVersion | admin, attorney, staff | files:write |
| quarantine | `POST` | `/api/v1/files/{file_id}/versions/{version_id}/quarantine` | `quarantineFileVersion` | required:quarantineFileVersion | admin, attorney, staff | files:write |
| file-renditions | `GET` | `/api/v1/files/{file_id}/renditions` | `listFileRenditions` | required:listFileRenditions | admin, attorney, staff, accountant, client | files:read |
| file-renditions | `GET` | `/api/v1/files/{file_id}/renditions/{rendition_id}` | `downloadFileRendition` | required:downloadFileRendition | admin, attorney, staff, accountant, client | files:read |
| file-uploads | `POST` | `/api/v1/file-uploads` | `createFileUpload` | required:createFileUpload | admin, attorney, staff, client | files:write |
| file-uploads | `GET` | `/api/v1/file-uploads/{upload_id}` | `getFileUpload` | required:getFileUpload | admin, attorney, staff, client | files:write |
| file-uploads | `PUT` | `/api/v1/file-uploads/{upload_id}` | `putFileChunk` | required:putFileChunk | admin, attorney, staff, client | files:write |
| file-uploads | `POST` | `/api/v1/file-uploads/{upload_id}/complete` | `completeFileUpload` | required:completeFileUpload | admin, attorney, staff, client | files:write |
| files | `POST` | `/api/v1/file-archives` | `createFileArchive` | required:createFileArchive | admin, attorney, staff, accountant, client | files:read |
| folders | `GET` | `/api/v1/folders` | `listFolder` | required:listFolder | admin, attorney, staff | files:read |
| folders | `POST` | `/api/v1/folders` | `createFolder` | required:createFolder | admin, attorney, staff | files:write |
| folders | `GET` | `/api/v1/folders/{folder_id}` | `getFolder` | required:getFolder | admin, attorney, staff | files:read |
| folders | `PATCH` | `/api/v1/folders/{folder_id}` | `updateFolder` | required:updateFolder | admin, attorney, staff | files:write |
| folders | `DELETE` | `/api/v1/folders/{folder_id}` | `deleteFolder` | required:deleteFolder | admin, attorney, staff | files:write |
| edit-locks, editor | `POST` | `/api/v1/files/{file_id}/lock` | `acquireEditLock` | required:acquireEditLock | admin, attorney, staff | files:write |
| edit-locks | `DELETE` | `/api/v1/files/{file_id}/lock` | `releaseEditLock` | required:releaseEditLock | admin, attorney, staff | files:write |
| edit-locks | `POST` | `/api/v1/files/{file_id}/lock/renew` | `renewEditLock` | required:renewEditLock | admin, attorney, staff | files:write |
| editor | `GET` | `/api/v1/editor` | `getEditor` | required:getEditor | admin, attorney, staff, accountant | files:read |
| editor-runs | `GET` | `/api/v1/editor/commands` | `listEditorCommands` | required:listEditorCommands | admin, attorney, staff | editor:run |
| editor-runs | `POST` | `/api/v1/files/{file_id}/editor-runs` | `runEditor` | required:runEditor | admin, attorney, staff | editor:run |
| conversions | `POST` | `/api/v1/files/{file_id}/conversions` | `convertFile` | required:convertFile | admin, attorney, staff | editor:run |
| court-profiles | `GET` | `/api/v1/court-profiles` | `listCourtProfile` | required:listCourtProfile | admin, attorney, staff | files:read |
| court-profiles | `POST` | `/api/v1/court-profiles` | `createCourtProfile` | required:createCourtProfile | admin, attorney, staff | files:write |
| court-profiles | `GET` | `/api/v1/court-profiles/{court_profile_id}` | `getCourtProfile` | required:getCourtProfile | admin, attorney, staff | files:read |
| court-profiles | `PATCH` | `/api/v1/court-profiles/{court_profile_id}` | `updateCourtProfile` | required:updateCourtProfile | admin, attorney, staff | files:write |
| court-profiles | `POST` | `/api/v1/court-profiles/{court_profile_id}/verify` | `verifyCourtProfile` | required:verifyCourtProfile | admin, attorney | files:write |
| storage-backends | `GET` | `/api/v1/storage-backends` | `listStorageBackend` | required:listStorageBackend | admin | files:write |
| storage-backends | `POST` | `/api/v1/storage-backends` | `createStorageBackend` | required:createStorageBackend | admin | files:write |
| storage-backends | `GET` | `/api/v1/storage-backends/{storage_backend_id}` | `getStorageBackend` | required:getStorageBackend | admin | files:write |
| storage-backends | `PATCH` | `/api/v1/storage-backends/{storage_backend_id}` | `updateStorageBackend` | required:updateStorageBackend | admin | files:write |
| storage-backends | `POST` | `/api/v1/storage-backends/{storage_backend_id}/test` | `testStorageBackend` | required:testStorageBackend | admin | files:write |
| storage-moves | `GET` | `/api/v1/storage-moves` | `listStorageMove` | required:listStorageMove | admin | files:read |
| storage-moves | `POST` | `/api/v1/storage-moves` | `createStorageMove` | required:createStorageMove | admin | files:write |
| storage-moves | `GET` | `/api/v1/storage-moves/{storage_move_id}` | `getStorageMove` | required:getStorageMove | admin | files:read |
| storage-moves | `POST` | `/api/v1/storage-moves/{storage_move_id}/confirm` | `confirmStorageMove` | required:confirmStorageMove | admin | files:write |
| doc-tender | `GET` | `/api/v1/doc-tender/jobs` | `listDocTenderJobs` | required:listDocTenderJobs | admin, attorney, staff | doc_tender:read |
| doc-tender | `GET` | `/api/v1/doc-tender/jobs/{job_id}` | `getDocTenderJob` | required:getDocTenderJob | admin, attorney, staff | doc_tender:read |
| doc-tender | `POST` | `/api/v1/doc-tender/jobs/{job_id}/retry` | `retryDocTenderJob` | required:retryDocTenderJob | admin, attorney | doc_tender:write |
| doc-tender | `GET` | `/api/v1/doc-tender/exceptions` | `listDocTenderException` | required:listDocTenderException | admin, attorney | doc_tender:read |
| doc-tender | `GET` | `/api/v1/doc-tender/exceptions/{exception_id}` | `getDocTenderException` | required:getDocTenderException | admin, attorney | doc_tender:read |
| doc-tender | `POST` | `/api/v1/doc-tender/exceptions/{exception_id}/resolve` | `resolveDocTenderException` | required:resolveDocTenderException | admin, attorney | doc_tender:write |
| doc-tender | `GET` | `/api/v1/doc-tender/categories` | `listDocCategory` | required:listDocCategory | admin, attorney, staff | doc_tender:read |
| doc-tender | `POST` | `/api/v1/doc-tender/categories` | `createDocCategory` | required:createDocCategory | admin | doc_tender:write |
| doc-tender | `GET` | `/api/v1/doc-tender/categories/{category_id}` | `getDocCategory` | required:getDocCategory | admin, attorney, staff | doc_tender:read |
| doc-tender | `PATCH` | `/api/v1/doc-tender/categories/{category_id}` | `updateDocCategory` | required:updateDocCategory | admin | doc_tender:write |
| doc-tender | `GET` | `/api/v1/doc-tender/naming` | `getDocTenderNaming` | required:getDocTenderNaming | admin, attorney, staff | doc_tender:read |
| doc-tender | `PUT` | `/api/v1/doc-tender/naming` | `putDocTenderNaming` | required:putDocTenderNaming | admin | doc_tender:write |
| search | `GET` | `/api/v1/search` | `search` | required:search | admin, attorney, staff | search:use |
| nav | `GET` | `/api/v1/nav/context` | `getNavContext` | required:getNavContext | admin, attorney, staff, accountant | profile:read |
| notifications | `GET` | `/api/v1/notifications` | `listNotification` | required:listNotification | admin, attorney, staff, accountant, client | notifications:read |
| notifications | `POST` | `/api/v1/notifications` | `createNotification` | required:createNotification | admin, attorney, staff | notifications:write |
| notifications | `GET` | `/api/v1/notifications/{notification_id}` | `getNotification` | required:getNotification | admin, attorney, staff, accountant, client | notifications:read |
| notifications | `POST` | `/api/v1/notifications/{notification_id}/read` | `markNotificationRead` | required:markNotificationRead | admin, attorney, staff, accountant, client | notifications:write |
| notifications | `POST` | `/api/v1/notifications:read-all` | `markAllNotificationsRead` | required:markAllNotificationsRead | admin, attorney, staff, accountant, client | notifications:write |
| widgets | `GET` | `/api/v1/widgets` | `listWidgets` | required:listWidgets | admin, attorney, staff, accountant | layouts:read |
| widgets, time-entries | `GET` | `/api/v1/widgets/my-time` | `widgetMyTime` | required:widgetMyTime | admin, attorney, staff | time:read |
| widgets, timers | `GET` | `/api/v1/widgets/running-timer` | `widgetRunningTimer` | required:widgetRunningTimer | admin, attorney, staff | time:read |
| widgets, time-entries | `GET` | `/api/v1/widgets/unbilled` | `widgetUnbilled` | required:widgetUnbilled | admin, attorney, staff, accountant | time:read |
| widgets, matters | `GET` | `/api/v1/widgets/my-open-matters` | `widgetMyOpenMatters` | required:widgetMyOpenMatters | admin, attorney, staff | matters:read |
| widgets, deadlines | `GET` | `/api/v1/widgets/upcoming-deadlines` | `widgetUpcomingDeadlines` | required:widgetUpcomingDeadlines | admin, attorney, staff | deadlines:read |
| widgets, calendar | `GET` | `/api/v1/widgets/todays-calendar` | `widgetTodaysCalendar` | required:widgetTodaysCalendar | admin, attorney, staff | calendar:read |
| widgets, trust-flags | `GET` | `/api/v1/widgets/trust-alerts` | `widgetTrustAlerts` | required:widgetTrustAlerts | admin, attorney, staff, accountant | trust:read |
| widgets, trust-replenishment | `GET` | `/api/v1/widgets/replenishment-requests` | `widgetReplenishmentRequests` | required:widgetReplenishmentRequests | admin, attorney, staff, accountant | trust:read |
| widgets, reports | `GET` | `/api/v1/widgets/ar-aging` | `widgetArAging` | required:widgetArAging | admin, attorney, staff, accountant | reports:read |
| widgets, activity | `GET` | `/api/v1/widgets/recent-activity` | `widgetRecentActivity` | required:widgetRecentActivity | admin, attorney, staff | events:read |
| widgets, conflicts | `GET` | `/api/v1/widgets/pending-conflict-checks` | `widgetPendingConflictChecks` | required:widgetPendingConflictChecks | admin, attorney, staff | conflicts:read |
| widgets, modules | `GET` | `/api/v1/widgets/module-alerts` | `widgetModuleAlerts` | required:widgetModuleAlerts | admin | modules:read |
| layouts | `GET` | `/api/v1/me/layout` | `getCurrentLayout` | required:getCurrentLayout | admin, attorney, staff, accountant | layouts:read |
| layouts | `PUT` | `/api/v1/me/layout` | `putCurrentLayout` | required:putCurrentLayout | admin, attorney, staff, accountant | layouts:write |
| layouts | `GET` | `/api/v1/me/layouts` | `listHomeLayout` | required:listHomeLayout | admin, attorney, staff, accountant | layouts:read |
| layouts | `POST` | `/api/v1/me/layouts` | `createHomeLayout` | required:createHomeLayout | admin, attorney, staff, accountant | layouts:write |
| layouts | `GET` | `/api/v1/me/layouts/{layout_id}` | `getHomeLayout` | required:getHomeLayout | admin, attorney, staff, accountant | layouts:read |
| layouts | `PATCH` | `/api/v1/me/layouts/{layout_id}` | `updateHomeLayout` | required:updateHomeLayout | admin, attorney, staff, accountant | layouts:write |
| layouts | `DELETE` | `/api/v1/me/layouts/{layout_id}` | `deleteHomeLayout` | required:deleteHomeLayout | admin, attorney, staff, accountant | layouts:write |
| layouts | `POST` | `/api/v1/me/layouts/{layout_id}/default` | `setDefaultLayout` | required:setDefaultLayout | admin, attorney, staff, accountant | layouts:write |
| layouts | `POST` | `/api/v1/me/layouts/{layout_id}/apply` | `applyLayout` | required:applyLayout | admin, attorney, staff, accountant | layouts:write |
| layouts, role-layouts | `GET` | `/api/v1/me/layout-reset-options` | `listLayoutResetOptions` | required:listLayoutResetOptions | admin, attorney, staff, accountant | layouts:read |
| role-layouts | `GET` | `/api/v1/role-layouts/{role}` | `getRoleLayout` | required:getRoleLayout | admin | layouts:read |
| role-layouts | `PUT` | `/api/v1/role-layouts/{role}` | `putRoleLayout` | required:putRoleLayout | admin | layouts:write |
| help | `GET` | `/api/v1/help/sections` | `listHelpSection` | required:listHelpSection | admin, attorney, staff, accountant | help:read |
| help | `POST` | `/api/v1/help/sections` | `createHelpSection` | required:createHelpSection | admin | help:write |
| help | `GET` | `/api/v1/help/sections/{section_id}` | `getHelpSection` | required:getHelpSection | admin, attorney, staff, accountant | help:read |
| help | `PATCH` | `/api/v1/help/sections/{section_id}` | `updateHelpSection` | required:updateHelpSection | admin | help:write |
| help | `DELETE` | `/api/v1/help/sections/{section_id}` | `deleteHelpSection` | required:deleteHelpSection | admin | help:write |
| help | `GET` | `/api/v1/help/items` | `listHelpItem` | required:listHelpItem | admin, attorney, staff, accountant | help:read |
| help | `POST` | `/api/v1/help/items` | `createHelpItem` | required:createHelpItem | admin | help:write |
| help | `GET` | `/api/v1/help/items/{item_id}` | `getHelpItem` | required:getHelpItem | admin, attorney, staff, accountant | help:read |
| help | `PATCH` | `/api/v1/help/items/{item_id}` | `updateHelpItem` | required:updateHelpItem | admin | help:write |
| help | `DELETE` | `/api/v1/help/items/{item_id}` | `deleteHelpItem` | required:deleteHelpItem | admin | help:write |
| help-guide | `GET` | `/api/v1/help/topics` | `listHelpTopics` | required:listHelpTopics | admin, attorney, staff, accountant | help:read |
| help-guide | `GET` | `/api/v1/help/guide` | `getHelpGuide` | required:getHelpGuide | admin, attorney, staff, accountant, client | help:read |
| help-guide | `GET` | `/api/v1/help/guide.pdf` | `getHelpGuidePdf` | required:getHelpGuidePdf | admin, attorney, staff, accountant, client | help:read |
| help-guide | `GET` | `/help/guide` | `getHelpGuideAlias` | alias:getHelpGuide | admin, attorney, staff, accountant, client | help:read |
| help-guide | `GET` | `/help/guide.pdf` | `getHelpGuidePdfAlias` | alias:getHelpGuidePdf | admin, attorney, staff, accountant, client | help:read |
| module-help | `GET` | `/api/v1/help/modules/{module_id}/pages` | `listModuleHelpPages` | required:listModuleHelpPages | admin, attorney, staff, accountant | help:read |
| module-help | `POST` | `/api/v1/help/modules/{module_id}/pages` | `createModuleHelpPage` | required:createModuleHelpPage | admin | help:write |
| module-help | `PUT` | `/api/v1/help/modules/{module_id}/pages` | `replaceModuleHelpPages` | required:replaceModuleHelpPages | admin | help:write |
| module-help | `DELETE` | `/api/v1/help/modules/{module_id}/pages/{page_id}` | `deleteModuleHelpPage` | required:deleteModuleHelpPage | admin | help:write |
| activity, events | `GET` | `/api/v1/events` | `listEvents` | required:listEvents | admin, attorney, staff | events:read |
| events | `POST` | `/api/v1/events` | `createEvent` | required:createEvent | admin, attorney, staff | events:write |
| events, bulk | `POST` | `/api/v1/events:bulk` | `bulkEvent` | required:bulkEvent | admin, attorney, staff | events:write |
| audit | `GET` | `/api/v1/audit-log` | `listAuditLog` | required:listAuditLog | admin | audit:read |
| webhooks | `GET` | `/api/v1/webhook-deliveries` | `listWebhookDeliveries` | required:listWebhookDeliveries | admin | webhooks:read |
| settings | `GET` | `/api/v1/settings` | `listSettings` | required:listSettings | admin | settings:read |
| settings | `GET` | `/api/v1/settings/{setting_key}` | `getSetting` | required:getSetting | admin | settings:read |
| settings | `PATCH` | `/api/v1/settings/{setting_key}` | `updateSetting` | required:updateSetting | admin | settings:write |
| settings | `POST` | `/api/v1/settings/{setting_key}/reset` | `resetSetting` | required:resetSetting | admin | settings:write |
| appearance | `GET` | `/api/v1/settings/appearance` | `getAppearance` | required:getAppearance | admin, attorney, staff, accountant | settings:read |
| appearance | `PUT` | `/api/v1/settings/appearance` | `putAppearance` | required:putAppearance | admin | settings:write |
| ai | `GET` | `/api/v1/ai/providers` | `listAiProviders` | required:listAiProviders | admin | ai:admin |
| ai | `PUT` | `/api/v1/ai/providers/{provider}/key` | `putAiProviderKey` | required:putAiProviderKey | admin | ai:admin |
| ai | `POST` | `/api/v1/ai/providers/{provider}/check` | `checkAiProvider` | required:checkAiProvider | admin | ai:admin |
| ai | `POST` | `/api/v1/ai/calls` | `createAiCall` | required:createAiCall | admin, attorney, staff | ai:use |
| ai | `GET` | `/api/v1/ai/calls` | `listAiCalls` | required:listAiCalls | admin | ai:admin |
| mail | `PUT` | `/api/v1/mail/smtp-password` | `putSmtpPassword` | required:putSmtpPassword | admin | settings:write |
| mail | `POST` | `/api/v1/mail/test` | `testMail` | required:testMail | admin | settings:write |
| problem-reports | `GET` | `/api/v1/problem-reports` | `listProblemReport` | required:listProblemReport | admin | settings:read |
| problem-reports | `POST` | `/api/v1/problem-reports` | `createProblemReport` | required:createProblemReport | admin, attorney, staff, accountant | profile:write |
| problem-reports | `GET` | `/api/v1/problem-reports/{problem_report_id}` | `getProblemReport` | required:getProblemReport | admin | settings:read |
| seats | `GET` | `/api/v1/system/seats` | `getSeatCount` | required:getSeatCount | admin | settings:read |
| backups | `GET` | `/api/v1/backups` | `listBackup` | required:listBackup | admin | backups:admin |
| backups | `POST` | `/api/v1/backups` | `createBackup` | required:createBackup | admin | backups:admin |
| backups | `GET` | `/api/v1/backups/{backup_id}` | `getBackup` | required:getBackup | admin | backups:admin |
| backups | `DELETE` | `/api/v1/backups/{backup_id}` | `deleteBackup` | required:deleteBackup | admin | backups:admin |
| backups | `GET` | `/api/v1/backups/{backup_id}/download` | `downloadBackup` | required:downloadBackup | admin | backups:admin |
| backup-destinations | `GET` | `/api/v1/backup-destinations` | `listBackupDestination` | required:listBackupDestination | admin | backups:admin |
| backup-destinations | `POST` | `/api/v1/backup-destinations` | `createBackupDestination` | required:createBackupDestination | admin | backups:admin |
| backup-destinations | `GET` | `/api/v1/backup-destinations/{destination_id}` | `getBackupDestination` | required:getBackupDestination | admin | backups:admin |
| backup-destinations | `PATCH` | `/api/v1/backup-destinations/{destination_id}` | `updateBackupDestination` | required:updateBackupDestination | admin | backups:admin |
| backup-destinations | `DELETE` | `/api/v1/backup-destinations/{destination_id}` | `deleteBackupDestination` | required:deleteBackupDestination | admin | backups:admin |
| backup-destinations | `POST` | `/api/v1/backup-destinations/{destination_id}/test` | `testBackupDestination` | required:testBackupDestination | admin | backups:admin |
| restores | `GET` | `/api/v1/restores` | `listRestore` | required:listRestore | admin | backups:admin |
| restores | `POST` | `/api/v1/restores` | `createRestore` | required:createRestore | admin | backups:admin |
| restores | `GET` | `/api/v1/restores/{restore_id}` | `getRestore` | required:getRestore | admin | backups:admin |
| updates | `GET` | `/api/v1/updates` | `listUpdates` | required:listUpdates | admin | updates:admin |
| updates | `POST` | `/api/v1/updates` | `startUpdate` | required:startUpdate | admin | updates:admin |
| modules | `GET` | `/api/v1/modules` | `listModules` | required:listModules | admin | modules:read |
| modules, marketplace | `POST` | `/api/v1/modules` | `installModule` | required:installModule | admin | modules:write |
| modules | `GET` | `/api/v1/modules/{module_id}` | `getModule` | required:getModule | admin | modules:read |
| modules | `POST` | `/api/v1/modules/{module_id}/permissions` | `decideModulePermissions` | required:decideModulePermissions | admin | modules:write |
| modules | `POST` | `/api/v1/modules/{module_id}/enable` | `enableModule` | required:enableModule | admin | modules:write |
| modules | `POST` | `/api/v1/modules/{module_id}/disable` | `disableModule` | required:disableModule | admin | modules:write |
| modules | `POST` | `/api/v1/modules/{module_id}/uninstall` | `uninstallModule` | required:uninstallModule | admin | modules:write |
| modules | `GET` | `/api/v1/modules/{module_id}/settings` | `getModuleSettings` | required:getModuleSettings | admin | modules:read |
| modules | `PUT` | `/api/v1/modules/{module_id}/settings` | `putModuleSettings` | required:putModuleSettings | admin | modules:write |
| modules | `POST` | `/api/v1/modules/{module_id}/context-tokens` | `createModuleContextToken` | required:createModuleContextToken | admin, attorney, staff, accountant | modules:read |
| marketplace | `GET` | `/api/v1/marketplace/listings` | `listMarketplaceListings` | required:listMarketplaceListings | admin | modules:read |
| marketplace | `GET` | `/api/v1/marketplace/listings/{listing_id}` | `getMarketplaceListing` | required:getMarketplaceListing | admin | modules:read |
| marketplace | `GET` | `/api/v1/module-alerts` | `listModuleAlert` | required:listModuleAlert | admin | modules:read |
| marketplace | `GET` | `/api/v1/module-alerts/{alert_id}` | `getModuleAlert` | required:getModuleAlert | admin | modules:read |
| modules | `GET` | `/api/v1/trusted-signing-keys` | `listTrustedKey` | required:listTrustedKey | admin | modules:read |
| modules | `POST` | `/api/v1/trusted-signing-keys` | `createTrustedKey` | required:createTrustedKey | admin | modules:write |
| modules | `GET` | `/api/v1/trusted-signing-keys/{key_id}` | `getTrustedKey` | required:getTrustedKey | admin | modules:read |
| modules | `PATCH` | `/api/v1/trusted-signing-keys/{key_id}` | `updateTrustedKey` | required:updateTrustedKey | admin | modules:write |
| modules | `DELETE` | `/api/v1/trusted-signing-keys/{key_id}` | `deleteTrustedKey` | required:deleteTrustedKey | admin | modules:write |
| domain-replacement | `GET` | `/api/v1/domain-handlers` | `listDomainHandlers` | required:listDomainHandlers | admin | modules:read |
| domain-replacement | `PUT` | `/api/v1/domain-handlers/{domain}` | `putDomainHandler` | required:putDomainHandler | admin | modules:write |

Operations: 405.
