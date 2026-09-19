package writing

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("writing material not found")
	ErrSlugExists       = errors.New("writing material slug already exists")
	ErrRevisionConflict = errors.New("writing material revision conflict")
	ErrMediaNotFound    = errors.New("writing media not found")
	ErrUnsupportedMedia = errors.New("unsupported writing media type")
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"
	TaskOne         = "task1"
	TaskTwo         = "task2"
)

type Task struct {
	ID              uuid.UUID  `json:"id"`
	Position        int        `json:"position"`
	Type            string     `json:"type"`
	Prompt          string     `json:"prompt"`
	MinimumWords    int        `json:"minimumWords"`
	VisualType      *string    `json:"visualType,omitempty"`
	VisualURL       *string    `json:"visualUrl,omitempty"`
	VisualAssetID   *uuid.UUID `json:"visualAssetId,omitempty"`
	AssessmentNotes string     `json:"assessmentNotes,omitempty"`
	EssayType       *string    `json:"essayType,omitempty"`
	LetterTone      *string    `json:"letterTone,omitempty"`
}

type PublicTask struct {
	ID            uuid.UUID  `json:"id"`
	Position      int        `json:"position"`
	Type          string     `json:"type"`
	Prompt        string     `json:"prompt"`
	MinimumWords  int        `json:"minimumWords"`
	VisualType    *string    `json:"visualType,omitempty"`
	VisualURL     *string    `json:"visualUrl,omitempty"`
	VisualAssetID *uuid.UUID `json:"visualAssetId,omitempty"`
	EssayType     *string    `json:"essayType,omitempty"`
	LetterTone    *string    `json:"letterTone,omitempty"`
}

type Media struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	OriginalName string    `json:"originalName"`
	MimeType     string    `json:"mimeType"`
	StorageKey   string    `json:"-"`
	ByteSize     int64     `json:"byteSize"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Material struct {
	ID                    uuid.UUID  `json:"id"`
	Slug                  string     `json:"slug"`
	ExamType              string     `json:"examType"`
	Difficulty            string     `json:"difficulty"`
	Status                string     `json:"status"`
	Revision              int64      `json:"revision"`
	Title                 string     `json:"title"`
	Description           string     `json:"description"`
	DurationMinutes       int        `json:"durationMinutes"`
	Tasks                 []Task     `json:"tasks"`
	CurrentVersionNumber  int        `json:"currentVersionNumber"`
	PublishedVersionID    *uuid.UUID `json:"publishedVersionId"`
	HasUnpublishedChanges bool       `json:"hasUnpublishedChanges"`
	PublishedAt           *time.Time `json:"publishedAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type MaterialSummary struct {
	ID              uuid.UUID  `json:"id"`
	Slug            string     `json:"slug"`
	ExamType        string     `json:"examType"`
	Difficulty      string     `json:"difficulty"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	DurationMinutes int        `json:"durationMinutes"`
	PublishedAt     *time.Time `json:"publishedAt"`
}

type PublicMaterial struct {
	ID              uuid.UUID    `json:"id"`
	Slug            string       `json:"slug"`
	ExamType        string       `json:"examType"`
	Difficulty      string       `json:"difficulty"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	DurationMinutes int          `json:"durationMinutes"`
	Tasks           []PublicTask `json:"tasks"`
}

type SaveInput struct {
	Slug            string `json:"slug"`
	ExamType        string `json:"examType"`
	Difficulty      string `json:"difficulty"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	DurationMinutes int    `json:"durationMinutes"`
	Tasks           []Task `json:"tasks"`
	Revision        int64  `json:"revision,omitempty"`
}

type PublishInput struct {
	Revision int64 `json:"revision"`
}

type ImportParseInput struct {
	Source string `json:"source"`
}

type ImportIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Item    int    `json:"item,omitempty"`
}

type ImportResult struct {
	Materials []SaveInput   `json:"materials"`
	Errors    []ImportIssue `json:"errors"`
}

type BulkCreateInput struct {
	Materials []SaveInput `json:"materials"`
}
