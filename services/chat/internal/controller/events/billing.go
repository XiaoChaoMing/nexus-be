package events

import (
	"context"
	"fmt"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// checkTier skips events whose tier can never be stored.
func checkTier(typ string, tier int) error {
	if !entity.ValidSubTier(tier) {
		return fmt.Errorf("%w: %s: tier %d", eventbus.ErrSkip, typ, tier)
	}

	return nil
}

func subscriptionHandlers(ent usecase.Entitlements) map[string]eventbus.Handler {
	return map[string]eventbus.Handler{
		events.TypeSubscriptionActivated: handle(func(p events.SubscriptionActivated) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, id string, p events.SubscriptionActivated) error {
				if err := checkTier(events.TypeSubscriptionActivated, p.Tier); err != nil {
					return err
				}

				return ent.Activated(ctx, id, p)
			}),
		events.TypeSubscriptionRenewed: handle(func(p events.SubscriptionRenewed) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, id string, p events.SubscriptionRenewed) error {
				if err := checkTier(events.TypeSubscriptionRenewed, p.Tier); err != nil {
					return err
				}

				return ent.Renewed(ctx, id, p)
			}),
		events.TypeSubscriptionUpdated: handle(func(p events.SubscriptionUpdated) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.SubscriptionUpdated) error {
				if err := checkTier(events.TypeSubscriptionUpdated, p.Tier); err != nil {
					return err
				}

				return ent.Updated(ctx, p)
			}),
		events.TypeSubscriptionExpired: handle(func(p events.SubscriptionExpired) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.SubscriptionExpired) error { return ent.Expired(ctx, p) }),
	}
}

func paymentHandlers(ent usecase.Entitlements) map[string]eventbus.Handler {
	return map[string]eventbus.Handler{
		events.TypePaymentSucceeded: handle(func(p events.PaymentSucceeded) []string { return []string{p.UserID, p.OrderID} },
			func(ctx context.Context, _ string, p events.PaymentSucceeded) error {
				return ent.PaymentUpdated(ctx, p.UserID, paymentSucceeded(p))
			}),
		events.TypePaymentFailed: handle(func(p events.PaymentFailed) []string { return []string{p.UserID, p.OrderID} },
			func(ctx context.Context, _ string, p events.PaymentFailed) error {
				reason := p.FailureReason

				return ent.PaymentUpdated(ctx, p.UserID, entity.PaymentUpdate{
					OrderID: p.OrderID, InvoiceNumber: p.InvoiceNumber, Status: p.Status, FailureReason: &reason,
				})
			}),
		events.TypePaymentVoided: handle(func(p events.PaymentVoided) []string { return []string{p.UserID, p.OrderID} },
			func(ctx context.Context, _ string, p events.PaymentVoided) error {
				return ent.PaymentUpdated(ctx, p.UserID, paymentVoided(p))
			}),
	}
}

// paymentSucceeded: an order under review changed no subscription, so none is sent.
func paymentSucceeded(p events.PaymentSucceeded) entity.PaymentUpdate {
	u := entity.PaymentUpdate{
		OrderID: p.OrderID, InvoiceNumber: p.InvoiceNumber, Status: entity.PaymentPaid,
		NeedsReview: p.Applied == events.PaymentAppliedNeedsReview,
	}

	if !u.NeedsReview && p.SubscriptionID != nil && p.CurrentPeriodEnd != nil {
		u.Subscription = &entity.PaymentSubState{
			ID: *p.SubscriptionID, ChannelID: p.ChannelID, Tier: p.Tier, Status: "ACTIVE", CurrentPeriodEnd: p.CurrentPeriodEnd.UTC(),
		}
	}

	return u
}

func paymentVoided(p events.PaymentVoided) entity.PaymentUpdate {
	u := entity.PaymentUpdate{OrderID: p.OrderID, InvoiceNumber: p.InvoiceNumber, Status: entity.PaymentVoided}

	if p.SubscriptionID != nil && p.SubscriptionStatus != nil && p.CurrentPeriodEnd != nil {
		u.Subscription = &entity.PaymentSubState{
			ID: *p.SubscriptionID, ChannelID: p.ChannelID, Tier: p.Tier, Status: *p.SubscriptionStatus,
			CurrentPeriodEnd: p.CurrentPeriodEnd.UTC(),
		}
	}

	return u
}
