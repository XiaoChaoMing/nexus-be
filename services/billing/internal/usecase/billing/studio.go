package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const (
	_range7  = "7d"
	_range28 = "28d"
	_days7   = 7
	_days28  = 28
)

func (uc *UseCase) ownChannel(ctx context.Context, ownerID string) (entity.Channel, error) {
	ch, err := uc.r.Channels.ByOwner(ctx, ownerID)
	if err != nil {
		if errors.Is(err, entity.ErrNotCreator) {
			return ch, err
		}

		return ch, fmt.Errorf("billing - studio channel: %w", err)
	}

	return ch, nil
}

// StudioTiers lists every tier of the caller's channel, inactive ones included.
func (uc *UseCase) StudioTiers(ctx context.Context, ownerID string) ([]usecase.StudioTier, error) {
	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	tiers, err := uc.r.Tiers.ByChannel(ctx, ch.ID, false)
	if err != nil {
		return nil, fmt.Errorf("billing - StudioTiers: %w", err)
	}

	counts, err := uc.r.Tiers.ActiveSubscribers(ctx, ch.ID, uc.now())
	if err != nil {
		return nil, fmt.Errorf("billing - StudioTiers: %w", err)
	}

	out := make([]usecase.StudioTier, 0, len(tiers))
	for _, t := range tiers {
		out = append(out, usecase.StudioTier{Tier: t, ActiveSubscribers: counts[t.Level]})
	}

	return out, nil
}

func validTierPatch(level int, in usecase.TierPatch) (repo.TierPatch, error) {
	if level < 1 || level > entity.TierLevels {
		return repo.TierPatch{}, entity.Invalid("level", entity.DetailEnum)
	}

	if in.Active == nil && in.Description == nil {
		return repo.TierPatch{}, entity.Invalid("body", entity.DetailRequired)
	}

	patch := repo.TierPatch{Active: in.Active}

	if in.Description != nil {
		d, err := entity.TierDescription(*in.Description)
		if err != nil {
			return patch, err
		}

		patch.Description = &d
	}

	if level == 1 && in.Active != nil && !*in.Active {
		return patch, entity.Invalid("active", entity.DetailInvalid)
	}

	return patch, nil
}

// UpdateTier turns a tier on / off or edits its description (prices are fixed).
func (uc *UseCase) UpdateTier(ctx context.Context, ownerID string, level int, in usecase.TierPatch) (usecase.StudioTier, error) {
	patch, err := validTierPatch(level, in)
	if err != nil {
		return usecase.StudioTier{}, err
	}

	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return usecase.StudioTier{}, err
	}

	if ch.Suspended() {
		return usecase.StudioTier{}, entity.ErrChannelSuspended
	}

	t, err := uc.r.Tiers.UpdateTier(ctx, ch.ID, level, patch, func(before, after entity.Tier) []repo.Outbox {
		return []repo.Outbox{audit(auditEntry{
			actorID: &ownerID, role: events.ActorOwner, action: "TIER_UPDATED", aggregate: aggTier, resType: resTier,
			resID: &after.ID, meta: map[string]any{
				"level":  level,
				"before": map[string]any{"active": before.Active, "description": before.Description},
				"after":  map[string]any{"active": after.Active, "description": after.Description},
			},
		})}
	})
	if err != nil {
		return usecase.StudioTier{}, fmt.Errorf("billing - UpdateTier: %w", err)
	}

	counts, err := uc.r.Tiers.ActiveSubscribers(ctx, ch.ID, uc.now())
	if err != nil {
		return usecase.StudioTier{}, fmt.Errorf("billing - UpdateTier: %w", err)
	}

	return usecase.StudioTier{Tier: t, ActiveSubscribers: counts[level]}, nil
}

// Summary computes the Studio overview from the ledger (§6.5); days start at 00:00 in loc.
func (uc *UseCase) Summary(ctx context.Context, ownerID, rng string, loc *time.Location) (entity.Summary, error) {
	days := _days7

	switch rng {
	case "", _range7:
		rng = _range7
	case _range28:
		days = _days28
	default:
		return entity.Summary{}, entity.Invalid("range", entity.DetailEnum)
	}

	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return entity.Summary{}, err
	}

	now := uc.now()
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -(days - 1))

	s := entity.Summary{
		Range: rng, TZ: loc.String(), From: from.UTC(), To: now.UTC(), FeeBps: uc.s.FeeBps, PayoutMin: uc.s.PayoutMin,
	}

	if err := uc.fillSummary(ctx, ch.ID, &s, today, now); err != nil {
		return s, fmt.Errorf("billing - Summary: %w", err)
	}

	return s, nil
}

func (uc *UseCase) fillSummary(ctx context.Context, channelID string, s *entity.Summary, today, now time.Time) error {
	period, err := uc.r.Ledger.Totals(ctx, channelID, s.From, now)
	if err != nil {
		return err
	}

	day, err := uc.r.Ledger.Totals(ctx, channelID, today, now)
	if err != nil {
		return err
	}

	available, held, err := uc.r.Ledger.Balance(ctx, channelID)
	if err != nil {
		return err
	}

	subs, err := uc.r.Tiers.ActiveSubscribers(ctx, channelID, now)
	if err != nil {
		return err
	}

	s.Gross, s.PlatformFee, s.Net = period.Gross, period.PlatformFee, period.Gross-period.PlatformFee
	s.Gifts = period.Gifts
	s.TodayNet = day.Gross - day.PlatformFee
	s.Balance, s.PendingPayout, s.Available = available+held, held, available
	s.Subscribers = subs

	return nil
}

