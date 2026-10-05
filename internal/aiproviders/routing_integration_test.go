package aiproviders

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestAtomicOrderIncludesVirtualEnvironmentAndRejectsStaleSnapshots(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	s := NewService(NewRepository(pool), testCipher(t), Provider{Endpoint: "https://env.example.test/v1/chat/completions", APIKey: "test-env-key", Model: "env-model"}, testLogger(io.Discard))
	first, err := s.Save(ctx, uuid.Nil, actor, input())
	if err != nil {
		t.Fatal(err)
	}
	secondInput := input()
	secondInput.Priority = 20
	second, err := s.Save(ctx, uuid.Nil, actor, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	order := []OrderItem{{EnvProviderID, 0}, {second.ID, second.Revision}, {first.ID, first.Revision}}
	if err := s.Reorder(ctx, order, actor); err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 3 || items[0].ID != EnvProviderID || items[1].ID != second.ID || items[2].ID != first.ID {
		t.Fatal("atomic order not persisted")
	}
	if err := s.Reorder(ctx, order, actor); !errors.Is(err, ErrConflict) {
		t.Fatal("stale reorder overwrote new state")
	}
	fresh := []OrderItem{}
	for _, p := range items {
		fresh = append(fresh, OrderItem{p.ID, p.Revision})
	}
	fresh[1].Revision--
	if err := s.Reorder(ctx, fresh, actor); !errors.Is(err, ErrConflict) {
		t.Fatal("partial reorder committed")
	}
	again, _ := s.List(ctx)
	for i := range items {
		if items[i].Priority != again[i].Priority || items[i].Revision != again[i].Revision {
			t.Fatal("failed reorder changed data")
		}
	}
	envRecord, err := s.repo.Get(ctx, EnvProviderID)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := s.cipher.Open(envRecord.Ciphertext, envRecord.aad())
	if err != nil || marker != "environment-credential-reference" {
		t.Fatal("virtual environment lost credential reference")
	}
}
func TestPriorityStackPersistsAndExecutesWithoutMigration32(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	if _, err := pool.Exec(ctx, `DROP TABLE ai_provider_routing; DROP TABLE ai_provider_calls; DROP TABLE ai_provider_rotation`); err != nil {
		t.Fatal(err)
	}
	s := NewService(NewRepository(pool), testCipher(t), Provider{Endpoint: "https://env.example.test", APIKey: "server-only", Model: "env-model"}, testLogger(io.Discard))
	s.routing = &postgresRoutingStore{pool: pool}
	in := input()
	in.Priority = 10
	first, err := s.Save(ctx, uuid.Nil, actor, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.loadEnvironment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	display := environmentDisplay(env)
	_, err = s.Save(ctx, EnvProviderID, actor, Input{Name: display.Name, Endpoint: display.Endpoint, Model: display.Model, SpeakingModel: display.SpeakingModel, Scopes: env.Scopes, Enabled: true, Priority: 10, TimeoutSeconds: env.TimeoutSeconds})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 2 || items[0].Priority != 10 || items[1].Priority != 10 {
		t.Fatal("shared priority not persisted")
	}
	for i := 0; i < 4; i++ {
		calls := 0
		winner, err := Execute(ctx, s, "assistant", func(_ context.Context, p Provider) (uuid.UUID, error) { calls++; return p.ID, nil })
		expected := first.ID
		if i%2 == 1 {
			expected = EnvProviderID
		}
		if err != nil || calls != 1 || winner != expected {
			t.Fatal("priority round-robin failed without routing schema")
		}
	}
}

func TestSharedRoundRobinAcrossInstancesAndRestart(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	first := NewService(NewRepository(pool), testCipher(t), Provider{}, testLogger(io.Discard))
	first.routing = &postgresRoutingStore{pool: pool}
	for i := 0; i < 2; i++ {
		in := input()
		in.Priority = 10
		if _, err := first.Save(ctx, uuid.Nil, actor, in); err != nil {
			t.Fatal(err)
		}
	}
	second := NewService(NewRepository(pool), testCipher(t), Provider{}, testLogger(io.Discard))
	second.routing = &postgresRoutingStore{pool: pool}
	providers, err := first.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		s := first
		if i%2 == 1 {
			s = second
		}
		calls := 0
		winner, err := Execute(ctx, s, "assistant", func(_ context.Context, p Provider) (uuid.UUID, error) { calls++; return p.ID, nil })
		if err != nil || calls != 1 || winner != providers[i%2].ID {
			t.Fatal("instances did not share one rotation")
		}
	}
	store := &postgresRoutingStore{pool: pool}
	cursor, err := store.NextRotation(ctx, 10, "assistant")
	if err != nil || cursor != 8 {
		t.Fatal("restart lost counter")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int64]bool{}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := store.NextRotation(ctx, 20, "writing")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[n] {
				t.Error("duplicate shared rotation ticket")
			}
			seen[n] = true
		}()
	}
	wg.Wait()
	if len(seen) != 40 {
		t.Fatal("rotation tickets lost")
	}
}

