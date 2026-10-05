package aiproviders

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestOnlyMissingTableIsMigrationReadiness(t *testing.T) {
	for _, code := range []string{"42P01", "42501", "08006", "42703"} {
		original := &pgconn.PgError{Code: code, Message: "redacted"}
		mapped := storageError(original)
		if errors.Is(mapped, ErrMigrationRequired) != (code == "42P01") {
			t.Fatalf("SQL state %s was misclassified", code)
		}
	}
}

func TestAdminListDoesNotHideUnexpectedStorageErrors(t *testing.T) {
	service := NewService(&memoryRepo{listErr: errors.New("unavailable storage")}, nil, Provider{}, testLogger(io.Discard))
	recorder := httptest.NewRecorder()
	NewHandler(service, testLogger(io.Discard)).List(recorder, httptest.NewRequest("GET", "/api/v1/admin/ai-providers", nil))
	if recorder.Code != 500 {
		t.Fatalf("unexpected storage error hidden: HTTP %d", recorder.Code)
	}
}
