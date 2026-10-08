package fullmock

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	attempts   *attempts.Service
}

func NewService(repository Repository, attemptService *attempts.Service) *Service {
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
	test, err := s.repository.Get(ctx, id)
	if err != nil {
		return Test{}, nil, err
	}
	details := map[string]string{}
	if err := s.validateMaterialsPublished(ctx, test); err != nil {
		details["materials"] = err.Error()
		return Test{}, details, nil
	}
	item, err := s.repository.Publish(ctx, id, revision)
	return item, nil, err
}

func (s *Service) validateMaterialsPublished(ctx context.Context, test Test) error {
	definitions := []struct {
		skill string
		id    uuid.UUID
	}{
		{attempts.MaterialListening, test.ListeningMaterialID},
		{attempts.MaterialReading, test.ReadingMaterialID},
		{attempts.MaterialWriting, test.WritingMaterialID},
		{attempts.MaterialSpeaking, test.SpeakingMaterialID},
	}
	for _, def := range definitions {
		if _, err := s.attempts.PublishedVersionID(ctx, def.skill, def.id); err != nil {
			return fmt.Errorf("material %s (%s) is not published: %w", def.skill, def.id, err)
		}
	}
	return nil
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID, revision int64) (Test, map[string]string, error) {
	if revision < 1 {
		return Test{}, map[string]string{"revision": "must be a positive integer"}, nil
	}
	item, err := s.repository.Archive(ctx, id, revision)
	return item, nil, err
}

func (s *Service) Start(ctx context.Context, userID, testID uuid.UUID, restart bool) (Session, bool, error) {
	if existing, err := s.repository.FindActiveSession(ctx, userID, testID); err == nil {
		if restart {
			if err := s.repository.Abandon(ctx, existing.ID); err != nil {
				return Session{}, false, err
			}
		} else {
			item, err := s.decorate(ctx, userID, existing)
			return item, false, err
		}
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
		versionID, err := s.attempts.PublishedVersionID(ctx, definition.skill, definition.materialID)
		if err != nil {
			return Session{}, false, err
		}
		att := attempts.Attempt{
			ID:                uuid.New(),
			UserID:            userID,
			MaterialType:      definition.skill,
			MaterialID:        definition.materialID,
			MaterialVersionID: versionID,
			Status:            attempts.StatusInProgress,
		}
		sections = append(sections, SessionSection{Position: definition.position, Skill: definition.skill, Attempt: att})
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
	if session.CurrentSection < 1 || session.CurrentSection > len(sections) {
		return Session{}, ErrSectionIncomplete
	}
	if err := s.expireSection(ctx, session, sections[session.CurrentSection-1]); err != nil {
		return Session{}, err
	}
	sections, err = s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	currentStatus := sections[session.CurrentSection-1].Attempt.Status
	if currentStatus != attempts.StatusSubmitted && currentStatus != attempts.StatusProcessing && currentStatus != attempts.StatusAbandoned {
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

// Pause freezes only the current section after the caller has saved its draft.
func (s *Service) Pause(ctx context.Context, userID, sessionID uuid.UUID) (Session, error) {
	session, err := s.GetSession(ctx, userID, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.Status != SessionInProgress {
		return Session{}, ErrSessionCompleted
	}
	if err := s.repository.PauseSection(ctx, session.ID, session.CurrentSection); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, userID, sessionID)
}

// Finish closes an in-progress Full Mock even if some sections are blank.
// It intentionally leaves OverallBand empty unless all four sections have a
// band, preserving the fact that this was an incomplete exam.
func (s *Service) Finish(ctx context.Context, userID, sessionID uuid.UUID) (Session, error) {
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
	if auth.Role(ctx) == "GUEST" {
		sections, err := s.repository.ListSessionSections(ctx, session.ID)
		if err != nil {
			return Session{}, err
		}
		if session.CurrentSection != 4 || len(sections) != 4 {
			return Session{}, ErrSectionIncomplete
		}
		for _, section := range sections {
			if section.Attempt.Status == attempts.StatusInProgress {
				return Session{}, ErrSectionIncomplete
			}
		}
	}
	if err := s.repository.Finish(ctx, session.ID); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, userID, sessionID)
}

// GetSection grants access only to the current Full Mock section and returns
// its already-created attempt plus the version pinned to that attempt.
func (s *Service) GetSection(ctx context.Context, userID, sessionID uuid.UUID, position int) (SessionSection, any, error) {
	session, err := s.repository.GetSession(ctx, sessionID)
	if err != nil {
		return SessionSection{}, nil, err
	}
	if session.UserID != userID {
		return SessionSection{}, nil, ErrSessionNotFound
	}
	if session.Status != SessionInProgress || position != session.CurrentSection {
		return SessionSection{}, nil, ErrSectionLocked
	}
	sections, err := s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return SessionSection{}, nil, err
	}
	if position < 1 || position > len(sections) {
		return SessionSection{}, nil, ErrSectionLocked
	}
	section := sections[position-1]
	if section.Position != position {
		return SessionSection{}, nil, ErrSectionLocked
	}
	duration := section.DurationMinutes
	if duration <= 0 {
		duration = sectionDuration(section.Skill)
	}
	if err := s.repository.StartSection(ctx, session.ID, position, duration); err != nil {
		return SessionSection{}, nil, err
	}
	sections, err = s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return SessionSection{}, nil, err
	}
	section = sections[position-1]
	if err := s.expireSection(ctx, session, section); err != nil {
		return SessionSection{}, nil, err
	}
	attempt, material, err := s.attempts.PublicMaterial(ctx, userID, section.Attempt.ID)
	if err != nil {
		return SessionSection{}, nil, err
	}
	section.Attempt = attempt
	return section, material, nil
}