func TestRoutingSettingsStatsAndMissingMigration(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	s := NewConfigured(pool, config.Config{}, testLogger(io.Discard))
	rules, missing, err := s.Routing(ctx)
	if err != nil || missing || rules.Revision != 1 {
		t.Fatal("routing defaults unavailable")
	}
	rules.Mode = "sequential"
	rules.HedgeDelayMS = 0
	rules.MaxParallel = 4
	saved, err := s.SaveRouting(ctx, rules)
	if err != nil || saved.Revision != 2 || saved.HedgeDelayMS != 0 || saved.MaxParallel != 4 {
		t.Fatal("routing save failed")
	}
	if _, err := s.SaveRouting(ctx, rules); !errors.Is(err, ErrConflict) {
		t.Fatal("stale rules accepted")
	}
	loaded, missing, err := s.Routing(ctx)
	if err != nil || missing || loaded.HedgeDelayMS != 0 || loaded.MaxParallel != 4 {
		t.Fatal("routing settings not persisted")
	}
	for _, invalid := range []Routing{{Mode: "hedged", MaxParallel: 5, HedgeDelayMS: 0, Revision: 2}, {Mode: "hedged", MaxParallel: 4, HedgeDelayMS: -1, Revision: 2}} {
		if _, err := s.SaveRouting(ctx, invalid); !errors.Is(err, ErrValidation) {
			t.Fatal("invalid routing settings accepted")
		}
	}
	run := uuid.New()
	provider := uuid.New()
	for _, outcome := range []string{"success", "failure", "timeout", "cancelled"} {
		metric := CallMetric{ID: uuid.New(), RunID: run, ProviderID: provider, ProviderName: "Provider", Model: "model", Purpose: "assistant", StartedAt: time.Now(), DurationMS: 250, Outcome: outcome, Won: outcome == "success", Trigger: "primary", Mode: "hedged"}
		if err := s.routing.Record(ctx, metric); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.Stats(ctx, 7)
	if err != nil || len(stats.Models) != 1 || len(stats.Recent) != 4 {
		t.Fatalf("stats missing: %v", err)
	}
	row := stats.Models[0]
	if row.Calls != 4 || row.Successes != 1 || row.Failures != 2 || row.Timeouts != 1 || row.Cancelled != 1 || row.Wins != 1 || row.P50MS == nil || *row.P50MS != 250 {
		t.Fatal("stats included cancellations as failures or latency")
	}
	if _, err := pool.Exec(ctx, `DROP TABLE ai_provider_routing; DROP TABLE ai_provider_calls`); err != nil {
		t.Fatal(err)
	}
	_, missing, err = s.Routing(ctx)
	if err != nil || !missing {
		t.Fatal("routing readiness hidden")
	}
	stats, err = s.Stats(ctx, 7)
	if err != nil || !stats.MigrationRequired {
		t.Fatalf("stats readiness hidden: err=%v migrationRequired=%t", err, stats.MigrationRequired)
	}
}
