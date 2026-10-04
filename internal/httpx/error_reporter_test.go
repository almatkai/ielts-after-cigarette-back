package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type sinkStub struct {
	events []ErrorEvent
}

func (s *sinkStub) report(ctx context.Context, event ErrorEvent) {
	s.events = append(s.events, event)
}

func TestInternalErrorForwardsToSinkAndKeepsResponseSafe(t *testing.T) {
	sink := &sinkStub{}
	SetErrorReporter(sink.report)
	t.Cleanup(func() { SetErrorReporter(nil) })

	logger := newDiscardLogger()
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(WithActor(r.Context(), "internal-user-id"))
		InternalError(w, r, logger, errors.New("private database failure"))
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	r.Header.Set("X-Request-ID", "support-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d", len(sink.events))
	}
	event := sink.events[0]
	if event.Error == nil || event.Error.Error() != "private database failure" {
		t.Fatalf("sink error = %v", event.Error)
	}
	if event.RequestID != "support-id" || event.ActorID != "internal-user-id" {
		t.Fatalf("sink correlation = %+v", event)
	}
	if event.Origin != "http" {
		t.Fatalf("origin = %q", event.Origin)
	}
}

func TestRecoverReportsPanic(t *testing.T) {
	sink := &sinkStub{}
	SetErrorReporter(sink.report)
	t.Cleanup(func() { SetErrorReporter(nil) })

	handler := RequestIDMiddleware(Recover(newDiscardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(errors.New("boom"))
	})))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if len(sink.events) != 1 || sink.events[0].Panic == nil {
		t.Fatalf("sink = %+v", sink.events)
	}
}

func TestReportBackgroundFieldAllowList(t *testing.T) {
	sink := &sinkStub{}
	SetErrorReporter(sink.report)
	t.Cleanup(func() { SetErrorReporter(nil) })

	attempt := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	ReportBackground(context.Background(), "writing_assessment", "attempt_id", attempt, "error", errors.New("boom"), "essay", "student answer")
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d", len(sink.events))
	}
	event := sink.events[0]
	if event.AttemptID != attempt || event.Error == nil {
		t.Fatalf("event = %+v", event)
	}
	// The unsupported key must not appear anywhere in the event.
	if _, dangerous := any(event).(*string); dangerous {
		t.Fatal("unexpected pointer field")
	}
}

func TestBrokenSinkDoesNotPanic(t *testing.T) {
	SetErrorReporter(func(context.Context, ErrorEvent) { panic("sink is broken") })
	t.Cleanup(func() { SetErrorReporter(nil) })
	w := httptest.NewRecorder()
	InternalError(w, httptest.NewRequest(http.MethodGet, "/", nil), newDiscardLogger(), errors.New("boom"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

// discardLogger placeholder retained for clarity; InternalError takes *slog.Logger.

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
