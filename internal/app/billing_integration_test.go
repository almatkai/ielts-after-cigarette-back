package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/guest"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestBillingHTTPAuthenticationOwnershipAdministrationAndFakePayment(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	cfg := config.Config{Environment: "test", JWTSecret: "billing-test-secret-at-least-32-characters", JWTIssuer: "billing-test", JWTAudience: "billing-test", AccessTokenTTL: time.Hour, MaxRequestBody: 1 << 20, RequestTimeout: time.Minute, MediaUploadTimeout: time.Minute, PaywallEnabled: true, PaymentProvider: "fake", PaymentReturnURL: "https://academy.test/billing/return", CORSAllowedOrigins: []string{"https://academy.test"}, GuestTrialEnabled: true}
	router := New(cfg, pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	user, other, admin, editor := testdb.User(t, pool), testdb.User(t, pool), testdb.User(t, pool), testdb.User(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='ADMIN' WHERE id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='EDITOR' WHERE id=$1`, editor); err != nil {
		t.Fatal(err)
	}
	token := func(id uuid.UUID, role string) string {
		value, _, err := tokens.NewAccessToken(id, role)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	studentToken, otherToken, adminToken, editorToken := token(user, auth.RoleStudent), token(other, auth.RoleStudent), token(admin, auth.RoleAdmin), token(editor, auth.RoleEditor)
	request := func(method, path, body, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/billing/plans", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"salePrice":4990`) {
		t.Fatalf("plans: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/billing/me", "", ""); w.Code != 401 {
		t.Fatalf("anonymous access: %d", w.Code)
	}
	if w := request("POST", "/billing/checkout", `{"planCode":"self"}`, studentToken); w.Code != 400 {
		t.Fatalf("terms: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/billing/checkout", `{"planCode":"unknown","acceptedTerms":true}`, studentToken); w.Code != 404 {
		t.Fatalf("unknown plan: %d %s", w.Code, w.Body.String())
	}
	w := request("POST", "/billing/checkout", `{"planCode":"group","acceptedTerms":true}`, studentToken)
	if w.Code != 201 {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	var checkout struct {
		OrderID     uuid.UUID `json:"orderId"`
		RedirectURL string    `json:"redirectUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checkout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checkout.RedirectURL, checkout.OrderID.String()) {
		t.Fatal("redirect missing orderId")
	}
	if w = request("GET", "/billing/orders/"+checkout.OrderID.String(), "", otherToken); w.Code != 404 {
		t.Fatalf("foreign order: %d %s", w.Code, w.Body.String())
	}
	if w = request("GET", "/billing/orders/"+checkout.OrderID.String(), "", studentToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"PAID"`) || !strings.Contains(w.Body.String(), `"amount":14990`) || !strings.Contains(w.Body.String(), `"providerOrderId":"fake-`) {
		t.Fatalf("paid order: %d %s", w.Code, w.Body.String())
	}
	if w = request("GET", "/billing/me", "", studentToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) || !strings.Contains(w.Body.String(), `"meetingsTotal":4`) {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	for _, bearer := range []string{studentToken, editorToken} {
		if w = request("GET", "/admin/billing/orders", "", bearer); w.Code != 403 {
			t.Fatalf("admin billing open: %d", w.Code)
		}
	}
	if w = request("GET", "/admin/billing/orders?status=PAID&limit=10&offset=0", "", adminToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"email":`) {
		t.Fatalf("orders: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"?status=UNKNOWN", "?limit=0", "?offset=-1"} {
		if w = request("GET", "/admin/billing/orders"+query, "", adminToken); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", query, w.Code)
		}
	}
	if w = request("POST", "/admin/users/"+user.String()+"/entitlements", `{"planCode":"personal","source":"BONUS","days":7,"note":"Trial extension"}`, adminToken); w.Code != 201 {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	var granted struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &granted); err != nil {
		t.Fatal(err)
	}
	if w = request("PATCH", "/admin/entitlements/"+granted.ID.String()+"/meetings", `{"meetingsUsed":5}`, adminToken); w.Code != 400 {
		t.Fatalf("meetings overflow: %d %s", w.Code, w.Body.String())
	}
	if w = request("PATCH", "/admin/entitlements/"+granted.ID.String()+"/meetings", `{"meetingsUsed":2}`, adminToken); w.Code != 200 {
		t.Fatalf("meetings: %d %s", w.Code, w.Body.String())
	}
	if w = request("POST", "/admin/entitlements/"+granted.ID.String()+"/revoke", `{"note":"Cancelled"}`, adminToken); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w = request("GET", "/admin/users/"+user.String()+"/billing", "", adminToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"entitlements":[`) || !strings.Contains(w.Body.String(), `"orders":[`) {
		t.Fatalf("user billing: %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", "/admin/billing/settings", `{"freeListening":2,"freeReading":3,"freeWriting":4,"freeSpeaking":5,"freeAssistantDailyLimit":0}`, adminToken); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", "/admin/billing/settings", `{"paywallEnabled":false}`, adminToken); w.Code != 400 {
		t.Fatalf("env flag writable: %d", w.Code)
	}
	if w = request("PUT", "/admin/billing/plans/self", `{"listPrice":9990,"salePrice":null,"active":true}`, adminToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"salePrice":null`) {
		t.Fatalf("remove discount: %d %s", w.Code, w.Body.String())
	}
	// Callback verification runs even for an Origin unrelated to the frontend.
	r := httptest.NewRequest("POST", "/api/v1/billing/callbacks/fake", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://bank.test")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_PAYMENT_CALLBACK") {
		t.Fatalf("callback blocked by CORS or unsigned callback accepted: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/api/v1/billing/checkout", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://bank.test")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("ordinary CORS bypassed: %d", w.Code)
	}
	t.Run("production manual grants without payment provider", func(t *testing.T) {
		production := cfg
		production.Environment = "production"
		production.PaymentProvider = "none"
		manual := New(production, pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		call := func(method, path, body, bearer string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+bearer)
			w := httptest.NewRecorder()
			manual.ServeHTTP(w, r)
			return w
		}
		w := call("POST", "/billing/checkout", `{"planCode":"self","acceptedTerms":true}`, otherToken)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "PAYMENT_PROVIDER_UNAVAILABLE") {
			t.Fatalf("manual checkout: %d %s", w.Code, w.Body.String())
		}
		var orders int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM billing_orders WHERE user_id=$1`, other).Scan(&orders); err != nil || orders != 0 {
			t.Fatalf("unavailable checkout created orders: %d %v", orders, err)
		}
		if w = call("POST", "/admin/users/"+other.String()+"/entitlements", `{"planCode":"self","source":"ADMIN","note":"Manual production access"}`, adminToken); w.Code != 201 {
			t.Fatalf("manual grant: %d %s", w.Code, w.Body.String())
		}
		if w = call("GET", "/billing/me", "", otherToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) || !strings.Contains(w.Body.String(), `"paywallEnabled":true`) {
			t.Fatalf("manual access: %d %s", w.Code, w.Body.String())
		}
	})
	// Cookie guests can read billing/me, but still cannot buy or read orders.
	t.Run("cookie guest", func(t *testing.T) {
		redisURL := os.Getenv("TEST_REDIS_URL")
		if redisURL == "" {
			t.Skip("TEST_REDIS_URL is not set")
		}
		options, err := redis.ParseURL(redisURL)
		if err != nil {
			t.Fatal(err)
		}
		client := redis.NewClient(options)
		defer client.Close()
		router := New(cfg, pool, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
		cookie := strings.Repeat("g", 43)
		mac := hmac.New(sha256.New, []byte(cfg.JWTSecret))
		mac.Write([]byte(cookie))
		if _, err := guest.NewPostgresRepository(pool).Create(ctx, mac.Sum(nil), "academic", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/billing/me", "/billing/orders/" + checkout.OrderID.String(), "/billing/checkout"} {
			method := "GET"
			if strings.HasSuffix(path, "checkout") {
				method = "POST"
			}
			r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(`{}`))
			r.AddCookie(&http.Cookie{Name: guest.CookieName, Value: cookie})
			w = httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if path == "/billing/me" {
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"guest":true`) {
					t.Fatalf("guest me: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 401 {
				t.Fatalf("guest billing capability widened: %s %d", path, w.Code)
			}
		}

	})
}