// ReviewAccess never advances clocks or grades answers. It prevents retrieving
// completed section feedback through the standalone attempt API mid-mock.
func (s *Service) ReviewAccess(ctx context.Context, userID, attemptID uuid.UUID) (*uuid.UUID, bool, error) {
	meta, err := s.repository.FindExamAttemptMeta(ctx, attemptID)
	if err != nil {
		return nil, false, err
	}
	if meta == nil {
		return nil, false, nil
	}
	if meta.UserID != userID {
		return nil, false, attempts.ErrNotFound
	}
	return &meta.SessionID, meta.SessionStatus != SessionSubmitted, nil
}

// ValidateAttemptAccess enforces exam session invariants:
// 1. The session must be in progress and this section must have been opened.
// 2. Only the current active section of the exam can be modified or submitted.
func (s *Service) ValidateAttemptAccess(ctx context.Context, userID, attemptID uuid.UUID) error {
	meta, err := s.repository.FindExamAttemptMeta(ctx, attemptID)
	if err != nil {
		return err
	}
	if meta == nil {
		return nil
	}
	if meta.UserID != userID {
		return attempts.ErrNotFound
	}
	if meta.SessionStatus != SessionInProgress {
		return attempts.ErrAlreadySubmitted
	}
	if meta.SectionPosition != meta.CurrentSection || meta.DeadlineAt == nil {
		return attempts.ErrSectionLocked
	}
	if time.Now().After(*meta.DeadlineAt) {
		session, err := s.repository.GetSession(ctx, meta.SessionID)
		if err != nil {
			return err
		}
		sections, err := s.repository.ListSessionSections(ctx, meta.SessionID)
		if err != nil {
			return err
		}
		for _, section := range sections {
			if section.Attempt.ID == attemptID {
				if err := s.expireSection(ctx, session, section); err != nil {
					return err
				}
			}
		}
		return attempts.ErrExamDeadlineExceeded
	}
	return nil
}

func (s *Service) decorate(ctx context.Context, userID uuid.UUID, session Session) (Session, error) {
	test, err := s.sessionTest(ctx, session)
	if err != nil {
		return Session{}, err
	}
	sections, err := s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	for _, section := range sections {
		if err := s.expireSection(ctx, session, section); err != nil {
			return Session{}, err
		}
	}
	sections, err = s.repository.ListSessionSections(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	if session.MockTestID == uuid.Nil {
		for _, section := range sections {
			switch section.Skill {
			case attempts.MaterialListening:
				test.ListeningMaterialID = section.Attempt.MaterialID
			case attempts.MaterialReading:
				test.ReadingMaterialID = section.Attempt.MaterialID
			case attempts.MaterialWriting:
				test.WritingMaterialID = section.Attempt.MaterialID
			case attempts.MaterialSpeaking:
				test.SpeakingMaterialID = section.Attempt.MaterialID
			}
		}
	}
	session.MockTest = test
	session.Sections = sections
	if session.Status == SessionSubmitted {
		bands := make([]float64, 0, len(sections))
		for _, section := range sections {
			if section.Attempt.Band == nil {
				return session, nil
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

// Expiry grades saved answers in this section only. Empty Writing/Speaking
// responses are closed without a band, while the next section stays available.
func (s *Service) expireSection(ctx context.Context, session Session, section SessionSection) error {
	if session.Status != SessionInProgress || section.Position != session.CurrentSection || section.Attempt.Status != attempts.StatusInProgress || section.DeadlineAt == nil || !time.Now().After(*section.DeadlineAt) {
		return nil
	}
	_, err := s.attempts.SubmitSavedForExpiredExam(ctx, session.UserID, section.Attempt.ID)
	if errors.Is(err, attempts.ErrWritingIncomplete) || errors.Is(err, attempts.ErrSpeakingIncomplete) {
		return s.repository.AbandonSection(ctx, section.Attempt.ID)
	}
	if errors.Is(err, attempts.ErrAlreadySubmitted) {
		return nil
	}
	return err
}

func sectionDuration(skill string) int {
	switch skill {
	case attempts.MaterialListening:
		return 30
	case attempts.MaterialSpeaking:
		return 15
	default:
		return 60
	}
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
