package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

func testHandler(s *Service) *Handler {
	return NewHandler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 1024)
}
func TestPaymentRequiredWireContract(t *testing.T) {
	for _, feature := range []string{"full_mock", "listening", "reading", "writing", "speaking", "assistant"} {
		t.Run(feature, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/", nil)
			e := &PaymentRequired{Feature: feature, Skill: "reading", Remaining: map[string]int{"listening": 1, "reading": 0, "writing": 2, "speaking": 3}}
			if !WritePaymentRequired(w, r, fmt.Errorf("start: %w", e)) {
				t.Fatal("wrapped error ignored")
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Feature   string         `json:"feature"`
					Skill     string         `json:"skill"`
					Remaining map[string]int `json:"remaining"`
				} `json:"details"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 402 || body.Code != "PAYMENT_REQUIRED" || body.Details.Feature != feature || body.Details.Skill != "reading" || body.Details.Remaining["speaking"] != 3 || body.Message != e.Error() {
				t.Fatalf("response: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if WritePaymentRequired(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), errors.New("unrelated")) {
		t.Fatal("wrong error handled")
	}
}
func TestCheckoutRejectsMissingTermsAndUnknownJSON(t *testing.T) {
	h := testHandler(NewService(nil, true, nil, "", nil, nil))
	for _, body := range []string{`{}`, `{"planCode":"self","acceptedTerms":false}`, `{"planCode":"self","acceptedTerms":true,"admin":true}`, `null`, `{} {}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		h.Checkout(w, r)
		var e httpx.ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != 400 || e.Code != "VALIDATION_ERROR" {
			t.Fatalf("body %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.Checkout(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"planCode":"self","acceptedTerms":true}`)))
	if w.Code != 503 {
		t.Fatalf("provider unavailable: %d", w.Code)
	}
}
func TestGuestMeOnlyReturnsFlagAndGuest(t *testing.T) {
	h := testHandler(NewService(nil, true, nil, "", nil, nil))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil).WithContext(auth.WithUser(httptest.NewRequest("GET", "/", nil).Context(), uuid.New(), "GUEST"))
	h.Me(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusOK || len(body) != 2 || body["guest"] != true || body["paywallEnabled"] != true {
		t.Fatalf("guest: %s", w.Body.String())
	}
}
