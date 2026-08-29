package attempts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testUserID    = uuid.New()
	testOtherUser = uuid.New()
	testTestID    = uuid.New()
	testVersionID = uuid.New()

	testMaterialID        = uuid.New()
	testMaterialVersionID = uuid.New()

	questionChoiceID   = uuid.New()
	questionMatchingID = uuid.New()
	questionGapID      = uuid.New()

	questionTFNGID  = uuid.New()
	questionMatchID = uuid.New()
	questionSumID   = uuid.New()
)

type stubRepository struct {
	attempts  map[uuid.UUID]Attempt
	answers   map[uuid.UUID][]Answer
	submitted *SubmitResult
	createErr error
}

func newStubRepository() *stubRepository {
	return &stubRepository{
		attempts: map[uuid.UUID]Attempt{},
		answers:  map[uuid.UUID][]Answer{},
	}
}

func (s *stubRepository) FindInProgress(_ context.Context, userID uuid.UUID, materialType string, materialID uuid.UUID) (Attempt, error) {
	for _, attempt := range s.attempts {
		if attempt.UserID == userID && attempt.MaterialType == materialType &&
			attempt.MaterialID == materialID && attempt.Status == StatusInProgress {
			return attempt, nil
		}
	}
	return Attempt{}, ErrNotFound
}

func (s *stubRepository) Create(_ context.Context, attempt Attempt) (Attempt, error) {
	if s.createErr != nil {
		return Attempt{}, s.createErr
	}
	attempt.Status = StatusInProgress
	attempt.StartedAt = time.Now()
	s.attempts[attempt.ID] = attempt
	return attempt, nil
}

func (s *stubRepository) Get(_ context.Context, id uuid.UUID) (Attempt, error) {
	attempt, ok := s.attempts[id]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	return attempt, nil
}

func (s *stubRepository) ListByUser(_ context.Context, userID uuid.UUID, materialType string) ([]Summary, error) {
	items := []Summary{}
	for _, attempt := range s.attempts {
		if attempt.UserID != userID {
			continue
		}
		if materialType != "" && attempt.MaterialType != materialType {
			continue
		}
		items = append(items, Summary{Attempt: attempt, TestTitle: "Material", TestSlug: "material"})
	}
	return items, nil
}

func (s *stubRepository) SaveAnswers(_ context.Context, attemptID uuid.UUID, answers []AnswerInput) error {
	saved := s.answers[attemptID]
	for _, item := range answers {
		replaced := false
		for i := range saved {
			if saved[i].QuestionID == item.QuestionID {
				saved[i].Answer = item.Answer
				replaced = true
			}
		}
		if !replaced {
			saved = append(saved, Answer{QuestionID: item.QuestionID, Answer: item.Answer})
		}
	}
	s.answers[attemptID] = saved
	return nil
}

func (s *stubRepository) ListAnswers(_ context.Context, attemptID uuid.UUID) ([]Answer, error) {
	return s.answers[attemptID], nil
}

func (s *stubRepository) Submit(_ context.Context, result SubmitResult) error {
	attempt, ok := s.attempts[result.AttemptID]
	if !ok {
		return ErrNotFound
	}
	if attempt.Status != StatusInProgress {
		return ErrAlreadySubmitted
	}
	s.submitted = &result
	attempt.Status = StatusSubmitted
	attempt.Score = &result.Score
	attempt.MaxScore = &result.MaxScore
	attempt.Band = &result.Band
	now := time.Now()
	attempt.SubmittedAt = &now
	s.attempts[attempt.ID] = attempt
	s.answers[result.AttemptID] = result.Answers
	return nil
}

type stubProvider struct {
	publishedVersionID uuid.UUID
	publishedErr       error
	public             any
	grading            GradingMaterial
	gradingErr         error
}

func (s stubProvider) PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	if s.publishedErr != nil {
		return uuid.Nil, s.publishedErr
	}
	return s.publishedVersionID, nil
}

func (s stubProvider) PublicStructure(context.Context, uuid.UUID, uuid.UUID) (any, error) {
	return s.public, s.gradingErr
}

