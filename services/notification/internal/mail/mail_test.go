package mail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/mail"
	"github.com/stretchr/testify/require"
)

// fullData has every key any template reads.
func fullData() map[string]string {
	return map[string]string{
		"Username": "an", "Link": "http://web.test/x?a=1&b=2", "ValidFor": "24 giờ", "Name": "An <b>", "Channel": "Kênh K",
		"Title": "Đêm nhạc", "When": "20:00 16/10/2026 (+07)", "NewWhen": "", "Reason": "CANCELLED", "Sender": "B",
		"Excerpt": "@an <script>", "Result": "APPROVED", "Until": "", "UnsubscribeURL": "http://web.test/notifications/unsubscribe?token=t",
		"Invoice": "NXL7Q2K9M4X1C", "Tier": "1", "SubAction": "NEW", "Method": "Chuyển khoản ngân hàng", "Amount": "59.000 ₫",
		"Price": "59.000 ₫", "Review": "", "Ended": "1", "Renewable": "1",
	}
}

func TestRenderEveryTemplate(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)
	require.Len(t, mail.Templates(), 16)

	for _, tpl := range mail.Templates() {
		for _, lc := range mail.Locales() {
			msg, err := m.Render(tpl, lc, "an@x.dev", fullData())
			require.NoError(t, err, tpl+"."+lc)
			require.NotEmpty(t, msg.Subject, tpl+"."+lc)
			require.NotContains(t, msg.Subject, "\n")
			require.NotContains(t, msg.Text, "<no value>")
			require.Contains(t, msg.HTML, "<html lang=\""+lc+"\">")
			require.NotContains(t, msg.HTML, "<script>", "HTML is escaped")
			require.Equal(t, "an@x.dev", msg.To)
		}
	}
}

func TestRenderNonMandatoryHasUnsubscribe(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)

	msg, err := m.Render("live_started", "en", "an@x.dev", fullData())
	require.NoError(t, err)
	require.Equal(t, "[LIVE] Kênh K is live: Đêm nhạc", msg.Subject)
	require.Contains(t, msg.Text, "unsubscribe?token=t")
	require.Contains(t, msg.HTML, "unsubscribe?token=t")

	msg, err = m.Render("channel_suspended", "vi", "an@x.dev", fullData())
	require.NoError(t, err)
	require.NotContains(t, msg.Text, "unsubscribe")
}

func TestRenderMissingKeyFails(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)

	data := fullData()
	delete(data, "Title")

	_, err = m.Render("live_started", "vi", "an@x.dev", data)
	require.Error(t, err)

	_, err = m.Render("nope", "vi", "an@x.dev", data)
	require.ErrorIs(t, err, mail.ErrUnknownTemplate)
}

func TestRenderUnknownLocaleIsVietnamese(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)

	msg, err := m.Render("verify_email", "fr", "an@x.dev", fullData())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(msg.Subject, "Xác minh"))
}

func TestSendWithoutSMTP(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)
	require.ErrorIs(t, m.Send(context.Background(), entity.Message{To: "an@x.dev"}), entity.ErrMailDisabled)
}

func TestRenderSubscriptionTemplates(t *testing.T) {
	t.Parallel()

	m, err := mail.New(nil)
	require.NoError(t, err)

	data := fullData()
	data["Until"] = "13:14 15/11/2026 (+07)"

	msg, err := m.Render("payment_receipt", "vi", "an@x.dev", data)
	require.NoError(t, err)
	require.Equal(t, "Biên nhận NXL7Q2K9M4X1C: Sub Tier 1 kênh Kênh K", msg.Subject)
	require.Contains(t, msg.Text, "Gói: Sub Tier 1 · Mua mới")
	require.Contains(t, msg.Text, "Tổng: 59.000 ₫")
	require.Contains(t, msg.Text, "Hiệu lực tới: 13:14 15/11/2026 (+07)")
	require.NotContains(t, msg.Text, "unsubscribe", "mandatory e-mail")

	data["Review"], data["SubAction"] = "1", "RENEW"
	msg, err = m.Render("payment_receipt", "en", "an@x.dev", data)
	require.NoError(t, err)
	require.Equal(t, "Payment NXL7Q2K9M4X1C received — under review", msg.Subject)
	require.Contains(t, msg.Text, "Renewal")

	msg, err = m.Render("subscription_expiring", "vi", "an@x.dev", data)
	require.NoError(t, err)
	require.Contains(t, msg.Text, "Gia hạn 59.000 ₫: http://web.test/x?a=1&b=2")
	require.Contains(t, msg.Text, "unsubscribe?token=t")

	data["Renewable"] = ""
	msg, err = m.Render("subscription_expiring", "en", "an@x.dev", data)
	require.NoError(t, err)
	require.Contains(t, msg.Text, "prepaid the maximum")

	msg, err = m.Render("payment_voided", "vi", "an@x.dev", data)
	require.NoError(t, err)
	require.Contains(t, msg.Text, "Subscription đã kết thúc.")

	data["Ended"] = ""
	msg, err = m.Render("payment_voided", "en", "an@x.dev", data)
	require.NoError(t, err)
	require.Contains(t, msg.Text, "valid until 13:14 15/11/2026 (+07)")
}
