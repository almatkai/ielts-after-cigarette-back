package billing

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var bccPreparePage = template.Must(template.New("prepare").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Переход к оплате</title><style>body{margin:0;min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;background:#090a0f;color:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}.spinner{width:40px;height:40px;border:3px solid rgba(255,255,255,0.1);border-radius:50%;border-top-color:#3b82f6;animation:spin .8s linear infinite;margin-bottom:16px}@keyframes spin{to{transform:rotate(360deg)}}h2{font-size:1.1rem;font-weight:500;margin:0 0 6px;color:#f9fafb}p{font-size:.85rem;color:#9ca3af;margin:0}.btn{margin-top:20px;background:#2563eb;color:#fff;border:none;padding:10px 20px;border-radius:8px;font-size:.9rem;cursor:pointer}</style></head><body><div class="spinner"></div><h2>Переход на страницу оплаты...</h2><p>Банк ЦентрКредит</p><form method="post" action="{{.Action}}" id="prepare"><input type="hidden" name="token" value="{{.Token}}"><input type="hidden" name="height" id="height" value="1080"><input type="hidden" name="width" id="width" value="1920"><input type="hidden" name="cc" value="7"><input type="hidden" name="subscriber" value="7770000000"><input type="hidden" name="address" value="Almaty"><noscript><button class="btn" type="submit">Перейти в банк</button></noscript></form><script nonce="{{.Nonce}}">var h=(window.screen&&window.screen.height)?String(window.screen.height):"1080";var w=(window.screen&&window.screen.width)?String(window.screen.width):"1920";document.getElementById('height').value=h;document.getElementById('width').value=w;document.getElementById('prepare').submit();</script></body></html>`))
var bccSubmitPage = template.Must(template.New("submit").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Переход в банк</title><style>body{margin:0;min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;background:#090a0f;color:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}.spinner{width:40px;height:40px;border:3px solid rgba(255,255,255,0.1);border-radius:50%;border-top-color:#3b82f6;animation:spin .8s linear infinite;margin-bottom:16px}@keyframes spin{to{transform:rotate(360deg)}}h2{font-size:1.1rem;font-weight:500;margin:0 0 6px;color:#f9fafb}p{font-size:.85rem;color:#9ca3af;margin:0}.btn{margin-top:20px;background:#2563eb;color:#fff;border:none;padding:10px 20px;border-radius:8px;font-size:.9rem;cursor:pointer}</style></head><body><div class="spinner"></div><h2>Подключение к шлюзу банка...</h2><form id="bank" method="post" action="{{.Action}}">{{range $key,$value:=.Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<noscript><button class="btn" type="submit">Продолжить оплату в BCC</button></noscript></form><script nonce="{{.Nonce}}">document.getElementById('bank').submit();</script></body></html>`))

func bccHeaders(w http.ResponseWriter, nonce, formOrigin string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+nonce+"'; form-action "+formOrigin+"; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}
func (h *Handler) BCCBridge(w http.ResponseWriter, r *http.Request) {
	p, ok := h.service.provider.(*BCCProvider)
	if !ok {
		h.writeError(w, r, ErrProviderUnavailable)
		return
	}
	nonce, err := bccNonce()
	if err != nil {
		h.writeError(w, r, ErrProviderUnavailable)
		return
	}
	bccHeaders(w, nonce, "'self'")
	var token string
	var values url.Values
	if r.Method == http.MethodGet {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) != 1 || len(q["token"]) != 1 {
			h.writeError(w, r, ErrValidation)
			return
		}
		token = q.Get("token")
	} else {
		values, err = readBCCForm(r)
		if err != nil || len(values) != 6 {
			h.writeError(w, r, ErrValidation)
			return
		}
		token = values.Get("token")
	}
	o, err := p.bridgeOrder(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrValidation) || errors.Is(err, ErrNotFound) {
			h.writeError(w, r, ErrValidation)
		} else {
			h.writeError(w, r, err)
		}
		return
	}
	action := strings.TrimRight(p.cfg.PublicBaseURL, "/") + "/api/v1" + bccBridgePath
	if r.Method == http.MethodGet {
		// No profile phone is assumed to be the cardholder's phone. Collect it
		// explicitly; do not expose stored profile PII through a bearer capability.
		_ = bccPreparePage.Execute(w, struct{ Action, Token, Nonce string }{action, token, nonce})
		return
	}
	// Capability protects order ownership; Origin/Sec-Fetch-Site additionally
	// blocks cross-site form submission, including attempts to force someone else's phone/address.
	sfs := r.Header.Get("Sec-Fetch-Site")
	origin := r.Header.Get("Origin")
	expectedOrigin := strings.TrimRight(p.cfg.PublicBaseURL, "/")
	if (sfs == "" && origin == "") || (sfs != "" && sfs != "same-origin") || (origin != expectedOrigin && sfs != "same-origin") {
		h.writeError(w, r, ErrValidation)
		return
	}
	info := bccInfo{Height: values.Get("height"), Width: values.Get("width"), Address: values.Get("address")}
	info.Phone.CC = values.Get("cc")
	info.Phone.Subscriber = values.Get("subscriber")
	fields, err := p.fields(o, info, p.clientIP(r))
	if err != nil {
		h.writeError(w, r, ErrValidation)
		return
	}
	gateway, _ := url.Parse(p.cfg.Gateway)
	bccHeaders(w, nonce, gateway.Scheme+"://"+gateway.Host)
	_ = bccSubmitPage.Execute(w, struct {
		Action, Nonce string
		Fields        map[string]string
	}{p.cfg.Gateway, nonce, fields})
}

// BCCBackref is navigation. Success claims are ignored because only the
// authenticated server notification can settle orders and grant access.
// Explicit bank decline/failure reports (ACTION != 0 or RC != 00) are noted
// immediately to mark the order failed and redirect with status=failed so the
// student is not left waiting indefinitely on an unpaid order.
func (h *Handler) BCCBackref(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p, ok := h.service.provider.(*BCCProvider)
	if !ok {
		h.writeError(w, r, ErrProviderUnavailable)
		return
	}
	id, ok := h.id(w, r, "orderID")
	if !ok {
		return
	}
	o, err := p.repo.Order(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeError(w, r, ErrNotFound)
			return
		}
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "bcc backref order lookup failed", "order_id", id, "error", err)
		}
	} else if o.Provider != "bcc" {
		h.writeError(w, r, ErrNotFound)
		return
	}

	var bankFailed bool
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, 32769))
		if err != nil || len(b) > 32768 {
			h.writeError(w, r, ErrValidation)
			return
		}
		if v, err := url.ParseQuery(string(b)); err == nil {
			action := v.Get("ACTION")
			rc := v.Get("RC")
			if (action != "" && action != "0") || (rc != "" && rc != "00") {
				bankFailed = true
			}
		}
	} else if r.Method == http.MethodGet {
		action := r.URL.Query().Get("ACTION")
		rc := r.URL.Query().Get("RC")
		if (action != "" && action != "0") || (rc != "" && rc != "00") {
			bankFailed = true
		}
	}

	if bankFailed && o.Status == "PENDING" && o.ProviderOrderID != nil && *o.ProviderOrderID != "" {
		_, _, _ = h.service.repo.Notify(r.Context(), "bcc", Notification{
			OrderID:         o.ID,
			ProviderOrderID: *o.ProviderOrderID,
			Amount:          o.Amount,
			Currency:        o.Currency,
			Status:          "FAILED",
			Payload:         map[string]any{"source": "backref"},
		})
	}

	target, err := returnTarget(p.returnURL, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if bankFailed {
		if u, err := url.Parse(target); err == nil {
			q := u.Query()
			q.Set("status", "failed")
			u.RawQuery = q.Encode()
			target = u.String()
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
