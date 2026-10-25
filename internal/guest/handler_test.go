package guest

import (
	"bytes"
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
	item       Trial
	creates    int
	media      bool
	err        error
	createHash []byte
	createErr  error
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
func (r *fakeRepo) Create(_ context.Context, hash []byte, _ string, expires time.Time) (Trial, error) {
	r.creates++
	r.createHash = append([]byte(nil), hash...)
	if r.createErr != nil {
		return Trial{}, r.createErr
	}
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
	h.cfg.TurnstileSecret = "secret"
	h.allow = func(context.Context, string, int64, time.Duration) (bool, error) {
		t.Error("resuming an active identity must not run fresh admission")
		return false, nil
	}
	request = httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(body))
	request.Header.Set("Origin", "https://app.test")
	request.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.Start(w, request)
	if w.Code != 200 || repo.creates != 1 || mock.starts != 1 || mock.gets != 1 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("repeat created another exam: %d %+v %+v", w.Code, repo, mock)
	}
}
func TestExpiredUnclaimedTrialStartsFreshAfterAdmission(t *testing.T) {
	oldSession := uuid.New()
	for _, tc := range []struct {
		name      string
		sessionID *uuid.UUID
	}{
		{name: "without an old mock"},
		{name: "with an old mock", sessionID: &oldSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler()
			h.cfg.Secure = true
			h.cfg.SameSite = http.SameSiteNoneMode
			h.cfg.TurnstileSecret = "secret"
			h.cfg.Hostnames = []string{"app.test"}
			repo := h.repo.(*fakeRepo)
			old := Trial{UserID: uuid.New(), SessionID: tc.sessionID, ExpiresAt: time.Now().Add(-time.Hour)}
			repo.item = old
			var admissions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				admissions = append(admissions, "turnstile")
				if err := r.ParseForm(); err != nil || r.Form.Get("response") != "new-token" {
					t.Error("new trial did not verify its challenge token")
				}
				io.WriteString(w, `{"success":true,"hostname":"app.test","action":"guest_mock"}`)
			}))
			defer server.Close()
			h.verifyURL = server.URL
			h.allow = func(_ context.Context, key string, _ int64, _ time.Duration) (bool, error) {
				switch {
				case strings.HasPrefix(key, "guest:challenge:"):
					admissions = append(admissions, "challenge")
				case strings.HasPrefix(key, "guest:starts:ip:"):
					admissions = append(admissions, "ip")
				case key == "guest:starts:global":
					admissions = append(admissions, "global")
				default:
					t.Errorf("unexpected admission key: %s", key)
				}
				if repo.creates != 0 {
					t.Error("created identity before completing admission")
				}
				return true, nil
			}
			request := httptest.NewRequest("POST", "/api/v1/guest/start", strings.NewReader(`{"examType":"academic","acceptedTerms":true,"turnstileToken":"new-token"}`))
			request.Header.Set("Origin", "https://app.test")
			oldToken := strings.Repeat("a", 43)
			request.AddCookie(&http.Cookie{Name: CookieName, Value: oldToken})
			w := httptest.NewRecorder()
			before := time.Now()
			h.Start(w, request)
			after := time.Now()
			if w.Code != http.StatusOK {
				t.Fatalf("expired unclaimed trial must admit a new trial: status=%d body=%s", w.Code, w.Body.String())
			}
			if got := strings.Join(admissions, ","); got != "challenge,turnstile,ip,global" {
				t.Fatalf("fresh admission order=%s", got)
			}
			mock := h.mocks.(*fakeMock)
			if repo.creates != 1 || repo.item.UserID == old.UserID || repo.item.UserID == uuid.Nil || repo.item.SessionID != nil || repo.item.ClaimedBy != nil {
				t.Fatalf("new trial must have an independent actor: creates=%d old=%+v new=%+v", repo.creates, old, repo.item)
			}
			if repo.item.ExpiresAt.Before(before.Add(trialTTL)) || repo.item.ExpiresAt.After(after.Add(trialTTL)) {
				t.Fatalf("new actor has incorrect access expiry: %s", repo.item.ExpiresAt)
			}
			if mock.starts != 1 || mock.gets != 0 || mock.retakes != 0 || mock.session.UserID != repo.item.UserID || mock.session.ID == oldSession {
				t.Fatalf("new trial resumed or retook the old mock: %+v", mock)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected a replacement cookie, got %+v", cookies)
			}
			cookie := cookies[0]
			if cookie.Name != CookieName || len(cookie.Value) != 43 || cookie.Value == oldToken || !bytes.Equal(repo.createHash, h.hash(cookie.Value)) || bytes.Equal(repo.createHash, h.hash(oldToken)) {
				t.Fatalf("replacement cookie does not identify the new actor: %+v", cookie)
			}
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode || cookie.Path != "/api/v1" || cookie.MaxAge != int(cookieTTL.Seconds()) || cookie.Expires.Before(before.Add(cookieTTL).Truncate(time.Second)) || cookie.Expires.After(after.Add(cookieTTL)) {
				t.Fatalf("unsafe replacement cookie: %+v", cookie)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("new trial response must not be cached")
			}
		})
	}
}

