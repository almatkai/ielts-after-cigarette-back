package attempts

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/google/uuid"
)

type Service struct {
	repository        Repository
	providers         map[string]MaterialProvider
	evaluator         WritingEvaluator
	speakingEvaluator SpeakingEvaluator
	speakingStore     objectstorage.Store
	maxSpeakingMedia  int64
	examGuard         ExamGuard
}

type ExamGuard interface {
	ValidateAttemptAccess(ctx context.Context, userID, attemptID uuid.UUID) error
}

func NewService(repository Repository, providers map[string]MaterialProvider, evaluators ...WritingEvaluator) *Service {
	service := &Service{repository: repository, providers: providers}
	if len(evaluators) > 0 {
		service.evaluator = evaluators[0]
	}
	return service
}

func (s *Service) SetExamGuard(guard ExamGuard) {
	s.examGuard = guard
}

// WithSpeakingRecordingStore enables authenticated recording storage for
// Speaking attempts. The same OpenRouter client can implement both evaluator
// interfaces, so it is accepted independently from the Writing evaluator.
func (s *Service) WithSpeakingRecordingStore(mediaDir string, maxBytes int64, evaluator SpeakingEvaluator) *Service {
	s.speakingStore = objectstorage.NewFileStore(strings.TrimSpace(mediaDir))
	s.maxSpeakingMedia = maxBytes
	s.speakingEvaluator = evaluator
	return s
}

func (s *Service) WithSpeakingObjectStore(store objectstorage.Store, maxBytes int64, evaluator SpeakingEvaluator) *Service {
	s.speakingStore = store
	s.maxSpeakingMedia = maxBytes
	s.speakingEvaluator = evaluator
	return s
}

func (s *Service) provider(materialType string) (MaterialProvider, error) {
	provider, ok := s.providers[materialType]
	if !ok {
		return nil, ErrUnsupportedMaterial
	}
	return provider, nil
}

// Start returns the existing IN_PROGRESS attempt for the material or creates
// a new one pinned to the currently published version. created reports which
// of the two happened. The returned material is the public structure (without
// answers) of the version the attempt is pinned to.
func (s *Service) Start(ctx context.Context, userID uuid.UUID, materialType string, materialID uuid.UUID) (Attempt, any, bool, error) {
	provider, err := s.provider(materialType)
	if err != nil {
		return Attempt{}, nil, false, err
	}
	versionID, err := provider.PublishedVersionID(ctx, materialID)
	if err != nil {
		return Attempt{}, nil, false, err
	}
	created := false
	attempt, err := s.repository.FindInProgress(ctx, userID, materialType, materialID)
	if errors.Is(err, ErrNotFound) {
		attempt, err = s.repository.Create(ctx, Attempt{
			ID:                uuid.New(),
			UserID:            userID,
			MaterialType:      materialType,
			MaterialID:        materialID,
			MaterialVersionID: versionID,
			Status:            StatusInProgress,
		})
		created = true
	}
	if err != nil {
		return Attempt{}, nil, false, err
	}
	material, err := provider.PublicStructure(ctx, materialID, attempt.MaterialVersionID)
	if err != nil {
		return Attempt{}, nil, false, err
	}
	return attempt, material, created, nil
}

// PublishedVersionID returns the ID of the current published version for a material.
func (s *Service) PublishedVersionID(ctx context.Context, materialType string, materialID uuid.UUID) (uuid.UUID, error) {
	provider, err := s.provider(materialType)
	if err != nil {
		return uuid.Nil, err
	}
	return provider.PublishedVersionID(ctx, materialID)
}

// StartForExamSession creates a new attempt pinned to the current published
// version. Unlike Start it deliberately does not reuse a standalone practice
// attempt, so a full mock keeps an independent exam record.
func (s *Service) StartForExamSession(ctx context.Context, userID uuid.UUID, materialType string, materialID uuid.UUID) (Attempt, error) {
	provider, err := s.provider(materialType)
	if err != nil {
		return Attempt{}, err
	}
	versionID, err := provider.PublishedVersionID(ctx, materialID)
	if err != nil {
		return Attempt{}, err
	}
	return s.repository.Create(ctx, Attempt{
		ID:                uuid.New(),
		UserID:            userID,
		MaterialType:      materialType,
		MaterialID:        materialID,
		MaterialVersionID: versionID,
		Status:            StatusInProgress,
	})
}

