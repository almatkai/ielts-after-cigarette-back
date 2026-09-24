package attempts

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestWritingFailurePreservesFinalAnswers(t *testing.T) {
	ctx := context.Background()
	repo := newStubRepository()
	svc := NewService(repo, map[string]MaterialProvider{MaterialWriting: writingStubProvider()}, mockWritingEvaluator{err: ErrAIEvaluationFailed})
	att, _, _, err := svc.Start(ctx, testUserID, MaterialWriting, testMaterialID)
	if err != nil {
		t.Fatal(err)
	}
	input := SaveAnswersInput{Answers: []AnswerInput{
		{QuestionID: questionChoiceID, Answer: map[string]any{"value": "Final task one"}},
		{QuestionID: questionMatchingID, Answer: map[string]any{"value": "Final task two"}},
	}}
	if _, err := svc.Submit(ctx, testUserID, att.ID, input); !errors.Is(err, ErrAIEvaluationFailed) {
		t.Fatalf("got %v", err)
	}
	detail, err := svc.Get(ctx, testUserID, att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != StatusInProgress || len(detail.Answers) != 2 {
		t.Fatalf("lost draft: %+v", detail)
	}
	for _, answer := range detail.Answers {
		if answer.Answer["value"] != "Final task one" && answer.Answer["value"] != "Final task two" {
			t.Fatalf("unexpected answer: %+v", answer)
		}
	}
}

type pinnedProvider struct {
	stubProvider
	requested uuid.UUID
}

func (p *pinnedProvider) PublicStructure(_ context.Context, _, version uuid.UUID) (any, error) {
	p.requested = version
	return map[string]any{"version": version}, nil
}

func TestReviewMaterialUsesPinnedVersionAndChecksOwner(t *testing.T) {
	ctx := context.Background()
	repo := newStubRepository()
	provider := &pinnedProvider{stubProvider: stubProvider{publishedVersionID: testVersionID}}
	svc := NewService(repo, map[string]MaterialProvider{MaterialReading: provider})
	att, _, _, err := svc.Start(ctx, testUserID, MaterialReading, testMaterialID)
	if err != nil {
		t.Fatal(err)
	}
	provider.publishedVersionID = uuid.New()
	_, _, err = svc.PublicMaterial(ctx, testUserID, att.ID)
	if err != nil || provider.requested != testVersionID {
		t.Fatalf("wrong version %s: %v", provider.requested, err)
	}
	if _, _, err := svc.PublicMaterial(ctx, testOtherUser, att.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign material exposed: %v", err)
	}
	svc.SetExamGuard(mockExamGuard{err: ErrSectionLocked})
	if _, _, err := svc.PublicMaterial(ctx, testUserID, att.ID); !errors.Is(err, ErrSectionLocked) {
		t.Fatalf("locked section exposed: %v", err)
	}
}

func TestAbandonedWritingDraftCanBeReadWithoutEvaluation(t *testing.T) {
	ctx := context.Background()
	repo := newStubRepository()
	id := uuid.New()
	repo.attempts[id] = Attempt{ID: id, UserID: testUserID, MaterialType: MaterialWriting, Status: StatusAbandoned}
	repo.answers[id] = []Answer{{QuestionID: questionChoiceID, Answer: map[string]any{"value": "unfinished work"}}}
	detail, err := NewService(repo, nil).Get(ctx, testUserID, id)
	if err != nil || len(detail.Answers) != 1 {
		t.Fatalf("draft unavailable: %+v, %v", detail, err)
	}
}
