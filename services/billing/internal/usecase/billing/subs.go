package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const (
	_subsCurrent = "current"
	_subsExpired = "expired"
)

// MySubscriptions -.
func (uc *UseCase) MySubscriptions(ctx context.Context, userID string, q usecase.SubQuery, cursor string, limit int,
) (usecase.SubPage, error) {
	if q.ChannelID != nil && !validID(*q.ChannelID) {
		return usecase.SubPage{}, entity.Invalid("channelId", entity.DetailInvalid)
	}

	now := uc.now()

	switch q.Status {
	case "":
		if q.ChannelID != nil {
			return uc.channelSub(ctx, userID, *q.ChannelID, now)
		}
	case _subsCurrent, _subsExpired:
	default:
		return usecase.SubPage{}, entity.Invalid("status", entity.DetailEnum)
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.SubPage{}, err
	}

	items, err := uc.r.Subs.Mine(ctx, userID, repo.SubFilter{
		Expired: q.Status == _subsExpired, ChannelID: q.ChannelID,
		Now: now,
	}, after, limit)
	if err != nil {
		return usecase.SubPage{}, fmt.Errorf("billing - MySubscriptions: %w", err)
	}

	return usecase.SubPage{Items: items, Next: next(items, limit, func(s entity.Subscription) (time.Time, string) {
		return s.CurrentPeriodEnd, s.ID
	})}, nil
}

// channelSub is the one row of a channel: the live subscription, else the most recently ended one.
func (uc *UseCase) channelSub(ctx context.Context, userID, channelID string, now time.Time) (usecase.SubPage, error) {
	s, err := uc.r.Subs.Latest(ctx, userID, channelID, now)
	if err != nil {
		return usecase.SubPage{}, fmt.Errorf("billing - MySubscriptions: %w", err)
	}

	page := usecase.SubPage{Items: []entity.Subscription{}}
	if s != nil {
		page.Items = append(page.Items, *s)
	}

	return page, nil
}

func subAudit(s entity.Subscription, action string) repo.Outbox {
	return audit(auditEntry{
		actorID: &s.UserID, role: events.ActorUser, action: action, aggregate: aggSubscription, resType: resSubscription,
		resID: &s.ID, meta: map[string]any{"channelId": s.ChannelID, "tier": s.Tier.Level, "currentPeriodEnd": s.CurrentPeriodEnd},
	})
}

// CancelSubscription turns renewal reminders off; benefits stay until the period ends.
func (uc *UseCase) CancelSubscription(ctx context.Context, userID, subID string) (entity.Subscription, error) {
	return uc.changeSub(ctx, userID, subID, "CancelSubscription", uc.r.Subs.Cancel, events.SubChangeCancelled,
		"SUBSCRIPTION_CANCELLED")
}

// ResumeSubscription turns renewal reminders back on.
func (uc *UseCase) ResumeSubscription(ctx context.Context, userID, subID string) (entity.Subscription, error) {
	return uc.changeSub(ctx, userID, subID, "ResumeSubscription", uc.r.Subs.Resume, events.SubChangeResumed,
		"SUBSCRIPTION_RESUMED")
}

type subChange func(ctx context.Context, userID, subID string, now time.Time, out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error)

func (uc *UseCase) changeSub(ctx context.Context, userID, subID, op string, change subChange, kind, action string,
) (entity.Subscription, error) {
	if !validID(subID) {
		return entity.Subscription{}, entity.ErrNotFound
	}

	s, err := change(ctx, userID, subID, uc.now(), func(s entity.Subscription) []repo.Outbox {
		return []repo.Outbox{subUpdated(s, kind), subAudit(s, action)}
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) {
			return s, err
		}

		return s, fmt.Errorf("billing - %s: %w", op, err)
	}

	return s, nil
}

// MyPayments -: newest first.
func (uc *UseCase) MyPayments(ctx context.Context, userID string, statuses []string, cursor string, limit int,
) (usecase.OrderPage, error) {
	st, err := orderStatuses(statuses)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	items, err := uc.r.Orders.ForUser(ctx, userID, st, after, limit)
	if err != nil {
		return usecase.OrderPage{}, fmt.Errorf("billing - MyPayments: %w", err)
	}

	return usecase.OrderPage{Items: items, Next: next(items, limit, orderKey)}, nil
}

func orderKey(o entity.Order) (at time.Time, id string) { return o.CreatedAt, o.ID }

func orderStatuses(in []string) ([]entity.OrderStatus, error) {
	out := make([]entity.OrderStatus, 0, len(in))

	for _, s := range in {
		switch st := entity.OrderStatus(s); st {
		case entity.OrderPending, entity.OrderPaid, entity.OrderCancelled, entity.OrderFailed, entity.OrderExpired,
			entity.OrderVoided:
			out = append(out, st)
		default:
			return nil, entity.Invalid("status", entity.DetailEnum)
		}
	}

	return out, nil
}
