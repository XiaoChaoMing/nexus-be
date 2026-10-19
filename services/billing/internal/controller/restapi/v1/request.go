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

	// RejectRequest is POST /admin/payouts/{id}/reject.
	RejectRequest struct {
		Reason string `json:"reason" example:"Account name does not match the channel owner"`
	}
)
