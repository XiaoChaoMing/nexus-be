package persistent

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const _giftItemCols = `id, name, price_vnd, icon, active, sort_order, created_at, updated_at`

func insertWallet(ctx context.Context, q querier, entries []entity.WalletEntry) error {
	for _, e := range entries {
		if _, err := q.Exec(ctx, `INSERT INTO wallet_entries (user_id, direction, kind, amount_vnd, transaction_id,
			gift_send_id) VALUES ($1, $2, $3, $4, $5, $6)`, e.UserID, string(e.Direction), string(e.Kind), e.Amount,
			e.TransactionID, e.GiftSendID); err != nil {
			return fmt.Errorf("insert wallet: %w", err)
		}
	}

	return nil
}

func walletBalance(ctx context.Context, q querier, userID string) (int64, error) {
	var bal int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END), 0)
		FROM wallet_entries WHERE user_id = $1`, userID).Scan(&bal); err != nil {
		return 0, fmt.Errorf("wallet balance: %w", err)
	}

	return bal, nil
}

// WalletBalance -.
func (r *Repo) WalletBalance(ctx context.Context, userID string) (int64, error) {
	bal, err := walletBalance(ctx, r.Pool, userID)
	if err != nil {
		return 0, fmt.Errorf("Repo - WalletBalance: %w", err)
	}

	return bal, nil
}

// WalletEntries -: newest first, with the top-up invoice or the gift.
func (r *Repo) WalletEntries(ctx context.Context, userID string, after *repo.Cursor, limit int) ([]entity.WalletEntry, error) {
	set, at, id := pageArgs(after)

	rows, err := r.Pool.Query(ctx, `SELECT w.id, w.user_id, w.direction, w.kind, w.amount_vnd, w.transaction_id,
			w.gift_send_id, w.created_at, o.invoice_number,
			g.item_name, g.icon, g.quantity, g.channel_id, c.handle, c.display_name, c.avatar_url
		FROM wallet_entries w
		LEFT JOIN transactions t ON t.id = w.transaction_id
		LEFT JOIN checkout_orders o ON o.id = t.order_id
		LEFT JOIN gift_sends g ON g.id = w.gift_send_id
		LEFT JOIN channels c ON c.id = g.channel_id
		WHERE w.user_id = $1 AND (NOT $2 OR (w.created_at, w.id) < ($3, $4::uuid))
		ORDER BY w.created_at DESC, w.id DESC LIMIT $5`, userID, set, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - WalletEntries: %w", err)
	}

	out, err := collect(rows, scanWalletEntry)
	if err != nil {
		return nil, fmt.Errorf("Repo - WalletEntries: %w", err)
	}

	return out, nil
}

func scanWalletEntry(s scanner) (entity.WalletEntry, error) {
	var (
		e                            entity.WalletEntry
		name, icon, chID, handle, dn *string
		qty                          *int
		avatar                       *string
	)

	if err := s.Scan(&e.ID, &e.UserID, &e.Direction, &e.Kind, &e.Amount, &e.TransactionID, &e.GiftSendID, &e.CreatedAt,
		&e.InvoiceNumber, &name, &icon, &qty, &chID, &handle, &dn, &avatar); err != nil {
		return e, err
	}

	if e.GiftSendID != nil && name != nil && icon != nil && qty != nil && chID != nil {
		g := &entity.GiftSend{ID: *e.GiftSendID, ItemName: *name, Icon: *icon, Quantity: *qty, ChannelID: *chID}
		g.Channel = entity.Channel{ID: *chID, AvatarURL: avatar}

		if handle != nil && dn != nil {
			g.Channel.Handle, g.Channel.DisplayName = *handle, *dn
		}

		e.Gift = g
	}

	return e, nil
}

func scanGiftItem(s scanner) (entity.GiftItem, error) {
	var it entity.GiftItem

	err := s.Scan(&it.ID, &it.Name, &it.Price, &it.Icon, &it.Active, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt)

	return it, err
}

func (r *Repo) giftItems(ctx context.Context, op, where string, args ...any) ([]entity.GiftItem, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_giftItemCols+` FROM gift_items WHERE `+where+
		` ORDER BY sort_order, created_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	out, err := collect(rows, scanGiftItem)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return out, nil
}

// ActiveGiftItems -.
func (r *Repo) ActiveGiftItems(ctx context.Context) ([]entity.GiftItem, error) {
	return r.giftItems(ctx, "Repo - ActiveGiftItems", `active`)
}

// GiftItems -.
func (r *Repo) GiftItems(ctx context.Context, active *bool) ([]entity.GiftItem, error) {
	return r.giftItems(ctx, "Repo - GiftItems", `($1::boolean IS NULL OR active = $1)`, active)
}