// PublicMaterial returns the exact public material version pinned to an
// existing attempt. Full Mock sessions use this instead of the normal Start
// flow so opening a section can never create or reuse a standalone attempt.
func (s *Service) PublicMaterial(ctx context.Context, userID, attemptID uuid.UUID) (Attempt, any, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return Attempt{}, nil, err
	}
	provider, err := s.provider(attempt.MaterialType)
	if err != nil {
		return Attempt{}, nil, err
	}
	material, err := provider.PublicStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return Attempt{}, nil, err
	}
	return attempt, material, nil
}

func (s *Service) SaveAnswers(ctx context.Context, userID, attemptID uuid.UUID, input SaveAnswersInput) error {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return err
	}
	if attempt.Status != StatusInProgress {
		return ErrAlreadySubmitted
	}
	if s.examGuard != nil {
		if err := s.examGuard.ValidateAttemptAccess(ctx, userID, attemptID); err != nil {
			return err
		}
	}
	return s.repository.SaveAnswers(ctx, attemptID, input.Answers)
}

func (s *Service) Submit(ctx context.Context, userID, attemptID uuid.UUID, input SaveAnswersInput) (Attempt, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return Attempt{}, err
	}
	if attempt.Status != StatusInProgress {
		return Attempt{}, ErrAlreadySubmitted
	}
	if s.examGuard != nil {
		if err := s.examGuard.ValidateAttemptAccess(ctx, userID, attemptID); err != nil {
			return Attempt{}, err
		}
	}
	if attempt.MaterialType == MaterialWriting {
		return s.submitWriting(ctx, attempt, input)
	}
	if attempt.MaterialType == MaterialSpeaking {
		return s.submitSpeaking(ctx, attempt, input)
	}
	provider, err := s.provider(attempt.MaterialType)
	if err != nil {
		return Attempt{}, err
	}
	material, err := provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return Attempt{}, err
	}
	saved, err := s.repository.ListAnswers(ctx, attemptID)
	if err != nil {
		return Attempt{}, err
	}
	given := make(map[uuid.UUID]map[string]any, len(saved)+len(input.Answers))
	for _, item := range saved {
		given[item.QuestionID] = item.Answer
	}
	for _, item := range input.Answers {
		given[item.QuestionID] = item.Answer
	}
	score, maxScore := 0, 0
	graded := []Answer{}
	for _, question := range material.Questions {
		answer := given[question.ID]
		if answer == nil {
			answer = map[string]any{}
		}
		points := gradePoints(question, answer)
		correct := points == question.Points
		score += points
		maxScore += question.Points
		graded = append(graded, Answer{
			QuestionID:    question.ID,
			Answer:        answer,
			IsCorrect:     &correct,
			PointsAwarded: &points,
		})
	}
	band := bandFor(attempt.MaterialType, material.ExamType, score, maxScore)
	accuracy := 0.0
	if maxScore > 0 {
		accuracy = math.Round(float64(score)*10000/float64(maxScore)) / 100
	}
	if err := s.repository.Submit(ctx, SubmitResult{
		AttemptID: attemptID,
		UserID:    userID,
		Skill:     attempt.MaterialType,
		Answers:   graded,
		Score:     score,
		MaxScore:  maxScore,
		Band:      band,
		Accuracy:  accuracy,
	}); err != nil {
		return Attempt{}, err
	}
	return s.repository.Get(ctx, attemptID)
}

func (s *Service) submitWriting(ctx context.Context, attempt Attempt, input SaveAnswersInput) (Attempt, error) {
	provider, err := s.provider(attempt.MaterialType)
	if err != nil {
		return Attempt{}, err
	}
	material, err := provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return Attempt{}, err
	}
	saved, err := s.repository.ListAnswers(ctx, attempt.ID)
	if err != nil {
		return Attempt{}, err
	}
	given := make(map[uuid.UUID]map[string]any, len(saved)+len(input.Answers))
	for _, item := range saved {
		given[item.QuestionID] = item.Answer
	}
	for _, item := range input.Answers {
		given[item.QuestionID] = item.Answer
	}
	request := WritingEvaluationRequest{ExamType: material.ExamType, Tasks: make([]WritingTaskAnswer, 0, len(material.WritingTasks))}
	answers := make([]Answer, 0, len(material.WritingTasks))
	for _, task := range material.WritingTasks {
		answer := given[task.ID]
		text, _ := answer["value"].(string)
		if strings.TrimSpace(text) == "" || len([]rune(text)) > 15000 {
			return Attempt{}, ErrWritingIncomplete
		}
		request.Tasks = append(request.Tasks, WritingTaskAnswer{Task: task, Text: text})
		answers = append(answers, Answer{QuestionID: task.ID, Answer: map[string]any{"value": text}})
	}
	if s.evaluator == nil {
		return Attempt{}, ErrAIUnavailable
	}
	evaluation, err := s.evaluator.Evaluate(ctx, request)
	if err != nil {
		return Attempt{}, err
	}
	evaluation.AttemptID = attempt.ID
	band := evaluation.OverallBand
	score := int(math.Round(band * 10))
	accuracy := math.Round(band/9*10000) / 100
	if err := s.repository.Submit(ctx, SubmitResult{
		AttemptID: attempt.ID, UserID: attempt.UserID, Skill: MaterialWriting,
		Answers: answers, Score: score, MaxScore: 90, Band: band, Accuracy: accuracy,
		WritingEvaluation: &evaluation,
	}); err != nil {
		return Attempt{}, err
	}
	return s.repository.Get(ctx, attempt.ID)
}

