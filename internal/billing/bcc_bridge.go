package billing

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var bccPreparePage = template.Must(template.New("prepare").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Оплата BCC</title><h1>Подготовка оплаты BCC</h1><p>Укажите телефон и адрес держателя карты. Реквизиты карты вводятся только на странице банка.</p><form method="post" action="{{.Action}}" id="prepare"><input type="hidden" name="token" value="{{.Token}}"><input type="hidden" name="height" id="height"><input type="hidden" name="width" id="width"><p><label>Код страны <input name="cc" inputmode="numeric" pattern="[1-9][0-9]{0,2}" maxlength="3" required autocomplete="tel-country-code"></label></p><p><label>Номер без кода страны <input name="subscriber" inputmode="numeric" pattern="[0-9]{1,15}" maxlength="15" required autocomplete="tel-national"></label></p><p><label>Адрес держателя карты (до 50 символов) <input name="address" maxlength="50" required autocomplete="billing address-line1"></label></p><button id="pay" disabled>Перейти в банк</button><noscript>Для безопасной оплаты включите JavaScript.</noscript></form><script nonce="{{.Nonce}}">document.getElementById('height').value=String(window.screen.height);document.getElementById('width').value=String(window.screen.width);document.getElementById('pay').disabled=false;</script></html>`))
var bccSubmitPage = template.Must(template.New("submit").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Переход в BCC</title><h1>Переход в банк</h1><form id="bank" method="post" action="{{.Action}}">{{range $key,$value:=.Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<button>Продолжить оплату в BCC</button></form><script nonce="{{.Nonce}}">document.getElementById('bank').submit();</script></html>`))

func bccHeaders(w http.ResponseWriter, nonce, formOrigin string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action "+formOrigin+"; base-uri 'none'; frame-ancestors 'none'")
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

// BCCBackref is navigation only. Ignore ALL bank/browser claims, including
// ACTION, RC and P_SIGN. Only the authenticated notification can settle orders.
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
	if r.Method == http.MethodPost {
		b, err := io.Copy(io.Discard, io.LimitReader(r.Body, 32769))
		if err != nil || b > 32768 {
			h.writeError(w, r, ErrValidation)
			return
		}
	}
	target, err := returnTarget(p.returnURL, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
