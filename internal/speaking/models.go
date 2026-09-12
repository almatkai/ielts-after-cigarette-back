package speaking

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("speaking material not found")
	ErrSlugExists       = errors.New("speaking material slug already exists")
	ErrRevisionConflict = errors.New("speaking material revision conflict")
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"

	PartOne   = "part1"
	PartTwo   = "part2"
	PartThree = "part3"
)

type Question struct {
	ID       uuid.UUID `json:"id"`
	Position int       `json:"position"`
	Prompt   string    `json:"prompt"`
}

// Part represents one IELTS Speaking section. Part 2 uses Title and CueCard;
// Parts 1 and 3 use their question lists.
type Part struct {
	ID                 uuid.UUID  `json:"id"`
	Position           int        `json:"position"`
	Type               string     `json:"type"`
	Title              string     `json:"title"`
	Instructions       string     `json:"instructions"`
	PreparationSeconds int        `json:"preparationSeconds"`
	ResponseSeconds    int        `json:"responseSeconds"`
	CueCard            []string   `json:"cueCard"`
	Questions          []Question `json:"questions"`
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
	Parts                 []Part     `json:"parts"`
	CurrentVersionNumber  int        `json:"currentVersionNumber"`
	PublishedVersionID    *uuid.UUID `json:"publishedVersionId"`
	HasUnpublishedChanges bool       `json:"hasUnpublishedChanges"`
	PublishedAt           *time.Time `json:"publishedAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type MaterialSummary struct {
	ID          uuid.UUID  `json:"id"`
	Slug        string     `json:"slug"`
	ExamType    string     `json:"examType"`
	Difficulty  string     `json:"difficulty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	PublishedAt *time.Time `json:"publishedAt"`
}

type PublicMaterial struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	ExamType    string    `json:"examType"`
	Difficulty  string    `json:"difficulty"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Parts       []Part    `json:"parts"`
}

type SaveInput struct {
	Slug        string `json:"slug"`
	ExamType    string `json:"examType"`
	Difficulty  string `json:"difficulty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Parts       []Part `json:"parts"`
	Revision    int64  `json:"revision,omitempty"`
}

type PublishInput struct {
	Revision int64 `json:"revision"`
}
