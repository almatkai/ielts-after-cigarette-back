package aiproviders

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestEnvironmentMetadataPersistsAndDoesNotConsumeDatabaseCapacity(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	repo := NewRepository(pool)
	env := Provider{Endpoint: "https://env.example.test/v1/chat/completions", Model: "env-model", APIKey: "env-secret", TimeoutSeconds: 45}
	service := NewService(repo, testCipher(t), env, testLogger(io.Discard))
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 || items[0].Revision != 0 {
		t.Fatal("virtual environment missing")
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM ai_providers`).Scan(&count)
	if count != 0 {
		t.Fatal("GET wrote metadata")
	}
	for i := 0; i < MaxChain; i++ {
		if _, err := service.Save(ctx, uuid.Nil, actor, input()); err != nil {
			t.Fatal(err)
		}
	}
	in := environmentInput(items[0])
	in.Name = "Env primary"
	in.Priority = 0
	saved, err := service.Save(ctx, EnvProviderID, actor, in)
	if err != nil {
		t.Fatalf("environment blocked by capacity: %v", err)
	}
	if _, err := service.Save(ctx, uuid.Nil, actor, input()); !errors.Is(err, ErrValidation) {
		t.Fatal("database capacity not enforced")
	}
	if _, err := service.Save(ctx, EnvProviderID, actor, in); !errors.Is(err, ErrConflict) {
		t.Fatal("first-create stale revision accepted")
	}
	in.Revision = saved.Revision
	in.Priority = 30
	saved, err = service.Save(ctx, EnvProviderID, actor, in)
	if err != nil || saved.Revision != 2 {
		t.Fatal("environment revision not advanced")
	}
	restarted := NewService(repo, testCipher(t), env, testLogger(io.Discard))
	p, err := restarted.loadEnvironment(ctx)
	if err != nil || p.Name != in.Name || p.Priority != 30 || p.APIKey != env.APIKey {
		t.Fatal("metadata not preserved across service restarts")
	}
	record, err := repo.Get(ctx, EnvProviderID)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := service.cipher.Open(record.Ciphertext, record.aad())
	if err != nil || marker != "environment-credential-reference" {
		t.Fatal("environment credential persisted unexpectedly")
	}
}
