package reading

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("reading material not found")
	ErrSlugExists       = errors.New("reading material slug already exists")
	ErrRevisionConflict = errors.New("reading material revision conflict")
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
)

type Material struct {
	ID                    uuid.UUID  `json:"id"`
	Slug                  string     `json:"slug"`
	ExamType              string     `json:"examType"`
	Difficulty            string     `json:"difficulty"`
	Status                string     `json:"status"`
	Revision              int64      `json:"revision"`
	Title                 string     `json:"title"`
	Description           string     `json:"description"`
	Body                  string     `json:"body"`
	SourceTitle           *string    `json:"sourceTitle"`
	SourceURL             *string    `json:"sourceUrl"`
	CurrentVersionNumber  int        `json:"currentVersionNumber"`
	PublishedVersionID    *uuid.UUID `json:"publishedVersionId"`
	HasUnpublishedChanges bool       `json:"hasUnpublishedChanges"`
	PublishedAt           *time.Time `json:"publishedAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type SaveInput struct {
	Slug        string  `json:"slug"`
	ExamType    string  `json:"examType"`
	Difficulty  string  `json:"difficulty"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Body        string  `json:"body"`
	SourceTitle *string `json:"sourceTitle"`
	SourceURL   *string `json:"sourceUrl"`
	Revision    int64   `json:"revision,omitempty"`
}

type PublishInput struct {
	Revision int64 `json:"revision"`
}
