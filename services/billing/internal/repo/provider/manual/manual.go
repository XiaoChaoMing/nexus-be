// Package manual is the payout provider of a manual bank transfer: an admin sends the money outside the system
// and records the outcome with mark-paid / mark-failed.
package manual

import (
	"context"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
)

var _ provider.PayoutProvider = Provider{}

// Provider -.
type Provider struct{}

// Name -.
func (Provider) Name() string { return provider.NamePayoutManual }

// Disburse leaves the payout PROCESSING; no network call.
func (Provider) Disburse(context.Context, provider.PayoutInstruction) (provider.PayoutResult, error) {
	return provider.PayoutResult{Status: entity.PayoutProcessing}, nil
}
