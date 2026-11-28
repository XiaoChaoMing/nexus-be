// Package devpsp serves /dev/fake-psp: a local payment page for the fake provider (NXL_ENV=dev only, not
// routed by the edge). It posts the same IPN SePay would, so the real webhook path is exercised.
package devpsp

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const _page = `<!doctype html>
<html lang="vi"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Fake PSP</title>
<style>body{font-family:system-ui,sans-serif;max-width:28rem;margin:2rem auto;padding:0 1rem;color:#111}
.banner{background:#fde68a;padding:.5rem .75rem;border-radius:.375rem;font-size:.875rem}
dl{display:grid;grid-template-columns:auto 1fr;gap:.25rem 1rem}dt{color:#555}
form{display:inline}button{font-size:1rem;padding:.5rem 1rem;margin:.25rem .25rem 0 0;border-radius:.375rem;border:1px solid #888;cursor:pointer}
.pay{background:#16a34a;color:#fff;border-color:#16a34a}.err{color:#b91c1c}</style></head><body>
<p class="banner">Môi trường dev — cổng thanh toán giả</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
{{if .Retry}}<form method="post" action="{{.Retry}}"><button type="submit">Thử lại</button></form>{{end}}
{{with .Order}}<h1>Đơn {{.InvoiceNumber}}</h1>
<dl><dt>Kênh</dt><dd>{{.Channel.DisplayName}}</dd><dt>Gói</dt><dd>{{.Tier.Name}}</dd><dt>Số tiền</dt><dd>{{$.Amount}} ₫</dd></dl>
<form method="post" action="/dev/fake-psp/{{.InvoiceNumber}}/pay"><button class="pay" type="submit" data-testid="fake-psp-pay">Trả tiền</button></form>
<form method="post" action="/dev/fake-psp/{{.InvoiceNumber}}/cancel"><button type="submit" data-testid="fake-psp-cancel">Hủy</button></form>
<form method="post" action="/dev/fake-psp/{{.InvoiceNumber}}/fail"><button type="submit" data-testid="fake-psp-fail">Báo lỗi</button></form>
{{end}}</body></html>`

type view struct {
	Order  *entity.Order
	Amount string
	Error  string
	Retry  string
}

type handlers struct {
	uc  usecase.DevPSP
	l   logger.Interface
	tpl *template.Template
}

// Register mounts the fake payment page.
func Register(app fiber.Router, uc usecase.DevPSP, l logger.Interface) {
	h := &handlers{uc: uc, l: l, tpl: template.Must(template.New("psp").Parse(_page))}

	g := app.Group("/dev/fake-psp")
	g.Post("/", h.open)
	g.Post("/:invoice/pay", h.action(uc.FakePay, http.StatusBadGateway))
	g.Post("/:invoice/cancel", h.action(uc.FakeCancel, http.StatusBadRequest))
	g.Post("/:invoice/fail", h.action(uc.FakeFail, http.StatusBadRequest))
}

func (h *handlers) render(ctx *fiber.Ctx, status int, v view) error {
	var b strings.Builder
	if err := h.tpl.Execute(&b, v); err != nil {
		return ctx.Status(http.StatusInternalServerError).SendString("template error")
	}

	ctx.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)

	return ctx.Status(status).SendString(b.String())
}

// open receives the checkout form; the inputs are read in posted order (the signature covers that order).
func (h *handlers) open(ctx *fiber.Ctx) error {
	var fields []entity.Field

	for k, v := range ctx.Request().PostArgs().All() {
		fields = append(fields, entity.Field{Name: string(k), Value: string(v)})
	}

	o, err := h.uc.FakeOpen(ctx.UserContext(), fields)
	if err != nil {
		h.l.Warn("devpsp - open: %v", err)

		return h.render(ctx, http.StatusBadRequest, view{Error: "Chữ ký không hợp lệ hoặc thiếu trường."})
	}

	return h.render(ctx, http.StatusOK, view{Order: &o, Amount: thousands(o.Amount)})
}

// action runs one button and redirects to the stored result URL (never a URL from the request).
func (h *handlers) action(fn func(ctx context.Context, invoice string) (string, error), failStatus int) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		to, err := fn(ctx.UserContext(), ctx.Params("invoice"))
		if err != nil {
			h.l.Warn("devpsp - %s: %v", ctx.Path(), err)

			status := failStatus
			if errors.Is(err, entity.ErrNotFound) {
				status = http.StatusNotFound
			}

			return h.render(ctx, status, view{Error: "Không thực hiện được.", Retry: ctx.Path()})
		}

		return ctx.Redirect(to, http.StatusSeeOther)
	}
}

func thousands(n int64) string {
	const group = 3

	s := strconv.FormatInt(n, 10)
	for i := len(s) - group; i > 0; i -= group {
		s = s[:i] + "." + s[i:]
	}

	return s
}
