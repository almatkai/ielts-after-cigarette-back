package fullmock

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/google/uuid"
)

type Service struct {
	repository *Repository
	attempts   *attempts.Service
}

func NewService(repository *Repository, attemptService *attempts.Service) *Service {
	return &Service{repository: repository, attempts: attemptService}
}

func (s *Service) ListPublic(ctx context.Context) ([]Test, error) {
	return s.repository.ListPublic(ctx)
}
func (s *Service) List(ctx context.Context) ([]Test, error) { return s.repository.List(ctx) }
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Test, error) {
	return s.repository.Get(ctx, id)
}
func (s *Service) GetPublic(ctx context.Context, id uuid.UUID) (Test, error) {
	return s.repository.GetPublic(ctx, id)
}

func (s *Service) Create(ctx context.Context, input SaveInput) (Test, map[string]string, error) {
	if details := validate(input); len(details) > 0 {
		return Test{}, details, nil
	}
	item, err := s.repository.Create(ctx, Test{
		ID: uuid.New(), Slug: strings.TrimSpace(input.Slug), ExamType: input.ExamType,
		Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description),
		DurationMinutes: input.DurationMinutes, ListeningMaterialID: input.ListeningMaterialID,
		ReadingMaterialID: input.ReadingMaterialID, WritingMaterialID: input.WritingMaterialID,
		SpeakingMaterialID: input.SpeakingMaterialID,
	})
	return item, nil, err
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, input SaveInput) (Test, map[string]string, error) {
	if details := validate(input); len(details) > 0 {
		return Test{}, details, nil
	}
	input.Slug, input.Title, input.Description = strings.TrimSpace(input.Slug), strings.TrimSpace(input.Title), strings.TrimSpace(input.Description)
	item, err := s.repository.Update(ctx, id, input)
	return item, nil, err
}

func (s *Service) Publish(ctx context.Context, id uuid.UUID, revision int64) (Test, map[string]string, error) {
	if revision < 1 {
		return Test{}, map[string]string{"revision": "must be a positive integer"}, nil
	}
	item, err := s.repository.Publish(ctx, id, revision)
	return item, nil, err
}

func (s *Service) Start(ctx context.Context, userID, testID uuid.UUID) (Session, bool, error) {
	if existing, err := s.repository.FindActiveSession(ctx, userID, testID); err == nil {
		item, err := s.decorate(ctx, userID, existing)
		return item, false, err
	} else if !errors.Is(err, ErrSessionNotFound) {
		return Session{}, false, err
	}
	item, err := s.repository.GetPublic(ctx, testID)
	if err != nil {
		return Session{}, false, err
	}
	definitions := []struct {
		position   int
		skill      string
		materialID uuid.UUID
	}{
		{1, attempts.MaterialListening, item.ListeningMaterialID},
		{2, attempts.MaterialReading, item.ReadingMaterialID},
		{3, attempts.MaterialWriting, item.WritingMaterialID},
		{4, attempts.MaterialSpeaking, item.SpeakingMaterialID},
	}
	sections := make([]SessionSection, 0, len(definitions))
	for _, definition := range definitions {
		attempt, err := s.attempts.StartForExamSession(ctx, userID, definition.skill, definition.materialID)
		if err != nil {
			return Session{}, false, err
		}
		sections = append(sections, SessionSection{Position: definition.position, Skill: definition.skill, Attempt: attempt})
	}
	session := Session{ID: uuid.New(), MockTestID: testID, UserID: userID, MockTest: item}
	if err := s.repository.CreateSession(ctx, session, sections); err != nil {
		return Session{}, false, err
	}
	session, err = s.repository.GetSession(ctx, session.ID)
	if err != nil {
		return Session{}, false, err
	}
	session, err = s.decorate(ctx, userID, session)
	return session, true, err
}

func (s *Service) GetSession(ctx context.Context, userID, sessionID uuid.UUID) (Session, error) {
	session, err := s.repository.GetSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.UserID != userID {
		return Session{}, ErrSessionNotFound
	}
	return s.decorate(ctx, userID, session)
}

func (s *Service) Advance(ctx context.Context, userID, sessionID uuid.UUID) (Session, error) {
	session, err := s.repository.GetSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.UserID != userID {
		return Session{}, ErrSessionNotFound
	}
	if session.Status != SessionInProgress {
		return Session{}, ErrSessionCompleted
	}
	sections, err := s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	if session.CurrentSection < 1 || session.CurrentSection > len(sections) || sections[session.CurrentSection-1].Attempt.Status != attempts.StatusSubmitted {
		return Session{}, ErrSectionIncomplete
	}
	if session.CurrentSection == len(sections) {
		err = s.repository.Advance(ctx, session.ID, 5, true)
	} else {
		err = s.repository.Advance(ctx, session.ID, session.CurrentSection+1, false)
	}
	if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, userID, sessionID)
}

func (s *Service) decorate(ctx context.Context, userID uuid.UUID, session Session) (Session, error) {
	test, err := s.repository.Get(ctx, session.MockTestID)
	if err != nil {
		return Session{}, err
	}
	sections, err := s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	session.MockTest = test
	session.Sections = sections
	session.DeadlineAt = session.StartedAt.Add(time.Duration(test.DurationMinutes) * time.Minute)
	if session.Status == SessionSubmitted {
		bands := make([]float64, 0, len(sections))
		for _, section := range sections {
			if section.Attempt.Band == nil {
				return Session{}, ErrSectionIncomplete
			}
			bands = append(bands, *section.Attempt.Band)
		}
		if len(bands) == 4 {
			overall := math.Round(((bands[0]+bands[1]+bands[2]+bands[3])/4)*2) / 2
			session.OverallBand = &overall
		}
	}
	return session, nil
}

func validate(input SaveInput) map[string]string {
	details := map[string]string{}
	if strings.TrimSpace(input.Slug) == "" {
		details["slug"] = "is required"
	}
	if strings.TrimSpace(input.Title) == "" {
		details["title"] = "is required"
	}
	if input.ExamType != "academic" && input.ExamType != "general" {
		details["examType"] = "must be academic or general"
	}
	if input.DurationMinutes < 60 || input.DurationMinutes > 300 {
		details["durationMinutes"] = "must be between 60 and 300"
	}
	if input.ListeningMaterialID == uuid.Nil {
		details["listeningMaterialId"] = "is required"
	}
	if input.ReadingMaterialID == uuid.Nil {
		details["readingMaterialId"] = "is required"
	}
	if input.WritingMaterialID == uuid.Nil {
		details["writingMaterialId"] = "is required"
	}
	if input.SpeakingMaterialID == uuid.Nil {
		details["speakingMaterialId"] = "is required"
	}
	return details
}