func (s *Service) submitSpeaking(ctx context.Context, attempt Attempt, input SaveAnswersInput) (Attempt, error) {
	provider, err := s.provider(attempt.MaterialType)
	if err != nil {
		return Attempt{}, err
	}
	material, err := provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return Attempt{}, err
	}
	saved, err := s.repository.ListAnswers(ctx, attempt.ID)
	if err != nil {
		return Attempt{}, err
	}
	given := make(map[uuid.UUID]map[string]any, len(saved)+len(input.Answers))
	for _, item := range saved {
		given[item.QuestionID] = item.Answer
	}
	for _, item := range input.Answers {
		given[item.QuestionID] = item.Answer
	}
	recordings, err := s.speakingRecordings(ctx, attempt.ID)
	if err != nil {
		return Attempt{}, err
	}
	recordingByPart := make(map[uuid.UUID]SpeakingRecording, len(recordings))
	for _, recording := range recordings {
		recordingByPart[recording.PartID] = recording
	}
	request := SpeakingEvaluationRequest{ExamType: material.ExamType, Parts: make([]SpeakingPartAnswer, 0, len(material.SpeakingParts))}
	for _, part := range material.SpeakingParts {
		answer := given[part.ID]
		transcript, _ := answer["value"].(string)
		transcript = strings.TrimSpace(transcript)
		recording, hasRecording := recordingByPart[part.ID]
		if transcript == "" && !hasRecording {
			return Attempt{}, ErrSpeakingIncomplete
		}
		item := SpeakingPartAnswer{Part: part, Transcript: transcript}
		if hasRecording {
			audio, err := s.readSpeakingAudio(ctx, recording)
			if err != nil {
				return Attempt{}, err
			}
			item.Audio = audio
		}
		request.Parts = append(request.Parts, item)
	}
	if s.speakingEvaluator == nil {
		return Attempt{}, ErrAIUnavailable
	}
	evaluation, err := s.speakingEvaluator.EvaluateSpeaking(ctx, request)
	if err != nil {
		return Attempt{}, err
	}
	evaluation.AttemptID = attempt.ID
	feedbackByPart := make(map[uuid.UUID]SpeakingPartFeedback, len(evaluation.Parts))
	for _, item := range evaluation.Parts {
		feedbackByPart[item.PartID] = item
	}
	answers := make([]Answer, 0, len(material.SpeakingParts))
	for _, part := range material.SpeakingParts {
		transcript, _ := given[part.ID]["value"].(string)
		transcript = strings.TrimSpace(transcript)
		if feedback, ok := feedbackByPart[part.ID]; ok && strings.TrimSpace(feedback.Transcript) != "" {
			transcript = strings.TrimSpace(feedback.Transcript)
		}
		answers = append(answers, Answer{QuestionID: part.ID, Answer: map[string]any{"value": transcript}})
	}
	band := evaluation.OverallBand
	score := int(math.Round(band * 10))
	accuracy := math.Round(band/9*10000) / 100
	if err := s.repository.Submit(ctx, SubmitResult{
		AttemptID: attempt.ID, UserID: attempt.UserID, Skill: MaterialSpeaking,
		Answers: answers, Score: score, MaxScore: 90, Band: band, Accuracy: accuracy,
		SpeakingEvaluation: &evaluation,
	}); err != nil {
		return Attempt{}, err
	}
	return s.repository.Get(ctx, attempt.ID)
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, materialType string) ([]Summary, error) {
	return s.repository.ListByUser(ctx, userID, materialType)
}

