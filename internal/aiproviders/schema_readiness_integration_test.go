package aiproviders

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestAdminListOpensBeforeProviderMigrationAndRecoversAfterMigration(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE ai_providers`); err != nil {
		t.Fatal(err)
	}
	service := NewService(NewRepository(pool), testCipher(t), Provider{Endpoint: "https://env.example.test/v1/chat/completions", Model: "existing-model", APIKey: "test-only-env-key", TimeoutSeconds: 45}, testLogger(io.Discard))
	handler := NewHandler(service, testLogger(io.Discard))
	check := func(migrationRequired bool) {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.List(recorder, httptest.NewRequest("GET", "/api/v1/admin/ai-providers", nil))
		if recorder.Code != 200 {
			t.Fatalf("GET provider settings with migrationRequired=%t: HTTP %d (want 200)", migrationRequired, recorder.Code)
		}
		var body struct {
			Items             []Provider `json:"items"`
			MigrationRequired bool       `json:"migrationRequired"`
			EnvConfigured     bool       `json:"envFallbackConfigured"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.MigrationRequired != migrationRequired || body.Items == nil || !body.EnvConfigured {
			t.Fatalf("incorrect readiness state: %+v", body)
		}
	}
	check(true)
	if _, err := service.Save(ctx, uuid.Nil, testdb.User(t, pool), input()); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("saving before migration must be blocked: %v", err)
	}
	if err := service.Delete(ctx, uuid.New(), 1); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("deleting before migration must be blocked: %v", err)
	}
	// Apply only the provider migration inside the isolated test schema; this
	// never touches the application's DATABASE_URL or working database.
	_, file, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/000031_ai_providers.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	check(false)
}
