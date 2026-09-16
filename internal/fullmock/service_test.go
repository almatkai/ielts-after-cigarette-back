package fullmock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/google/uuid"
)

type stubAttemptsRepo struct {
	attempts map[uuid.UUID]attempts.Attempt
	answers  map[uuid.UUID][]attempts.Answer
}

func newStubAttemptsRepo() *stubAttemptsRepo {
	return &stubAttemptsRepo{
		attempts: make(map[uuid.UUID]attempts.Attempt),
		answers:  make(map[uuid.UUID][]attempts.Answer),
	}
}

func (r *stubAttemptsRepo) FindInProgress(_ context.Context, userID uuid.UUID, materialType string, materialID uuid.UUID) (attempts.Attempt, error) {
	for _, a := range r.attempts {
		if a.UserID == userID && a.MaterialType == materialType && a.MaterialID == materialID && a.Status == attempts.StatusInProgress {
			return a, nil
		}
	}
	return attempts.Attempt{}, attempts.ErrNotFound
}

func (r *stubAttemptsRepo) Create(_ context.Context, a attempts.Attempt) (attempts.Attempt, error) {
	a.Status = attempts.StatusInProgress
	a.StartedAt = time.Now()
	r.attempts[a.ID] = a
	return a, nil
}

func (r *stubAttemptsRepo) Get(_ context.Context, id uuid.UUID) (attempts.Attempt, error) {
	a, ok := r.attempts[id]
	if !ok {
		return attempts.Attempt{}, attempts.ErrNotFound
	}
	return a, nil
}

func (r *stubAttemptsRepo) ListByUser(_ context.Context, userID uuid.UUID, materialType string) ([]attempts.Summary, error) {
	var items []attempts.Summary
	for _, a := range r.attempts {
		if a.UserID == userID && (materialType == "" || a.MaterialType == materialType) {
			items = append(items, attempts.Summary{Attempt: a})
		}
	}
	return items, nil
}

func (r *stubAttemptsRepo) SaveAnswers(_ context.Context, attemptID uuid.UUID, answers []attempts.AnswerInput) error {
	saved := r.answers[attemptID]
	for _, in := range answers {
		replaced := false
		for i := range saved {
			if saved[i].QuestionID == in.QuestionID {
				saved[i].Answer = in.Answer
				replaced = true
			}
		}
		if !replaced {
			saved = append(saved, attempts.Answer{QuestionID: in.QuestionID, Answer: in.Answer})
		}
	}
	r.answers[attemptID] = saved
	return nil
}

func (r *stubAttemptsRepo) ListAnswers(_ context.Context, attemptID uuid.UUID) ([]attempts.Answer, error) {
	return r.answers[attemptID], nil
}

func (r *stubAttemptsRepo) Submit(_ context.Context, res attempts.SubmitResult) error {
	a, ok := r.attempts[res.AttemptID]
	if !ok {
		return attempts.ErrNotFound
	}
	if a.Status != attempts.StatusInProgress {
		return attempts.ErrAlreadySubmitted
	}
	now := time.Now()
	a.Status = attempts.StatusSubmitted
	a.Score = &res.Score
	a.MaxScore = &res.MaxScore
	a.Band = &res.Band
	a.SubmittedAt = &now
	r.attempts[res.AttemptID] = a
	return nil
}

type stubMaterialProvider struct {
	publishedVersionID uuid.UUID
	publishedErr       error
}

func (p stubMaterialProvider) PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	if p.publishedErr != nil {
		return uuid.Nil, p.publishedErr
	}
	return p.publishedVersionID, nil
}

func (p stubMaterialProvider) PublicStructure(context.Context, uuid.UUID, uuid.UUID) (any, error) {
	return map[string]any{"ok": true}, nil
}

