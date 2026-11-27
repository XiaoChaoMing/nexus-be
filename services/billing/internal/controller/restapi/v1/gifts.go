package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

type (
	// Wallet -.
	Wallet struct {
		Balance Money `json:"balance"`
	}

	// GiftItemRef is the item of a gift or a wallet entry.
	GiftItemRef struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name" example:"Hoa hồng"`
		Icon string `json:"icon" example:"rose"`
	}

	// WalletGift is the gift a GIFT_SPEND wallet entry paid for.
	WalletGift struct {
		Item     GiftItemRef `json:"item"`
		Quantity int         `json:"quantity" example:"5"`
		Channel  ChannelRef  `json:"channel"`
	}

	// WalletEntry -.
	WalletEntry struct {
		ID            string      `json:"id"`
		Kind          string      `json:"kind"      example:"TOPUP"`
		Direction     string      `json:"direction" example:"CREDIT"`
		Amount        Money       `json:"amount"`
		CreatedAt     time.Time   `json:"createdAt"`
		InvoiceNumber *string     `json:"invoiceNumber"`
		Gift          *WalletGift `json:"gift"`
	}

	// GiftItem is a catalog entry as viewers see it.
	GiftItem struct {
		ID        string `json:"id"`
		Name      string `json:"name"      example:"Hoa hồng"`
		Price     Money  `json:"price"`
		Icon      string `json:"icon"      example:"rose"`
		SortOrder int    `json:"sortOrder" example:"2"`
	}

	// AdminGiftItem -.
	AdminGiftItem struct {
		GiftItem

		Active    bool      `json:"active"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	// Items wraps a list.
	Items[T any] struct {
		Items []T `json:"items"`
	}

	// GiftSend -.
	GiftSend struct {
		ID        string      `json:"id"`
		Item      GiftItemRef `json:"item"`
		Quantity  int         `json:"quantity"  example:"5"`
		UnitPrice Money       `json:"unitPrice"`
		Total     Money       `json:"total"`
		Message   *string     `json:"message"`
		Channel   ChannelRef  `json:"channel"`
		CreatedAt time.Time   `json:"createdAt"`
	}

	// GiftResult is the answer of a gift: the gift and the balance after it.
	GiftResult struct {
		Gift    GiftSend `json:"gift"`
		Balance Money    `json:"balance"`
	}
)

func newGiftItem(it entity.GiftItem) GiftItem {
	return GiftItem{ID: it.ID, Name: it.Name, Price: VND(it.Price), Icon: it.Icon, SortOrder: it.SortOrder}
}

func newAdminGiftItem(it entity.GiftItem) AdminGiftItem {
	return AdminGiftItem{GiftItem: newGiftItem(it), Active: it.Active, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
}

func mapItems[T, U any](in []T, f func(T) U) Items[U] {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}

	return Items[U]{Items: out}
}

func newWalletEntry(e entity.WalletEntry) WalletEntry {
	out := WalletEntry{
		ID: e.ID, Kind: string(e.Kind), Direction: string(e.Direction), Amount: VND(e.Amount), CreatedAt: e.CreatedAt,
		InvoiceNumber: e.InvoiceNumber,
	}

	if e.Gift != nil {
		out.Gift = &WalletGift{
			Item: GiftItemRef{Name: e.Gift.ItemName, Icon: e.Gift.Icon}, Quantity: e.Gift.Quantity,
			Channel: channelRef(e.Gift.Channel),
		}
	}

	return out
}

// jsonInt reads a JSON integer; anything else (string, fraction, missing) is nil.
func jsonInt(raw []byte) *int64 {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return nil
	}

	return &n
}

// wallet -.
//
//	@Summary     The caller's wallet balance
//	@Description Σ credits − Σ debits of the wallet (VND); negative after a spent top-up was voided.
//	@Tags        wallet
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[Wallet]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Router      /me/wallet [get]
func (r *routes) wallet(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	bal, err := r.uc.Wallet.WalletBalance(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "wallet", err)
	}

	return ctx.JSON(Data[Wallet]{Data: Wallet{Balance: VND(bal)}})
}

// walletEntries -.
//
//	@Summary     The caller's wallet history
//	@Description Newest first. kind TOPUP | TOPUP_VOID (invoiceNumber set) or GIFT_SPEND (gift set).
//	@Tags        wallet
//	@Produce     json
//	@Security    BearerAuth
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[Page[WalletEntry]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Router      /me/wallet/entries [get]
func (r *routes) walletEntries(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	cur, limit, ok := pageQuery(ctx)
	if !ok {
		return badLimit(ctx)
	}

	p, err := r.uc.Wallet.WalletEntries(ctx.UserContext(), userID, cur, limit)
	if err != nil {
		return r.fail(ctx, "walletEntries", err)
	}

	return ctx.JSON(Data[Page[WalletEntry]]{Data: Page[WalletEntry]{
		Items: mapItems(p.Items, newWalletEntry).Items, NextCursor: p.Next,
	}})
}

// createTopup -.
//
//	@Summary     Start a wallet top-up checkout
//	@Description amount 10.000–5.000.000 VND (JSON integer). Creates a PENDING TOPUP order and the signed form, like /checkout/orders; the caller's pending top-up of the same amount is returned with 200. Paid → the wallet is credited. Requires Idempotency-Key. 409 INVALID_STATE ORDER_PENDING (orderId, amount) for a pending top-up of another amount; 409 CONFLICT for 5 pending orders.
//	@Tags        wallet
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       Idempotency-Key header   string       true "UUID"
//	@Param       body            body     TopupRequest true "top-up"
//	@Success     201             {object} Data[OrderCheckout]
//	@Success     200             {object} Data[OrderCheckout] "pending top-up of the same amount"
//	@Failure     400             {object} Error "VALIDATION_FAILED"
//	@Failure     401             {object} Error "UNAUTHORIZED"
//	@Failure     409             {object} Error "INVALID_STATE / CONFLICT"
//	@Failure     422             {object} Error "IDEMPOTENCY_KEY_REUSED"
//	@Router      /checkout/topups [post]
func (r *routes) createTopup(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body TopupRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	o, c, created, err := r.uc.Checkout.CreateTopup(ctx.UserContext(), userID, usecase.TopupInput{
		Amount: jsonInt(body.Amount), PaymentMethod: body.PaymentMethod,
		IdempotencyKey: ctx.Get(middleware.HeaderIdempotencyKey),
	})
	if err != nil {
		return r.fail(ctx, "createTopup", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(Data[OrderCheckout]{Data: OrderCheckout{Order: NewOrder(o), Checkout: NewCheckout(c)}})
}

// giftItems -.
//
//	@Summary     The gift catalog
//	@Description Active items, in display order. Public.
//	@Tags        gifts
//	@Produce     json
//	@Success     200 {object} Data[Items[GiftItem]]
//	@Router      /gift-items [get]
func (r *routes) giftItems(ctx *fiber.Ctx) error {
	items, err := r.uc.Gifts.GiftItems(ctx.UserContext())
	if err != nil {
		return r.fail(ctx, "giftItems", err)
	}

	return ctx.JSON(Data[Items[GiftItem]]{Data: mapItems(items, newGiftItem)})
}

// sendGift -.
//
//	@Summary     Give a gift to a channel
//	@Description quantity 1–99 (JSON integer), total ≤ 5.000.000 VND; message ≤ 200 characters (optional). The wallet pays item price × quantity; the channel gets it less the platform fee. Requires Idempotency-Key. 409 INVALID_STATE reason INSUFFICIENT_FUNDS (balance) or GIFT_INACTIVE.
//	@Tags        gifts
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       Idempotency-Key header   string      true "UUID"
//	@Param       channelId       path     string      true "channel id"
//	@Param       body            body     GiftRequest true "gift"
//	@Success     201             {object} Data[GiftResult]
//	@Failure     400             {object} Error "VALIDATION_FAILED"
//	@Failure     401             {object} Error "UNAUTHORIZED"
//	@Failure     403             {object} Error "CHANNEL_SUSPENDED"
//	@Failure     404             {object} Error "NOT_FOUND"
//	@Failure     409             {object} Error "INVALID_STATE / CONFLICT"
//	@Failure     422             {object} Error "CANNOT_GIFT_SELF / IDEMPOTENCY_KEY_REUSED"
//	@Router      /channels/{channelId}/gifts [post]
func (r *routes) sendGift(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body GiftRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	g, bal, err := r.uc.Gifts.SendGift(ctx.UserContext(), userID, ctx.Params("channelId"), usecase.GiftInput{
		ItemID: body.ItemID, Quantity: jsonInt(body.Quantity), Message: body.Message,
	})
	if err != nil {
		return r.fail(ctx, "sendGift", err)
	}

	return ctx.Status(http.StatusCreated).JSON(Data[GiftResult]{Data: GiftResult{
		Gift: GiftSend{
			ID: g.ID, Item: GiftItemRef{ID: g.ItemID, Name: g.ItemName, Icon: g.Icon}, Quantity: g.Quantity,
			UnitPrice: VND(g.UnitPrice), Total: VND(g.Total), Message: g.Message, Channel: channelRef(g.Channel),
			CreatedAt: g.CreatedAt,
		},
		Balance: VND(bal),
	}})
}

// adminGiftItems -.
//
//	@Summary     The gift catalog (admin)
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       active query    bool false "true | false (default: all)"
//	@Success     200    {object} Data[Items[AdminGiftItem]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Failure     403    {object} Error "FORBIDDEN"
//	@Router      /admin/gift-items [get]
func (r *routes) adminGiftItems(ctx *fiber.Ctx) error {
	var active *bool

	if s := ctx.Query("active"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return validationFailed(ctx, map[string]string{"active": entity.DetailFormat})
		}

		active = &b
	}

	items, err := r.uc.Admin.AdminGiftItems(ctx.UserContext(), active)
	if err != nil {
		return r.fail(ctx, "adminGiftItems", err)
	}

	return ctx.JSON(Data[Items[AdminGiftItem]]{Data: mapItems(items, newAdminGiftItem)})
}

// createGiftItem -.
//
//	@Summary     Add a gift to the catalog
//	@Description name 1–40 characters, price 1.000–5.000.000 VND, icon one of heart, rose, star, fire, clap, cake, diamond, crown, trophy, rocket, car, castle; active (default true), sortOrder 0–9999 (default 0). Audited.
//	@Tags        admin-billing
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     GiftItemRequest true "item"
//	@Success     201  {object} Data[AdminGiftItem]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     403  {object} Error "FORBIDDEN"
//	@Router      /admin/gift-items [post]
func (r *routes) createGiftItem(ctx *fiber.Ctx) error {
	return r.giftItemAction(ctx, "createGiftItem", http.StatusCreated, func(adminID string, in usecase.GiftItemInput) (entity.GiftItem, error) {
		return r.uc.Admin.CreateGiftItem(ctx.UserContext(), adminID, in)
	})
}

// updateGiftItem -.
//
//	@Summary     Change a catalog gift
//	@Description Only the given fields change; gifts already given keep their name, icon and price. active=false hides the item from viewers. Audited.
//	@Tags        admin-billing
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string          true "item id"
//	@Param       body body     GiftItemRequest true "fields to change"
//	@Success     200  {object} Data[AdminGiftItem]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     403  {object} Error "FORBIDDEN"
//	@Failure     404  {object} Error "NOT_FOUND"
//	@Router      /admin/gift-items/{id} [patch]
func (r *routes) updateGiftItem(ctx *fiber.Ctx) error {
	return r.giftItemAction(ctx, "updateGiftItem", http.StatusOK, func(adminID string, in usecase.GiftItemInput) (entity.GiftItem, error) {
		return r.uc.Admin.UpdateGiftItem(ctx.UserContext(), adminID, ctx.Params("id"), in)
	})
}

func (r *routes) giftItemAction(ctx *fiber.Ctx, op string, status int,
	act func(adminID string, in usecase.GiftItemInput) (entity.GiftItem, error),
) error {
	adminID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body GiftItemRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	it, err := act(adminID, usecase.GiftItemInput{
		Name: body.Name, Price: body.Price, Icon: body.Icon, Active: body.Active, SortOrder: body.SortOrder,
	})
	if err != nil {
		return r.fail(ctx, op, err)
	}

	return ctx.Status(status).JSON(Data[AdminGiftItem]{Data: newAdminGiftItem(it)})
}
