package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/config"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
)

const (
	_cliTimeout  = 10 * time.Minute
	_postTimeout = 10 * time.Second
	_maxReply    = 4 << 10
)

var (
	errUsage       = errors.New("usage: billing [backfill-channels | republish-entitlements [--channel ID] | gift seed-demo | dev-ipn replay|synth ... | dev-void --invoice NXL...]")
	errNotDev      = errors.New("dev commands need NXL_ENV=dev")
	errIPNSecret   = errors.New("set BILLING_SEPAY_IPN_SECRET (the running billing-svc must use the same value)")
	errNotPaid     = errors.New("the provider does not report this invoice as paid")
	errNotSePay    = errors.New("dev-void needs BILLING_PAYMENT_PROVIDER=sepay")
	errMissingFlag = errors.New("missing required flag")
)

// RunCLI runs one maintenance command and returns the process exit code.
func RunCLI(cfg *config.Config, args []string) int {
	l := logger.New("warn")

	ctx, cancel := context.WithTimeout(context.Background(), _cliTimeout)
	defer cancel()

	if err := runCLI(ctx, cfg, l, args); err != nil {
		fmt.Fprintln(os.Stderr, "billing:", err)

		return 1
	}

	return 0
}

func runCLI(ctx context.Context, cfg *config.Config, l logger.Interface, args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	d, err := build(ctx, cfg, l)
	if err != nil {
		return err
	}
	defer d.close()

	switch args[0] {
	case "backfill-channels":
		n, err := d.uc.Backfill(ctx)
		fmt.Printf("backfill-channels: %d channels copied (tiers created when missing)\n", n) //nolint:forbidigo // CLI output

		return err
	case "republish-entitlements":
		return republish(ctx, d, args[1:])
	case "gift":
		if len(args) != 2 || args[1] != "seed-demo" {
			return errUsage
		}

		n, err := d.uc.SeedGiftItems(ctx)
		fmt.Printf("gift seed-demo: %d items created (only into an empty catalog)\n", n) //nolint:forbidigo // CLI output

		return err
	case "dev-ipn":
		return devIPN(ctx, cfg, d, args[1:])
	case "dev-void":
		return devVoid(ctx, cfg, d, args[1:])
	}

	return errUsage
}

func republish(ctx context.Context, d *deps, args []string) error {
	fs := flag.NewFlagSet("republish-entitlements", flag.ContinueOnError)
	channel := fs.String("channel", "", "only this channel")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("republish-entitlements: %w", err)
	}

	var ch *string
	if *channel != "" {
		ch = channel
	}

	n, err := d.uc.RepublishEntitlements(ctx, ch)
	fmt.Printf("republish-entitlements: %d subscriptions announced (replay)\n", n) //nolint:forbidigo // CLI output

	return err
}

func devIPN(ctx context.Context, cfg *config.Config, d *deps, args []string) error {
	if cfg.App.Env != "dev" {
		return errNotDev
	}

	if cfg.SePay.IPNSecretGenerated {
		return errIPNSecret
	}

	if len(args) == 0 {
		return errUsage
	}

	fs := flag.NewFlagSet("dev-ipn "+args[0], flag.ContinueOnError)
	txn := fs.String("transaction", "", "transaction id (replay)")
	invoice := fs.String("invoice", "", "invoice number (synth)")
	typ := fs.String("type", sepay.TypeOrderPaid, "ORDER_PAID | TRANSACTION_VOID (synth)")
	times := fs.Int("times", 2, "how many times to send") //nolint:mnd // documented default
	target := fs.String("url", cfg.Fake.DevIPNURL, "webhook URL")

	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("dev-ipn: %w", err)
	}

	var (
		body []byte
		err  error
	)

	switch args[0] {
	case "replay":
		body, err = replayBody(ctx, d, *txn)
	case "synth":
		body, err = synthBody(ctx, d, *invoice, *typ)
	default:
		return errUsage
	}

	if err != nil {
		return err
	}

	return send(ctx, *target, cfg.SePay.IPNSecret, body, *times)
}

func replayBody(ctx context.Context, d *deps, txnID string) ([]byte, error) {
	if txnID == "" {
		return nil, fmt.Errorf("%w: --transaction", errMissingFlag)
	}

	t, err := d.store.Transaction(ctx, txnID)
	if err != nil {
		return nil, fmt.Errorf("dev-ipn replay: %w", err)
	}

	return t.Raw, nil
}

func synthBody(ctx context.Context, d *deps, invoice, typ string) ([]byte, error) {
	if invoice == "" {
		return nil, fmt.Errorf("%w: --invoice", errMissingFlag)
	}

	o, err := d.store.ByInvoice(ctx, invoice)
	if err != nil {
		return nil, fmt.Errorf("dev-ipn synth: %w", err)
	}

	if typ == sepay.TypeTransactionVoid {
		charge, err := d.store.ChargeOf(ctx, o.ID)
		if err != nil {
			return nil, fmt.Errorf("dev-ipn synth: charge of %s: %w", invoice, err)
		}

		return sepay.WithType(charge.Raw, sepay.TypeTransactionVoid, time.Now())
	}

	st, err := d.prov.QueryOrder(ctx, invoice)
	if err != nil {
		return nil, fmt.Errorf("dev-ipn synth: %w", err)
	}

	if st.Status != provider.StatusPaid {
		return nil, errNotPaid
	}

	txnID := st.TxnID
	if txnID == "" {
		txnID = uuid.NewString()
	}

	return sepay.BuildIPN(sepay.IPNInput{
		Type: sepay.TypeOrderPaid, Invoice: invoice, ProviderOrderID: st.ProviderOrderID, TxnID: txnID, Amount: st.Amount.Value,
		Method: st.Method, CustomerID: o.UserID, At: time.Now(),
	})
}

// send posts body n times with the IPN secret and prints each answer (never the secret).
func send(ctx context.Context, target, secret string, body []byte, n int) error {
	client := &http.Client{Timeout: _postTimeout}

	for i := 1; i <= n; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("dev-ipn: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(sepay.HeaderSecret, secret)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("dev-ipn: %w", err)
		}

		reply, _ := io.ReadAll(io.LimitReader(resp.Body, _maxReply)) //nolint:errcheck // printed as is
		resp.Body.Close()

		fmt.Printf("#%d HTTP %d %s\n", i, resp.StatusCode, bytes.TrimSpace(reply)) //nolint:forbidigo // CLI output
	}

	return nil
}

func devVoid(ctx context.Context, cfg *config.Config, d *deps, args []string) error {
	if cfg.App.Env != "dev" {
		return errNotDev
	}

	voider, ok := d.prov.(provider.DevVoider)
	if !ok {
		return errNotSePay
	}

	fs := flag.NewFlagSet("dev-void", flag.ContinueOnError)
	invoice := fs.String("invoice", "", "invoice number")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("dev-void: %w", err)
	}

	if *invoice == "" {
		return fmt.Errorf("%w: --invoice", errMissingFlag)
	}

	if err := voider.VoidTransaction(ctx, *invoice); err != nil {
		return fmt.Errorf("dev-void: %w", err)
	}

	fmt.Println("dev-void: requested; the TRANSACTION_VOID IPN applies it") //nolint:forbidigo // CLI output

	return nil
}
