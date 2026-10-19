package config_test

import (
	"testing"

	"github.com/minhnn/nexuslive-be/services/billing/config"
	"github.com/stretchr/testify/require"
)

const encKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=" // 32 bytes

func base(t *testing.T) {
	t.Helper()

	t.Setenv("NXL_PG_URL_BILLING", "postgres://localhost/nxl_billing")
	t.Setenv("BILLING_PAYOUT_ENC_KEY", encKey)
	t.Setenv("NXL_ENV", "dev")
}

func TestFakeDefaults(t *testing.T) { //nolint:paralleltest // t.Setenv
	base(t)

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	require.Equal(t, config.ProviderFake, cfg.Billing.Provider)
	require.Equal(t, [3]int64{59000, 119000, 299000}, cfg.Billing.Prices)
	require.NotEmpty(t, cfg.SePay.IPNSecret, "drawn at startup")
	require.True(t, cfg.SePay.IPNSecretGenerated)
	require.Equal(t, "8098", cfg.HTTP.Port)
	require.Equal(t, []string{"BANK_TRANSFER"}, cfg.SePay.Methods)
}

func TestFailFast(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		err  error
	}{
		{"fake outside dev", map[string]string{"NXL_ENV": "prod", "NXL_INTERNAL_SECRET": "x"}, config.ErrProvider},
		{"unknown provider", map[string]string{"BILLING_PAYMENT_PROVIDER": "paypal"}, config.ErrProvider},
		{"sepay without credentials", map[string]string{"BILLING_PAYMENT_PROVIDER": "sepay"}, config.ErrSePayCredentials},
		{"sepay production host", map[string]string{
			"BILLING_PAYMENT_PROVIDER": "sepay", "BILLING_SEPAY_MERCHANT_ID": "m", "BILLING_SEPAY_SECRET_KEY": "s",
			"BILLING_SEPAY_IPN_SECRET": "i", "BILLING_SEPAY_CHECKOUT_URL": "https://pay.sepay.vn/v1/checkout/init",
		}, config.ErrSePayEnv},
		{"sepay production env", map[string]string{
			"BILLING_PAYMENT_PROVIDER": "sepay", "BILLING_SEPAY_MERCHANT_ID": "m", "BILLING_SEPAY_SECRET_KEY": "s",
			"BILLING_SEPAY_IPN_SECRET": "i", "BILLING_SEPAY_ENV": "production",
		}, config.ErrSePayEnv},
		{"bad key", map[string]string{"BILLING_PAYOUT_ENC_KEY": "c2hvcnQ="}, config.ErrPayoutEncKey},
		{"relative web url", map[string]string{"BILLING_WEB_BASE_URL": "/web"}, config.ErrWebBaseURL},
		{"two prices", map[string]string{"BILLING_TIER_PRICES": "1,2"}, config.ErrTierPrices},
		{"negative price", map[string]string{"BILLING_TIER_PRICES": "1,-2,3"}, config.ErrTierPrices},
		{"fee", map[string]string{"BILLING_PLATFORM_FEE_BPS": "10001"}, config.ErrFeeBps},
		{"method", map[string]string{"BILLING_SEPAY_METHODS": "BANK_TRANSFER,CASH"}, config.ErrMethods},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base(t)

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := config.NewConfig()
			require.ErrorIs(t, err, tt.err)
			require.NotContains(t, err.Error(), "s3cr3t")
		})
	}
}

func TestSePayOK(t *testing.T) {
	base(t)
	t.Setenv("BILLING_PAYMENT_PROVIDER", "sepay")
	t.Setenv("BILLING_SEPAY_MERCHANT_ID", "m")
	t.Setenv("BILLING_SEPAY_SECRET_KEY", "s3cr3t")
	t.Setenv("BILLING_SEPAY_IPN_SECRET", "i")

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	require.False(t, cfg.SePay.IPNSecretGenerated)
	require.Equal(t, "s3cr3t", cfg.SePay.SecretKey)
}