func (s stubProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (GradingMaterial, error) {
	return s.grading, s.gradingErr
}

func listeningStubProvider() stubProvider {
	return stubProvider{
		publishedVersionID: testVersionID,
		public:             map[string]any{"id": testTestID, "title": "Cambridge 18 Test 1"},
		grading: GradingMaterial{
			ExamType: "academic",
			Questions: []GradingQuestion{
				{ID: questionChoiceID, Number: 1, Prompt: "Q1", Points: 1, Answer: map[string]any{"optionId": "B"}},
				{ID: questionMatchingID, Number: 2, Prompt: "Q2", Points: 1, Answer: map[string]any{"optionIds": []any{"A", "C"}}},
				{ID: questionGapID, Number: 3, Prompt: "Q3", Points: 2, Answer: map[string]any{"accepted": []any{"Green Street", "green street"}}},
			},
		},
	}
}

func readingStubProvider(examType string) stubProvider {
	return stubProvider{
		publishedVersionID: testMaterialVersionID,
		public:             map[string]any{"id": testMaterialID, "title": "Reading Passage 1"},
		grading: GradingMaterial{
			ExamType: examType,
			Questions: []GradingQuestion{
				{ID: questionTFNGID, Number: 1, Prompt: "The library moved.", Points: 1, Answer: map[string]any{"value": "TRUE"}},
				{ID: questionMatchID, Number: 2, Prompt: "Match the heading.", Points: 1, Answer: map[string]any{"optionId": "C"}},
				{ID: questionSumID, Number: 3, Prompt: "Complete the summary.", Points: 2, Answer: map[string]any{"accepted": []any{"North Campus"}}},
			},
		},
	}
}

func testService() (*Service, *stubRepository) {
	repository := newStubRepository()
	service := NewService(repository, map[string]MaterialProvider{
		MaterialListening: listeningStubProvider(),
		MaterialReading:   readingStubProvider("academic"),
	})
	return service, repository
}

func startForTest(t *testing.T, service *Service, userID uuid.UUID) Attempt {
	t.Helper()
	attempt, _, created, err := service.Start(context.Background(), userID, MaterialListening, testTestID)
	if err != nil || !created {
		t.Fatalf("start failed: created=%v err=%v", created, err)
	}
	return attempt
}

func TestStartCreatesAttemptAndReturnsExisting(t *testing.T) {
	service, repository := testService()
	attempt := startForTest(t, service, testUserID)
	if attempt.MaterialType != MaterialListening || attempt.MaterialID != testTestID ||
		attempt.MaterialVersionID != testVersionID || attempt.Status != StatusInProgress {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}

	again, _, created, err := service.Start(context.Background(), testUserID, MaterialListening, testTestID)
	if err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	if created || again.ID != attempt.ID {
		t.Fatalf("expected existing attempt %v, got %+v (created=%v)", attempt.ID, again, created)
	}
	if len(repository.attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(repository.attempts))
	}
}

func TestStartFailsWhenMaterialNotPublished(t *testing.T) {
	repository := newStubRepository()
	provider := listeningStubProvider()
	provider.publishedErr = ErrMaterialNotFound
	service := NewService(repository, map[string]MaterialProvider{MaterialListening: provider})
	if _, _, _, err := service.Start(context.Background(), testUserID, MaterialListening, testTestID); !errors.Is(err, ErrMaterialNotFound) {
		t.Fatalf("expected ErrMaterialNotFound, got %v", err)
	}
}

func TestStartFailsForUnsupportedMaterialType(t *testing.T) {
	service, _ := testService()
	if _, _, _, err := service.Start(context.Background(), testUserID, "writing", testTestID); !errors.Is(err, ErrUnsupportedMaterial) {
		t.Fatalf("expected ErrUnsupportedMaterial, got %v", err)
	}
}

