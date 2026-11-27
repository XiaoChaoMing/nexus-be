package v1

// Request bodies the REST API accepts.

import "encoding/json"

type (
	// CreateOrderRequest is POST /checkout/orders. paymentMethod absent or null = choose on the payment page.
	CreateOrderRequest struct {
		TierID        string  `json:"tierId"        example:"0199e001-0000-7000-8000-000000000001"`
		PaymentMethod *string `json:"paymentMethod" example:"BANK_TRANSFER"`
	}

	// SyncRequest is POST /checkout/orders/{id}/sync: what the payment page redirect said.
	SyncRequest struct {
		Outcome string `json:"outcome" example:"SUCCESS" enums:"SUCCESS,ERROR,CANCEL"`
	}

	// TierPatchRequest is PATCH /studio/monetization/tiers/{level}.
	TierPatchRequest struct {
		Active      *bool   `json:"active"`
		Description *string `json:"description"`
	}

	// PayoutRequest is POST /studio/monetization/payouts. amount must be a JSON integer.
	PayoutRequest struct {
		Amount        json.RawMessage `json:"amount"        swaggertype:"integer" example:"100000"`
		BankCode      string          `json:"bankCode"      example:"VCB"`
		AccountNumber string          `json:"accountNumber" example:"0123456789"`
		AccountName   string          `json:"accountName"   example:"Nguyen Van An"`
	}

	// RejectRequest is POST /admin/payouts/{id}/reject and /mark-failed.
	RejectRequest struct {
		Reason string `json:"reason" example:"Account name does not match the channel owner"`
	}

	// TopupRequest is POST /checkout/topups. amount must be a JSON integer.
	TopupRequest struct {
		Amount        json.RawMessage `json:"amount"        swaggertype:"integer" example:"100000"`
		PaymentMethod *string         `json:"paymentMethod" example:"BANK_TRANSFER"`
	}

	// GiftRequest is POST /channels/{channelId}/gifts. quantity must be a JSON integer.
	GiftRequest struct {
		ItemID   string          `json:"itemId"`
		Quantity json.RawMessage `json:"quantity" swaggertype:"integer" example:"5"`
		Message  string          `json:"message"  example:"Chúc stream vui!"`
	}

	// GiftItemRequest is POST /admin/gift-items (name, price, icon required) and PATCH (fields optional).
	GiftItemRequest struct {
		Name      *string `json:"name"      example:"Hoa hồng"`
		Price     *int64  `json:"price"     example:"10000"`
		Icon      *string `json:"icon"      example:"rose"`
		Active    *bool   `json:"active"    example:"true"`
		SortOrder *int    `json:"sortOrder" example:"2"`
	}

	// MarkPaidRequest is POST /admin/payouts/{id}/mark-paid.
	MarkPaidRequest struct {
		Reference string `json:"reference" example:"FT24123456789"`
	}
)
