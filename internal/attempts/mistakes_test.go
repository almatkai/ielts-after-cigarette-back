package attempts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// loadBarrier makes every load of one mistakes page wait until all of them have
// started. A page that ran its loads one after another would never get past the
// barrier, so the timeout turns a sequential implementation into a failure
// instead of a silently slow response.
type loadBarrier struct {
	mu      sync.Mutex
	total   int
	arrived int
	release chan struct{}
}

func newLoadBarrier(total int) *loadBarrier {
	return &loadBarrier{total: total, release: make(chan struct{})}
}

func (b *loadBarrier) wait() error {
	b.mu.Lock()
	b.arrived++
	complete := b.arrived == b.total
	b.mu.Unlock()
	if complete {
		close(b.release)
	}
	select {
	case <-b.release:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("mistakes loads did not run concurrently")
	}
}

// gatedRepository signs in at the barrier from every bulk load the page makes,
// so the test observes which loads are in flight at the same time.
type gatedRepository struct {
	*stubRepository
	gate *loadBarrier
}

func (r *gatedRepository) ListAnswersByAttempts(ctx context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID][]Answer, error) {
	if err := r.gate.wait(); err != nil {
		return nil, err
	}
	return r.stubRepository.ListAnswersByAttempts(ctx, attemptIDs)
}

func (r *gatedRepository) GetWritingEvaluations(_ context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID]WritingEvaluation, error) {
	if err := r.gate.wait(); err != nil {
		return nil, err
	}
	items := map[uuid.UUID]WritingEvaluation{}
	for _, attemptID := range attemptIDs {
		if evaluation, ok := r.writingEvaluations[attemptID]; ok {
			items[attemptID] = evaluation
		}
	}
	return items, nil
}

func (r *gatedRepository) GetSpeakingEvaluations(_ context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID]SpeakingEvaluation, error) {
	if err := r.gate.wait(); err != nil {
		return nil, err
	}
	items := map[uuid.UUID]SpeakingEvaluation{}
	for _, attemptID := range attemptIDs {
		if evaluation, ok := r.speakingEvaluations[attemptID]; ok {
			items[attemptID] = evaluation
		}
	}
	return items, nil
}

// gatedProvider signs in at the barrier from the single-version grading load
// that a material type without a bulk loader falls back to.
type gatedProvider struct {
	stubProvider
	gate *loadBarrier
}

func (p gatedProvider) GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error) {
	if err := p.gate.wait(); err != nil {
		return GradingMaterial{}, err
	}
	return p.stubProvider.GradingStructure(ctx, materialID, versionID)
}

// submittedAttemptsForMistakes records one submitted attempt of every skill so
// the mistakes page has to run all of its loads.
func submittedAttemptsForMistakes(repository *stubRepository, userID uuid.UUID) []Attempt {
	attempts := []Attempt{
		{ID: uuid.New(), UserID: userID, MaterialType: MaterialListening, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusSubmitted},
		{ID: uuid.New(), UserID: userID, MaterialType: MaterialReading, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusSubmitted},
		{ID: uuid.New(), UserID: userID, MaterialType: MaterialWriting, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusSubmitted},
		{ID: uuid.New(), UserID: userID, MaterialType: MaterialSpeaking, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusSubmitted},
	}
	for _, attempt := range attempts {
		repository.attempts[attempt.ID] = attempt
	}
	wrong := false
	for _, attempt := range attempts[:2] {
		repository.answers[attempt.ID] = []Answer{
			{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "A"}, IsCorrect: &wrong},
		}
	}
	repository.writingEvaluations[attempts[2].ID] = WritingEvaluation{AttemptID: attempts[2].ID, OverallBand: 6.5}
	repository.speakingEvaluations[attempts[3].ID] = SpeakingEvaluation{AttemptID: attempts[3].ID, OverallBand: 6}
	return attempts
}

