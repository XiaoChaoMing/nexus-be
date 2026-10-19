package billing

import (
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

// keyInvoice is the invoice number key of audit metadata.
const keyInvoice = "invoiceNumber"

// Aggregate types of billing's outbox rows.
const (
	aggOrder        = "checkout_order"
	aggSubscription = "subscription"
	aggPayout       = "payout"
	aggTier         = "tier"
)

// Audit resource types (safety-svc audit log).
const (
	resOrder        = "ORDER"
	resSubscription = "SUBSCRIPTION"
	resTier         = "TIER"
	resPayout       = "PAYOUT"
)

func paymentSucceeded(o entity.Order, paid paidResult, source string) repo.Outbox {
	var method *string

	if paid.Method != nil {
		method = ptr(string(*paid.Method))
	}

	p := events.PaymentSucceeded{
		OrderID: o.ID, InvoiceNumber: o.InvoiceNumber, UserID: o.UserID, ChannelID: o.ChannelID, Tier: o.Tier.Level,
		Kind: string(o.SubAction), Amount: o.Amount, PaymentMethod: method, PaidAt: paid.At, Applied: paid.Applied,
		Late: o.Status != entity.OrderPending, Source: source,
	}

	if paid.Sub != nil {
		p.SubscriptionID, p.CurrentPeriodEnd = &paid.Sub.ID, &paid.Sub.CurrentPeriodEnd
	}

	return repo.Outbox{AggregateType: aggOrder, AggregateID: o.ID, EventType: events.TypePaymentSucceeded, Payload: p}
}

func paymentFailed(o entity.Order) repo.Outbox {
	reason := ""
	if o.FailureReason != nil {
		reason = string(*o.FailureReason)
	}

	return repo.Outbox{
		AggregateType: aggOrder, AggregateID: o.ID, EventType: events.TypePaymentFailed,
		Payload: events.PaymentFailed{
			OrderID: o.ID, InvoiceNumber: o.InvoiceNumber, UserID: o.UserID, ChannelID: o.ChannelID, Tier: o.Tier.Level,
			Status: string(o.Status), FailureReason: reason,
		},
	}
}

func paymentVoided(o entity.Order, sub *entity.Subscription) repo.Outbox {
	p := events.PaymentVoided{
		OrderID: o.ID, InvoiceNumber: o.InvoiceNumber, UserID: o.UserID, ChannelID: o.ChannelID, Tier: o.Tier.Level,
		Amount: o.Amount, SubscriptionID: o.SubscriptionID,
	}

	if sub != nil {
		p.SubscriptionStatus, p.CurrentPeriodEnd = ptr(string(sub.Status)), &sub.CurrentPeriodEnd
	}

	return repo.Outbox{AggregateType: aggOrder, AggregateID: o.ID, EventType: events.TypePaymentVoided, Payload: p}
}

func subActivated(s entity.Subscription, orderID *string, replay bool) repo.Outbox {
	return repo.Outbox{
		AggregateType: aggSubscription, AggregateID: s.ID, EventType: events.TypeSubscriptionActivated,
		Payload: events.SubscriptionActivated{
			SubscriptionID: s.ID, UserID: s.UserID, ChannelID: s.ChannelID, Tier: s.Tier.Level,
			CurrentPeriodStart: s.CurrentPeriodStart, CurrentPeriodEnd: s.CurrentPeriodEnd, OrderID: orderID, Replay: replay,
		},
	}
}

func subRenewed(s entity.Subscription, orderID string) repo.Outbox {
	return repo.Outbox{
		AggregateType: aggSubscription, AggregateID: s.ID, EventType: events.TypeSubscriptionRenewed,
		Payload: events.SubscriptionRenewed{
			SubscriptionID: s.ID, UserID: s.UserID, ChannelID: s.ChannelID, Tier: s.Tier.Level,
			CurrentPeriodEnd: s.CurrentPeriodEnd, OrderID: orderID,
		},
	}
}

func subUpdated(s entity.Subscription, change string) repo.Outbox {
	return repo.Outbox{
		AggregateType: aggSubscription, AggregateID: s.ID, EventType: events.TypeSubscriptionUpdated,
		Payload: events.SubscriptionUpdated{
			SubscriptionID: s.ID, UserID: s.UserID, ChannelID: s.ChannelID, Tier: s.Tier.Level, Status: string(s.Status),
			CurrentPeriodEnd: s.CurrentPeriodEnd, Change: change,
		},
	}
}

func subExpiring(s entity.Subscription, renewable bool) repo.Outbox {
	return repo.Outbox{
		AggregateType: aggSubscription, AggregateID: s.ID, EventType: events.TypeSubscriptionExpiring,
		Payload: events.SubscriptionExpiring{
			SubscriptionID: s.ID, UserID: s.UserID, ChannelID: s.ChannelID, Tier: s.Tier.Level,
			CurrentPeriodEnd: s.CurrentPeriodEnd, Price: s.Tier.Price, Renewable: renewable,
		},
	}
}

func subExpired(s entity.Subscription, reason string) repo.Outbox {
	return repo.Outbox{
		AggregateType: aggSubscription, AggregateID: s.ID, EventType: events.TypeSubscriptionExpired,
		Payload: events.SubscriptionExpired{
			SubscriptionID: s.ID, UserID: s.UserID, ChannelID: s.ChannelID, Tier: s.Tier.Level, Reason: reason,
			CurrentPeriodEnd: s.CurrentPeriodEnd,
		},
	}
}

// auditEntry is one audit.recorded.v1 row; a nil resource id (unknown invoice) gets a fresh aggregate id.
type auditEntry struct {
	actorID   *string
	role      string
	action    string
	aggregate string
	resType   string
	resID     *string
	reason    *string
	meta      map[string]any
}

func audit(a auditEntry) repo.Outbox {
	aggID := uuid.NewString()
	if a.resID != nil {
		aggID = *a.resID
	}

	return repo.Outbox{
		AggregateType: a.aggregate, AggregateID: aggID, EventType: events.TypeAuditRecorded,
		Payload: events.AuditRecorded{
			ActorID: a.actorID, ActorRole: a.role, Action: a.action, ResourceType: a.resType, ResourceID: a.resID,
			Reason: a.reason, Metadata: a.meta,
		},
	}
}

func orderAudit(o entity.Order, action string, meta map[string]any) repo.Outbox {
	return audit(auditEntry{
		role: events.ActorSystem, action: action, aggregate: aggOrder, resType: resOrder, resID: &o.ID, meta: meta,
	})
}