func TestGradeOptionID(t *testing.T) {
	correct := map[string]any{"optionId": "B"}
	if !gradeAnswer(correct, map[string]any{"optionId": "B"}) {
		t.Fatal("exact match must be correct")
	}
	if gradeAnswer(correct, map[string]any{"optionId": "b"}) {
		t.Fatal("optionId comparison must be case-sensitive")
	}
	if gradeAnswer(correct, map[string]any{"optionId": "A"}) {
		t.Fatal("wrong option must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{}) {
		t.Fatal("empty answer must be incorrect")
	}
}

func TestGradeOptionIDs(t *testing.T) {
	correct := map[string]any{"optionIds": []any{"A", "C"}}
	if !gradeAnswer(correct, map[string]any{"optionIds": []any{"C", "A"}}) {
		t.Fatal("optionIds comparison must ignore order")
	}
	if gradeAnswer(correct, map[string]any{"optionIds": []any{"A"}}) {
		t.Fatal("partial selection must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{"optionIds": []any{"A", "B"}}) {
		t.Fatal("wrong option in selection must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{"optionIds": []any{"A", "A"}}) {
		t.Fatal("duplicated option must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{}) {
		t.Fatal("empty answer must be incorrect")
	}
}

func TestGradeValue(t *testing.T) {
	correct := map[string]any{"value": "NOT_GIVEN"}
	if !gradeAnswer(correct, map[string]any{"value": "NOT_GIVEN"}) {
		t.Fatal("exact value must be correct")
	}
	if !gradeAnswer(correct, map[string]any{"value": " not_given "}) {
		t.Fatal("value comparison must trim and ignore case")
	}
	if gradeAnswer(correct, map[string]any{"value": "TRUE"}) {
		t.Fatal("wrong value must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{"value": "  "}) {
		t.Fatal("blank value must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{}) {
		t.Fatal("missing value must be incorrect")
	}
}

func TestGradeAccepted(t *testing.T) {
	correct := map[string]any{"accepted": []any{"Green Street", "green street"}}
	if !gradeAnswer(correct, map[string]any{"value": "  GREEN street "}) {
		t.Fatal("accepted comparison must trim and lower-case")
	}
	if gradeAnswer(correct, map[string]any{"value": "Red Street"}) {
		t.Fatal("wrong value must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{"value": "   "}) {
		t.Fatal("blank value must be incorrect")
	}
	if gradeAnswer(correct, map[string]any{}) {
		t.Fatal("missing value must be incorrect")
	}
}

func TestListeningBandBoundaries(t *testing.T) {
	cases := []struct {
		raw  int
		band float64
	}{
		{40, 9.0}, {39, 9.0}, {38, 8.5}, {37, 8.5}, {36, 8.0}, {35, 8.0},
		{34, 7.5}, {32, 7.5}, {31, 7.0}, {30, 7.0}, {29, 6.5}, {26, 6.5},
		{25, 6.0}, {23, 6.0}, {22, 5.5}, {18, 5.5}, {17, 5.0}, {16, 5.0},
		{15, 4.5}, {13, 4.5}, {12, 4.0}, {10, 4.0}, {9, 3.5}, {8, 3.5},
		{7, 3.0}, {6, 3.0}, {5, 2.5}, {4, 2.5}, {3, 0.0}, {0, 0.0},
	}
	for _, tc := range cases {
		if band := bandFor(MaterialListening, "", tc.raw, 40); band != tc.band {
			t.Errorf("listening raw %d: expected band %.1f, got %.1f", tc.raw, tc.band, band)
		}
	}
}

func TestReadingAcademicBandBoundaries(t *testing.T) {
	cases := []struct {
		raw  int
		band float64
	}{
		{40, 9.0}, {39, 9.0}, {38, 8.5}, {37, 8.5}, {36, 8.0}, {35, 8.0},
		{34, 7.5}, {33, 7.5}, {32, 7.0}, {30, 7.0}, {29, 6.5}, {27, 6.5},
		{26, 6.0}, {23, 6.0}, {22, 5.5}, {19, 5.5}, {18, 5.0}, {15, 5.0},
		{14, 4.5}, {13, 4.5}, {12, 4.0}, {10, 4.0}, {9, 3.5}, {8, 3.5},
		{7, 3.0}, {6, 3.0}, {5, 2.5}, {4, 2.5}, {3, 0.0}, {0, 0.0},
	}
	for _, tc := range cases {
		if band := bandFor(MaterialReading, "academic", tc.raw, 40); band != tc.band {
			t.Errorf("reading academic raw %d: expected band %.1f, got %.1f", tc.raw, tc.band, band)
		}
	}
}

func TestReadingGeneralBandBoundaries(t *testing.T) {
	cases := []struct {
		raw  int
		band float64
	}{
		{40, 9.0}, {39, 8.5}, {38, 8.0}, {37, 8.0}, {36, 7.5}, {35, 7.0},
		{34, 7.0}, {33, 6.5}, {32, 6.5}, {31, 6.0}, {30, 6.0}, {29, 5.5},
		{27, 5.5}, {26, 5.0}, {23, 5.0}, {22, 4.5}, {19, 4.5}, {18, 4.0},
		{15, 4.0}, {14, 3.5}, {12, 3.5}, {11, 3.0}, {9, 3.0}, {8, 2.5},
		{6, 2.5}, {5, 0.0}, {0, 0.0},
	}
	for _, tc := range cases {
		if band := bandFor(MaterialReading, "general", tc.raw, 40); band != tc.band {
			t.Errorf("reading general raw %d: expected band %.1f, got %.1f", tc.raw, tc.band, band)
		}
	}
}

func TestBandScalesToFortyQuestions(t *testing.T) {
	if band := bandFor(MaterialListening, "", 20, 20); band != 9.0 {
		t.Errorf("20/20 should scale to raw 40 and band 9.0, got %.1f", band)
	}
	if band := bandFor(MaterialListening, "", 10, 20); band != 5.5 {
		t.Errorf("10/20 should scale to raw 20 and band 5.5, got %.1f", band)
	}
	if band := bandFor(MaterialListening, "", 1, 3); band != 4.5 {
		t.Errorf("1/3 should scale to raw 13 and band 4.5, got %.1f", band)
	}
	if band := bandFor(MaterialListening, "", 0, 20); band != 0.0 {
		t.Errorf("0/20 should be band 0.0, got %.1f", band)
	}
	// Same raw score gives different reading bands by exam type.
	if academic, general := bandFor(MaterialReading, "academic", 34, 40), bandFor(MaterialReading, "general", 34, 40); academic != 7.5 || general != 7.0 {
		t.Errorf("raw 34: expected academic 7.5 and general 7.0, got %.1f and %.1f", academic, general)
	}
}

func TestSubmitGradesAndUpdatesProgress(t *testing.T) {
	service, repository := testService()
	attempt := startForTest(t, service, testUserID)

	// Draft answer saved before submit must be merged with the final body.
	if err := service.SaveAnswers(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{{QuestionID: questionGapID, Answer: map[string]any{"value": "green street"}}},
	}); err != nil {
		t.Fatalf("save answers failed: %v", err)
	}

	submitted, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{
			{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "B"}},
			{QuestionID: questionMatchingID, Answer: map[string]any{"optionIds": []any{"C", "A"}}},
		},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if submitted.Status != StatusSubmitted || submitted.Score == nil || *submitted.Score != 4 ||
		submitted.MaxScore == nil || *submitted.MaxScore != 4 {
		t.Fatalf("unexpected submitted attempt: %+v", submitted)
	}
	// 4/4 scales to raw 40 → band 9.0.
	if submitted.Band == nil || *submitted.Band != 9.0 {
		t.Fatalf("expected band 9.0, got %+v", submitted.Band)
	}
	if repository.submitted == nil {
		t.Fatal("expected repository submit call")
	}
	if repository.submitted.Skill != MaterialListening {
		t.Fatalf("expected skill listening, got %q", repository.submitted.Skill)
	}
	if repository.submitted.Accuracy != 100 {
		t.Fatalf("expected accuracy 100, got %v", repository.submitted.Accuracy)
	}
	for _, answer := range repository.submitted.Answers {
		if answer.IsCorrect == nil || !*answer.IsCorrect {
			t.Fatalf("expected all answers correct, got %+v", answer)
		}
	}
}

func TestSubmitEmptyBodyGradesDraftsOnly(t *testing.T) {
	service, repository := testService()
	attempt := startForTest(t, service, testUserID)
	if err := service.SaveAnswers(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "A"}}},
	}); err != nil {
		t.Fatalf("save answers failed: %v", err)
	}
	submitted, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if *submitted.Score != 0 || *submitted.MaxScore != 4 || *submitted.Band != 0.0 {
		t.Fatalf("unexpected grading result: %+v", submitted)
	}
	if repository.submitted.Accuracy != 0 {
		t.Fatalf("expected accuracy 0, got %v", repository.submitted.Accuracy)
	}
}

func TestSubmitTwiceConflicts(t *testing.T) {
	service, _ := testService()
	attempt := startForTest(t, service, testUserID)
	if _, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{}); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if _, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{}); !errors.Is(err, ErrAlreadySubmitted) {
		t.Fatalf("expected ErrAlreadySubmitted, got %v", err)
	}
}

