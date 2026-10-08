package guest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestGuestStartResumeAlsoRedactsSectionGrades(t *testing.T) {
	h := testHandler()
	id := uuid.New()
	h.repo.(*fakeRepo).item = Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour), SessionID: &id}
	band := 7.5
	h.mocks.(*fakeMock).session = fullmock.Session{ID: id, OverallBand: &band, Sections: []fullmock.SessionSection{{Attempt: attempts.Attempt{Band: &band}}}}
	request := cookieRequest("POST", "/api/v1/guest/start")
	request.Body = io.NopCloser(strings.NewReader(`{"examType":"academic","acceptedTerms":true}`))
	w := httptest.NewRecorder()
	h.Start(w, request)
	var got fullmock.Session
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !got.ResultsLocked || got.OverallBand == nil || got.Sections[0].Attempt.Band != nil {
		t.Fatalf("start leaks grades: %s", w.Body.String())
	}
}

func TestClaimRequiresAccountAndOrigin(t *testing.T) {
	h := testHandler()
	for _, tc := range []struct {
		role, origin string
		status       int
	}{
		{"", "https://app.test", 401}, {"GUEST", "https://app.test", 401},
		{"STUDENT", "", 403}, {"STUDENT", "https://attacker.test", 403}, {"STUDENT", "https://app.test", 200},
	} {
		r := cookieRequest("POST", "/api/v1/guest/claim")
		r.Header.Set("Origin", tc.origin)
		if tc.role != "" {
			r = r.WithContext(auth.WithUser(r.Context(), uuid.New(), tc.role))
		}
		w := httptest.NewRecorder()
		h.Claim(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.role, tc.origin, w.Code, w.Body.String())
		}
	}
	h.repo.(*fakeRepo).err = ErrAlreadyClaimed
	r := cookieRequest("POST", "/api/v1/guest/claim").WithContext(auth.WithUser(context.Background(), uuid.New(), "STUDENT"))
	w := httptest.NewRecorder()
	h.Claim(w, r)
	if w.Code != 409 {
		t.Fatalf("already claimed: %d", w.Code)
	}
}

func TestClaimedCookieCannotResumeOrRestoreGuest(t *testing.T) {
	h := testHandler()
	account := uuid.New()
	h.repo.(*fakeRepo).item = Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour), ClaimedBy: &account}
	w := httptest.NewRecorder()
	h.Session(w, cookieRequest("GET", "/api/v1/guest/session"))
	if w.Code != 401 {
		t.Fatalf("claimed cookie restored: %d", w.Code)
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) {
		api.With(h.Authenticate(auth.NewTokenManager("secret", "issuer", "audience", time.Hour, time.Hour))).Get("/attempts/{attemptID}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(204)
		})
	})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, cookieRequest("GET", "/api/v1/attempts/abc"))
	if w.Code != 401 {
		t.Fatalf("claimed cookie accessed protected review: %d", w.Code)
	}
	w = httptest.NewRecorder()
	r := cookieRequest("POST", "/api/v1/guest/start")
	r.Body = io.NopCloser(strings.NewReader(`{"examType":"academic","acceptedTerms":true}`))
	h.Start(w, r)
	if w.Code != 403 || h.mocks.(*fakeMock).starts != 0 {
		t.Fatalf("claimed cookie restarted: %d", w.Code)
	}
}
