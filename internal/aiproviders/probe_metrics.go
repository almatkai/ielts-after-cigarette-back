package aiproviders

import (
	"context"
	"errors"
	"net/http/httptrace"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

func (s *Service) testProvider(ctx context.Context, p Provider) (TestResult, error) {
	started := time.Now()
	o := &observation{started: started}
	callCtx := context.WithValue(ctx, observationKey{}, o)
	callCtx = httptrace.WithClientTrace(callCtx, &httptrace.ClientTrace{GotFirstResponseByte: func() { o.mark(false) }})
	result, err := s.probeProvider(callCtx, p)
	m := CallMetric{ID: uuid.New(), RunID: uuid.New(), ProviderID: p.ID, ProviderName: p.Name, Model: p.Model, Purpose: "test", FromEnv: p.FromEnv, StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Trigger: "test", Mode: "test", Code: result.Code, HTTPStatus: result.HTTPStatus, RequestID: httpx.RequestID(ctx)}
	o.mu.Lock()
	m.FirstResponseMS = o.response
	m.FirstTokenMS = o.token
	o.mu.Unlock()
	m.Outcome = "failure"
	if result.OK && err == nil {
		m.Outcome = "success"
		m.Won = true
	} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		m.Outcome = "cancelled"
		m.Code = "caller_cancelled"
	} else if result.Code == "timeout" {
		m.Outcome = "timeout"
	}
	s.record(ctx, m)
	return result, err
}