func TestExpiredUnclaimedTrialAdmissionFailuresKeepOldIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, stage, code, admissions string
		unavailable                   bool
		status                        int
	}{
		{name: "challenge throttled", stage: "challenge", status: 429, code: "GUEST_LIMIT_EXCEEDED", admissions: "challenge"},
		{name: "challenge limiter unavailable", stage: "challenge", unavailable: true, status: 503, code: "DEPENDENCY_UNAVAILABLE", admissions: "challenge"},
		{name: "Turnstile rejected", stage: "turnstile", status: 403, code: "GUEST_CHALLENGE_FAILED", admissions: "challenge,turnstile"},
		{name: "Turnstile unavailable", stage: "turnstile", unavailable: true, status: 503, code: "DEPENDENCY_UNAVAILABLE", admissions: "challenge,turnstile"},
		{name: "IP quota denied", stage: "ip", status: 429, code: "GUEST_LIMIT_EXCEEDED", admissions: "challenge,turnstile,ip"},
		{name: "IP quota unavailable", stage: "ip", unavailable: true, status: 503, code: "DEPENDENCY_UNAVAILABLE", admissions: "challenge,turnstile,ip"},
		{name: "global quota denied", stage: "global", status: 429, code: "GUEST_LIMIT_EXCEEDED", admissions: "challenge,turnstile,ip,global"},
		{name: "global quota unavailable", stage: "global", unavailable: true, status: 503, code: "DEPENDENCY_UNAVAILABLE", admissions: "challenge,turnstile,ip,global"},
		{name: "actor creation failed", stage: "create", status: 500, code: "INTERNAL_ERROR", admissions: "challenge,turnstile,ip,global"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler()
			h.cfg.TurnstileSecret = "secret"
			h.cfg.Hostnames = []string{"app.test"}
			repo := h.repo.(*fakeRepo)
			oldSession := uuid.New()
			old := Trial{UserID: uuid.New(), SessionID: &oldSession, ExpiresAt: time.Now().Add(-time.Hour)}
			repo.item = old
			if tc.stage == "create" {
				repo.createErr = errors.New("database unavailable")
			}
			var admissions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				admissions = append(admissions, "turnstile")
				if tc.stage == "turnstile" {
					if tc.unavailable {
						w.WriteHeader(http.StatusServiceUnavailable)
					} else {
						io.WriteString(w, `{"success":false}`)
					}
					return
				}
				io.WriteString(w, `{"success":true,"hostname":"app.test","action":"guest_mock"}`)
			}))
			defer server.Close()
			h.verifyURL = server.URL
			h.allow = func(_ context.Context, key string, _ int64, _ time.Duration) (bool, error) {
				stage := ""
				switch {
				case strings.HasPrefix(key, "guest:challenge:"):
					stage = "challenge"
				case strings.HasPrefix(key, "guest:starts:ip:"):
					stage = "ip"
				case key == "guest:starts:global":
					stage = "global"
				default:
					t.Errorf("unexpected admission key: %s", key)
				}
				admissions = append(admissions, stage)
				if stage == tc.stage {
					if tc.unavailable {
						return false, errors.New("redis unavailable")
					}
					return false, nil
				}
				return true, nil
			}
			r := cookieRequest("POST", "/api/v1/guest/start")
			r.Body = io.NopCloser(strings.NewReader(`{"examType":"academic","acceptedTerms":true,"turnstileToken":"new-token"}`))
			w := httptest.NewRecorder()
			h.Start(w, r)
			var body httpx.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body.Code != tc.code {
				t.Fatalf("status=%d code=%s want=%d/%s: %s", w.Code, body.Code, tc.status, tc.code, w.Body.String())
			}
			if got := strings.Join(admissions, ","); got != tc.admissions {
				t.Fatalf("admission stages=%s want=%s", got, tc.admissions)
			}
			wantCreates := 0
			if tc.stage == "create" {
				wantCreates = 1
			}
			mock := h.mocks.(*fakeMock)
			if repo.creates != wantCreates || repo.item != old || len(w.Result().Cookies()) != 0 || mock.starts != 0 || mock.gets != 0 || mock.retakes != 0 {
				t.Fatalf("failed admission changed identity/cookie or accessed old mock: repo=%+v mock=%+v cookies=%+v", repo, mock, w.Result().Cookies())
			}
		})
	}
}

