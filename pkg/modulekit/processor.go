package modulekit

import "github.com/CharitonMedia/togavsu/internal/modules"

// ProcessorPaymentContract names the core-brokered pre-matter operating card
// authorization contract. Both sides need an admin-approved contract link.
const ProcessorPaymentContract = modules.ProcessorPaymentContract

// ProcessorPaymentWrite is the authorize operation's request. The token comes from
// processor-hosted fields; send no raw card data to either core or a module.
type ProcessorPaymentWrite = modules.ProcessorPaymentWrite

// ProcessorPaymentResult is approved, declined or pending. Only an approved result
// permits the consumer to continue its paid intake matter writes.
type ProcessorPaymentResult = modules.ProcessorPaymentResult
