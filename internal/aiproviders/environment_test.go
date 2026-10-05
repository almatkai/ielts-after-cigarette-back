package aiproviders

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func environmentInput(p Provider) Input {
	p = environmentDisplay(p)
	return Input{Name: p.Name, Endpoint: p.Endpoint, Model: p.Model, SpeakingModel: p.SpeakingModel, Scopes: p.Scopes, Enabled: p.Enabled, Priority: p.Priority, TimeoutSeconds: p.TimeoutSeconds, Revision: p.Revision}
}

func TestEnvironmentVisibleEditableAndOrderedWithoutPersistingSecret(t *testing.T) {
	ctx := context.Background()
	repo := &memoryRepo{}
	env := Provider{Endpoint: "http://localhost:9876/v1/chat/completions?key=url-secret", Model: "env-model", APIKey: "actual-env-secret", TimeoutSeconds: 45}
	service := NewService(repo, testCipher(t), env, testLogger(io.Discard))
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 || !items[0].FromEnv || !items[0].HasKey || len(repo.items) != 0 {
		t.Fatal("environment not exposed as virtual provider")
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "key=") {
		t.Fatal("environment credential leaked")
	}
	edit := environmentInput(items[0])
	edit.Name = "Мой OpenRouter"
	edit.Priority = 0
	saved, err := service.Save(ctx, EnvProviderID, uuid.New(), edit)
	if err != nil || saved.Name != edit.Name || saved.Revision != 1 {
		t.Fatalf("rename/priority failed: %v", err)
	}
	record := repo.items[0]
	plaintext, err := service.cipher.Open(record.Ciphertext, record.aad())
	if err != nil || plaintext != "environment-credential-reference" || record.APIKey != "" || strings.Contains(record.Endpoint, "secret") {
		t.Fatal("environment key copied into database")
	}
	database, err := service.Save(ctx, uuid.Nil, uuid.New(), input())
	if err != nil {
		t.Fatal(err)
	}
	var order []uuid.UUID
	err = service.Run(ctx, "assistant", func(_ context.Context, p Provider) error {
		order = append(order, p.ID)
		if p.FromEnv {
			if p.APIKey != env.APIKey || p.Endpoint != env.Endpoint || p.Name != edit.Name {
				t.Fatal("runtime did not use trusted environment")
			}
			return ErrAllFailed
		}
		return nil
	})
	if err != nil || len(order) != 2 || order[0] != EnvProviderID || order[1] != database.ID {
		t.Fatal("environment priority ignored or provider retried twice")
	}
	items, _ = service.List(ctx)
	if items[0].ID != EnvProviderID {
		t.Fatal("display order differs from execution")
	}
	if _, err := service.Save(ctx, EnvProviderID, uuid.New(), edit); !errors.Is(err, ErrConflict) {
		t.Fatal("stale environment edit accepted")
	}
	unsafe := environmentInput(saved)
	unsafe.Endpoint = "https://attacker.example.com/v1/chat/completions"
	if _, err := service.Save(ctx, EnvProviderID, uuid.New(), unsafe); !errors.Is(err, ErrValidation) {
		t.Fatal("trusted credential could be redirected")
	}
	unsafe = environmentInput(saved)
	unsafe.APIKey = "replacement"
	if _, err := service.Save(ctx, EnvProviderID, uuid.New(), unsafe); !errors.Is(err, ErrValidation) {
		t.Fatal("environment key could be replaced")
	}
	if err := service.Delete(ctx, EnvProviderID, saved.Revision); !errors.Is(err, ErrValidation) {
		t.Fatal("environment provider could be deleted")
	}
	// Key/model changes are reflected on restart without reimporting credentials.
	env.APIKey = "rotated-env-secret"
	env.Model = "new-env-model"
	restarted := NewService(repo, testCipher(t), env, testLogger(io.Discard))
	p, err := restarted.loadEnvironment(ctx)
	if err != nil || p.APIKey != env.APIKey || p.Model != env.Model || p.Name != edit.Name || p.Priority != 0 {
		t.Fatal("environment rotation or metadata persistence broken")
	}
	// A database outage still permits the original emergency environment reserve.
	repo.listErr = errors.New("database unavailable")
	if err := restarted.Run(ctx, "assistant", func(_ context.Context, p Provider) error {
		if !p.FromEnv {
			t.Fatal("unexpected database provider")
		}
		return nil
	}); err != nil {
		t.Fatal("environment lost during database outage")
	}
}

func TestEnvironmentProbeUsesTrustedClientAndNeverWritesMetadata(t *testing.T) {
	repo := &memoryRepo{}
	service := NewService(repo, nil, Provider{Endpoint: "http://127.0.0.1:9876/v1/chat/completions", Model: "env-model", APIKey: "env-secret"}, testLogger(io.Discard))
	service.envClient = &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer env-secret" {
			t.Fatal("wrong environment credential")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`)), Request: r}, nil
	})}
	result, err := service.Test(context.Background(), EnvProviderID, nil)
	if err != nil || !result.OK || len(repo.items) != 0 {
		t.Fatal("environment probe failed or stored settings")
	}
}