func TestMistakesLoadsEveryPhaseConcurrently(t *testing.T) {
	repository := newStubRepository()
	userID := uuid.New()
	attempts := submittedAttemptsForMistakes(repository, userID)
	// Answers, the material of both objective skills, and the AI feedback of
	// Writing and Speaking all have to be in flight at the same time.
	gate := newLoadBarrier(5)
	service := NewService(
		&gatedRepository{stubRepository: repository, gate: gate},
		map[string]MaterialProvider{
			MaterialListening: gatedProvider{stubProvider: listeningStubProvider(), gate: gate},
			MaterialReading:   gatedProvider{stubProvider: readingStubProvider("academic"), gate: gate},
		},
	)

	reports, err := service.Mistakes(context.Background(), userID)
	if err != nil {
		t.Fatalf("mistakes failed: %v", err)
	}
	if len(reports) != len(attempts) {
		t.Fatalf("reports = %d, want %d", len(reports), len(attempts))
	}
	byType := map[string]MistakeReport{}
	for _, report := range reports {
		byType[report.Attempt.MaterialType] = report
	}
	listening, ok := byType[MaterialListening]
	if !ok {
		t.Fatal("listening attempt is missing from the report")
	}
	// The seeded wrong answer and the two questions the stub material has and
	// nobody answered are all mistakes, and the review carries the prompt of
	// the material the page loaded for the attempt.
	var answered *ReviewAnswer
	for index := range listening.Review {
		if listening.Review[index].QuestionID == questionChoiceID {
			answered = &listening.Review[index]
		}
	}
	if answered == nil || answered.Prompt != "Q1" || answered.IsCorrect {
		t.Fatalf("listening review = %+v, want the seeded wrong answer with its prompt", listening.Review)
	}
	writing, ok := byType[MaterialWriting]
	if !ok || writing.WritingEvaluation == nil || writing.WritingEvaluation.OverallBand != 6.5 {
		t.Fatalf("writing report = %+v, want the seeded evaluation", writing)
	}
	speaking, ok := byType[MaterialSpeaking]
	if !ok || speaking.SpeakingEvaluation == nil || speaking.SpeakingEvaluation.OverallBand != 6 {
		t.Fatalf("speaking report = %+v, want the seeded evaluation", speaking)
	}
}

// failingRepository fails the answer load slowly and the speaking load at once,
// which is the case where the phase that fails first is not the phase that is
// reported.
type failingRepository struct {
	*stubRepository
	answersErr error
}

func (r *failingRepository) ListAnswersByAttempts(context.Context, []uuid.UUID) (map[uuid.UUID][]Answer, error) {
	time.Sleep(50 * time.Millisecond)
	return nil, r.answersErr
}

func (r *failingRepository) GetSpeakingEvaluations(context.Context, []uuid.UUID) (map[uuid.UUID]SpeakingEvaluation, error) {
	return nil, errors.New("speaking feedback unavailable")
}

func TestMistakesReportsTheEarliestPhaseError(t *testing.T) {
	repository := newStubRepository()
	userID := uuid.New()
	submittedAttemptsForMistakes(repository, userID)
	answersErr := errors.New("answers unavailable")
	service := NewService(
		&failingRepository{stubRepository: repository, answersErr: answersErr},
		map[string]MaterialProvider{
			MaterialListening: listeningStubProvider(),
			MaterialReading:   readingStubProvider("academic"),
		},
	)

	_, err := service.Mistakes(context.Background(), userID)
	if !errors.Is(err, answersErr) {
		t.Fatalf("error = %v, want the answer load error of the earliest phase", err)
	}
}

func TestMistakesRejectsUnsupportedObjectiveMaterial(t *testing.T) {
	repository := newStubRepository()
	userID := uuid.New()
	submittedAttemptsForMistakes(repository, userID)
	service := NewService(repository, map[string]MaterialProvider{})

	if _, err := service.Mistakes(context.Background(), userID); !errors.Is(err, ErrUnsupportedMaterial) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedMaterial)
	}
}

func TestMistakesSkipsAttemptsStillInProgress(t *testing.T) {
	repository := newStubRepository()
	userID := uuid.New()
	submitted := submittedAttemptsForMistakes(repository, userID)
	repository.attempts[uuid.New()] = Attempt{
		ID: uuid.New(), UserID: userID, MaterialType: MaterialListening,
		MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusInProgress,
	}
	gate := newLoadBarrier(5)
	service := NewService(
		&gatedRepository{stubRepository: repository, gate: gate},
		map[string]MaterialProvider{
			MaterialListening: gatedProvider{stubProvider: listeningStubProvider(), gate: gate},
			MaterialReading:   gatedProvider{stubProvider: readingStubProvider("academic"), gate: gate},
		},
	)

	reports, err := service.Mistakes(context.Background(), userID)
	if err != nil {
		t.Fatalf("mistakes failed: %v", err)
	}
	if len(reports) != len(submitted) {
		t.Fatalf("reports = %d, want %d", len(reports), len(submitted))
	}
}

func TestMistakesReturnsEmptyPageWithoutSubmittedAttempts(t *testing.T) {
	repository := newStubRepository()
	repository.attempts[uuid.New()] = Attempt{
		ID: uuid.New(), UserID: testUserID, MaterialType: MaterialListening,
		MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: StatusInProgress,
	}
	service := NewService(repository, map[string]MaterialProvider{})

	reports, err := service.Mistakes(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("mistakes failed: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports = %d, want none", len(reports))
	}
}
