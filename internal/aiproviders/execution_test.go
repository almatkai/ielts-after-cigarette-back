package aiproviders

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memoryRouting struct {
	mu    sync.Mutex
	rules Routing
	calls []CallMetric
}

func (r *memoryRouting) Load(context.Context) (Routing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rules, nil
}
func (r *memoryRouting) Save(_ context.Context, p Routing) (Routing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.Revision != r.rules.Revision {
		return p, ErrConflict
	}
	p.Revision++
	r.rules = p
	return p, nil
}
func (r *memoryRouting) Record(_ context.Context, p CallMetric) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, p)
	return nil
}
func (r *memoryRouting) Stats(context.Context, int) (Stats, error) {
	return Stats{Models: []ModelStats{}, Recent: []CallMetric{}}, nil
}

func TestFailedProviderFallsBackOneAtATimeAndRecordsRecoveredError(t *testing.T) {
	s, providers := rotationService(t)
	store := &memoryRouting{rules: Routing{Mode: "hedged", MaxParallel: 4, HedgeDelayMS: 0}}
	s.routing = store
	var order []uuid.UUID
	value, err := Execute(context.Background(), s, "writing", func(ctx context.Context, p Provider) (string, error) {
		order = append(order, p.ID)
		if p.ID == providers[0].ID {
			return "invalid", ErrValidation
		}
		MarkFirstToken(ctx)
		return "valid", nil
	})
	if err != nil || value != "valid" || len(order) != 2 || order[0] != providers[0].ID || order[1] != providers[1].ID {
		t.Fatal("fallback order/validation broken")
	}
	if len(store.calls) != 2 || store.calls[0].Outcome != "failure" || store.calls[0].Code != "invalid_configuration" || store.calls[1].Outcome != "success" || !store.calls[1].Won || store.calls[1].FirstTokenMS == nil {
		t.Fatal("metrics incomplete")
	}
	// Old hedged configuration must never broadcast a successful request.
	calls := 0
	_, err = Execute(context.Background(), s, "writing", func(context.Context, Provider) (string, error) { calls++; return "valid", nil })
	if err != nil || calls != 1 {
		t.Fatal("legacy configuration enabled fan-out")
	}
}
func TestNextLevelIsUsedOnlyAfterCurrentLevelFails(t *testing.T) {
	s, providers := rotationService(t)
	in := input()
	in.Priority = 20
	reserve, err := s.Save(context.Background(), uuid.Nil, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	var order []uuid.UUID
	_, err = Execute(context.Background(), s, "assistant", func(_ context.Context, p Provider) (string, error) {
		order = append(order, p.ID)
		if p.ID == reserve.ID {
			return "reserve", nil
		}
		return "", ErrValidation
	})
	if err != nil || len(order) != 5 || order[4] != reserve.ID {
		t.Fatal("reserve started before current level was exhausted")
	}
	for i, p := range providers {
		if order[i] != p.ID {
			t.Fatal("fallback within level not sequential")
		}
	}
}
func TestCallerCancellationNeverTriggersMoreProviders(t *testing.T) {
	s, _ := rotationService(t)
	store := &memoryRouting{}
	s.routing = store
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	go func() {
		_, err := Execute(ctx, s, "assistant", func(ctx context.Context, _ Provider) (string, error) {
			calls++
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
	if calls != 1 || len(store.calls) != 1 || store.calls[0].Outcome != "cancelled" || store.calls[0].Code != "caller_cancelled" {
		t.Fatal("cancelled request retried or counted as provider failure")
	}
}
func TestEnvironmentProviderParticipatesInRoundRobin(t *testing.T) {
	s, providers := rotationService(t)
	s.env = Provider{ID: EnvProviderID, Name: "env", Endpoint: "https://env.example.test", Model: "env-model", APIKey: "server-only", FromEnv: true, Enabled: true, Scopes: []string{"assistant"}, Priority: 10, TimeoutSeconds: 20}
	for i := 0; i < 10; i++ {
		calls := 0
		winner, err := Execute(context.Background(), s, "assistant", func(_ context.Context, p Provider) (uuid.UUID, error) { calls++; return p.ID, nil })
		if err != nil || calls != 1 {
			t.Fatal("request duplicated")
		}
		expected := EnvProviderID
		if i%5 < 4 {
			expected = providers[i%5].ID
		}
		if winner != expected {
			t.Fatal("env was not included in rotation")
		}
	}
}