// CreateGiftItem -.
func (r *Repo) CreateGiftItem(ctx context.Context, it entity.GiftItem, out func(entity.GiftItem) []repo.Outbox,
) (entity.GiftItem, error) {
	var created entity.GiftItem

	err := r.inTx(ctx, "Repo - CreateGiftItem", func(tx pgx.Tx) error {
		var err error

		created, err = scanGiftItem(tx.QueryRow(ctx, `INSERT INTO gift_items (name, price_vnd, icon, active, sort_order)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+_giftItemCols, it.Name, it.Price, it.Icon, it.Active, it.SortOrder))
		if err != nil {
			return fmt.Errorf("Repo - CreateGiftItem - insert: %w", err)
		}

		return insertOutbox(ctx, tx, out(created))
	})

	return created, err
}

// UpdateGiftItem -.
func (r *Repo) UpdateGiftItem(ctx context.Context, id string, p repo.GiftItemPatch,
	out func(before, after entity.GiftItem) []repo.Outbox,
) (entity.GiftItem, error) {
	var after entity.GiftItem

	err := r.inTx(ctx, "Repo - UpdateGiftItem", func(tx pgx.Tx) error {
		before, err := scanGiftItem(tx.QueryRow(ctx, `SELECT `+_giftItemCols+` FROM gift_items WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return fmt.Errorf("Repo - UpdateGiftItem: %w", notFound(err))
		}

		after, err = scanGiftItem(tx.QueryRow(ctx, `UPDATE gift_items SET name = COALESCE($2, name),
				price_vnd = COALESCE($3, price_vnd), icon = COALESCE($4, icon), active = COALESCE($5, active),
				sort_order = COALESCE($6, sort_order), updated_at = now()
			WHERE id = $1 RETURNING `+_giftItemCols, id, p.Name, p.Price, p.Icon, p.Active, p.SortOrder))
		if err != nil {
			return fmt.Errorf("Repo - UpdateGiftItem - update: %w", err)
		}

		return insertOutbox(ctx, tx, out(before, after))
	})

	return after, err
}

// SendGift -. The advisory lock per user serializes a user's gifts so the balance check holds.
func (r *Repo) SendGift(ctx context.Context, g entity.GiftSend, rules repo.GiftRules, out func(entity.GiftSend) []repo.Outbox,
) (entity.GiftSend, int64, error) {
	var balance int64

	err := r.inTx(ctx, "Repo - SendGift", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('billing:wallet:' || $1, 0))`,
			g.UserID); err != nil {
			return fmt.Errorf("Repo - SendGift - lock: %w", err)
		}

		var (
			bal int64
			err error
		)

		if g, bal, err = priceGift(ctx, tx, g, rules); err != nil {
			return err
		}

		if err := tx.QueryRow(ctx, `INSERT INTO gift_sends (user_id, channel_id, gift_item_id, item_name, icon,
				unit_price_vnd, quantity, total_vnd, fee_vnd, message)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at`,
			g.UserID, g.ChannelID, g.ItemID, g.ItemName, g.Icon, g.UnitPrice, g.Quantity, g.Total, g.Fee, g.Message,
		).Scan(&g.ID, &g.CreatedAt); err != nil {
			return fmt.Errorf("Repo - SendGift - insert: %w", err)
		}

		if err := insertWallet(ctx, tx, []entity.WalletEntry{{
			UserID: g.UserID, Direction: entity.Debit, Kind: entity.WalletGiftSpend, Amount: g.Total, GiftSendID: &g.ID,
		}}); err != nil {
			return fmt.Errorf("Repo - SendGift: %w", err)
		}

		if err := insertLedger(ctx, tx, giftLedger(g)); err != nil {
			return fmt.Errorf("Repo - SendGift: %w", err)
		}

		balance = bal - g.Total

		return insertOutbox(ctx, tx, out(g))
	})

	return g, balance, err
}

// priceGift copies the locked item into the gift, checks the total and the balance and returns the balance.
// The price under the lock wins over the one the caller saw.
func priceGift(ctx context.Context, tx pgx.Tx, g entity.GiftSend, rules repo.GiftRules) (entity.GiftSend, int64, error) {
	it, err := scanGiftItem(tx.QueryRow(ctx, `SELECT `+_giftItemCols+` FROM gift_items WHERE id = $1 FOR SHARE`, g.ItemID))
	if err != nil {
		return g, 0, fmt.Errorf("Repo - SendGift - item: %w", notFound(err))
	}

	if !it.Active {
		return g, 0, entity.StateError(entity.ReasonGiftInactive)
	}

	qty := int64(g.Quantity)
	if _, err := entity.GiftQuantity(&qty, it.Price, rules.MaxTotal); err != nil {
		return g, 0, err
	}

	g.ItemName, g.Icon, g.UnitPrice = it.Name, it.Icon, it.Price
	g.Total = it.Price * qty
	g.Fee = entity.Fee(g.Total, rules.FeeBps)

	bal, err := walletBalance(ctx, tx, g.UserID)
	if err != nil {
		return g, 0, fmt.Errorf("Repo - SendGift: %w", err)
	}

	if bal < g.Total {
		return g, 0, entity.StateError(entity.ReasonInsufficientFunds, "balance", strconv.FormatInt(bal, 10))
	}

	return g, bal, nil
}

func giftLedger(g entity.GiftSend) []entity.LedgerEntry {
	entries := []entity.LedgerEntry{{
		ChannelID: g.ChannelID, GiftSendID: &g.ID, Direction: entity.Credit, Kind: entity.LedgerGiftRevenue, Amount: g.Total,
	}}

	if g.Fee > 0 {
		entries = append(entries, entity.LedgerEntry{
			ChannelID: g.ChannelID, GiftSendID: &g.ID, Direction: entity.Debit, Kind: entity.LedgerPlatformFee, Amount: g.Fee,
		})
	}

	return entries
}
