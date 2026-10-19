// Package config reads billing-svc settings from the environment.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Payment providers.
const (
	ProviderSePay = "sepay"
	ProviderFake  = "fake"

	_envDev          = "dev"
	_sandbox         = "sandbox"
	_sandboxCheckout = "pay-sandbox.sepay.vn"
	_sandboxAPI      = "pgapi-sandbox.sepay.vn"
	_tierCount       = 3
	_maxBps          = 10_000
	_encKeyLen       = 32
	_devSecretLen    = 24
)

type (
	// Config is read from the environment only.
	Config struct {
		App      App
		HTTP     HTTP
		Log      Log
		PG       PG
		NATS     NATS
		Redis    Redis
		Auth     Auth
		Internal Internal
		Billing  Billing
		SePay    SePay
		Fake     Fake
		Swagger  Swagger
	}

	// Swagger -.
	Swagger struct {
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"BILLING_APP_NAME" envDefault:"billing-svc"`
		Env  string `env:"NXL_ENV"          envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"BILLING_HTTP_PORT" envDefault:"8098"`
	}

	// Log -.
	Log struct {
		Level string `env:"BILLING_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"BILLING_PG_POOL_MAX"         envDefault:"8"`
		URL     string `env:"NXL_PG_URL_BILLING,required"`
	}

	// NATS -.
	NATS struct {
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Redis holds job locks, the sync limit and the fake PSP states.
	Redis struct {
		Addr string `env:"BILLING_REDIS_ADDR" envDefault:"localhost:6379"`
	}

	// Auth -.
	Auth struct {
		JWKSURL   string `env:"BILLING_JWKS_URL"    envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
	}

	// Internal is channel-svc's internal API (backfill-channels only).
	Internal struct {
		Secret     string `env:"NXL_INTERNAL_SECRET"`
		ChannelURL string `env:"BILLING_CHANNEL_INTERNAL_URL" envDefault:"http://localhost:8088"`
	}

	// Billing are the NXL-H values.
	Billing struct {
		Provider          string        `env:"BILLING_PAYMENT_PROVIDER"    envDefault:"fake"`
		WebBaseURL        string        `env:"BILLING_WEB_BASE_URL"        envDefault:"http://localhost:5173"`
		PayoutEncKey      string        `env:"BILLING_PAYOUT_ENC_KEY,required"`
		TierPrices        string        `env:"BILLING_TIER_PRICES"         envDefault:"59000,119000,299000"`
		FeeBps            int           `env:"BILLING_PLATFORM_FEE_BPS"    envDefault:"500"`
		PayoutMin         int64         `env:"BILLING_PAYOUT_MIN"          envDefault:"100000"`
		OrderTTL          time.Duration `env:"BILLING_ORDER_TTL"           envDefault:"30m"`
		ReconcileAfter    time.Duration `env:"BILLING_RECONCILE_AFTER"     envDefault:"2m"`
		ReconcileInterval time.Duration `env:"BILLING_RECONCILE_INTERVAL"  envDefault:"1m"`
		ReconcileGiveUp   time.Duration `env:"BILLING_RECONCILE_GIVEUP"    envDefault:"30m"`
		RenewReminderDays int           `env:"BILLING_RENEW_REMINDER_DAYS" envDefault:"3"`

		// Prices is TierPrices parsed by NewConfig.
		Prices [_tierCount]int64 `env:"-"`
	}

	// SePay is the SePay Payment Gateway (sandbox only for this project).
	SePay struct {
		Env          string        `env:"BILLING_SEPAY_ENV"                envDefault:"sandbox"`
		CheckoutURL  string        `env:"BILLING_SEPAY_CHECKOUT_URL"       envDefault:"https://pay-sandbox.sepay.vn/v1/checkout/init"`
		APIURL       string        `env:"BILLING_SEPAY_API_URL"            envDefault:"https://pgapi-sandbox.sepay.vn"`
		MerchantID   string        `env:"BILLING_SEPAY_MERCHANT_ID"`
		SecretKey    string        `env:"BILLING_SEPAY_SECRET_KEY"`
		IPNSecret    string        `env:"BILLING_SEPAY_IPN_SECRET"`
		VerifyViaAPI bool          `env:"BILLING_SEPAY_IPN_VERIFY_VIA_API" envDefault:"false"`
		Methods      []string      `env:"BILLING_SEPAY_METHODS"            envDefault:"BANK_TRANSFER"`
		APITimeout   time.Duration `env:"BILLING_SEPAY_API_TIMEOUT"        envDefault:"3s"`

		// IPNSecretGenerated: no IPN secret was set (fake provider), one was drawn at startup.
		IPNSecretGenerated bool `env:"-"`
	}

	// Fake is the dev payment page.
	Fake struct {
		PSPURL    string `env:"BILLING_FAKE_PSP_URL" envDefault:"http://localhost:8098/dev/fake-psp"`
		IPNURL    string `env:"BILLING_FAKE_IPN_URL" envDefault:"http://localhost:8098/v1/webhooks/sepay"`
		DevIPNURL string `env:"BILLING_DEV_IPN_URL"  envDefault:"http://localhost:8080/v1/webhooks/sepay"`
	}
)

// Configuration errors name the variable, never its value.
var (
	ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")
	ErrProvider               = errors.New("BILLING_PAYMENT_PROVIDER must be sepay or fake (fake only with NXL_ENV=dev)")
	ErrSePayCredentials       = errors.New(
		"BILLING_SEPAY_MERCHANT_ID, BILLING_SEPAY_SECRET_KEY and BILLING_SEPAY_IPN_SECRET are required with sepay")
	ErrSePayEnv = errors.New("BILLING_SEPAY_ENV must be sandbox, with BILLING_SEPAY_CHECKOUT_URL on " + _sandboxCheckout +
		" and BILLING_SEPAY_API_URL on " + _sandboxAPI)
	ErrWebBaseURL   = errors.New("BILLING_WEB_BASE_URL must be an absolute http(s) URL")
	ErrPayoutEncKey = errors.New("BILLING_PAYOUT_ENC_KEY must be 32 bytes, base64 (openssl rand -base64 32)")
	ErrTierPrices   = errors.New("BILLING_TIER_PRICES must be 3 positive integers")
	ErrFeeBps       = errors.New("BILLING_PLATFORM_FEE_BPS must be 0–10000")
	ErrMethods      = errors.New("BILLING_SEPAY_METHODS must be a subset of CARD,BANK_TRANSFER,NAPAS_BANK_TRANSFER")
)

// _devInternalSecret keeps the dev stack working without an .env; refused outside dev.
const _devInternalSecret = "dev-internal-secret" //nolint:gosec // dev-only default, refused outside dev

// NewConfig returns the config, failing fast on a misconfigured payment provider (E-H17).
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	if cfg.Internal.Secret == "" {
		if cfg.App.Env != _envDev {
			return nil, fmt.Errorf("config error: %w", ErrInternalSecretRequired)
		}

		cfg.Internal.Secret = _devInternalSecret
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	prices, err := parsePrices(c.Billing.TierPrices)
	if err != nil {
		return err
	}

	c.Billing.Prices = prices

	if err := c.Billing.validate(); err != nil {
		return err
	}

	if err := checkMethods(c.SePay.Methods); err != nil {
		return err
	}

	switch c.Billing.Provider {
	case ProviderSePay:
		return c.SePay.validate()
	case ProviderFake:
		if c.App.Env != _envDev {
			return ErrProvider
		}

		return c.devSecrets()
	}

	return ErrProvider
}

func (b Billing) validate() error {
	if key, err := base64.StdEncoding.DecodeString(b.PayoutEncKey); err != nil || len(key) != _encKeyLen {
		return ErrPayoutEncKey
	}

	if !absoluteHTTP(b.WebBaseURL) {
		return ErrWebBaseURL
	}

	if b.FeeBps < 0 || b.FeeBps > _maxBps {
		return ErrFeeBps
	}

	return nil
}

func (s SePay) validate() error {
	if s.MerchantID == "" || s.SecretKey == "" || s.IPNSecret == "" {
		return ErrSePayCredentials
	}

	if s.Env != _sandbox || hostOf(s.CheckoutURL) != _sandboxCheckout || hostOf(s.APIURL) != _sandboxAPI {
		return ErrSePayEnv
	}

	return nil
}

// devSecrets draws the fake provider's signing and IPN secrets when they are not set.
func (c *Config) devSecrets() error {
	var err error

	if c.SePay.SecretKey == "" {
		if c.SePay.SecretKey, err = randomSecret(); err != nil {
			return err
		}
	}

	if c.SePay.IPNSecret == "" {
		if c.SePay.IPNSecret, err = randomSecret(); err != nil {
			return err
		}

		c.SePay.IPNSecretGenerated = true
	}

	return nil
}

func randomSecret() (string, error) {
	b := make([]byte, _devSecretLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random secret: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func parsePrices(s string) ([_tierCount]int64, error) {
	var out [_tierCount]int64

	parts := strings.Split(s, ",")
	if len(parts) != _tierCount {
		return out, ErrTierPrices
	}

	for i, p := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || n <= 0 {
			return out, ErrTierPrices
		}

		out[i] = n
	}

	return out, nil
}

func checkMethods(ms []string) error {
	for _, m := range ms {
		switch m {
		case "CARD", "BANK_TRANSFER", "NAPAS_BANK_TRANSFER":
		default:
			return ErrMethods
		}
	}

	return nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return ""
	}

	return u.Hostname()
}

func absoluteHTTP(raw string) bool {
	u, err := url.Parse(raw)

	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
