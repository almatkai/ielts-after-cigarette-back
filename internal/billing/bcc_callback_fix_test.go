package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// bccCallbackRecordingRepository mirrors the Postgres status transitions and
// records every parsed notification, so the callback contract can be asserted
// without a database.
type bccCallbackRecordingRepository struct {
	*bccMemoryRepository
	notified []Notification
}

func (r *bccCallbackRecordingRepository) Notify(_ context.Context, provider string, n Notification) (Order, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notified = append(r.notified, n)
	o, ok := r.orders[n.OrderID]
	if !ok || o.Provider != provider || o.ProviderOrderID == nil || *o.ProviderOrderID != n.ProviderOrderID || o.Amount != n.Amount || o.Currency != n.Currency {
		return o, false, ErrInvalidCallback
	}
	apply, err := notifyTransition(o.Status, n.Status)
	if err != nil || !apply {
		return o, false, err
	}
	o.Status = n.Status
	paid := n.Status == "PAID"
	if paid {
		r.grants++
	}
	r.orders[o.ID] = o
	return o, paid, nil
}

func bccCallbackFixFixture(t *testing.T) (*BCCProvider, *bccCallbackRecordingRepository, *Service, http.Handler) {
	t.Helper()
	repo := &bccCallbackRecordingRepository{bccMemoryRepository: &bccMemoryRepository{orders: map[uuid.UUID]Order{}}}
	p, err := NewBCCProvider(testBCCConfig(), repo, "https://app.example/app/billing/return?keep=yes", "development")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, true, p, p.returnURL, nil, nil)
	return p, repo, s, bccTestRouter(s, uuid.New())
}

func TestBCCDeclineCallbackIsRecordedAsFailed(t *testing.T) {
	p, repo, s, router := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	v := callbackValues(o)
	v.Set("ACTION", "2")
	v.Set("RC", "05")

	n, err := p.ParseCallback(callbackRequest(v))
	if err != nil {
		t.Fatalf("authenticated decline rejected: %v", err)
	}
	if n.Status != "FAILED" || n.Payload["ACTION"] != "2" || n.Payload["RC"] != "05" || n.Payload["TRTYPE"] != "1" {
		t.Fatalf("decline parsed as %+v", n)
	}

	// Service level: the decline must be acknowledged, not retried as invalid.
	if err := s.Callback(callbackRequest(v), "bcc"); err != nil {
		t.Fatalf("decline callback failed: %v", err)
	}
	stored, _ := repo.Order(context.Background(), o.ID)
	if stored.Status != "FAILED" || repo.grants != 0 {
		t.Fatalf("status=%s grants=%d", stored.Status, repo.grants)
	}
	if len(repo.notified) != 1 || repo.notified[0].Status != "FAILED" {
		t.Fatalf("notification not forwarded as FAILED: %+v", repo.notified)
	}

	// HTTP level: the bank must receive 200 so it stops retrying the decline.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, callbackRequest(v))
	if w.Code != http.StatusOK {
		t.Fatalf("decline status=%d: %s", w.Code, w.Body)
	}
}

func TestBCCCallbackRejectsMalformedResultFields(t *testing.T) {
	p, repo, s, _ := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	for _, tc := range []struct{ key, value string }{
		{"ACTION", ""},
		{"ACTION", "x"},
		{"ACTION", "1x"},
		{"ACTION", "123"},
		{"ACTION", "-1"},
		{"ACTION", " 2"},
		{"RC", ""},
		{"RC", "0"},
		{"RC", "000"},
		{"RC", "0$"},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			v := callbackValues(o)
			v.Set(tc.key, tc.value)
			if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
				t.Fatalf("malformed %s=%q accepted", tc.key, tc.value)
			}
		})
	}
	// A well-formed non-approval ACTION is a definitive result, not malformed.
	for _, action := range []string{"0", "1", "2", "4", "99"} {
		v := callbackValues(o)
		v.Set("ACTION", action)
		v.Set("RC", "05")
		if _, err := p.ParseCallback(callbackRequest(v)); err != nil {
			t.Fatalf("well-formed ACTION=%q rejected: %v", action, err)
		}
	}
}

func TestBCCDeclineCallbackRequiresAuthenticationAndMatchingAmount(t *testing.T) {
	p, repo, s, _ := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)

	decline := func() *http.Request {
		v := callbackValues(o)
		v.Set("ACTION", "2")
		v.Set("RC", "05")
		return callbackRequest(v)
	}
	if _, err := p.ParseCallback(decline()); err != nil {
		t.Fatalf("authenticated decline rejected: %v", err)
	}
	r := decline()
	r.Header.Del("Authorization")
	if _, err := p.ParseCallback(r); err == nil {
		t.Fatal("unauthenticated decline accepted")
	}
	r = decline()
	r.SetBasicAuth("bank-notify", "wrong")
	if _, err := p.ParseCallback(r); err == nil {
		t.Fatal("decline with wrong password accepted")
	}

	v := callbackValues(o)
	v.Set("ACTION", "2")
	v.Set("RC", "05")
	v.Set("AMOUNT", "4991.00")
	if _, err := p.ParseCallback(callbackRequest(v)); err == nil {
		t.Fatal("decline with mismatched amount accepted")
	}
	if len(repo.notified) != 0 || repo.grants != 0 {
		t.Fatal("rejected callback settled the order")
	}
}

