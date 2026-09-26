package auth

import (
	"context"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestGoogleRegistrationsHaveNoPasswordInDatabase(t *testing.T) {
	pool := testdb.Open(t)
	repository := NewPostgresRepository(pool)
	ctx := context.Background()
	now := time.Now().UTC()

	created, err := repository.CreateGoogleCompletedUser(ctx, "new@example.test", "New Student", "+77001234567", "google-new", now)
	if err != nil {
		t.Fatal(err)
	}
	leadID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, email, phone, status, google_sub, referral_code) VALUES ($1, $2, $3, 'WAITING', $4, 'lead1234')`, leadID, "lead@example.test", "+77001234568", "google-lead")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.CompleteWaitlistUser(ctx, leadID, "lead@example.test", "", "Lead Student", "+77001234568", "google-lead", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if completed.ID != leadID {
		t.Fatalf("waitlist lead was not reused: got %s, want %s", completed.ID, leadID)
	}

	for _, id := range []uuid.UUID{created.ID, completed.ID} {
		var passwordIsNull bool
		if err := pool.QueryRow(ctx, `SELECT password_hash IS NULL FROM users WHERE id = $1`, id).Scan(&passwordIsNull); err != nil {
			t.Fatal(err)
		}
		if !passwordIsNull {
			t.Fatalf("Google user %s must not have a password hash", id)
		}
	}
}