// Ledger -: newest first.
func (uc *UseCase) Ledger(ctx context.Context, ownerID, cursor string, limit int) (usecase.LedgerPage, error) {
	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return usecase.LedgerPage{}, err
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.LedgerPage{}, err
	}

	items, err := uc.r.Ledger.Entries(ctx, ch.ID, after, limit)
	if err != nil {
		return usecase.LedgerPage{}, fmt.Errorf("billing - Ledger: %w", err)
	}

	return usecase.LedgerPage{Items: items, Next: next(items, limit, func(e entity.LedgerEntry) (time.Time, string) {
		return e.CreatedAt, e.ID
	})}, nil
}

// StudioPayouts -: newest first.
func (uc *UseCase) StudioPayouts(ctx context.Context, ownerID, cursor string, limit int) (usecase.PayoutPage, error) {
	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return usecase.PayoutPage{}, err
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.PayoutPage{}, err
	}

	items, err := uc.r.Payouts.ForChannel(ctx, ch.ID, after, limit)
	if err != nil {
		return usecase.PayoutPage{}, fmt.Errorf("billing - StudioPayouts: %w", err)
	}

	out := make([]usecase.PayoutView, 0, len(items))
	for _, p := range items {
		out = append(out, uc.payoutView(p))
	}

	return usecase.PayoutPage{Items: out, Next: next(items, limit, payoutKey)}, nil
}

func payoutKey(p entity.Payout) (at time.Time, id string) { return p.CreatedAt, p.ID }

func bankAAD(channelID string) []byte { return []byte("payout:" + channelID) }

// openAccount decrypts a payout's bank account.
func (uc *UseCase) openAccount(p entity.Payout) (entity.BankAccount, error) {
	plain, err := uc.r.Cipher.Open(p.BankEncrypted, bankAAD(p.ChannelID))
	if err != nil {
		return entity.BankAccount{}, entity.ErrDecrypt
	}

	var a entity.BankAccount
	if err := json.Unmarshal(plain, &a); err != nil {
		return a, entity.ErrDecrypt
	}

	return a, nil
}

// payoutView adds the account name; a blob that does not open shows no name (logged, never returned).
func (uc *UseCase) payoutView(p entity.Payout) usecase.PayoutView {
	a, err := uc.openAccount(p)
	if err != nil {
		uc.l.Error("billing - payout %s: cannot decrypt bank account", p.ID)

		return usecase.PayoutView{Payout: p}
	}

	return usecase.PayoutView{Payout: p, AccountName: &a.Name}
}

func payoutAudit(actorID, role, action string, p entity.Payout, reason *string) repo.Outbox {
	return audit(auditEntry{
		actorID: &actorID, role: role, action: action, aggregate: aggPayout, resType: resPayout, resID: &p.ID, reason: reason,
		meta: map[string]any{
			"channelId": p.ChannelID, "amount": p.Amount, "bankCode": p.BankCode, "last4": p.AccountLast4,
			"provider": p.Provider, "reference": p.ProviderRef,
		},
	})
}

// RequestPayout validates the form, seals the account (AES-256-GCM, AAD = channel) and files the request.
func (uc *UseCase) RequestPayout(ctx context.Context, ownerID string, in usecase.PayoutInput) (usecase.PayoutView, error) {
	req, err := entity.NewPayoutRequest(in.Amount, in.BankCode, in.AccountNumber, in.AccountName, uc.s.PayoutMin)
	if err != nil {
		return usecase.PayoutView{}, err
	}

	ch, err := uc.ownChannel(ctx, ownerID)
	if err != nil {
		return usecase.PayoutView{}, err
	}

	if ch.Suspended() {
		return usecase.PayoutView{}, entity.ErrChannelSuspended
	}

	plain, err := json.Marshal(req.Account)
	if err != nil {
		return usecase.PayoutView{}, fmt.Errorf("billing - RequestPayout - marshal: %w", err)
	}

	blob, err := uc.r.Cipher.Seal(plain, bankAAD(ch.ID))
	if err != nil {
		return usecase.PayoutView{}, fmt.Errorf("billing - RequestPayout - seal: %w", err)
	}

	p, err := uc.r.Payouts.CreatePayout(ctx, entity.Payout{
		ChannelID: ch.ID, Amount: req.Amount, BankCode: req.Account.Code, AccountLast4: req.Account.Last4(), BankEncrypted: blob,
	}, func(p entity.Payout) []repo.Outbox {
		return []repo.Outbox{payoutAudit(ownerID, events.ActorOwner, "PAYOUT_REQUESTED", p, nil)}
	})
	if err != nil {
		if errors.Is(err, entity.ErrInvalidState) {
			return usecase.PayoutView{}, err
		}

		return usecase.PayoutView{}, fmt.Errorf("billing - RequestPayout: %w", err)
	}

	uc.obs.PayoutStatus(entity.PayoutRequested)

	return usecase.PayoutView{Payout: p, AccountName: &req.Account.Name}, nil
}
