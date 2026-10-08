package guest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedClaimTrial(t *testing.T, pool *pgxpool.Pool, hash string, status string, expired bool) (Trial, uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	expires := time.Now().Add(time.Hour)
	if expired {
		expires = time.Now().Add(-time.Hour)
	}
	trial, err := NewPostgresRepository(pool).Create(ctx, []byte(hash), "academic", expires)
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO full_mock_sessions (id,user_id,exam_type,title,duration_minutes,status,current_section) VALUES ($1,$2,'academic','Trial',165,$3,4)`, session, trial.UserID, status); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{}
	for index, skill := range []string{"listening", "reading", "writing", "speaking"} {
		id := uuid.New()
		if _, err = pool.Exec(ctx, `INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id,status,band,score,max_score,submitted_at) VALUES ($1,$2,$3,$4,$5,'SUBMITTED',7.5,30,40,CURRENT_TIMESTAMP)`, id, trial.UserID, skill, uuid.New(), uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO full_mock_session_sections (session_id,position,skill,attempt_id) VALUES ($1,$2,$3,$4)`, session, index+1, skill, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return trial, session, ids
}

func TestClaimTransfersCompletedMockAndAllAttemptsOnce(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	account, other := testdb.User(t, pool), testdb.User(t, pool)
	trial, session, ids := seedClaimTrial(t, pool, "claim-token", "SUBMITTED", false)
	// A separate running account mock must not be changed by claiming the trial.
	active := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO full_mock_sessions (id,user_id,exam_type,title,duration_minutes) VALUES ($1,$2,'academic','Account',165)`, active, account); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := repo.Claim(ctx, []byte("claim-token"), account)
			if err != nil || id == nil || *id != session {
				t.Errorf("claim: id=%v err=%v", id, err)
			}
		}()
	}
	wg.Wait()
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM full_mock_sessions WHERE id=$1`, session).Scan(&owner); err != nil || owner != account {
		t.Fatalf("session owner=%s err=%v", owner, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attempts WHERE id=ANY($1) AND user_id=$2`, ids, account).Scan(&count); err != nil || count != 4 {
		t.Fatalf("attempts transferred=%d err=%v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT user_id FROM full_mock_sessions WHERE id=$1`, active).Scan(&owner); err != nil || owner != account {
		t.Fatal("account mock changed")
	}
	if _, err := repo.Claim(ctx, []byte("claim-token"), other); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("stolen trial: %v", err)
	}
	found, err := repo.Find(ctx, []byte("claim-token"))
	if err != nil || found.ClaimedBy == nil || *found.ClaimedBy != account || found.SessionID == nil || *found.SessionID != session {
		t.Fatalf("tombstone=%+v err=%v", found, err)
	}
	attemptRepo := attempts.NewPostgresRepository(pool)
	for _, id := range ids {
		item, err := attemptRepo.Get(ctx, id)
		if err != nil || item.UserID != account || item.Band == nil || *item.Band != 7.5 {
			t.Fatalf("grade lost: %+v %v", item, err)
		}
	}
	mockService := fullmock.NewService(fullmock.NewPostgresRepository(pool), attempts.NewService(attemptRepo, nil))
	if _, err := mockService.GetSession(auth.WithUser(ctx, trial.UserID, "GUEST"), trial.UserID, session); !errors.Is(err, fullmock.ErrSessionNotFound) {
		t.Fatalf("old guest accessed transferred mock: %v", err)
	}
	got, err := mockService.GetSession(auth.WithUser(ctx, account, "STUDENT"), account, session)
	if err != nil || got.OverallBand == nil || *got.OverallBand != 7.5 || got.Sections[0].Attempt.Band == nil {
		t.Fatalf("account report did not unlock: %+v %v", got, err)
	}
	if _, err := mockService.GetSession(auth.WithUser(ctx, other, "STUDENT"), other, session); !errors.Is(err, fullmock.ErrSessionNotFound) {
		t.Fatalf("foreign account accessed mock: %v", err)
	}
}

func TestConcurrentAccountsCannotBothClaimTheSameTrial(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	_, session, _ := seedClaimTrial(t, pool, "contested", "SUBMITTED", false)
	accounts := []uuid.UUID{testdb.User(t, pool), testdb.User(t, pool)}
	results := make(chan error, len(accounts))
	start := make(chan struct{})
	for _, account := range accounts {
		go func() {
			<-start
			id, err := repo.Claim(ctx, []byte("contested"), account)
			if err == nil && (id == nil || *id != session) {
				err = errors.New("claim returned the wrong session")
			}
			results <- err
		}()
	}
	close(start)
	winners, denied := 0, 0
	for range accounts {
		err := <-results
		if err == nil {
			winners++
		} else if errors.Is(err, ErrAlreadyClaimed) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || denied != 1 {
		t.Fatalf("winners=%d denied=%d", winners, denied)
	}
}

func TestClaimDoesNotTransferExpiredRunningOrUnknownTrial(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	account := testdb.User(t, pool)
	for _, tc := range []struct {
		hash, status string
		expired      bool
	}{
		{"running", "IN_PROGRESS", false}, {"expired", "SUBMITTED", true},
	} {
		trial, session, _ := seedClaimTrial(t, pool, tc.hash, tc.status, tc.expired)
		id, err := repo.Claim(ctx, []byte(tc.hash), account)
		if err != nil || id != nil {
			t.Fatalf("invalid trial claimed: %v %v", id, err)
		}
		var owner uuid.UUID
		if err = pool.QueryRow(ctx, `SELECT user_id FROM full_mock_sessions WHERE id=$1`, session).Scan(&owner); err != nil || owner != trial.UserID {
			t.Fatal("unclaimable trial moved")
		}
	}
	if id, err := repo.Claim(ctx, []byte("forged"), account); err != nil || id != nil {
		t.Fatalf("forged cookie claimed: %v %v", id, err)
	}
}
