package guest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakeRepo struct {
	item    Trial
	creates int
	media   bool
	err     error
}

func (r *fakeRepo) Find(_ context.Context, hash []byte) (Trial, error) {
	if r.err != nil {
		return Trial{}, r.err
	}
	if r.item.UserID == uuid.Nil {
		return Trial{}, ErrNotFound
	}
	return r.item, nil
}
func (r *fakeRepo) Create(_ context.Context, _ []byte, _ string, expires time.Time) (Trial, error) {
	r.creates++
	r.item = Trial{UserID: uuid.New(), ExpiresAt: expires}
	return r.item, nil
}
func (r *fakeRepo) OwnsMedia(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID) (bool, error) {
	return r.media, nil
}

func (r *fakeRepo) Claim(_ context.Context, _ []byte, _ uuid.UUID) (*uuid.UUID, error) {
	return r.item.SessionID, r.err
}

type fakeMock struct {
	starts, gets, retakes int
	session               fullmock.Session
	retakeErr             error
}

func (m *fakeMock) StartGenerated(_ context.Context, user uuid.UUID, restart bool) (fullmock.Session, bool, error) {
	m.starts++
	m.session.UserID = user
	return m.session, true, nil
}
func (m *fakeMock) GetSession(_ context.Context, user, id uuid.UUID) (fullmock.Session, error) {
	m.gets++
	return m.session, nil
}
func (m *fakeMock) RetakeGuest(ctx context.Context, user, source uuid.UUID, admit func(context.Context) error) (fullmock.Session, bool, error) {
	if m.retakeErr != nil {
		return fullmock.Session{}, false, m.retakeErr
	}
	if err := admit(ctx); err != nil {
		return fullmock.Session{}, false, err
	}
	m.retakes++
	m.session.UserID = user
	return m.session, true, nil
}