// Mistakes returns all submitted attempts relevant to the mistakes page in
// one API response. It keeps the detailed grading and AI feedback private to
// the server, avoiding an HTTP request per historical attempt in the client.
func (s *Service) Mistakes(ctx context.Context, userID uuid.UUID) ([]MistakeReport, error) {
	attempts, err := s.repository.ListByUser(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	reports := make([]MistakeReport, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.Status != StatusSubmitted {
			continue
		}
		detail, err := s.Get(ctx, userID, attempt.ID)
		if err != nil {
			return nil, err
		}
		report := MistakeReport{
			Attempt:            attempt,
			WritingEvaluation:  detail.WritingEvaluation,
			SpeakingEvaluation: detail.SpeakingEvaluation,
		}
		for _, item := range detail.Review {
			if !item.IsCorrect {
				report.Review = append(report.Review, item)
			}
		}
		if len(report.Review) > 0 || report.WritingEvaluation != nil || report.SpeakingEvaluation != nil {
			reports = append(reports, report)
		}
	}
	return reports, nil
}

func (s *Service) Get(ctx context.Context, userID, attemptID uuid.UUID) (Detail, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return Detail{}, err
	}
	saved, err := s.repository.ListAnswers(ctx, attemptID)
	if err != nil {
		return Detail{}, err
	}
	if saved == nil {
		saved = []Answer{}
	}
	if attempt.MaterialType == MaterialSpeaking {
		recordings, err := s.speakingRecordings(ctx, attemptID)
		if err != nil {
			return Detail{}, err
		}
		if attempt.Status == StatusInProgress {
			return Detail{Attempt: attempt, Answers: saved, Recordings: recordings}, nil
		}
		repository, ok := s.repository.(interface {
			GetSpeakingEvaluation(context.Context, uuid.UUID) (SpeakingEvaluation, error)
		})
		if !ok {
			return Detail{}, ErrNotFound
		}
		evaluation, err := repository.GetSpeakingEvaluation(ctx, attemptID)
		if err != nil {
			return Detail{}, err
		}
		return Detail{Attempt: attempt, Answers: saved, Recordings: recordings, SpeakingEvaluation: &evaluation}, nil
	}
	if attempt.Status == StatusInProgress {
		return Detail{Attempt: attempt, Answers: saved}, nil
	}
	if attempt.MaterialType == MaterialWriting {
		repository, ok := s.repository.(interface {
			GetWritingEvaluation(context.Context, uuid.UUID) (WritingEvaluation, error)
		})
		if !ok {
			return Detail{}, ErrNotFound
		}
		evaluation, err := repository.GetWritingEvaluation(ctx, attemptID)
		if err != nil {
			return Detail{}, err
		}
		return Detail{Attempt: attempt, Answers: saved, WritingEvaluation: &evaluation}, nil
	}
	provider, err := s.provider(attempt.MaterialType)
	if err != nil {
		return Detail{}, err
	}
	material, err := provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return Detail{}, err
	}
	byID := make(map[uuid.UUID]Answer, len(saved))
	for _, item := range saved {
		byID[item.QuestionID] = item
	}
	review := []ReviewAnswer{}
	for _, question := range material.Questions {
		item := ReviewAnswer{
			QuestionID:    question.ID,
			Number:        question.Number,
			Prompt:        question.Prompt,
			Answer:        map[string]any{},
			CorrectAnswer: question.Answer,
			Explanation:   question.Explanation,
		}
		if answer, ok := byID[question.ID]; ok {
			item.Answer = answer.Answer
			if answer.IsCorrect != nil {
				item.IsCorrect = *answer.IsCorrect
			}
			if answer.PointsAwarded != nil {
				item.PointsAwarded = *answer.PointsAwarded
			}
		}
		review = append(review, item)
	}
	return Detail{Attempt: attempt, Review: review}, nil
}

// own loads the attempt and hides foreign attempts behind ErrNotFound.
func (s *Service) own(ctx context.Context, userID, attemptID uuid.UUID) (Attempt, error) {
	attempt, err := s.repository.Get(ctx, attemptID)
	if err != nil {
		return Attempt{}, err
	}
	if attempt.UserID != userID {
		return Attempt{}, ErrNotFound
	}
	return attempt, nil
}

