package attempts

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type blockedAssessment struct{ release <-chan struct{} }

func (b blockedAssessment) Evaluate(context.Context, WritingEvaluationRequest) (WritingEvaluation, error) {
	<-b.release
	return WritingEvaluation{}, nil
}
func (b blockedAssessment) EvaluateSpeaking(context.Context, SpeakingEvaluationRequest) (SpeakingEvaluation, error) {
	<-b.release
	return SpeakingEvaluation{}, nil
}

func (s *stubRepository) QueueSpeakingAssessment(ctx context.Context, id uuid.UUID, answers []AnswerInput) error {
	if err := s.SaveAnswers(ctx, id, answers); err != nil {
		return err
	}
	a := s.attempts[id]
	a.Status = StatusProcessing
	s.attempts[id] = a
	return nil
}

func TestSaveReturnsWhileAIBlockedWithoutRedisOrSpeechPipeline(t *testing.T) {
	for _, skill := range []string{MaterialWriting, MaterialSpeaking} {
		t.Run(skill, func(t *testing.T) {
			release := make(chan struct{})
			repo := newStubRepository()
			evaluator := blockedAssessment{release: release}
			svc := NewService(repo, map[string]MaterialProvider{MaterialWriting: writingStubProvider(), MaterialSpeaking: speakingStubProvider()}, evaluator).
				WithSpeakingRecordingStore(t.TempDir(), 10<<20, evaluator)
			att, _, _, err := svc.Start(context.Background(), testUserID, skill, testMaterialID)
			if err != nil {
				t.Fatal(err)
			}
			answers := []AnswerInput{{QuestionID: questionChoiceID, Answer: map[string]any{"value": "My saved response"}}}
			if skill == MaterialWriting {
				answers = append(answers, AnswerInput{QuestionID: questionMatchingID, Answer: map[string]any{"value": "My saved essay"}})
			}
			done := make(chan error, 1)
			go func() {
				result, err := svc.Submit(context.Background(), testUserID, att.ID, SaveAnswersInput{Answers: answers})
				if err == nil && result.Status != StatusProcessing {
					err = ErrAlreadySubmitted
				}
				done <- err
			}()
			select {
			case err := <-done:
				close(release)
				if err != nil {
					t.Fatalf("save did not persist a background assessment: %v", err)
				}
				if len(repo.answers[att.ID]) != len(answers) {
					t.Fatal("save returned before answers persisted")
				}
				if repo.submitted != nil {
					t.Fatal("AI result was submitted during save")
				}
			case <-time.After(100 * time.Millisecond):
				close(release)
				<-done
				t.Fatal("save waited for blocked AI; must persist and return independently")
			}
		})
	}
}
