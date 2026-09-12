package fullmock

import (
	"errors"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/google/uuid"
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"

	SessionInProgress = "IN_PROGRESS"
	SessionSubmitted  = "SUBMITTED"
)

var (
	ErrNotFound          = errors.New("full mock not found")
	ErrSessionNotFound   = errors.New("full mock session not found")
	ErrSectionIncomplete = errors.New("current full mock section is not submitted")
	ErrSessionCompleted  = errors.New("full mock session is already completed")
	ErrRevisionConflict  = errors.New("full mock revision conflict")
	ErrSlugExists        = errors.New("full mock slug already exists")
)

type Test struct {
	ID                  uuid.UUID  `json:"id"`
	Slug                string     `json:"slug"`
	Status              string     `json:"status"`
	Revision            int64      `json:"revision"`
	ExamType            string     `json:"examType"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	DurationMinutes     int        `json:"durationMinutes"`
	ListeningMaterialID uuid.UUID  `json:"listeningMaterialId"`
	ReadingMaterialID   uuid.UUID  `json:"readingMaterialId"`
	WritingMaterialID   uuid.UUID  `json:"writingMaterialId"`
	SpeakingMaterialID  uuid.UUID  `json:"speakingMaterialId"`
	PublishedAt         *time.Time `json:"publishedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

type SaveInput struct {
	Slug                string    `json:"slug"`
	ExamType            string    `json:"examType"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	DurationMinutes     int       `json:"durationMinutes"`
	ListeningMaterialID uuid.UUID `json:"listeningMaterialId"`
	ReadingMaterialID   uuid.UUID `json:"readingMaterialId"`
	WritingMaterialID   uuid.UUID `json:"writingMaterialId"`
	SpeakingMaterialID  uuid.UUID `json:"speakingMaterialId"`
	Revision            int64     `json:"revision"`
}

type PublishInput struct {
	Revision int64 `json:"revision"`
}

type SessionSection struct {
	Position int              `json:"position"`
	Skill    string           `json:"skill"`
	Attempt  attempts.Attempt `json:"attempt"`
}

type Session struct {
	ID             uuid.UUID        `json:"id"`
	MockTestID     uuid.UUID        `json:"mockTestId"`
	UserID         uuid.UUID        `json:"-"`
	Status         string           `json:"status"`
	CurrentSection int              `json:"currentSection"`
	StartedAt      time.Time        `json:"startedAt"`
	SubmittedAt    *time.Time       `json:"submittedAt"`
	DeadlineAt     time.Time        `json:"deadlineAt"`
	MockTest       Test             `json:"mockTest"`
	Sections       []SessionSection `json:"sections"`
	OverallBand    *float64         `json:"overallBand"`
}
