package studyplan

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
)

func TestWriteMapsPaymentRequiredTo402(t *testing.T) {
	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/study-plan/tasks/x/start", nil)
	err := fmt.Errorf("start task: %w", &billing.PaymentRequired{Feature: "writing", Skill: "writing", Remaining: map[string]int{"writing": 0, "reading": 2}})

	h.write(rec, req, nil, err)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body %s", rec.Code, rec.Body)
	}
	var body struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "PAYMENT_REQUIRED" || body.Details["skill"] != "writing" {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
}
