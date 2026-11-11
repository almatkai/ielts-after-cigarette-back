package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type bccErrRepoWrapper struct {
	*bccMemoryRepository
	orderErr error
}

func (r *bccErrRepoWrapper) Order(ctx context.Context, id uuid.UUID) (Order, error) {
	if r.orderErr != nil {
		return Order{}, r.orderErr
	}
	return r.bccMemoryRepository.Order(ctx, id)
}

func TestBCCBridgeFixes(t *testing.T) {
	memRepo := &bccMemoryRepository{orders: map[uuid.UUID]Order{}}
	repo := &bccErrRepoWrapper{bccMemoryRepository: memRepo}
	p, err := NewBCCProvider(testBCCConfig(), repo, "https://app.example/app/billing/return?keep=yes", "development")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, true, p, p.returnURL, nil, nil)
	userID := uuid.New()
	router := bccTestRouter(s, userID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/billing/checkout", strings.NewReader(`{"planCode":"self","acceptedTerms":true}`))
	r.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, r)
	var checkoutResult struct {
		OrderID  uuid.UUID `json:"orderId"`
		Redirect string    `json:"redirectUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checkoutResult); err != nil || w.Code != 201 {
		t.Fatalf("checkout: %d %s %v", w.Code, w.Body, err)
	}

	u, err := url.Parse(checkoutResult.Redirect)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")

	t.Run("GET page has Referrer-Policy same-origin", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", u.RequestURI(), nil)
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("expected Referrer-Policy same-origin, got %q", got)
		}
	})

	validForm := func() url.Values {
		return url.Values{
			"token":      {token},
			"height":     {"1080"},
			"width":      {"1920"},
			"cc":         {"7"},
			"subscriber": {"7475558888"},
			"address":    {"Almaty"},
		}
	}

	t.Run("POST with Origin null and Sec-Fetch-Site same-origin is accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		html := rec.Body.String()
		if !strings.Contains(html, `action="`+p.cfg.Gateway+`"`) || !strings.Contains(html, "document.getElementById('bank').submit()") {
			t.Fatalf("expected auto-submit page to gateway, got: %s", html)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("expected Referrer-Policy same-origin on submit page, got %q", got)
		}
	})

	t.Run("POST with public origin and no Sec-Fetch-Site is accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", p.cfg.PublicBaseURL)
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST with public origin and Sec-Fetch-Site same-origin is accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", p.cfg.PublicBaseURL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST Sec-Fetch-Site cross-site returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", p.cfg.PublicBaseURL)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST Sec-Fetch-Site same-site returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", p.cfg.PublicBaseURL)
		req.Header.Set("Sec-Fetch-Site", "same-site")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST Sec-Fetch-Site none returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", p.cfg.PublicBaseURL)
		req.Header.Set("Sec-Fetch-Site", "none")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST with another site Origin and no Sec-Fetch-Site returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://evil.example")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST with neither header returns 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("bridge with a non-validation repo error returns 5xx", func(t *testing.T) {
		repo.orderErr = errors.New("database connection refused")
		defer func() { repo.orderErr = nil }()

		recGET := httptest.NewRecorder()
		reqGET := httptest.NewRequest("GET", u.RequestURI(), nil)
		router.ServeHTTP(recGET, reqGET)
		if recGET.Code < 500 || recGET.Code > 599 {
			t.Fatalf("expected 5xx on GET bridge error, got %d: %s", recGET.Code, recGET.Body.String())
		}

		recPOST := httptest.NewRecorder()
		reqPOST := httptest.NewRequest("POST", u.Path, strings.NewReader(validForm().Encode()))
		reqPOST.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqPOST.Header.Set("Sec-Fetch-Site", "same-origin")
		router.ServeHTTP(recPOST, reqPOST)
		if recPOST.Code < 500 || recPOST.Code > 599 {
			t.Fatalf("expected 5xx on POST bridge error, got %d: %s", recPOST.Code, recPOST.Body.String())
		}
	})

	t.Run("BCCBackref with non-NotFound repo error returns 303 with orderId", func(t *testing.T) {
		repo.orderErr = errors.New("db pool exhausted")
		defer func() { repo.orderErr = nil }()

		backrefID := uuid.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/billing/bcc/return/"+backrefID.String(), nil)
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected 303 See Other, got %d: %s", rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		target, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("invalid redirect location %q: %v", loc, err)
		}
		if got := target.Query().Get("orderId"); got != backrefID.String() {
			t.Fatalf("expected orderId=%s in redirect query, got %q (location: %s)", backrefID.String(), got, loc)
		}
		if got := target.Query().Get("keep"); got != "yes" {
			t.Fatalf("expected keep=yes preserved in query, got %q", got)
		}

		recPost := httptest.NewRecorder()
		reqPost := httptest.NewRequest("POST", "/api/v1/billing/bcc/return/"+backrefID.String(), strings.NewReader("action=0"))
		reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(recPost, reqPost)

		if recPost.Code != http.StatusSeeOther {
			t.Fatalf("expected 303 See Other on POST backref, got %d: %s", recPost.Code, recPost.Body.String())
		}
		locPost := recPost.Header().Get("Location")
		targetPost, err := url.Parse(locPost)
		if err != nil {
			t.Fatalf("invalid redirect location %q: %v", locPost, err)
		}
		if got := targetPost.Query().Get("orderId"); got != backrefID.String() {
			t.Fatalf("expected orderId=%s in POST redirect query, got %q", backrefID.String(), got)
		}
	})

	t.Run("BCCBackref with NotFound returns 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/billing/bcc/return/"+uuid.New().String(), nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
