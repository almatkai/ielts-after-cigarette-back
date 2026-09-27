package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestRotateSessionAtomicCTE(t *testing.T) {
	pool := testdb.Open(t)
	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	// 1. Create a test user
	now := time.Now().UTC().Truncate(time.Microsecond)
	user, err := repo.CreateGoogleCompletedUser(
		ctx,
		"rotate-test-"+uuid.NewString()[:8]+"@example.test",
		"Rotate Tester",
		fmt.Sprintf("+7701%07d", time.Now().UnixNano()%10000000),
		"google-rotate-"+uuid.NewString()[:8],
		now,
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 2. Create initial session
	token1Hash := make([]byte, 32)
	rand.Read(token1Hash)
	session1ID := uuid.New()
	session1 := Session{
		ID:        session1ID,
		UserID:    user.ID,
		TokenHash: token1Hash,
		ExpiresAt: now.Add(24 * time.Hour),
		UserAgent: "Mozilla/5.0 Test",
		IPAddress: "127.0.0.1",
	}
	if err := repo.CreateSession(ctx, session1); err != nil {
		t.Fatalf("create initial session: %v", err)
	}

	// 3. Rotate session (Happy path: single CTE round-trip)
	token2Hash := make([]byte, 32)
	rand.Read(token2Hash)
	session2ID := uuid.New()
	session2 := Session{
		ID:        session2ID,
		UserID:    user.ID,
		TokenHash: token2Hash,
		ExpiresAt: now.Add(48 * time.Hour),
		UserAgent: "Mozilla/5.0 Test",
		IPAddress: "127.0.0.1",
	}

	start := time.Now()
	rotatedUserID, err := repo.RotateSession(ctx, token1Hash, session2, now.Add(time.Hour))
	elapsed := time.Since(start)
	t.Logf("Happy path RotateSession took %v", elapsed)

	if err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if rotatedUserID != user.ID {
		t.Fatalf("rotated user ID = %v, want %v", rotatedUserID, user.ID)
	}

	// Verify old session was revoked and replaced_by points to session2
	var revokedAt *time.Time
	var replacedBy *uuid.UUID
	err = pool.QueryRow(ctx, `SELECT revoked_at, replaced_by FROM refresh_sessions WHERE id = $1`, session1ID).
		Scan(&revokedAt, &replacedBy)
	if err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("expected old session to be revoked, but revoked_at is nil")
	}
	if replacedBy == nil || *replacedBy != session2ID {
		t.Fatalf("expected replaced_by = %v, got %v", session2ID, replacedBy)
	}

	// Verify replacement session is active
	var newRevokedAt *time.Time
	err = pool.QueryRow(ctx, `SELECT revoked_at FROM refresh_sessions WHERE id = $1`, session2ID).
		Scan(&newRevokedAt)
	if err != nil {
		t.Fatalf("query new session: %v", err)
	}
	if newRevokedAt != nil {
		t.Fatalf("expected replacement session to be active, got revoked_at = %v", newRevokedAt)
	}

	// 4. Token Reuse Detection
	// Trying to rotate using token1 again must trigger reuse detection and revoke session2
	token3Hash := make([]byte, 32)
	rand.Read(token3Hash)
	session3 := Session{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: token3Hash,
		ExpiresAt: now.Add(72 * time.Hour),
	}

	_, err = repo.RotateSession(ctx, token1Hash, session3, now.Add(2*time.Hour))
	if err != ErrRefreshReuse {
		t.Fatalf("expected ErrRefreshReuse on token reuse, got: %v", err)
	}

	// Verify session2 was revoked by the security reuse mitigation
	err = pool.QueryRow(ctx, `SELECT revoked_at FROM refresh_sessions WHERE id = $1`, session2ID).
		Scan(&newRevokedAt)
	if err != nil {
		t.Fatalf("query session2: %v", err)
	}
	if newRevokedAt == nil {
		t.Fatal("expected session2 to be revoked due to reuse attack detection, but was active")
	}
}