// gradeAnswer compares a student answer with the correct one. The comparison
// is driven by the shape of the correct answer:
//   - {"optionId": "A"} — exact case-sensitive match (choice, matching);
//   - {"optionIds": ["A","B"]} — set equality, order does not matter;
//   - {"value": "TRUE"} — true/false/not given and yes/no/not given,
//     compared case-insensitively after trimming;
//   - {"accepted": ["v1","v2"]} — the student sends {"value": "..."},
//     compared case-insensitively after trimming against every variant.
func gradeAnswer(correct, given map[string]any) bool {
	if len(given) == 0 {
		return false
	}
	if option, ok := correct["optionId"]; ok {
		expected, _ := option.(string)
		actual, _ := given["optionId"].(string)
		return expected != "" && actual == expected
	}
	if _, ok := correct["optionIds"]; ok {
		expected := stringList(correct["optionIds"])
		actual := stringList(given["optionIds"])
		if expected == nil || actual == nil || len(expected) != len(actual) {
			return false
		}
		seen := make(map[string]int, len(actual))
		for _, value := range actual {
			seen[value]++
		}
		for _, value := range expected {
			if seen[value] == 0 {
				return false
			}
			seen[value]--
		}
		return true
	}
	if value, ok := correct["value"]; ok {
		expected, _ := value.(string)
		actual, _ := given["value"].(string)
		return expected != "" && strings.EqualFold(normalizeText(actual), normalizeText(expected))
	}
	if accepted, ok := correct["accepted"]; ok {
		value, _ := given["value"].(string)
		value = normalizeText(value)
		if value == "" {
			return false
		}
		for _, variant := range stringList(accepted) {
			if value == normalizeText(variant) {
				return true
			}
		}
	}
	return false
}

func gradePoints(question GradingQuestion, given map[string]any) int {
	if question.Points <= 1 || question.Answer["optionIds"] == nil {
		if gradeAnswer(question.Answer, given) {
			return question.Points
		}
		return 0
	}
	expected := stringList(question.Answer["optionIds"])
	actual := stringList(given["optionIds"])
	if len(expected) == 0 || len(actual) == 0 || len(actual) > len(expected) {
		return 0
	}
	wanted := make(map[string]bool, len(expected))
	for _, value := range expected {
		wanted[value] = true
	}
	seen := make(map[string]bool, len(actual))
	points := 0
	for _, value := range actual {
		if seen[value] {
			continue
		}
		seen[value] = true
		if wanted[value] {
			points++
		}
	}
	if points > question.Points {
		return question.Points
	}
	return points
}

func normalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func stringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	return result
}

// Band tables map the raw score (out of 40) to an IELTS band, ordered from
// the highest threshold down. They approximate the official IELTS
// listening/reading conversions; reading uses separate tables for academic
// and general training.
type bandThreshold struct {
	min  int
	band float64
}

var listeningBandThresholds = []bandThreshold{
	{39, 9.0}, {37, 8.5}, {35, 8.0}, {32, 7.5}, {30, 7.0}, {26, 6.5},
	{23, 6.0}, {18, 5.5}, {16, 5.0}, {13, 4.5}, {10, 4.0}, {8, 3.5},
	{6, 3.0}, {4, 2.5},
}

var readingAcademicBandThresholds = []bandThreshold{
	{39, 9.0}, {37, 8.5}, {35, 8.0}, {33, 7.5}, {30, 7.0}, {27, 6.5},
	{23, 6.0}, {19, 5.5}, {15, 5.0}, {13, 4.5}, {10, 4.0}, {8, 3.5},
	{6, 3.0}, {4, 2.5},
}

var readingGeneralBandThresholds = []bandThreshold{
	{40, 9.0}, {39, 8.5}, {37, 8.0}, {36, 7.5}, {34, 7.0}, {32, 6.5},
	{30, 6.0}, {27, 5.5}, {23, 5.0}, {19, 4.5}, {15, 4.0}, {12, 3.5},
	{9, 3.0}, {6, 2.5},
}

func bandFor(materialType, examType string, score, maxScore int) float64 {
	thresholds := listeningBandThresholds
	if materialType == MaterialReading {
		thresholds = readingAcademicBandThresholds
		if examType == "general" {
			thresholds = readingGeneralBandThresholds
		}
	}
	if maxScore < 1 || score < 1 {
		return 0
	}
	raw := int(math.Round(float64(score) * 40 / float64(maxScore)))
	for _, threshold := range thresholds {
		if raw >= threshold.min {
			return threshold.band
		}
	}
	return 0
}
