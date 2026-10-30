package studyplan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/reading"
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

func TestPlanStartUsesProductionBillingAndManualGrant(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	h.seedReading(20)
	bills := billing.NewService(billing.NewPostgresRepository(h.pool), true, nil, "", nil, nil)
	rs := reading.NewService(reading.NewPostgresRepository(h.pool))
	h.service.attempts = attempts.NewService(attempts.NewPostgresRepository(h.pool).WithBilling(bills), map[string]attempts.MaterialProvider{"reading": attempts.NewReadingProvider(rs)})
	h.sql(`UPDATE billing_settings SET free_reading=0 WHERE id=1`)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "PRACTICE")
	path := "/study-plan/assignments/" + a.ID + "/start"
	input := mutationInput{ExpectedRevision: r.Plan.Revision}
	denied := h.request("POST", path, input, h.token)
	if denied.Code != 402 {
		t.Fatalf("expected paywall: %d %s", denied.Code, denied.Body)
	}
	var count int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM attempts WHERE user_id=$1`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied start persisted attempt: %d %v", count, err)
	}
	after := h.call("GET", "/study-plan", nil, 200)
	if after.Plan.Revision != r.Plan.Revision {
		t.Fatal("denied start mutated plan")
	}
	if _, err := bills.Grant(ctx, h.user, h.other, billing.GrantInput{PlanCode: "self", Source: "ADMIN", Note: "release manual grant"}); err != nil {
		t.Fatal(err)
	}
	allowed := h.request("POST", path, input, h.token)
	if allowed.Code != 200 {
		t.Fatalf("manual grant start: %d %s", allowed.Code, allowed.Body)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM free_usage WHERE user_id=$1`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatalf("paid start consumed free quota: %d %v", count, err)
	}
}
