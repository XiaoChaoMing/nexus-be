package manual_test

import (
	"testing"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/manual"
	"github.com/stretchr/testify/require"
)

func TestDisburse(t *testing.T) {
	t.Parallel()

	p := manual.Provider{}
	res, err := p.Disburse(t.Context(), provider.PayoutInstruction{PayoutID: "p1", Code: "PO-0001", Amount: 100000})
	require.NoError(t, err)
	require.Equal(t, provider.PayoutResult{Status: entity.PayoutProcessing}, res)
	require.Equal(t, "MANUAL", p.Name())
}
