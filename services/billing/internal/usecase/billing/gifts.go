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

// Prices of the demo catalog (gift seed-demo).
const (
	_demoHeart   = 1_000
	_demoRose    = 10_000
	_demoCake    = 20_000
	_demoDiamond = 50_000
	_demoCrown   = 100_000
	_demoRocket  = 500_000
)

// WalletBalance -.
func (uc *UseCase) WalletBalance(ctx context.Context, userID string) (int64, error) {
	bal, err := uc.r.Wallet.WalletBalance(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("billing - WalletBalance: %w", err)
	}

	return bal, nil
}

// WalletEntries -: newest first.
func (uc *UseCase) WalletEntries(ctx context.Context, userID, cursor string, limit int) (usecase.WalletPage, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.WalletPage{}, err
	}

	items, err := uc.r.Wallet.WalletEntries(ctx, userID, after, limit)
	if err != nil {
		return usecase.WalletPage{}, fmt.Errorf("billing - WalletEntries: %w", err)
	}

	return usecase.WalletPage{Items: items, Next: next(items, limit, func(e entity.WalletEntry) (time.Time, string) {
		return e.CreatedAt, e.ID
	})}, nil
}

// GiftItems is the catalog viewers pick from.
func (uc *UseCase) GiftItems(ctx context.Context) ([]entity.GiftItem, error) {
	items, err := uc.r.Gifts.ActiveGiftItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing - GiftItems: %w", err)
	}

	return items, nil
}

// SendGift spends the wallet on a gift for a channel; the channel's ledger gets the revenue less the fee.
func (uc *UseCase) SendGift(ctx context.Context, userID, channelID string, in usecase.GiftInput,
) (entity.GiftSend, int64, error) {
	// The total cap needs the price: the repo checks it again under the item lock.
	qty, err := entity.GiftQuantity(in.Quantity, 0, uc.s.GiftMaxTotal)
	if err != nil {
		return entity.GiftSend{}, 0, err
	}

	msg, err := entity.GiftMessage(in.Message)
	if err != nil {
		return entity.GiftSend{}, 0, err
	}

	if !validID(in.ItemID) {
		return entity.GiftSend{}, 0, entity.ErrNotFound
	}

	ch, err := uc.giftableChannel(ctx, userID, channelID)
	if err != nil {
		return entity.GiftSend{}, 0, err
	}

	g, bal, err := uc.r.Gifts.SendGift(ctx, entity.GiftSend{
		UserID: userID, ChannelID: channelID, ItemID: in.ItemID, Quantity: qty, Message: msg,
	}, repo.GiftRules{FeeBps: uc.s.FeeBps, MaxTotal: uc.s.GiftMaxTotal}, giftSent)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidState) || errors.Is(err, entity.ErrValidation) || errors.Is(err, entity.ErrNotFound) {
			return entity.GiftSend{}, 0, err
		}

		return entity.GiftSend{}, 0, fmt.Errorf("billing - SendGift: %w", err)
	}

	g.Channel = ch

	return g, bal, nil
}

// giftableChannel is the channel copy, if userID may give it a gift.
func (uc *UseCase) giftableChannel(ctx context.Context, userID, channelID string) (entity.Channel, error) {
	if !validID(channelID) {
		return entity.Channel{}, entity.ErrNotFound
	}

	ch, err := uc.r.Channels.Channel(ctx, channelID)
	if err != nil {
		return ch, fmt.Errorf("billing - SendGift - channel: %w", err)
	}

	switch {
	case ch.OwnerID == userID:
		return ch, entity.ErrGiftSelf
	case ch.Suspended():
		return ch, entity.ErrChannelSuspended
	}

	return ch, nil
}

// AdminGiftItems -.
func (uc *UseCase) AdminGiftItems(ctx context.Context, active *bool) ([]entity.GiftItem, error) {
	items, err := uc.r.Gifts.GiftItems(ctx, active)
	if err != nil {
		return nil, fmt.Errorf("billing - AdminGiftItems: %w", err)
	}

	return items, nil
}