func testHandler() *Handler {
	return NewHandler(Config{Enabled: true, Secret: "test-secret", Origins: []string{"https://app.test"}, IPLimit: 5, GlobalLimit: 100, SameSite: http.SameSiteLaxMode}, &fakeRepo{}, &fakeMock{session: fullmock.Session{ID: uuid.New()}}, func(context.Context, string, int64, time.Duration) (bool, error) { return true, nil }, func(*http.Request) string { return "127.0.0.1" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestUnavailableExamTypeDoesNotConsumeTrial(t *testing.T) {
	h := testHandler()
	h.cfg.ExamTypes = []string{"academic"}
	limits := 0
	h.allow = func(context.Context, string, int64, time.Duration) (bool, error) {
		limits++
		return true, nil
	}
	r := httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(`{"examType":"general","acceptedTerms":true}`))
	r.Header.Set("Origin", "https://app.test")
	w := httptest.NewRecorder()
	h.Start(w, r)
	if w.Code != 422 || limits != 0 || h.repo.(*fakeRepo).creates != 0 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("unavailable exam consumed a trial: status=%d limits=%d creates=%d", w.Code, limits, h.repo.(*fakeRepo).creates)
	}
}
func cookieRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("a", 43)})
	r.Header.Set("Origin", "https://app.test")
	return r
}
func TestGuestCapabilityBoundary(t *testing.T) {
	h := testHandler()
	h.repo.(*fakeRepo).item = Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	tokens := auth.NewTokenManager("test-secret", "issuer", "audience", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) {
		api.Group(func(protected chi.Router) {
			protected.Use(h.Authenticate(tokens))
			for _, path := range []string{"/full-mock-sessions/{sessionID}", "/attempts/{attemptID}", "/attempts/{attemptID}/status", "/profile", "/attempts", "/admin/analytics/overview", "/listening/tests"} {
				protected.Get(path, func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/listening/tests") && auth.Role(r.Context()) != "GUEST" {
						t.Error("missing guest actor")
					}
					w.WriteHeader(204)
				})
			}
			for _, path := range []string{"/full-mocks/start", "/listening/tests/{testID}/attempts", "/attempts/{attemptID}/submit", "/attempts/{attemptID}/assessment/retry"} {
				protected.Post(path, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
			}
		})
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/full-mock-sessions/abc", 204}, {"GET", "/api/v1/attempts/abc", 204}, {"GET", "/api/v1/attempts/abc/status", 204}, {"POST", "/api/v1/attempts/abc/submit", 204}, {"POST", "/api/v1/attempts/abc/assessment/retry", 204},
		{"GET", "/api/v1/profile", 401}, {"GET", "/api/v1/attempts", 401}, {"GET", "/api/v1/admin/analytics/overview", 401}, {"GET", "/api/v1/listening/tests", 204}, {"POST", "/api/v1/full-mocks/start", 401}, {"POST", "/api/v1/listening/tests/abc/attempts", 401},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, cookieRequest(tc.method, tc.path))
			if w.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	// A broken Bearer token must not fall back to a guest's cookies.
	r := cookieRequest("GET", "/api/v1/attempts/abc")
	r.Header.Set("Authorization", "Bearer forged")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("forged bearer accepted: %d", w.Code)
	}
	r = cookieRequest("POST", "/api/v1/attempts/abc/submit")
	r.Header.Del("Origin")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing origin accepted: %d", w.Code)
	}
	h.allow = func(context.Context, string, int64, time.Duration) (bool, error) {
		return false, errors.New("redis down")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, cookieRequest("GET", "/api/v1/attempts/abc"))
	if w.Code != 503 {
		t.Fatalf("limiter failure opened access: %d", w.Code)
	}
}
func TestStartCreatesCookieAndResumesOnlyExistingMock(t *testing.T) {
	h := testHandler()
	body := `{"examType":"academic","acceptedTerms":true}`
	request := httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(body))
	request.Header.Set("Origin", "https://app.test")
	w := httptest.NewRecorder()
	h.Start(w, request)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/api/v1" || cookies[0].MaxAge != int(cookieTTL.Seconds()) {
		t.Fatalf("unsafe cookie: %+v", cookies)
	}
	mock := h.mocks.(*fakeMock)
	repo := h.repo.(*fakeRepo)
	repo.item.SessionID = &mock.session.ID
	request = httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(body))
	request.Header.Set("Origin", "https://app.test")
	request.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.Start(w, request)
	if w.Code != 200 || repo.creates != 1 || mock.starts != 1 || mock.gets != 1 {
		t.Fatalf("repeat created another exam: %d %+v %+v", w.Code, repo, mock)
	}
	repo.item.ExpiresAt = time.Now().Add(-time.Minute)
	request = httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(body))
	request.Header.Set("Origin", "https://app.test")
	request.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.Start(w, request)
	if w.Code != 403 || repo.creates != 1 {
		t.Fatalf("expiration granted another trial: %d", w.Code)
	}
}
func TestTurnstileValidatesActionAndHostname(t *testing.T) {
	h := testHandler()
	h.cfg.TurnstileSecret = "secret"
	h.cfg.Hostnames = []string{"app.test"}
	for _, response := range []struct {
		body  string
		valid bool
	}{
		{`{"success":true,"hostname":"app.test","action":"guest_mock"}`, true},
		{`{"success":true,"hostname":"foreign.test","action":"guest_mock"}`, false},
		{`{"success":true,"hostname":"app.test","action":"login"}`, false},
		{`{"success":false}`, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.ParseForm()
			if r.Form.Get("secret") != "secret" || r.Form.Get("response") != "token" {
				t.Error("bad validation request")
			}
			io.WriteString(w, response.body)
		}))
		h.verifyURL = server.URL
		err := h.verify(context.Background(), "token")
		server.Close()
		if (err == nil) != response.valid {
			t.Fatalf("validation=%v body=%s", err, response.body)
		}
	}
}
func TestGuestBudgetsBlockRepeatedUploadsAndSubmissions(t *testing.T) {
	h := testHandler()
	h.repo.(*fakeRepo).item = Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	counts := map[string]int64{}
	h.allow = func(_ context.Context, key string, limit int64, _ time.Duration) (bool, error) {
		counts[key]++
		return counts[key] <= limit, nil
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) {
		api.With(h.Authenticate(auth.NewTokenManager("secret", "", "", time.Hour, time.Hour))).Post("/attempts/{attemptID}/recordings", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		api.With(h.Authenticate(auth.NewTokenManager("secret", "", "", time.Hour, time.Hour))).Post("/attempts/{attemptID}/submit", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	})
	for _, tc := range []struct {
		path   string
		budget int
	}{{"recordings", 6}, {"submit", 3}} {
		for i := 0; i <= tc.budget; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, cookieRequest("POST", "/api/v1/attempts/abc/"+tc.path))
			want := 204
			if i == tc.budget {
				want = 429
			}
			if w.Code != want {
				t.Fatalf("%s %d: got %d want %d", tc.path, i, w.Code, want)
			}
		}
	}
}

