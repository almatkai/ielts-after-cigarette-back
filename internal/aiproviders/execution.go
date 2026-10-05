package aiproviders

import (
	"context"
	"errors"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type observation struct {
	mu              sync.Mutex
	started         time.Time
	response, token *int64
}
type observationKey struct{}

func (o *observation) mark(token bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	value := time.Since(o.started).Milliseconds()
	if token {
		if o.token == nil {
			o.token = &value
		}
	} else if o.response == nil {
		o.response = &value
	}
}

// MarkFirstToken records timing only; token content never enters telemetry.
func MarkFirstToken(ctx context.Context) {
	if o, ok := ctx.Value(observationKey{}).(*observation); ok {
		o.mark(true)
	}
}
func (s *Service) record(ctx context.Context, m CallMetric) {
	s.logger.InfoContext(ctx, "AI provider attempt", "run_id", m.RunID, "provider_id", m.ProviderID, "model", m.Model, "purpose", m.Purpose, "duration_ms", m.DurationMS, "first_token_ms", m.FirstTokenMS, "outcome", m.Outcome, "won", m.Won, "trigger", m.Trigger, "mode", m.Mode, "code", m.Code, "http_status", m.HTTPStatus, "request_id", m.RequestID)
	if s.routing != nil {
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := s.routing.Record(storeCtx, m); err != nil && !errors.Is(err, ErrMigrationRequired) {
			s.logger.WarnContext(ctx, "AI telemetry persistence unavailable")
		}
	}
}

// Execute routes one operation to one provider. A level rotates its starting
// provider per operation; other providers are contacted only after a failure.
// Each callback returns its own fully validated result, never shared output.
func Execute[T any](ctx context.Context, s *Service, purpose string, call func(context.Context, Provider) (T, error)) (T, error) {
	return execute(ctx, s, purpose, call, false)
}
func execute[T any](parent context.Context, s *Service, purpose string, call func(context.Context, Provider) (T, error), _ bool) (T, error) {
	var zero T
	if parent.Err() != nil {
		return zero, parent.Err()
	}
	ctx, cancel := context.WithTimeout(parent, ChainTimeout)
	defer cancel()
	loadCtx, stopLoad := context.WithTimeout(ctx, 5*time.Second)
	items, err := s.repo.List(loadCtx)
	stopLoad()
	if err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		s.report(ctx, Provider{}, purpose, ErrValidation)
		items = nil
	}
	groups := [][]Provider{}
	databaseCount := 0
	for _, p := range s.providers(items) {
		if !p.Enabled || !includes(p.Scopes, purpose) {
			continue
		}
		if !p.FromEnv {
			if databaseCount >= MaxChain {
				continue
			}
			databaseCount++
		}
		if len(groups) == 0 || groups[len(groups)-1][0].Priority != p.Priority {
			groups = append(groups, []Provider{})
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], p)
	}
	if len(groups) == 0 {
		return zero, ErrUnavailable
	}
	remaining := 0
	for _, group := range groups {
		remaining += len(group)
	}
	runID := uuid.New()
	attempt := 0
	for _, group := range groups {
		offset := s.rotationOffset(ctx, purpose, group[0].Priority, len(group))
		for i := 0; i < len(group); i++ {
			if ctx.Err() != nil {
				if parent.Err() != nil {
					return zero, parent.Err()
				}
				return zero, ErrAllFailed
			}
			p := group[(i+offset)%len(group)]
			deadline, _ := ctx.Deadline()
			timeout := min(time.Duration(p.TimeoutSeconds)*time.Second, time.Until(deadline)/time.Duration(max(1, remaining)))
			remaining--
			attemptCtx, stopAttempt := context.WithTimeout(ctx, timeout)
			started := time.Now()
			o := &observation{started: started}
			attemptCtx = context.WithValue(attemptCtx, observationKey{}, o)
			attemptCtx = httptrace.WithClientTrace(attemptCtx, &httptrace.ClientTrace{GotFirstResponseByte: func() { o.mark(false) }})
			trigger := "rotation"
			if attempt > 0 {
				trigger = "fallback"
			}
			attempt++
			m := CallMetric{ID: uuid.New(), RunID: runID, ProviderID: p.ID, ProviderName: p.Name, Model: p.Model, Purpose: purpose, FromEnv: p.FromEnv, StartedAt: started, Trigger: trigger, Mode: "round_robin", RequestID: httpx.RequestID(parent)}
			var value T
			var failure error
			if !p.FromEnv {
				if p.TimeoutSeconds < 5 || p.TimeoutSeconds > 60 {
					failure = ErrValidation
				} else {
					failure = ValidateEndpoint(p.Endpoint)
				}
				if failure == nil {
					p.APIKey, failure = s.cipher.Open(p.Ciphertext, p.aad())
				}
			}
			if purpose == "speaking" && p.SpeakingModel != "" {
				p.Model = p.SpeakingModel
				m.Model = p.Model
			}
			if failure == nil {
				value, failure = call(attemptCtx, p)
			}
			if attemptCtx.Err() != nil {
				failure = attemptCtx.Err()
			}
			m.DurationMS = time.Since(started).Milliseconds()
			o.mu.Lock()
			m.FirstResponseMS = o.response
			m.FirstTokenMS = o.token
			o.mu.Unlock()
			stopAttempt()
			switch {
			case parent.Err() != nil:
				m.Outcome = "cancelled"
				m.Code = "caller_cancelled"
				failure = parent.Err()
			case failure == nil:
				m.Outcome = "success"
				m.Won = true
			case errors.Is(failure, context.Canceled):
				m.Outcome = "cancelled"
				m.Code = "cancelled"
			default:
				m.Outcome = "failure"
				if errors.Is(failure, context.DeadlineExceeded) {
					m.Outcome = "timeout"
				}
				safe := s.report(parent, p, purpose, failure)
				m.Code = safe.Code
				m.HTTPStatus = safe.Status
			}
			s.record(parent, m)
			if parent.Err() != nil {
				return zero, parent.Err()
			}
			if failure == nil {
				return value, nil
			}
		}
	}
	return zero, ErrAllFailed
}
