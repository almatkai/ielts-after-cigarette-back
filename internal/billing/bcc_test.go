package billing

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func testBCCConfig() BCCConfig {
	return BCCConfig{Gateway: BCCTestGateway, Terminal: "88888881", Merchant: "00000001", MerchantName: "IELTS TEST", MACKey: "6BB0AC02E47BDF73D98FEB777F3B5294", NotifyURL: "https://pay.example:443/api/v1/billing/callbacks/bcc", NotifyUser: "bank-notify", NotifyPassword: "test-only-long-password", PublicBaseURL: "https://pay.example", BridgeKey: strings.Repeat("ab", 32), Language: "ru", BridgeTTL: 15 * time.Minute}
}

// This fake exercises the HTTP contract without a database. Database locking,
// UNIQUE mapping and real entitlement idempotency are covered separately.
type bccMemoryRepository struct {
	Repository
	mu           sync.Mutex
	orders       map[uuid.UUID]Order
	grants       int
	bankSequence int64
}

func (r *bccMemoryRepository) Plan(context.Context, string) (Plan, error) {
	return Plan{Code: "self", Active: true, Price: 4990, ListPrice: 9990, AccessDays: 30}, nil
}
func (r *bccMemoryRepository) Snapshot(context.Context, uuid.UUID) (Access, error) {
	return Access{}, nil
}
func (r *bccMemoryRepository) CreateOrder(_ context.Context, o Order) (Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o.CreatedAt = time.Now().UTC()
	o.Currency = "KZT"
	o.Status = "PENDING"
	r.orders[o.ID] = o
	return o, nil
}
func (r *bccMemoryRepository) SetProviderOrder(_ context.Context, id uuid.UUID, bankID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o := r.orders[id]
	o.ProviderOrderID = &bankID
	r.orders[id] = o
	return nil
}
func (r *bccMemoryRepository) Order(_ context.Context, id uuid.UUID) (Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return o, ErrNotFound
	}
	return o, nil
}
func (r *bccMemoryRepository) OrderByProvider(_ context.Context, provider, id string) (Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.orders {
		if o.Provider == provider && o.ProviderOrderID != nil && *o.ProviderOrderID == id {
			return o, nil
		}
	}
	return Order{}, ErrNotFound
}
func (r *bccMemoryRepository) ReserveBCCOrder(_ context.Context, id uuid.UUID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return "", ErrNotFound
	}
	if o.ProviderOrderID != nil {
		return *o.ProviderOrderID, nil
	}
	r.bankSequence++
	bankID := fmt.Sprintf("%016d", 1000000000000000+r.bankSequence)
	o.ProviderOrderID = &bankID
	r.orders[id] = o
	return bankID, nil
}
func (r *bccMemoryRepository) Notify(_ context.Context, provider string, n Notification) (Order, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o := r.orders[n.OrderID]
	if o.Provider != provider || o.ProviderOrderID == nil || *o.ProviderOrderID != n.ProviderOrderID || o.Amount != n.Amount || o.Currency != n.Currency {
		return o, false, ErrInvalidCallback
	}
	apply, err := notifyTransition(o.Status, n.Status)
	if err != nil || !apply {
		return o, false, err
	}
	o.Status = n.Status
	r.orders[o.ID] = o
	paid := n.Status == "PAID"
	if paid {
		r.grants++
	}
	return o, paid, nil
}
func bccFixture(t *testing.T) (*BCCProvider, *bccMemoryRepository, *Service, http.Handler) {
	t.Helper()
	repo := &bccMemoryRepository{orders: map[uuid.UUID]Order{}}
	p, err := NewBCCProvider(testBCCConfig(), repo, "https://app.example/app/billing/return?keep=yes", "development")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, true, p, p.returnURL, nil, nil)
	return p, repo, s, bccTestRouter(s, uuid.New())
}
func bccTestRouter(s *Service, user uuid.UUID) http.Handler {
	h := NewHandler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 65536)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		h.RegisterBCCRoutes(r)
		r.Post("/billing/callbacks/{provider}", h.Callback)
		r.With(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user, "STUDENT")))
			})
		}).Post("/billing/checkout", h.Checkout)
		r.With(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user, "STUDENT")))
			})
		}).Get("/billing/orders/{orderID}", h.Order)
	})
	return r
}
func TestBCCGoldenMAC(t *testing.T) {
	key, _ := hex.DecodeString("6BB0AC02E47BDF73D98FEB777F3B5294")
	got := bccMAC(key, "350.00", "398", "3558714461568", "merchantname", "88888881", "0", "20200224073921", "1", "F2B2DD7E603A7AAF5E1BC35DEE1F6C9A")
	if got != "9B1C58714CFF6E4BCC6E97B4D503275838F4ED68" {
		t.Fatal(got)
	}
}
func TestBCCAmountsNoFloat(t *testing.T) {
	for _, n := range []int64{1, 350, 4990, 9007199254740993, math.MaxInt64} {
		s, err := bccAmount(n)
		if err != nil {
			t.Fatal(err)
		}
		got, err := bccParseAmount(s)
		if err != nil || got != n {
			t.Fatalf("%d -> %s -> %d %v", n, s, got, err)
		}
	}
	for _, s := range []string{"350", "350.0", "350.00", "000350.00"} {
		if got, err := bccParseAmount(s); err != nil || got != 350 {
			t.Fatalf("%s: %d %v", s, got, err)
		}
	}
	for _, s := range []string{"", "0", "-1", "+350", "350.01", "350.000", "350.", "350,00", "3e2", "NaN", "9223372036854775808.00", " 350.00", "350\x00"} {
		if _, err := bccParseAmount(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, n := range []int64{0, -1} {
		if _, err := bccAmount(n); err == nil {
			t.Fatal("nonpositive amount")
		}
	}
}
func validBCCInfo() bccInfo {
	i := bccInfo{Height: "1080", Width: "1920", Address: "Алматы, Абая 88"}
	i.Phone.CC = "7"
	i.Phone.Subscriber = "7475558888"
	return i
}
func TestBCCRequiredFieldsUTF8AndValidation(t *testing.T) {
	p, repo, s, _ := bccFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	info := validBCCInfo()
	info.Address = `Алматы, <script>alert('x')</script>`
	f, err := p.fields(o, info, "203.0.113.11")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"AMOUNT", "CURRENCY", "ORDER", "MERCH_RN_ID", "DESC", "MERCHANT", "MERCH_NAME", "TERMINAL", "TIMESTAMP", "MERCH_GMT", "TRTYPE", "BACKREF", "LANG", "NONCE", "P_SIGN", "NOTIFY_URL", "CLIENT_IP", "M_INFO"} {
		if f[key] == "" {
			t.Fatalf("missing %s", key)
		}
	}
	if f["AMOUNT"] != "4990.00" || f["CURRENCY"] != "398" || f["TRTYPE"] != "1" || f["MERCH_GMT"] != "0" || f["ORDER"] == o.ID.String() || f["ORDER"] != f["MERCH_RN_ID"] || len(f["MERCH_RN_ID"]) != 16 || f["MK_TOKEN"] != "" {
		t.Fatalf("wrong purchase fields: %+v", f)
	}
	if _, err := time.Parse("20060102150405", f["TIMESTAMP"]); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(f["M_INFO"])
	if err != nil {
		t.Fatal(err)
	}
	var got bccInfo
	if err := json.Unmarshal(decoded, &got); err != nil || got != info {
		t.Fatalf("UTF8: %+v %v", got, err)
	}
	f2, err := p.fields(o, info, "203.0.113.11")
	if err != nil || f2["NONCE"] == f["NONCE"] || f2["ORDER"] != f["ORDER"] {
		t.Fatal("nonce/order not stable")
	}
	for _, mutate := range []func(*bccInfo){func(i *bccInfo) { i.Height = "0" }, func(i *bccInfo) { i.Width = "1000000" }, func(i *bccInfo) { i.Height = "1.5" }, func(i *bccInfo) { i.Phone.CC = "" }, func(i *bccInfo) { i.Phone.CC = "000" }, func(i *bccInfo) { i.Phone.Subscriber = "+777" }, func(i *bccInfo) { i.Phone.Subscriber = strings.Repeat("7", 15) }, func(i *bccInfo) { i.Address = "" }, func(i *bccInfo) { i.Address = strings.Repeat("я", 51) }, func(i *bccInfo) { i.Address = "x\ny" }, func(i *bccInfo) { i.Address = string([]byte{0xff}) }} {
		i := validBCCInfo()
		mutate(&i)
		if i.validate() == nil {
			t.Fatalf("accepted %+v", i)
		}
	}
	info.Address = strings.Repeat("я", 50)
	if info.validate() != nil {
		t.Fatal("50 unicode characters rejected")
	}
	for _, ip := range []string{"", "garbage", "0.0.0.0", "::", "224.0.0.1"} {
		if _, err := p.fields(o, info, ip); err == nil {
			t.Fatalf("accepted IP %q", ip)
		}
	}
}
func TestBCCBridgeCapabilityBinding(t *testing.T) {
	p, repo, s, _ := bccFixture(t)
	o, redirect, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	token := u.Query().Get("token")
	o, _ = repo.Order(context.Background(), o.ID)
	if _, err := p.bridgeOrder(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{o.ID.String(), token + "x", strings.Replace(token, o.ID.String(), uuid.NewString(), 1), strings.Replace(token, ".", ".0", 1)} {
		if _, err := p.bridgeOrder(context.Background(), bad); err == nil {
			t.Fatal("forgery accepted")
		}
	}
	old := o
	for _, mutate := range []func(*Order){func(o *Order) { o.UserID = uuid.New() }, func(o *Order) { o.Amount++ }, func(o *Order) { v := "1234567890123456"; o.ProviderOrderID = &v }, func(o *Order) { o.Provider = "fake" }, func(o *Order) { o.Status = "PAID" }, func(o *Order) { o.ProviderOrderID = nil }} {
		v := old
		mutate(&v)
		repo.orders[v.ID] = v
		if _, err := p.bridgeOrder(context.Background(), token); err == nil {
			t.Fatal("ownership/state change accepted")
		}
	}
	repo.orders[old.ID] = old
	p.now = func() time.Time { return old.CreatedAt.Add(p.cfg.BridgeTTL) }
	if _, err := p.bridgeOrder(context.Background(), token); err == nil {
		t.Fatal("expired bridge")
	}
}
func callbackValues(o Order) url.Values {
	return url.Values{"ORDER": {*o.ProviderOrderID}, "MERCH_RN_ID": {*o.ProviderOrderID}, "MERCHANT": {"00000001"}, "TERMINAL": {"88888881"}, "AMOUNT": {strconv.FormatInt(o.Amount, 10) + ".00"}, "CURRENCY": {"398"}, "TRTYPE": {"1"}, "ACTION": {"0"}, "RC": {"00"}}
}
func callbackRequest(v url.Values) *http.Request {
	r := httptest.NewRequest("POST", "/api/v1/billing/callbacks/bcc", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("bank-notify", "test-only-long-password")
	return r
}

type bccLookupFailureRepository struct {
	*bccMemoryRepository
	lookupErr error
}

func (r bccLookupFailureRepository) OrderByProvider(context.Context, string, string) (Order, error) {
	return Order{}, r.lookupErr
}

func TestBCCNotificationDatabaseFailureRemainsRetryable(t *testing.T) {
	p, repo, s, router := bccFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"unknown bank order", ErrNotFound, http.StatusBadRequest},
		{"temporary database outage", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.repo = bccLookupFailureRepository{repo, tc.err}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, callbackRequest(callbackValues(o)))
			if w.Code != tc.code {
				t.Fatalf("status=%d, want=%d: %s", w.Code, tc.code, w.Body)
			}
			if repo.grants != 0 {
				t.Fatal("failed lookup granted access")
			}
		})
	}
}