func TestGuestStartKeepsClaimedAndExpiredRetakeDenials(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		expired, claimed, retake bool
		code                     string
	}{
		{name: "claimed active", claimed: true, code: "GUEST_CLAIMED"},
		{name: "claimed expired", expired: true, claimed: true, code: "GUEST_CLAIMED"},
		{name: "claimed expired retake", expired: true, claimed: true, retake: true, code: "GUEST_CLAIMED"},
		{name: "unclaimed expired retake", expired: true, retake: true, code: "GUEST_EXPIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler()
			repo := h.repo.(*fakeRepo)
			session, account := uuid.New(), uuid.New()
			old := Trial{UserID: uuid.New(), SessionID: &session, ExpiresAt: time.Now().Add(time.Hour)}
			if tc.expired {
				old.ExpiresAt = time.Now().Add(-time.Hour)
			}
			if tc.claimed {
				old.ClaimedBy = &account
			}
			repo.item = old
			h.allow = func(context.Context, string, int64, time.Duration) (bool, error) {
				t.Error("denied identity must not enter admission")
				return false, nil
			}
			input := map[string]any{"examType": "academic", "acceptedTerms": true}
			if tc.retake {
				input["retakeSessionId"] = session
			}
			encoded, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			r := cookieRequest("POST", "/api/v1/guest/start")
			r.Body = io.NopCloser(bytes.NewReader(encoded))
			w := httptest.NewRecorder()
			h.Start(w, r)
			var body httpx.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			mock := h.mocks.(*fakeMock)
			if w.Code != 403 || body.Code != tc.code || repo.creates != 0 || repo.item != old || mock.starts != 0 || mock.gets != 0 || mock.retakes != 0 || len(w.Result().Cookies()) != 0 {
				t.Fatalf("denied identity changed or wrong precedence: status=%d code=%s repo=%+v mock=%+v cookies=%+v", w.Code, body.Code, repo, mock, w.Result().Cookies())
			}
		})
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
			h.allow = func(context.Context, string, int64, time.Duration) (bool, error) {
				t.Error("session lookup must not consume admission")
				return false, nil
			}
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
			repo, mock := h.repo.(*fakeRepo), h.mocks.(*fakeMock)
			if repo.item != tc.item || repo.creates != 0 || mock.starts != 0 || mock.gets != 0 || mock.retakes != 0 {
				t.Fatal("session lookup changed trial or accessed its mock")
			}
			if w.Code == 200 {
				return
			}
			var body httpx.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			wantReason := ""
			if tc.expired {
				wantReason = "session_expired"
			}
			if body.Details["reason"] != wantReason {
				t.Fatalf("expiry signal=%v want=%s: %s", body.Details, wantReason, w.Body.String())
			}
			if tc.status == 401 && (body.Code != "GUEST_EXPIRED" || body.Message != "Гостевая сессия отсутствует или истекла") {
				t.Fatalf("generic denial changed: %+v", body)
			}
		})
	}
}
