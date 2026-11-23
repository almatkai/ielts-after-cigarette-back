package billing

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestBCCCallbackRejectionReasons(t *testing.T) {
	p, repo, s, _ := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)

	cases := []struct {
		name   string
		mutate func(url.Values, *http.Request) *http.Request
		reason string
	}{
		{"no auth", func(v url.Values, r *http.Request) *http.Request { r.Header.Del("Authorization"); return r }, "auth_header_count"},
		{"wrong password", func(v url.Values, r *http.Request) *http.Request {
			r.SetBasicAuth("bank-notify", "wrong")
			return r
		}, "auth_mismatch"},
		{"wrong content type", func(v url.Values, r *http.Request) *http.Request {
			r.Header.Set("Content-Type", "application/json")
			return r
		}, "form_unreadable"},
		{"missing MERCH_RN_ID", func(v url.Values, _ *http.Request) *http.Request { v.Del("MERCH_RN_ID"); return callbackRequest(v) }, "field_mismatch:MERCH_RN_ID"},
		{"wrong terminal", func(v url.Values, _ *http.Request) *http.Request { v.Set("TERMINAL", "1"); return callbackRequest(v) }, "field_mismatch:TERMINAL"},
		{"bad amount", func(v url.Values, _ *http.Request) *http.Request { v.Set("AMOUNT", "1,00"); return callbackRequest(v) }, "field_invalid:AMOUNT"},
		{"other amount", func(v url.Values, _ *http.Request) *http.Request { v.Set("AMOUNT", "1.00"); return callbackRequest(v) }, "amount_mismatch"},
		{"unknown order", func(v url.Values, _ *http.Request) *http.Request {
			v.Set("ORDER", "1999999999999999")
			v.Set("MERCH_RN_ID", bccMerchRNID("88888881", "1999999999999999"))
			return callbackRequest(v)
		}, "order_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := callbackValues(o)
			_, err := p.ParseCallback(tc.mutate(v, callbackRequest(v)))
			var rej *callbackRejection
			if !errors.Is(err, ErrInvalidCallback) || !errors.As(err, &rej) || rej.reason != tc.reason {
				t.Fatalf("err=%v, want reason %q", err, tc.reason)
			}
		})
	}
}

func TestBCCCallbackRejectionIsLoggedWithoutSecrets(t *testing.T) {
	_, repo, s, _ := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)

	var logs bytes.Buffer
	h := NewHandler(s, slog.New(slog.NewJSONHandler(&logs, nil)), 1<<16)
	router := chi.NewRouter()
	router.Post("/api/v1/billing/callbacks/{provider}", h.Callback)

	v := callbackValues(o)
	v.Del("MERCH_RN_ID")
	v.Set("CARD", "4405********1234")
	req := callbackRequest(v)
	req.SetBasicAuth("bank-notify", "test-only-long-password")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_PAYMENT_CALLBACK") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	out := logs.String()
	for _, want := range []string{`"reason":"field_mismatch:MERCH_RN_ID"`, `"auth_scheme":"basic"`, `"fields":"ACTION,AMOUNT,CARD,CURRENCY,MERCHANT,ORDER,RC,TERMINAL,TRTYPE"`, `"f_order":"` + *o.ProviderOrderID + `"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %s: %s", want, out)
		}
	}
	for _, secret := range []string{"test-only-long-password", "4405", "1234"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaked %q: %s", secret, out)
		}
	}
}