func (p stubMaterialProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (attempts.GradingMaterial, error) {
	return attempts.GradingMaterial{ExamType: "academic"}, nil
}

type stubFullMockRepo struct {
	tests        map[uuid.UUID]Test
	sessions     map[uuid.UUID]Session
	sections     map[uuid.UUID][]SessionSection
	attemptMeta  map[uuid.UUID]*ExamAttemptMeta
	attemptsRepo *stubAttemptsRepo
}

func newStubFullMockRepo() *stubFullMockRepo {
	return &stubFullMockRepo{
		tests:       make(map[uuid.UUID]Test),
		sessions:    make(map[uuid.UUID]Session),
		sections:    make(map[uuid.UUID][]SessionSection),
		attemptMeta: make(map[uuid.UUID]*ExamAttemptMeta),
	}
}

func (r *stubFullMockRepo) ListPublic(_ context.Context) ([]Test, error) {
	var list []Test
	for _, t := range r.tests {
		if t.Status == StatusPublished {
			list = append(list, t)
		}
	}
	return list, nil
}

func (r *stubFullMockRepo) List(_ context.Context) ([]Test, error) {
	var list []Test
	for _, t := range r.tests {
		list = append(list, t)
	}
	return list, nil
}

func (r *stubFullMockRepo) Get(_ context.Context, id uuid.UUID) (Test, error) {
	t, ok := r.tests[id]
	if !ok {
		return Test{}, ErrNotFound
	}
	return t, nil
}

func (r *stubFullMockRepo) GetPublic(_ context.Context, id uuid.UUID) (Test, error) {
	t, ok := r.tests[id]
	if !ok || t.Status != StatusPublished {
		return Test{}, ErrNotFound
	}
	return t, nil
}

func (r *stubFullMockRepo) Create(_ context.Context, t Test) (Test, error) {
	t.Status = StatusDraft
	t.Revision = 1
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()
	r.tests[t.ID] = t
	return t, nil
}

func (r *stubFullMockRepo) Update(_ context.Context, id uuid.UUID, input SaveInput) (Test, error) {
	t, ok := r.tests[id]
	if !ok {
		return Test{}, ErrNotFound
	}
	t.Slug = input.Slug
	t.ExamType = input.ExamType
	t.Title = input.Title
	t.Description = input.Description
	t.DurationMinutes = input.DurationMinutes
	t.ListeningMaterialID = input.ListeningMaterialID
	t.ReadingMaterialID = input.ReadingMaterialID
	t.WritingMaterialID = input.WritingMaterialID
	t.SpeakingMaterialID = input.SpeakingMaterialID
	t.Revision++
	t.UpdatedAt = time.Now()
	r.tests[id] = t
	return t, nil
}

func (r *stubFullMockRepo) Publish(_ context.Context, id uuid.UUID, revision int64) (Test, error) {
	t, ok := r.tests[id]
	if !ok {
		return Test{}, ErrNotFound
	}
	if t.Revision != revision {
		return Test{}, ErrRevisionConflict
	}
	t.Status = StatusPublished
	now := time.Now()
	t.PublishedAt = &now
	t.Revision++
	t.UpdatedAt = now
	r.tests[id] = t
	return t, nil
}

func (r *stubFullMockRepo) Archive(_ context.Context, id uuid.UUID, revision int64) (Test, error) {
	t, ok := r.tests[id]
	if !ok {
		return Test{}, ErrNotFound
	}
	if t.Revision != revision {
		return Test{}, ErrRevisionConflict
	}
	t.Status = StatusArchived
	t.Revision++
	t.UpdatedAt = time.Now()
	r.tests[id] = t
	return t, nil
}

func (r *stubFullMockRepo) FindActiveSession(_ context.Context, userID, testID uuid.UUID) (Session, error) {
	for _, s := range r.sessions {
		if s.UserID == userID && s.MockTestID == testID && s.Status == SessionInProgress {
			return s, nil
		}
	}
	return Session{}, ErrSessionNotFound
}

func (r *stubFullMockRepo) GetSession(_ context.Context, id uuid.UUID) (Session, error) {
	s, ok := r.sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}

func (r *stubFullMockRepo) ListSessionSections(_ context.Context, sessionID uuid.UUID) ([]SessionSection, error) {
	return r.sections[sessionID], nil
}

func (r *stubFullMockRepo) CreateSession(_ context.Context, session Session, sections []SessionSection) error {
	session.Status = SessionInProgress
	session.CurrentSection = 1
	session.StartedAt = time.Now()
	r.sessions[session.ID] = session
	r.sections[session.ID] = sections
	test := r.tests[session.MockTestID]
	for _, sec := range sections {
		if r.attemptsRepo != nil {
			r.attemptsRepo.attempts[sec.Attempt.ID] = sec.Attempt
		}
		r.attemptMeta[sec.Attempt.ID] = &ExamAttemptMeta{
			SessionID:       session.ID,
			UserID:          session.UserID,
			SessionStatus:   session.Status,
			CurrentSection:  session.CurrentSection,
			StartedAt:       session.StartedAt,
			DurationMinutes: test.DurationMinutes,
			SectionPosition: sec.Position,
			SectionSkill:    sec.Skill,
		}
	}
	return nil
}

func (r *stubFullMockRepo) Advance(_ context.Context, id uuid.UUID, nextSection int, complete bool) error {
	if complete {
		return r.Finish(context.Background(), id)
	}
	s, ok := r.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	s.CurrentSection = nextSection
	r.sessions[id] = s
	for _, meta := range r.attemptMeta {
		if meta.SessionID == id {
			meta.CurrentSection = nextSection
		}
	}
	return nil
}

func (r *stubFullMockRepo) Finish(_ context.Context, id uuid.UUID) error {
	s, ok := r.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	s.Status = SessionSubmitted
	now := time.Now()
	s.SubmittedAt = &now
	r.sessions[id] = s
	if r.attemptsRepo != nil {
		for _, sec := range r.sections[id] {
			if a, ok := r.attemptsRepo.attempts[sec.Attempt.ID]; ok && a.Status == attempts.StatusInProgress {
				a.Status = attempts.StatusAbandoned
				a.SubmittedAt = &now
				r.attemptsRepo.attempts[sec.Attempt.ID] = a
			}
		}
	}
	for _, meta := range r.attemptMeta {
		if meta.SessionID == id {
			meta.SessionStatus = SessionSubmitted
		}
	}
	return nil
}

func (r *stubFullMockRepo) Abandon(_ context.Context, id uuid.UUID) error {
	s, ok := r.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	s.Status = SessionAbandoned
	now := time.Now()
	s.SubmittedAt = &now
	r.sessions[id] = s
	if r.attemptsRepo != nil {
		for _, sec := range r.sections[id] {
			if a, ok := r.attemptsRepo.attempts[sec.Attempt.ID]; ok && a.Status == attempts.StatusInProgress {
				a.Status = attempts.StatusAbandoned
				a.SubmittedAt = &now
				r.attemptsRepo.attempts[sec.Attempt.ID] = a
			}
		}
	}
	for _, meta := range r.attemptMeta {
		if meta.SessionID == id {
			meta.SessionStatus = SessionAbandoned
		}
	}
	return nil
}

func (r *stubFullMockRepo) FindExamAttemptMeta(_ context.Context, attemptID uuid.UUID) (*ExamAttemptMeta, error) {
	meta, ok := r.attemptMeta[attemptID]
	if !ok {
		return nil, nil
	}
	return meta, nil
}

func setupTestFullMockService(t *testing.T) (*Service, *stubFullMockRepo, *attempts.Service, *stubAttemptsRepo, Test) {
	t.Helper()
	attemptsRepo := newStubAttemptsRepo()
	lVid := uuid.New()
	rVid := uuid.New()
	wVid := uuid.New()
	sVid := uuid.New()

	attemptsSvc := attempts.NewService(attemptsRepo, map[string]attempts.MaterialProvider{
		attempts.MaterialListening: stubMaterialProvider{publishedVersionID: lVid},
		attempts.MaterialReading:   stubMaterialProvider{publishedVersionID: rVid},
		attempts.MaterialWriting:   stubMaterialProvider{publishedVersionID: wVid},
		attempts.MaterialSpeaking:  stubMaterialProvider{publishedVersionID: sVid},
	})

	fullMockRepo := newStubFullMockRepo()
	fullMockRepo.attemptsRepo = attemptsRepo
	mockSvc := NewService(fullMockRepo, attemptsSvc)
	attemptsSvc.SetExamGuard(mockSvc)

	testItem := Test{
		ID:                  uuid.New(),
		Slug:                "ielts-mock-1",
		ExamType:            "academic",
		Title:               "IELTS Academic Mock 1",
		Description:         "Full test",
		DurationMinutes:     180,
		ListeningMaterialID: uuid.New(),
		ReadingMaterialID:   uuid.New(),
		WritingMaterialID:   uuid.New(),
		SpeakingMaterialID:  uuid.New(),
		Status:              StatusPublished,
		Revision:            1,
	}
	fullMockRepo.tests[testItem.ID] = testItem

	return mockSvc, fullMockRepo, attemptsSvc, attemptsRepo, testItem
}

func TestValidationOnCreateAndUpdate(t *testing.T) {
	svc, _, _, _, _ := setupTestFullMockService(t)
	ctx := context.Background()

	// Missing required fields
	_, details, err := svc.Create(ctx, SaveInput{Slug: "invalid slug", ExamType: "unknown"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(details) == 0 {
		t.Fatalf("expected validation errors for invalid slug and exam type")
	}

	validInput := SaveInput{
		Slug:                "ielts-mock-2",
		ExamType:            "academic",
		Title:               "Mock 2",
		Description:         "Description",
		DurationMinutes:     150,
		ListeningMaterialID: uuid.New(),
		ReadingMaterialID:   uuid.New(),
		WritingMaterialID:   uuid.New(),
		SpeakingMaterialID:  uuid.New(),
	}
	created, details, err := svc.Create(ctx, validInput)
	if err != nil || len(details) > 0 {
		t.Fatalf("create failed: err=%v details=%v", err, details)
	}
	if created.Slug != "ielts-mock-2" {
		t.Fatalf("unexpected slug: %s", created.Slug)
	}
}

func TestPublishValidationMaterials(t *testing.T) {
	attemptsRepo := newStubAttemptsRepo()
	lVid := uuid.New()
	// Reading provider has error (not published)
	attemptsSvc := attempts.NewService(attemptsRepo, map[string]attempts.MaterialProvider{
		attempts.MaterialListening: stubMaterialProvider{publishedVersionID: lVid},
		attempts.MaterialReading:   stubMaterialProvider{publishedErr: attempts.ErrMaterialNotFound},
		attempts.MaterialWriting:   stubMaterialProvider{publishedVersionID: uuid.New()},
		attempts.MaterialSpeaking:  stubMaterialProvider{publishedVersionID: uuid.New()},
	})

	fullMockRepo := newStubFullMockRepo()
	mockSvc := NewService(fullMockRepo, attemptsSvc)

	draftTest := Test{
		ID:                  uuid.New(),
		Slug:                "draft-mock",
		ExamType:            "academic",
		Title:               "Draft Mock",
		DurationMinutes:     180,
		ListeningMaterialID: uuid.New(),
		ReadingMaterialID:   uuid.New(),
		WritingMaterialID:   uuid.New(),
		SpeakingMaterialID:  uuid.New(),
		Status:              StatusDraft,
		Revision:            1,
	}
	fullMockRepo.tests[draftTest.ID] = draftTest

	// Publishing should fail because reading is not published
	_, details, err := mockSvc.Publish(context.Background(), draftTest.ID, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details["materials"] == "" {
		t.Fatalf("expected materials error when reading material is not published, got %v", details)
	}
}

func TestStartSessionAtomicAndRestart(t *testing.T) {
	mockSvc, _, _, _, testItem := setupTestFullMockService(t)
	ctx := context.Background()
	userID := uuid.New()

	session, created, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil || !created {
		t.Fatalf("expected session created: created=%v err=%v", created, err)
	}
	if session.CurrentSection != 1 {
		t.Fatalf("expected currentSection 1, got %d", session.CurrentSection)
	}
	if len(session.Sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(session.Sections))
	}
	// Section ordering: 1=listening, 2=reading, 3=writing, 4=speaking
	expectedSkills := []string{attempts.MaterialListening, attempts.MaterialReading, attempts.MaterialWriting, attempts.MaterialSpeaking}
	for i, sec := range session.Sections {
		if sec.Skill != expectedSkills[i] {
			t.Fatalf("expected section %d skill %s, got %s", i+1, expectedSkills[i], sec.Skill)
		}
		if sec.Attempt.ID == uuid.Nil {
			t.Fatalf("section %d attempt id is nil", i+1)
		}
	}

	// Re-start without restart=true should return existing session
	existingSession, created, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil || created {
		t.Fatalf("expected existing session returned: created=%v err=%v", created, err)
	}
	if existingSession.ID != session.ID {
		t.Fatalf("expected same session ID, got %s vs %s", existingSession.ID, session.ID)
	}

	// Re-start with restart=true should abandon old session and create new
	newSession, created, err := mockSvc.Start(ctx, userID, testItem.ID, true)
	if err != nil || !created {
		t.Fatalf("expected new session on restart: created=%v err=%v", created, err)
	}
	if newSession.ID == session.ID {
		t.Fatalf("expected different session ID on restart")
	}
}

func TestGetSectionAndAdvanceRules(t *testing.T) {
	mockSvc, fullMockRepo, _, attemptsRepo, testItem := setupTestFullMockService(t)
	ctx := context.Background()
	userID := uuid.New()

	session, _, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}

	// Section 1 is accessible
	sec1, _, err := mockSvc.GetSection(ctx, userID, session.ID, 1)
	if err != nil {
		t.Fatalf("expected section 1 accessible, got: %v", err)
	}
	if sec1.Skill != attempts.MaterialListening {
		t.Fatalf("expected listening, got %s", sec1.Skill)
	}

	// Section 2 is locked (currentSection is 1)
	_, _, err = mockSvc.GetSection(ctx, userID, session.ID, 2)
	if !errors.Is(err, ErrSectionLocked) {
		t.Fatalf("expected ErrSectionLocked for section 2, got: %v", err)
	}

	// Attempting to advance before completing section 1 fails
	_, err = mockSvc.Advance(ctx, userID, session.ID)
	if !errors.Is(err, ErrSectionIncomplete) {
		t.Fatalf("expected ErrSectionIncomplete, got: %v", err)
	}

	// Submit section 1 attempt
	att1 := attemptsRepo.attempts[sec1.Attempt.ID]
	att1.Status = attempts.StatusSubmitted
	band7 := 7.0
	att1.Band = &band7
	attemptsRepo.attempts[sec1.Attempt.ID] = att1

	// Update section in fullMockRepo
	for i := range fullMockRepo.sections[session.ID] {
		if fullMockRepo.sections[session.ID][i].Position == 1 {
			fullMockRepo.sections[session.ID][i].Attempt = att1
		}
	}

	// Now advance to section 2
	_, err = mockSvc.Advance(ctx, userID, session.ID)
	if err != nil {
		t.Fatalf("advance failed: %v", err)
	}

	updated, err := mockSvc.GetSession(ctx, userID, session.ID)
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}
	if updated.CurrentSection != 2 {
		t.Fatalf("expected currentSection 2, got %d", updated.CurrentSection)
	}

	// Section 2 is now accessible
	sec2, _, err := mockSvc.GetSection(ctx, userID, session.ID, 2)
	if err != nil {
		t.Fatalf("expected section 2 accessible, got %v", err)
	}
	if sec2.Skill != attempts.MaterialReading {
		t.Fatalf("expected reading, got %s", sec2.Skill)
	}
}

func TestValidateAttemptAccess(t *testing.T) {
	mockSvc, _, _, _, testItem := setupTestFullMockService(t)
	ctx := context.Background()
	userID := uuid.New()
	otherUser := uuid.New()

	session, _, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	sec1AttemptID := session.Sections[0].Attempt.ID
	sec2AttemptID := session.Sections[1].Attempt.ID

	// Non-exam attempt ID (nil meta) allows access
	randomAttemptID := uuid.New()
	if err := mockSvc.ValidateAttemptAccess(ctx, userID, randomAttemptID); err != nil {
		t.Fatalf("expected practice attempt to be allowed, got: %v", err)
	}

	// Wrong user accessing exam attempt
	if err := mockSvc.ValidateAttemptAccess(ctx, otherUser, sec1AttemptID); !errors.Is(err, attempts.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for other user, got: %v", err)
	}

	// Active section 1 accessible
	if err := mockSvc.ValidateAttemptAccess(ctx, userID, sec1AttemptID); err != nil {
		t.Fatalf("expected section 1 access valid, got: %v", err)
	}

	// Inactive section 2 locked
	if err := mockSvc.ValidateAttemptAccess(ctx, userID, sec2AttemptID); !errors.Is(err, attempts.ErrSectionLocked) {
		t.Fatalf("expected ErrSectionLocked for section 2, got: %v", err)
	}
}

func TestDeadlineEnforcement(t *testing.T) {
	mockSvc, fullMockRepo, _, _, testItem := setupTestFullMockService(t)
	ctx := context.Background()
	userID := uuid.New()

	session, _, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Set session startedAt in the past beyond test duration (180 minutes)
	expiredTime := time.Now().Add(-200 * time.Minute)
	s := fullMockRepo.sessions[session.ID]
	s.StartedAt = expiredTime
	fullMockRepo.sessions[session.ID] = s

	// Update meta startedAt
	for _, meta := range fullMockRepo.attemptMeta {
		if meta.SessionID == session.ID {
			meta.StartedAt = expiredTime
		}
	}

	// ValidateAttemptAccess should reject with ErrExamDeadlineExceeded and mark session finished
	sec1AttemptID := session.Sections[0].Attempt.ID
	err = mockSvc.ValidateAttemptAccess(ctx, userID, sec1AttemptID)
	if !errors.Is(err, attempts.ErrExamDeadlineExceeded) {
		t.Fatalf("expected ErrExamDeadlineExceeded, got: %v", err)
	}

	// Verify session status is now submitted (finished)
	finishedSession, _ := fullMockRepo.GetSession(ctx, session.ID)
	if finishedSession.Status != SessionSubmitted {
		t.Fatalf("expected session status %s, got %s", SessionSubmitted, finishedSession.Status)
	}
}

func TestOverallBandRounding(t *testing.T) {
	mockSvc, fullMockRepo, _, _, testItem := setupTestFullMockService(t)
	ctx := context.Background()
	userID := uuid.New()

	session, _, err := mockSvc.Start(ctx, userID, testItem.ID, false)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Assign bands to sections: 6.5, 7.0, 7.0, 7.5 -> sum 28.0 / 4 = 7.0
	bands := []float64{6.5, 7.0, 7.0, 7.5}
	for i := range fullMockRepo.sections[session.ID] {
		b := bands[i]
		fullMockRepo.sections[session.ID][i].Attempt.Band = &b
		fullMockRepo.sections[session.ID][i].Attempt.Status = attempts.StatusSubmitted
	}

	// Mark session as submitted
	_ = fullMockRepo.Finish(ctx, session.ID)

	decorated, err := mockSvc.GetSession(ctx, userID, session.ID)
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}
	if decorated.OverallBand == nil || *decorated.OverallBand != 7.0 {
		t.Fatalf("expected overall band 7.0, got %v", decorated.OverallBand)
	}
}
