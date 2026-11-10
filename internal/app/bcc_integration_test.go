package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestBCCAppRoutingCORSAndBankNotification(t *testing.T) {
	pool := testdb.Open(t)
	user := testdb.User(t, pool)
	cfg := config.Config{Environment: "test", JWTSecret: "bcc-app-test-at-least-32-characters-long", JWTIssuer: "test", JWTAudience: "test", AccessTokenTTL: time.Hour, MaxRequestBody: 65536, RequestTimeout: time.Minute, MediaUploadTimeout: time.Minute, PaywallEnabled: true, PaymentProvider: "bcc", PaymentReturnURL: "https://app.example/app/billing/return", CORSAllowedOrigins: []string{"https://app.example"}, BCC: billing.BCCConfig{Gateway: billing.BCCTestGateway, Terminal: "88888881", Merchant: "00000001", MerchantName: "IELTS TEST", MACKey: "6BB0AC02E47BDF73D98FEB777F3B5294", NotifyURL: "https://pay.example:443/api/v1/billing/callbacks/bcc", NotifyUser: "notify", NotifyPassword: "test-notify-password-long", PublicBaseURL: "https://pay.example", BridgeKey: strings.Repeat("ab", 32), BridgeTTL: 15 * time.Minute, Language: "ru"}}
	router := New(cfg, pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tm := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	token, _, err := tm.NewAccessToken(user, auth.RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/billing/checkout", strings.NewReader(`{"planCode":"self","acceptedTerms":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var checkout struct {
		ID       uuid.UUID `json:"orderId"`
		Redirect string    `json:"redirectUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checkout); err != nil || w.Code != 201 {
		t.Fatalf("checkout: %d %s %v", w.Code, w.Body, err)
	}
	bridge, _ := url.Parse(checkout.Redirect)
	form := url.Values{"token": {bridge.Query().Get("token")}, "height": {"1080"}, "width": {"1920"}, "cc": {"7"}, "subscriber": {"7475558888"}, "address": {"Алматы 88"}}
	r = httptest.NewRequest("POST", bridge.Path, strings.NewReader(form.Encode()))
	r.Header.Set("Origin", "https://pay.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), billing.BCCTestGateway) {
		t.Fatalf("backend same-origin bridge blocked: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/api/v1/billing/bcc/return/"+checkout.ID.String(), strings.NewReader("ACTION=0&RC=00"))
	r.Header.Set("Origin", "null")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatalf("bank return blocked: %d %s", w.Code, w.Body)
	}
	repo := billing.NewPostgresRepository(pool)
	o, err := repo.Order(context.Background(), checkout.ID)
	if err != nil || o.Status != "PENDING" {
		t.Fatalf("forged BACKREF changed status: %+v %v", o, err)
	}
	bank := url.Values{"ORDER": {*o.ProviderOrderID}, "MERCH_RN_ID": {*o.ProviderOrderID}, "MERCHANT": {cfg.BCC.Merchant}, "TERMINAL": {cfg.BCC.Terminal}, "AMOUNT": {strconv.FormatInt(o.Amount, 10) + ".00"}, "CURRENCY": {"398"}, "TRTYPE": {"1"}, "ACTION": {"0"}, "RC": {"00"}}
	r = httptest.NewRequest("POST", "/api/v1/billing/callbacks/bcc", strings.NewReader(bank.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://test3ds.bcc.kz:5445")
	r.SetBasicAuth(cfg.BCC.NotifyUser, cfg.BCC.NotifyPassword)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("notification blocked: %d %s", w.Code, w.Body)
	}
	o, err = repo.Order(context.Background(), checkout.ID)
	if err != nil || o.Status != "PAID" {
		t.Fatal("bank notification did not settle")
	}
}
