package modulekit

import (
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Cents is an integer amount of money, exposed so external modules can construct
// contract requests from int64 settings without importing a core internal package.
type Cents = domain.Cents

// ProcessorPaymentContract names the core-brokered pre-matter operating card
// authorization contract. Both sides need an admin-approved contract link.
const ProcessorPaymentContract = modules.ProcessorPaymentContract

// ProcessorPaymentWrite is the authorize operation's request. The token comes from
// processor-hosted fields; send no raw card data to either core or a module.
type ProcessorPaymentWrite = modules.ProcessorPaymentWrite

// ProcessorPaymentResult is approved, declined or pending. Only an approved result
// permits the consumer to continue its paid intake matter writes.
type ProcessorPaymentResult = modules.ProcessorPaymentResult

// BindingContractEvent is the batch 2 event envelope. Push it to the contract's
// /events endpoint using the authenticated provider bearer. Consumers verify with
// VerifyWebhookSender (or coretest.ReadDelivery) and deduplicate EventID forever.
type BindingContractEvent = modules.BindingContractEvent

// ProcessorPaymentRecord binds a stored approval through the contract record operation.
type ProcessorPaymentRecord = modules.ProcessorPaymentRecord

// ID is the public core record identifier used in typed continuation requests.
type ID = domain.ID
