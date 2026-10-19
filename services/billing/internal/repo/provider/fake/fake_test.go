package fake_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/fake"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/stretchr/testify/require"
)

type store struct {
	mu sync.Mutex
	m  map[string]map[string]string
}

func (s *store) Get(_ context.Context, inv string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.m[inv], nil
}

func (s *store) Set(_ context.Context, inv string, f map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[inv] = f

	return nil
}

func (s *store) Invoices(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}

	return out, nil
}

func TestFakeProvider(t *testing.T) { //nolint:funlen // one story
	t.Parallel()

	var (
		mu   sync.Mutex
		ipns []provider.Notification
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		n, perr := sepay.ParseIPN(r.Header, body, "is")
		if perr != nil {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		mu.Lock()

		ipns = append(ipns, n)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	st := &store{m: map[string]map[string]string{}}
	p := fake.New(fake.Config{
		PSPURL: "http://localhost:8098/dev/fake-psp", IPNURL: srv.URL, SecretKey: "sk",
		IPNSecret: "is",
	}, st)
	ctx := context.Background()

	c, err := p.BuildCheckout(ctx, provider.CheckoutOrder{Invoice: "NXL1", Amount: 59000, UserID: "u", SuccessURL: "s"})
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8098/dev/fake-psp", c.ActionURL)
	require.True(t, p.VerifyForm(c.Fields))
	require.Contains(t, c.Fields, entity.Field{Name: "merchant", Value: "FAKE"})

	_, err = p.QueryOrder(ctx, "NXL1")
	require.ErrorIs(t, err, entity.ErrProviderNotFound)

	require.NoError(t, p.Pay(ctx, provider.FakePayment{Invoice: "NXL1", Amount: 59000, CustomerID: "u"}))
	require.Len(t, ipns, 1)
	require.Equal(t, provider.KindPaid, ipns[0].Kind)
	require.True(t, ipns[0].Approved())
	require.Equal(t, int64(59000), ipns[0].OrderAmount.Value)

	q, err := p.QueryOrder(ctx, "NXL1")
	require.NoError(t, err)
	require.Equal(t, provider.StatusPaid, q.Status)
	require.Equal(t, ipns[0].TxnID, q.TxnID)

	require.NoError(t, p.CancelOrder(ctx, "NXL1"))
	q, err = p.QueryOrder(ctx, "NXL1")
	require.NoError(t, err)
	require.Equal(t, provider.StatusPaid, q.Status, "a paid order cannot be cancelled")

	require.NoError(t, p.Decline(ctx, "NXL2"))
	q, err = p.QueryOrder(ctx, "NXL2")
	require.NoError(t, err)
	require.Equal(t, entity.FailDeclined, q.Failure)

	require.NoError(t, p.Cancel(ctx, "NXL3"))

	listed, err := p.ListOrders(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, listed, 3)

	// A webhook refusing the secret surfaces as an error (the page shows 502).
	bad := fake.New(fake.Config{IPNURL: srv.URL, IPNSecret: "wrong"}, st)
	require.ErrorIs(t, bad.Pay(ctx, provider.FakePayment{Invoice: "NXL4", Amount: 1}), fake.ErrIPNRejected)
	require.Equal(t, provider.NameFake, p.Name())
}
