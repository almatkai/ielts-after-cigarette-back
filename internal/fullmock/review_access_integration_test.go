package fullmock

import (
	"context"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestPendingMockCannotLeakThroughDetailHistoryOrMistakeBanks(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	owner := mockUser(t, pool, "academic")
	seedMockBank(t, pool, owner)
	var questionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT q.id FROM listening_questions q LIMIT 1`).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	providers := map[string]attempts.MaterialProvider{}
	for _, skill := range mockSkills {
		providers[skill] = expiryProvider{questionID}
	}
	attemptRepo := attempts.NewPostgresRepository(pool)
	attemptService := attempts.NewService(attemptRepo, providers)
	service := NewService(NewPostgresRepository(pool), attemptService)
	attemptService.SetExamGuard(service)
	session, _, err := service.StartGenerated(ctx, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	id := session.Sections[0].Attempt.ID
	if err := attemptRepo.SaveAnswers(ctx, id, []attempts.AnswerInput{{QuestionID: questionID, Answer: map[string]any{"value": "wrong"}}}); err != nil {
		t.Fatal(err)
	}
	execSeed(t, pool, `UPDATE attempt_answers SET is_correct=false,points_awarded=0 WHERE attempt_id=$1`, id)
	execSeed(t, pool, `UPDATE attempts SET status='SUBMITTED',score=31,max_score=40,band=7.5,submitted_at=CURRENT_TIMESTAMP WHERE id=$1`, id)
	execSeed(t, pool, `UPDATE full_mock_sessions SET current_section=2 WHERE id=$1`, session.ID)
	for _, role := range []string{"GUEST", "STUDENT"} {
		detail, err := attemptService.Get(auth.WithUser(ctx, owner, role), owner, id)
		if err != nil || !detail.ReviewLocked || detail.Band != nil || len(detail.Review) > 0 || len(detail.Answers) > 0 {
			t.Fatalf("%s detail leaks: %+v %v", role, detail, err)
		}
	}
	history, err := attemptRepo.ListHistory(ctx, owner, "listening", 10, attempts.HistoryCursor{})
	if err != nil || len(history.Items) != 1 || history.Items[0].Band != nil || history.Items[0].Score != nil || history.Items[0].MaxScore != nil {
		t.Fatalf("history leaks: %+v %v", history, err)
	}
	reports, err := attemptService.Mistakes(ctx, owner)
	if err != nil || len(reports) != 0 {
		t.Fatalf("legacy mistake bank leaks: %+v %v", reports, err)
	}
	bank, err := attemptRepo.ListMistakeAttempts(ctx, owner, "listening", 1, 10)
	if err != nil || len(bank.Items) != 0 {
		t.Fatalf("mistake bank leaks: %+v %v", bank, err)
	}
	// Completing the parent must reveal the original grade, not rewrite it.
	if _, err := service.Finish(ctx, owner, session.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := attemptService.Get(ctx, owner, id)
	if err != nil || detail.ReviewLocked || detail.Band == nil || *detail.Band != 7.5 || len(detail.Review) != 1 || detail.Review[0].CorrectAnswer["value"] != "correct" {
		t.Fatalf("finished detail: %+v %v", detail, err)
	}
	history, err = attemptRepo.ListHistory(ctx, owner, "listening", 10, attempts.HistoryCursor{})
	if err != nil || len(history.Items) != 1 || history.Items[0].Band == nil || *history.Items[0].Band != 7.5 {
		t.Fatalf("finished history: %+v %v", history, err)
	}
	bank, err = attemptRepo.ListMistakeAttempts(ctx, owner, "listening", 1, 10)
	if err != nil || len(bank.Items) != 1 {
		t.Fatalf("finished bank: %+v %v", bank, err)
	}
}