func TestGlobalStartsRemainBoundedAcrossCookieAndIPRotation(t *testing.T) {
	h := testHandler()
	h.cfg.IPLimit = 2
	h.cfg.GlobalLimit = 3
	ip := "shared-ip"
	h.ip = func(*http.Request) string { return ip }
	counts := map[string]int64{}
	h.allow = func(_ context.Context, key string, limit int64, _ time.Duration) (bool, error) {
		counts[key]++
		return counts[key] <= limit, nil
	}
	for i, tc := range []struct {
		ip     string
		status int
	}{{"shared-ip", 200}, {"shared-ip", 200}, {"shared-ip", 429}, {"vpn-ip", 200}, {"another-vpn-ip", 429}} {
		ip = tc.ip
		r := httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(`{"examType":"academic","acceptedTerms":true}`))
		r.Header.Set("Origin", "https://app.test")
		w := httptest.NewRecorder()
		h.Start(w, r)
		if w.Code != tc.status {
			t.Fatalf("start %d (%s): %d want %d", i, tc.ip, w.Code, tc.status)
		}
	}
	if h.repo.(*fakeRepo).creates != 3 || h.mocks.(*fakeMock).starts != 3 {
		t.Fatal("rotation exceeded global admissions")
	}
}

func TestAnonymousBrowsingOnlyAllowsCatalogSummaries(t *testing.T) {
	h := testHandler()
	tokens := auth.NewTokenManager("test-secret", "issuer", "audience", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) {
		api.Group(func(protected chi.Router) {
			protected.Use(h.Authenticate(tokens))
			for _, path := range []string{"/listening/tests", "/reading/materials", "/writing/materials", "/speaking/materials", "/writing/materials/{id}", "/attempts/{id}"} {
				protected.Get(path, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
			}
		})
	})
	for _, path := range []string{"listening/tests", "reading/materials", "writing/materials", "speaking/materials"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/"+path, nil))
		if w.Code != 204 {
			t.Fatalf("public catalog %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"writing/materials/private", "attempts/private"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/"+path, nil))
		if w.Code != 401 {
			t.Fatalf("private content %s: %d", path, w.Code)
		}
	}
	h.cfg.Enabled = false
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/listening/tests", nil))
	if w.Code != 401 {
		t.Fatal("browsing enabled when feature off")
	}
}

func TestSessionDistinguishesRecognizedExpiredTrial(t *testing.T) {
	claimed := uuid.New()
	for _, tc := range []struct {
		name    string
		cookie  bool
		item    Trial
		repoErr error
		enabled bool
		status  int
		expired bool
	}{
		{name: "missing cookie", enabled: true, status: 401},
		{name: "unknown cookie", cookie: true, enabled: true, status: 401},
		{name: "claimed expired cookie", cookie: true, item: Trial{UserID: uuid.New(), ClaimedBy: &claimed, ExpiresAt: time.Now().Add(-time.Hour)}, enabled: true, status: 401},
		{name: "expired cookie", cookie: true, item: Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(-time.Hour)}, enabled: true, status: 401, expired: true},
		{name: "valid cookie", cookie: true, item: Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}, enabled: true, status: 200},
		{name: "disabled", cookie: true, item: Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(-time.Hour)}, status: 401},
		{name: "repository unavailable", cookie: true, repoErr: errors.New("database unavailable"), enabled: true, status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler()
			h.cfg.Enabled = tc.enabled
			h.repo.(*fakeRepo).item = tc.item
			h.repo.(*fakeRepo).err = tc.repoErr
			r := httptest.NewRequest("GET", "/api/v1/guest/session", nil)
			if tc.cookie {
				r = cookieRequest("GET", "/api/v1/guest/session")
			}
			w := httptest.NewRecorder()
			h.Session(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatal("session response changed cookie or cache policy")
			}
			if w.Code == 200 {
				return
			}
			var body httpx.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if (body.Details["reason"] == "trial_expired") != tc.expired {
				t.Fatalf("expiry signal=%v want=%v: %s", body.Details, tc.expired, w.Body.String())
			}
			if tc.expired && (body.Code != "GUEST_EXPIRED" || body.Message != "Срок гостевого доступа истёк. Войдите для продолжения подготовки") {
				t.Fatalf("unexpected expiry response: %+v", body)
			}
			if tc.status == 401 && !tc.expired && (body.Code != "GUEST_EXPIRED" || body.Message != "Гостевая сессия отсутствует или истекла") {
				t.Fatalf("generic denial changed: %+v", body)
			}
		})
	}
}
