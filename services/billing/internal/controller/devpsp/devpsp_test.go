package devpsp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/devpsp"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/stretchr/testify/require"
)

var errIPN = errors.New("ipn rejected")

type psp struct {
	fields []entity.Field
}

func (p *psp) FakeOpen(_ context.Context, fields []entity.Field) (entity.Order, error) {
	p.fields = fields
	if fields[len(fields)-1].Value != "good" {
		return entity.Order{}, entity.ErrValidation
	}

	return entity.Order{
		InvoiceNumber: "NXL1", Amount: 1190000, Channel: entity.Channel{DisplayName: "<b>Kaelen</b>"},
		Tier: entity.Tier{Level: 2},
	}, nil
}

func (*psp) FakePay(_ context.Context, inv string) (string, error) {
	if inv == "NXLBAD" {
		return "", errIPN
	}

	return "http://localhost:5173/checkout/result?orderId=o&r=success", nil
}

func (*psp) FakeCancel(context.Context, string) (string, error) { return "", entity.ErrNotFound }
func (*psp) FakeFail(context.Context, string) (string, error)   { return "http://x/?r=error", nil }

func post(t *testing.T, a *fiber.App, path string, form url.Values) (status int, body, location string) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

func TestFakePSPPage(t *testing.T) {
	t.Parallel()

	p := &psp{}
	a := fiber.New()
	devpsp.Register(a, p, logger.New("error"))

	status, body, _ := post(t, a, "/dev/fake-psp", url.Values{"order_amount": {"1190000"}, "signature": {"bad"}})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, "Chữ ký")

	status, body, _ = post(t, a, "/dev/fake-psp", url.Values{"signature": {"good"}})
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "1.190.000 ₫")
	require.Contains(t, body, "Tier 2")
	require.Contains(t, body, "&lt;b&gt;Kaelen", "escaped")
	require.Contains(t, body, `action="/dev/fake-psp/NXL1/pay"`)

	status, _, loc := post(t, a, "/dev/fake-psp/NXL1/pay", nil)
	require.Equal(t, http.StatusSeeOther, status)
	require.Equal(t, "http://localhost:5173/checkout/result?orderId=o&r=success", loc)

	status, body, _ = post(t, a, "/dev/fake-psp/NXLBAD/pay", nil)
	require.Equal(t, http.StatusBadGateway, status)
	require.Contains(t, body, "Thử lại")

	status, _, _ = post(t, a, "/dev/fake-psp/NXL1/cancel", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _, loc = post(t, a, "/dev/fake-psp/NXL1/fail", nil)
	require.Equal(t, http.StatusSeeOther, status)
	require.Equal(t, "http://x/?r=error", loc)
}
