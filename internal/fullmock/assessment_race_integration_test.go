package fullmock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

// Hold the same attempt lock used by submit/retry, begin closing concurrently,
// then commit the queued job. Readiness must be checked AFTER the lock wait,
// inside the transaction which closes the session (not in a stale service read).
func TestReportCloseSerializesWithAssessmentSubmission(t *testing.T) {
	for _, action := range []string{"finish", "final advance"} {
		for _, jobStatus := range []string{"QUEUED", "PROCESSING", "FAILED"} {
			t.Run(action+"/"+jobStatus, func(t *testing.T) {
				pool := testdb.Open(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				user := mockUser(t, pool, "academic")
				seedMockBank(t, pool, user)
				repo := NewPostgresRepository(pool)
				providers := map[string]attempts.MaterialProvider{}
				for _, skill := range mockSkills {
					providers[skill] = expiryProvider{uuid.New()}
				}
				service := NewService(repo, attempts.NewService(attempts.NewPostgresRepository(pool), providers))
				session, _, err := service.StartGenerated(ctx, user, false)
				if err != nil {
					t.Fatal(err)
				}
				sections, err := repo.ListSessionSections(ctx, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				// Simulate a submit/retry that has passed service validation but has not
				// yet committed. Keep attempt IN_PROGRESS for QUEUED/FAILED to prove the
				// authoritative job check also handles a mixed attempt/job snapshot.
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				id := sections[2].Attempt.ID
				if _, err := tx.Exec(ctx, `SELECT id FROM attempts WHERE id=$1 FOR UPDATE`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO writing_assessment_jobs(attempt_id,status) VALUES($1,$2)`, id, jobStatus); err != nil {
					t.Fatal(err)
				}
				if jobStatus == "PROCESSING" {
					if _, err := tx.Exec(ctx, `UPDATE attempts SET status='PROCESSING' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan error, 1)
				go func() {
					if action == "finish" {
						done <- repo.Finish(ctx, session.ID)
					} else {
						done <- repo.Advance(ctx, session.ID, 5, true)
					}
				}()
				// Wait until the close transaction is actually blocked in PostgreSQL,
				// avoiding a timing-only test that might never exercise the race.
				deadline := time.After(2 * time.Second)
				for {
					var waiting bool
					if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, int32(tx.Conn().PgConn().PID())).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case <-deadline:
						t.Fatal("close never waited for concurrent submit lock")
					case <-time.After(5 * time.Millisecond):
					}
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-done; !errors.Is(err, ErrAssessmentPending) {
					t.Fatalf("close exposed report after concurrent %s assessment: %v", jobStatus, err)
				}
				saved, err := repo.GetSession(ctx, session.ID)
				if err != nil || saved.Status != SessionInProgress {
					t.Fatalf("session closed: %+v %v", saved, err)
				}
			})
		}
	}
}