// CreateGiftItem -; name, price and icon are required.
func (uc *UseCase) CreateGiftItem(ctx context.Context, adminID string, in usecase.GiftItemInput) (entity.GiftItem, error) {
	if in.Name == nil {
		in.Name = new(string)
	}

	if in.Icon == nil {
		in.Icon = new(string)
	}

	p, err := giftPatch(in, true)
	if err != nil {
		return entity.GiftItem{}, err
	}

	it := entity.GiftItem{Name: *p.Name, Price: *p.Price, Icon: *p.Icon, Active: true}
	if p.Active != nil {
		it.Active = *p.Active
	}

	if p.SortOrder != nil {
		it.SortOrder = *p.SortOrder
	}

	created, err := uc.r.Gifts.CreateGiftItem(ctx, it, func(c entity.GiftItem) []repo.Outbox {
		return []repo.Outbox{giftItemAudit(adminID, "GIFT_ITEM_CREATED", c.ID, map[string]any{"after": giftItemMeta(c)})}
	})
	if err != nil {
		return entity.GiftItem{}, fmt.Errorf("billing - CreateGiftItem: %w", err)
	}

	return created, nil
}

// UpdateGiftItem changes the given fields; sends already made keep their copied name, icon and price.
func (uc *UseCase) UpdateGiftItem(ctx context.Context, adminID, id string, in usecase.GiftItemInput) (entity.GiftItem, error) {
	p, err := giftPatch(in, false)
	if err != nil {
		return entity.GiftItem{}, err
	}

	if !validID(id) {
		return entity.GiftItem{}, entity.ErrNotFound
	}

	it, err := uc.r.Gifts.UpdateGiftItem(ctx, id, p, func(before, after entity.GiftItem) []repo.Outbox {
		return []repo.Outbox{giftItemAudit(adminID, "GIFT_ITEM_UPDATED", after.ID, map[string]any{
			"before": giftItemMeta(before), "after": giftItemMeta(after),
		})}
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return entity.GiftItem{}, err
		}

		return entity.GiftItem{}, fmt.Errorf("billing - UpdateGiftItem: %w", err)
	}

	return it, nil
}

// giftPatch validates the admin form; on create name, price and icon must be present.
func giftPatch(in usecase.GiftItemInput, create bool) (repo.GiftItemPatch, error) {
	p := repo.GiftItemPatch{Active: in.Active, SortOrder: in.SortOrder}

	if in.Name != nil {
		n, err := entity.GiftItemName(*in.Name)
		if err != nil {
			return p, err
		}

		p.Name = &n
	}

	if in.Price != nil || create {
		pr, err := entity.GiftItemPrice(in.Price)
		if err != nil {
			return p, err
		}

		p.Price = &pr
	}

	err := giftIconSort(in, &p)

	return p, err
}

func giftIconSort(in usecase.GiftItemInput, p *repo.GiftItemPatch) error {
	if in.Icon != nil {
		ic, err := entity.GiftItemIcon(*in.Icon)
		if err != nil {
			return err
		}

		p.Icon = &ic
	}

	if in.SortOrder != nil && (*in.SortOrder < 0 || *in.SortOrder > entity.GiftSortMax) {
		return entity.Invalid("sortOrder", entity.DetailMax)
	}

	return nil
}

func giftItemMeta(it entity.GiftItem) map[string]any {
	return map[string]any{"name": it.Name, "price": it.Price, "icon": it.Icon, "active": it.Active, "sortOrder": it.SortOrder}
}

func giftItemAudit(adminID, action, id string, meta map[string]any) repo.Outbox {
	return audit(auditEntry{
		actorID: &adminID, role: events.ActorAdmin, action: action, aggregate: aggGiftItem, resType: resGiftItem,
		resID: &id, meta: meta,
	})
}

// SeedGiftItems fills an empty catalog with the demo items (dev, E2E); a catalog with any item is left alone.
func (uc *UseCase) SeedGiftItems(ctx context.Context) (int, error) {
	items, err := uc.r.Gifts.GiftItems(ctx, nil)
	if err != nil || len(items) > 0 {
		return 0, err
	}

	demo := []entity.GiftItem{
		{Name: "Tim", Price: _demoHeart, Icon: "heart"},
		{Name: "Hoa hồng", Price: _demoRose, Icon: "rose"},
		{Name: "Bánh kem", Price: _demoCake, Icon: "cake"},
		{Name: "Kim cương", Price: _demoDiamond, Icon: "diamond"},
		{Name: "Vương miện", Price: _demoCrown, Icon: "crown"},
		{Name: "Tên lửa", Price: _demoRocket, Icon: "rocket"},
	}

	for i, it := range demo {
		it.Active, it.SortOrder = true, i+1
		if _, err := uc.r.Gifts.CreateGiftItem(ctx, it, func(entity.GiftItem) []repo.Outbox { return nil }); err != nil {
			return i, fmt.Errorf("billing - SeedGiftItems: %w", err)
		}
	}

	return len(demo), nil
}