func TestSaveAnswersAfterSubmitConflicts(t *testing.T) {
	service, _ := testService()
	attempt := startForTest(t, service, testUserID)
	if _, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	err := service.SaveAnswers(context.Background(), testUserID, attempt.ID, SaveAnswersInput{})
	if !errors.Is(err, ErrAlreadySubmitted) {
		t.Fatalf("expected ErrAlreadySubmitted, got %v", err)
	}
}

func TestForeignAttemptIsHidden(t *testing.T) {
	service, _ := testService()
	attempt := startForTest(t, service, testUserID)

	if err := service.SaveAnswers(context.Background(), testOtherUser, attempt.ID, SaveAnswersInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("save answers: expected ErrNotFound, got %v", err)
	}
	if _, err := service.Submit(context.Background(), testOtherUser, attempt.ID, SaveAnswersInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("submit: expected ErrNotFound, got %v", err)
	}
	if _, err := service.Get(context.Background(), testOtherUser, attempt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: expected ErrNotFound, got %v", err)
	}
}

func TestGetDetailIncludesReviewOnlyAfterSubmit(t *testing.T) {
	service, _ := testService()
	attempt := startForTest(t, service, testUserID)

	draft, err := service.Get(context.Background(), testUserID, attempt.ID)
	if err != nil {
		t.Fatalf("get draft failed: %v", err)
	}
	if draft.Review != nil {
		t.Fatal("in-progress attempt must not expose review")
	}

	if _, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "B"}}},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	detail, err := service.Get(context.Background(), testUserID, attempt.ID)
	if err != nil {
		t.Fatalf("get submitted failed: %v", err)
	}
	if len(detail.Review) != 3 {
		t.Fatalf("expected review for 3 questions, got %d", len(detail.Review))
	}
	byID := map[uuid.UUID]ReviewAnswer{}
	for _, item := range detail.Review {
		byID[item.QuestionID] = item
	}
	if !byID[questionChoiceID].IsCorrect || byID[questionChoiceID].PointsAwarded != 1 {
		t.Fatalf("expected correct graded choice, got %+v", byID[questionChoiceID])
	}
	if byID[questionMatchingID].IsCorrect || byID[questionMatchingID].PointsAwarded != 0 {
		t.Fatalf("expected unanswered matching to be incorrect, got %+v", byID[questionMatchingID])
	}
	if correct, _ := byID[questionGapID].CorrectAnswer["accepted"].([]any); len(correct) != 2 {
		t.Fatalf("review must include the correct answer, got %+v", byID[questionGapID].CorrectAnswer)
	}
}