func TestBCCNotificationRejectsForgedAndAmbiguousResults(t *testing.T) {
	p, repo, s, _ := bccFixture(t)
	o, _, _ := s.Checkout(context.Background(), uuid.New(), "self", true)
	o, _ = repo.Order(context.Background(), o.ID)
	n, err := p.ParseCallback(callbackRequest(callbackValues(o)))
	if err != nil || n.OrderID != o.ID || n.Status != "PAID" {
		t.Fatalf("valid: %+v %v", n, err)
	}
	for _, tc := range []struct{ key, value string }{{"ORDER", uuid.NewString()}, {"ORDER", "1234567890123456"}, {"MERCH_RN_ID", "1111111111111111"}, {"MERCHANT", "wrong"}, {"TERMINAL", "88888888"}, {"AMOUNT", "4990.01"}, {"AMOUNT", "4991.00"}, {"CURRENCY", "KZT"}, {"CURRENCY", "840"}, {"TRTYPE", "14"}, {"TRTYPE", "90"}, {"ACTION", ""}, {"ACTION", "123"}, {"ACTION", "x"}, {"RC", "0"}, {"RC", ""}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			v := callbackValues(o)
			v.Set(tc.key, tc.value)
			v.Set("P_SIGN", "copied-original-request-signature")
			if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
				t.Fatal("invalid callback accepted")
			}
		})
	}
	for key := range callbackValues(o) {
		v := callbackValues(o)
		v.Del(key)
		if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
			t.Fatalf("missing %s accepted", key)
		}
		v = callbackValues(o)
		v.Add(key, v.Get(key))
		if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
			t.Fatalf("duplicate %s accepted", key)
		}
	}
	v := callbackValues(o)
	v["unknown"] = []string{"a", "b"}
	if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
		t.Fatal("duplicate unknown field accepted")
	}
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.SetBasicAuth("", "") }, func(r *http.Request) { r.SetBasicAuth("bank-notify", "wrong") }, func(r *http.Request) { r.SetBasicAuth("wrong", "test-only-long-password") }, func(r *http.Request) { r.Header.Add("Authorization", r.Header.Get("Authorization")) }, func(r *http.Request) { r.Method = "GET" }, func(r *http.Request) { r.URL.RawQuery = "ACTION=0" }, func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("x=" + strings.Repeat("a", 32768))) }, func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("AMOUNT=%zz")) }} {
		r := callbackRequest(callbackValues(o))
		mutate(r)
		if _, err := p.ParseCallback(r); err == nil {
			t.Fatal("bad HTTP callback accepted")
		}
	}
	// Authenticated non-approvals are definitive results, not forgeries.
	for _, tc := range []struct{ action, rc string }{{"1", "00"}, {"2", "05"}, {"4", "00"}, {"0", "10"}, {"0", "05"}} {
		v := callbackValues(o)
		v.Set("ACTION", tc.action)
		v.Set("RC", tc.rc)
		if n, err := p.ParseCallback(callbackRequest(v)); err != nil || n.Status != "FAILED" {
			t.Fatalf("%s/%s: %+v %v", tc.action, tc.rc, n, err)
		}
	}
	if repo.grants != 0 {
		t.Fatal("parser settled an order")
	}
}
func TestBCCClientIPTrustBoundary(t *testing.T) {
	p, repo, _, _ := bccFixture(t)
	c := testBCCConfig()
	c.TrustedProxyCIDRs = []string{"10.10.0.0/24"}
	p, err := NewBCCProvider(c, repo, p.returnURL, "development")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ peer, xff, want string }{{"203.0.113.1:99", "1.2.3.4", "203.0.113.1"}, {"192.168.1.1:99", "1.2.3.4", "192.168.1.1"}, {"10.10.0.2:99", "1.2.3.4, 203.0.113.7", "203.0.113.7"}, {"10.10.0.2:99", "1.2.3.4, 10.10.0.3", "1.2.3.4"}, {"10.10.0.2:99", "bad", ""}, {"10.10.0.2:99", "", "10.10.0.2"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := p.clientIP(r); got != tc.want {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
}
func TestBCCPurchaseHTTPContract(t *testing.T) {
	p, repo, _, router := bccFixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/billing/checkout", strings.NewReader(`{"planCode":"self","acceptedTerms":true}`))
	r.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, r)
	var result struct {
		OrderID  uuid.UUID `json:"orderId"`
		Redirect string    `json:"redirectUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 201 {
		t.Fatalf("checkout: %d %s %v", w.Code, w.Body, err)
	}
	o, err := repo.Order(context.Background(), result.OrderID)
	if err != nil || o.ProviderOrderID == nil || !bccBankOrder.MatchString(*o.ProviderOrderID) {
		t.Fatal("bank mapping not saved before redirect")
	}
	if strings.Contains(result.Redirect, p.cfg.MACKey) || strings.Contains(result.Redirect, p.cfg.NotifyPassword) {
		t.Fatal("credentials in URL")
	}
	u, _ := url.Parse(result.Redirect)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "window.screen.height") || !strings.Contains(w.Body.String(), "window.screen.width") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("prepare: %d %s", w.Code, w.Body)
	}
	form := url.Values{"token": {u.Query().Get("token")}, "height": {"1080"}, "width": {"1920"}, "cc": {"7"}, "subscriber": {"7475558888"}, "address": {`Алматы <script>alert(1)</script>`}}
	post := func(v url.Values, origin string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", u.Path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		router.ServeHTTP(w, r)
		return w
	}
	if post(form, "https://evil.example").Code != 400 {
		t.Fatal("cross-origin bridge accepted")
	}
	form.Add("AMOUNT", "1.00")
	if post(form, p.cfg.PublicBaseURL).Code != 400 {
		t.Fatal("extra bank fields accepted")
	}
	form.Del("AMOUNT")
	w = post(form, p.cfg.PublicBaseURL)
	html := w.Body.String()
	if w.Code != 200 || !strings.Contains(html, `method="post" action="`+BCCTestGateway+`"`) || strings.Contains(html, "<script>alert") || strings.Contains(html, p.cfg.MACKey) || strings.Contains(html, p.cfg.NotifyPassword) || strings.Contains(html, "MK_TOKEN") {
		t.Fatalf("bank form: %d %s", w.Code, html)
	}
	if !regexp.MustCompile(`name="P_SIGN" value="[A-F0-9]{40}"`).MatchString(html) || !strings.Contains(html, `name="ORDER" value="`+*o.ProviderOrderID+`"`) {
		t.Fatal("unsigned/wrong order bank form")
	}
	// Browser can claim success, but only redirects and does not settle anything.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/billing/bcc/return/"+o.ID.String(), strings.NewReader("ACTION=0&RC=00&P_SIGN=forged")))
	if w.Code != 303 || repo.grants != 0 {
		t.Fatal("forged BACKREF settled order")
	}
	target, _ := url.Parse(w.Header().Get("Location"))
	if target.Host != "app.example" || target.Query().Get("orderId") != o.ID.String() || target.Query().Get("keep") != "yes" {
		t.Fatal("unsafe return target")
	}
	// Unauthenticated even with an outgoing signature is not a bank notification.
	r = callbackRequest(callbackValues(o))
	r.Header.Del("Authorization")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 400 || repo.grants != 0 {
		t.Fatal("unsigned callback settled")
	}
	for range 2 {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, callbackRequest(callbackValues(o)))
		if w.Code != 200 {
			t.Fatalf("notify: %d %s", w.Code, w.Body)
		}
	}
	if repo.grants != 1 {
		t.Fatalf("entitlements=%d", repo.grants)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	if w.Code != 400 {
		t.Fatal("settled bridge reopened")
	}
	// Existing authenticated Order endpoint still enforces ownership.
	router = bccTestRouter(NewService(repo, true, p, p.returnURL, nil, nil), uuid.New())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/billing/orders/"+o.ID.String(), nil))
	if w.Code != 404 {
		t.Fatal("another user's order exposed")
	}
}
func TestBCCHTTPBridgeAndCallbackLimits(t *testing.T) {
	p, repo, s, router := bccFixture(t)
	o, redirect, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	u, _ := url.Parse(redirect)
	for _, path := range []string{u.Path, u.RequestURI() + "x", u.RequestURI() + "&token=duplicate", u.Path + "?token=" + o.ID.String()} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("bridge forgery accepted: %s %d", path, w.Code)
		}
	}
	// The provider's independent 32 KiB cap holds even with a larger app limit.
	v := callbackValues(o)
	v.Set("padding", strings.Repeat("x", 32768))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, callbackRequest(v))
	if w.Code != 400 || repo.grants != 0 {
		t.Fatal("oversized notification accepted")
	}
	// The handler's configured smaller MaxBytesReader is also honored.
	h := NewHandler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 64)
	r := chi.NewRouter()
	r.Post("/api/v1/billing/callbacks/{provider}", h.Callback)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, callbackRequest(callbackValues(o)))
	if w.Code != 400 || repo.grants != 0 {
		t.Fatal("app body cap bypassed")
	}
	p.now = func() time.Time { return o.CreatedAt.Add(p.cfg.BridgeTTL) }
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	if w.Code != 400 {
		t.Fatal("expired capability accepted over HTTP")
	}
}

func TestBCCCanonicalPublicOrigin(t *testing.T) {
	_, repo, _, _ := bccFixture(t)
	c := testBCCConfig()
	c.PublicBaseURL = "https://PAY.EXAMPLE:443/"
	p, err := NewBCCProvider(c, repo, "https://app.example/return", "test")
	if err != nil || p.cfg.PublicBaseURL != "https://pay.example" {
		t.Fatalf("noncanonical browser origin: %v", err)
	}
}

func TestBCCRepositoryFailureDoesNotExposeRedirect(t *testing.T) {
	p, repo, _, _ := bccFixture(t)
	f := &bccFailMappingRepository{bccMemoryRepository: repo}
	s := NewService(f, true, p, p.returnURL, nil, nil)
	_, redirect, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err == nil || redirect != "" {
		t.Fatal("mapping failure exposed capability")
	}
}

type bccFailMappingRepository struct{ *bccMemoryRepository }

func (*bccFailMappingRepository) SetProviderOrder(context.Context, uuid.UUID, string) error {
	return errors.New("mapping failed")
}
