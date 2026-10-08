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

func TestExpiredSectionGradesDraftAndLeavesNextSectionUntimed(t *testing.T) {
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
	if _, _, err := svc.GetSection(ctx, user, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	id := session.Sections[0].Attempt.ID
	if err := attemptService.SaveAnswers(ctx, user, id, attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: questionID, Answer: map[string]any{"value": "correct"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE full_mock_session_sections SET started_at=CURRENT_TIMESTAMP-INTERVAL '4 hours',deadline_at=CURRENT_TIMESTAMP-INTERVAL '1 minute' WHERE session_id=$1 AND position=1`, session.ID); err != nil {
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
	if finished.Status != SessionInProgress || finished.Sections[0].Attempt.Score == nil || *finished.Sections[0].Attempt.Score != 1 {
		t.Fatalf("saved answer not graded: %+v", finished)
	}
	if finished.Sections[1].Attempt.Status != attempts.StatusInProgress || finished.Sections[1].DeadlineAt != nil || finished.OverallBand != nil {
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

func TestOpeningStartsSectionClockExactlyOnce(t *testing.T) {
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
	if err := repo.StartSection(ctx, session.ID, 1, 30); err != nil {
		t.Fatal(err)
	}
	first, _ := repo.ListSessionSections(ctx, session.ID)
	listeningStart := first[0].Attempt.StartedAt
	if err := repo.Advance(ctx, session.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved[1].DeadlineAt != nil {
		t.Fatal("advancing started the next timer")
	}
	if err := repo.StartSection(ctx, session.ID, 2, 60); err != nil {
		t.Fatal(err)
	}
	saved, err = repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := saved[1].Attempt.StartedAt
	deadline := *saved[1].DeadlineAt
	if started.Before(before) || deadline.Sub(started) != time.Hour {
		t.Fatal("reading lost time before opening")
	}
	if !saved[0].Attempt.StartedAt.Equal(listeningStart) {
		t.Fatal("changed previous section clock")
	}
	if err := repo.StartSection(ctx, session.ID, 2, 60); err != nil {
		t.Fatal(err)
	}
	if err := repo.Advance(ctx, session.ID, 2, false); err == nil {
		t.Fatal("duplicate advance accepted")
	}
	saved, err = repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !saved[1].Attempt.StartedAt.Equal(started) || !saved[1].DeadlineAt.Equal(deadline) {
		t.Fatal("duplicate advance reset timer")
	}
}

// Real validation rejects empty AI answers before calling a provider; expiry
// must still unlock the next section rather than leaving the whole mock stuck.
type incompleteAIProvider struct{ expiryProvider }

func (p incompleteAIProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (attempts.GradingMaterial, error) {
	return attempts.GradingMaterial{ExamType: "academic", WritingTasks: []attempts.WritingTask{{ID: p.questionID}}, SpeakingParts: []attempts.SpeakingPart{{ID: p.questionID}}}, nil
}

func TestEmptyAISectionExpiresWithoutBlockingNextSection(t *testing.T) {
	for _, position := range []int{3, 4} {
		t.Run(mockSkills[position-1], func(t *testing.T) {
			pool := testdb.Open(t)
			ctx := context.Background()
			user := mockUser(t, pool, "academic")
			seedMockBank(t, pool, user)
			repo := NewPostgresRepository(pool)
			providers := map[string]attempts.MaterialProvider{}
			for _, skill := range mockSkills {
				providers[skill] = incompleteAIProvider{expiryProvider{uuid.New()}}
			}
			attemptService := attempts.NewService(attempts.NewPostgresRepository(pool), providers)
			svc := NewService(repo, attemptService)
			attemptService.SetExamGuard(svc)
			session, _, err := svc.StartGenerated(ctx, user, false)
			if err != nil {
				t.Fatal(err)
			}
			for next := 2; next <= position; next++ {
				if err := repo.Advance(ctx, session.ID, next, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := svc.GetSection(ctx, user, session.ID, position); err != nil {
				t.Fatal(err)
			}
			execSeed(t, pool, `UPDATE full_mock_session_sections SET started_at=CURRENT_TIMESTAMP-INTERVAL '2 hours',deadline_at=CURRENT_TIMESTAMP-INTERVAL '1 minute' WHERE session_id=$1 AND position=$2`, session.ID, position)
			saved, err := svc.GetSession(ctx, user, session.ID)
			if err != nil || saved.Status != SessionInProgress || saved.Sections[position-1].Attempt.Status != attempts.StatusAbandoned {
				t.Fatalf("expiry: %+v %v", saved, err)
			}
			advanced, err := svc.Advance(ctx, user, session.ID)
			if err != nil || advanced.CurrentSection != position+1 {
				t.Fatalf("advance: %+v %v", advanced, err)
			}
			if position < 4 && advanced.Sections[position].DeadlineAt != nil {
				t.Fatal("unopened next section consumed time")
			}
		})
	}
}
