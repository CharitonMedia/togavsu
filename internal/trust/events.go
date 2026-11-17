package trust

import (
	"fmt"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// stampActor fills ActorKind and ActorID from who when missing, and a readable
// Summary from the event name (or Data) when Summary is empty.
func stampActor(who domain.Principal, e Event) Event {
	if e.ActorKind == "" {
		e.ActorKind = eventActorKind(who)
		e.ActorID = actorID(who)
	}
	if strings.TrimSpace(e.Summary) == "" {
		e.Summary = defaultEventSummary(e)
	}
	return e
}

func stampActors(who domain.Principal, events []Event) []Event {
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = stampActor(who, e)
	}
	return out
}

func eventActorKind(p domain.Principal) string {
	switch p.Source {
	case domain.SourceSystem:
		return "system"
	case domain.SourceModule, domain.SourceService:
		return "module"
	case domain.SourceAPIToken:
		return "agent"
	}
	if p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) &&
		!p.HasRole(domain.RoleStaff) && !p.HasRole(domain.RoleAccountant) {
		return "client"
	}
	return "staff"
}

// entryPostedSummary is the Activities-feed wording for a committed ledger row.
func entryPostedSummary(e Entry, matter domain.Matter) string {
	label := "Trust entry"
	switch e.EntryType {
	case TypeDeposit:
		label = "Trust deposit"
	case TypeWithdrawal:
		label = "Trust withdrawal"
	case TypeTransfer:
		label = "Trust transfer to operating"
	case TypeRefund:
		label = "Trust refund"
	case TypeAdjustment:
		label = "Trust adjustment"
	case TypeReversal:
		label = "Trust reversal"
	}
	target := strings.TrimSpace(matter.Name)
	if target == "" {
		target = strings.TrimSpace(matter.Number)
	}
	if target == "" {
		target = string(matter.ID)
	}
	return fmt.Sprintf("%s of $%s posted to %s", label, absCents(e.Signed).String(), target)
}

func defaultEventSummary(e Event) string {
	switch e.Name {
	case "trust.entry_posted":
		if typ := e.Data["entry_type"]; typ != "" {
			amt := e.Data["amount_cents"]
			if strings.HasPrefix(amt, "-") {
				amt = strings.TrimPrefix(amt, "-")
			}
			label := "Trust entry"
			switch typ {
			case TypeDeposit:
				label = "Trust deposit"
			case TypeWithdrawal:
				label = "Trust withdrawal"
			case TypeTransfer:
				label = "Trust transfer to operating"
			case TypeRefund:
				label = "Trust refund"
			case TypeAdjustment:
				label = "Trust adjustment"
			case TypeReversal:
				label = "Trust reversal"
			}
			if amt != "" {
				return fmt.Sprintf("%s of $%s posted", label, amt)
			}
			return label + " posted"
		}
		return "Trust entry posted"
	case "trust.entry_reversed":
		return "Trust entry reversed"
	case "trust.flag_raised":
		return "Trust balance flag raised"
	case "trust.flag_resolved":
		return "Trust balance flag resolved"
	case "trust.reconciled":
		return "Trust account reconciled"
	case "trust.replenishment_requested":
		return "Trust replenishment requested"
	case "trust.replenishment_satisfied":
		return "Trust replenishment satisfied"
	case "trust.replenishment_cancelled":
		return "Trust replenishment cancelled"
	case "trust_account.created":
		return "Trust account created"
	case "trust_account.updated":
		return "Trust account updated"
	default:
		return e.Name
	}
}
