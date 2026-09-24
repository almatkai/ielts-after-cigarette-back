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

type expiryProvider struct{ questionID uuid.UUID }

func (p expiryProvider) PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (p expiryProvider) PublicStructure(context.Context, uuid.UUID, uuid.UUID) (any, error) {
	return nil, nil
}
func (p expiryProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (attempts.GradingMaterial, error) {
	return attempts.GradingMaterial{ExamType: "academic", Questions: []attempts.GradingQuestion{{ID: p.questionID, Points: 1, Answer: map[string]any{"value": "correct"}}}}, nil
}

func TestExpiredExamGradesDraftAndRejectsLateAnswer(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	attemptRepo := attempts.NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	questionID := uuid.New()
	providers := map[string]attempts.MaterialProvider{}
	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		providers[skill] = expiryProvider{questionID}
	}
	attemptService := attempts.NewService(attemptRepo, providers)
	svc := NewService(repo, attemptService)
	attemptService.SetExamGuard(svc)
	item, err := repo.Create(ctx, Test{ID: uuid.New(), Slug: "expiry-test", ExamType: "academic", Title: "Expiry test", DurationMinutes: 180,
		ListeningMaterialID: uuid.New(), ReadingMaterialID: uuid.New(), WritingMaterialID: uuid.New(), SpeakingMaterialID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Publish(ctx, item.ID, item.Revision); err != nil {
		t.Fatal(err)
	}
	session, _, err := svc.Start(ctx, user, item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	id := session.Sections[0].Attempt.ID
	if err := attemptService.SaveAnswers(ctx, user, id, attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: questionID, Answer: map[string]any{"value": "correct"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE full_mock_sessions SET started_at=CURRENT_TIMESTAMP-INTERVAL '4 hours' WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	_, err = attemptService.Submit(ctx, user, id, attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: questionID, Answer: map[string]any{"value": "late replacement"}}}})
	if !errors.Is(err, attempts.ErrExamDeadlineExceeded) {
		t.Fatalf("late submission: %v", err)
	}
	finished, err := svc.GetSession(ctx, user, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != SessionSubmitted || finished.Sections[0].Attempt.Score == nil || *finished.Sections[0].Attempt.Score != 1 {
		t.Fatalf("saved answer not graded: %+v", finished)
	}
	if finished.Sections[1].Attempt.Status != attempts.StatusAbandoned || finished.OverallBand != nil {
		t.Fatal("unopened section should not receive a grade")
	}
	var completed int
	if err := pool.QueryRow(ctx, `SELECT completed_tasks FROM user_skill_progress WHERE user_id=$1 AND skill='listening'`, user).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("duplicate grade: %d", completed)
	}
}

func TestAdvanceStartsSectionClockExactlyOnce(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	item, err := repo.Create(ctx, Test{ID: uuid.New(), Slug: "clock-test", ExamType: "academic", Title: "Clock test", DurationMinutes: 180,
		ListeningMaterialID: uuid.New(), ReadingMaterialID: uuid.New(), WritingMaterialID: uuid.New(), SpeakingMaterialID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{ID: uuid.New(), MockTestID: item.ID, UserID: user}
	var sections []SessionSection
	for i, skill := range []string{attempts.MaterialListening, attempts.MaterialReading, attempts.MaterialWriting, attempts.MaterialSpeaking} {
		sections = append(sections, SessionSection{Position: i + 1, Skill: skill, Attempt: attempts.Attempt{ID: uuid.New(), UserID: user, MaterialType: skill, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: attempts.StatusInProgress}})
	}
	if err := repo.CreateSession(ctx, session, sections); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE attempts SET started_at=CURRENT_TIMESTAMP-INTERVAL '30 minutes'`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := repo.Advance(ctx, session.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := saved[1].Attempt.StartedAt
	if started.Before(before) {
		t.Fatal("reading timer includes listening time")
	}
	if !saved[0].Attempt.StartedAt.Before(before) {
		t.Fatal("changed previous section clock")
	}
	if err := repo.Advance(ctx, session.ID, 2, false); err == nil {
		t.Fatal("duplicate advance accepted")
	}
	saved, err = repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !saved[1].Attempt.StartedAt.Equal(started) {
		t.Fatal("duplicate advance reset timer")
	}
}