func TestReadingStartAndSubmit(t *testing.T) {
	service, repository := testService()
	attempt, material, created, err := service.Start(context.Background(), testUserID, MaterialReading, testMaterialID)
	if err != nil || !created {
		t.Fatalf("reading start failed: created=%v err=%v", created, err)
	}
	if attempt.MaterialType != MaterialReading || attempt.MaterialVersionID != testMaterialVersionID {
		t.Fatalf("unexpected reading attempt: %+v", attempt)
	}
	if material == nil {
		t.Fatal("expected public material structure in start response")
	}

	submitted, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{
			{QuestionID: questionTFNGID, Answer: map[string]any{"value": "true"}},
			{QuestionID: questionMatchID, Answer: map[string]any{"optionId": "C"}},
			{QuestionID: questionSumID, Answer: map[string]any{"value": " north campus "}},
		},
	})
	if err != nil {
		t.Fatalf("reading submit failed: %v", err)
	}
	if *submitted.Score != 4 || *submitted.MaxScore != 4 {
		t.Fatalf("unexpected reading score: %+v", submitted)
	}
	// Academic reading, 4/4 scales to raw 40 → band 9.0.
	if *submitted.Band != 9.0 {
		t.Fatalf("expected band 9.0, got %v", *submitted.Band)
	}
	if repository.submitted.Skill != MaterialReading {
		t.Fatalf("expected skill reading, got %q", repository.submitted.Skill)
	}

	detail, err := service.Get(context.Background(), testUserID, attempt.ID)
	if err != nil {
		t.Fatalf("get submitted reading attempt failed: %v", err)
	}
	if len(detail.Review) != 3 {
		t.Fatalf("expected review for 3 questions, got %d", len(detail.Review))
	}
	for _, item := range detail.Review {
		if !item.IsCorrect {
			t.Fatalf("expected all reading answers correct, got %+v", item)
		}
		if item.CorrectAnswer == nil {
			t.Fatalf("review must include correct answer, got %+v", item)
		}
	}
}

func TestReadingSubmitUsesGeneralBandTable(t *testing.T) {
	repository := newStubRepository()
	service := NewService(repository, map[string]MaterialProvider{
		MaterialReading: readingStubProvider("general"),
	})
	attempt, _, created, err := service.Start(context.Background(), testUserID, MaterialReading, testMaterialID)
	if err != nil || !created {
		t.Fatalf("reading start failed: created=%v err=%v", created, err)
	}
	// 3 of 4 points → raw 30 → general band 6.0 (academic would be 7.0).
	if _, err := service.Submit(context.Background(), testUserID, attempt.ID, SaveAnswersInput{
		Answers: []AnswerInput{
			{QuestionID: questionTFNGID, Answer: map[string]any{"value": "TRUE"}},
			{QuestionID: questionSumID, Answer: map[string]any{"value": "North Campus"}},
		},
	}); err != nil {
		t.Fatalf("reading submit failed: %v", err)
	}
	if repository.submitted == nil || repository.submitted.Band != 6.0 {
		t.Fatalf("expected general band 6.0, got %+v", repository.submitted)
	}
}