func bccCallbackFixProxyProvider(t *testing.T, cidrs ...string) *BCCProvider {
	t.Helper()
	_, repo, _, _ := bccFixture(t)
	c := testBCCConfig()
	c.TrustedProxyCIDRs = cidrs
	p, err := NewBCCProvider(c, repo, "https://app.example/return", "development")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBCCClientIPJoinsAllForwardedForLines(t *testing.T) {
	p := bccCallbackFixProxyProvider(t, "10.10.0.0/24")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.10.0.2:443"
	// The client forges its own XFF line; the trusted edge proxy appends the
	// real peer as a separate header line. Only the rightmost hop is trusted.
	r.Header.Add("X-Forwarded-For", "1.2.3.4")
	r.Header.Add("X-Forwarded-For", "203.0.113.7")
	if got := p.clientIP(r); got != "203.0.113.7" {
		t.Fatalf("spoofed first line won: %q", got)
	}

	// A forged line that points at another trusted proxy must not be chosen
	// when a proxy-appended real client follows it either.
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.10.0.2:443"
	r.Header.Add("X-Forwarded-For", "10.10.0.9")
	r.Header.Add("X-Forwarded-For", "203.0.113.8")
	if got := p.clientIP(r); got != "203.0.113.8" {
		t.Fatalf("trusted spoofed line won: %q", got)
	}

	// Empty entries from a trailing comma are skipped, not fatal.
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.10.0.2:443"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9,")
	if got := p.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("trailing comma broke resolution: %q", got)
	}
}

func TestBCCClientIPTrustedProxyWithoutForwardedForUsesPeer(t *testing.T) {
	p := bccCallbackFixProxyProvider(t, "10.10.0.0/24")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.10.0.2:443"
	if got := p.clientIP(r); got != "10.10.0.2" {
		t.Fatalf("missing XFF: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "")
	if got := p.clientIP(r); got != "10.10.0.2" {
		t.Fatalf("empty XFF: %q", got)
	}
	// Genuinely malformed entries still fail closed.
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := p.clientIP(r); got != "" {
		t.Fatalf("malformed hop: %q", got)
	}
}

func TestBCCBridgeOrderToleratesClockSkew(t *testing.T) {
	p, repo, s, _ := bccFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	now := time.Now()
	p.now = func() time.Time { return now }
	for _, tc := range []struct {
		name   string
		offset time.Duration
		ok     bool
	}{
		{"30s future", 30 * time.Second, true},
		{"at skew boundary", bccClockSkew, true},
		{"10m future", 10 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := o
			v.CreatedAt = now.Add(tc.offset)
			repo.orders[v.ID] = v
			token := p.capability(v, *v.ProviderOrderID)
			_, err := p.bridgeOrder(context.Background(), token)
			if tc.ok && err != nil {
				t.Fatalf("skewed order rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("future-dated order accepted")
			}
		})
	}
}

func TestNotifyTransitionTable(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		apply    bool
		invalid  bool
	}{
		{"PENDING", "PAID", true, false},
		{"PENDING", "FAILED", true, false},
		{"PENDING", "CANCELLED", true, false},
		{"PENDING", "PENDING", false, false},
		{"PENDING", "REFUNDED", false, true},
		{"FAILED", "PAID", true, false},
		{"CANCELLED", "PAID", true, false},
		{"FAILED", "FAILED", false, false},
		{"FAILED", "CANCELLED", false, false},
		{"FAILED", "REFUNDED", false, true},
		{"PAID", "PAID", false, false},
		{"PAID", "FAILED", false, false},
		{"PAID", "CANCELLED", false, false},
		{"PAID", "REFUNDED", true, false},
		{"REFUNDED", "PAID", false, true},
		{"REFUNDED", "FAILED", false, true},
	} {
		apply, err := notifyTransition(tc.from, tc.to)
		if apply != tc.apply || (err != nil) != tc.invalid {
			t.Fatalf("%s->%s: apply=%v err=%v", tc.from, tc.to, apply, err)
		}
	}
}

func TestBCCApprovalAfterDeclineGrantsAndLateDeclineKeepsPaid(t *testing.T) {
	_, repo, s, router := bccCallbackFixFixture(t)
	o, _, err := s.Checkout(context.Background(), uuid.New(), "self", true)
	if err != nil {
		t.Fatal(err)
	}
	o, _ = repo.Order(context.Background(), o.ID)
	post := func(action, rc string) int {
		v := callbackValues(o)
		v.Set("ACTION", action)
		v.Set("RC", rc)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, callbackRequest(v))
		return w.Code
	}
	if code := post("2", "05"); code != http.StatusOK {
		t.Fatalf("decline: %d", code)
	}
	if code := post("0", "00"); code != http.StatusOK || repo.grants != 1 {
		t.Fatalf("approval after decline: %d grants=%d", code, repo.grants)
	}
	if code := post("2", "05"); code != http.StatusOK || repo.grants != 1 {
		t.Fatalf("late decline: %d grants=%d", code, repo.grants)
	}
	if got, _ := repo.Order(context.Background(), o.ID); got.Status != "PAID" {
		t.Fatalf("late decline changed status to %s", got.Status)
	}
}
