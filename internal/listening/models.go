package listening

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("listening test not found")
	ErrSlugExists       = errors.New("listening test slug already exists")
	ErrRevisionConflict = errors.New("listening test revision conflict")
	ErrMediaNotFound    = errors.New("listening media not found")
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"

	TypeMultipleChoice     = "multiple_choice"
	TypeMatching           = "matching"
	TypeMapLabelling       = "map_labelling"
	TypePlanLabelling      = "plan_labelling"
	TypeDiagramLabelling   = "diagram_labelling"
	TypeFormCompletion     = "form_completion"
	TypeNoteCompletion     = "note_completion"
	TypeTableCompletion    = "table_completion"
	TypeFlowCompletion     = "flow_chart_completion"
	TypeSentenceCompletion = "sentence_completion"
	TypeShortAnswer        = "short_answer"
)

var SupportedQuestionTypes = []string{
	TypeMultipleChoice, TypeMatching, TypeMapLabelling, TypePlanLabelling,
	TypeDiagramLabelling, TypeFormCompletion, TypeNoteCompletion,
	TypeTableCompletion, TypeFlowCompletion, TypeSentenceCompletion, TypeShortAnswer,
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

type Question struct {
	ID          uuid.UUID      `json:"id"`
	Position    int            `json:"position"`
	Number      int            `json:"number"`
	Prompt      string         `json:"prompt"`
	Content     map[string]any `json:"content"`
	Answer      map[string]any `json:"answer"`
	Explanation string         `json:"explanation"`
	Points      int            `json:"points"`
}

type QuestionGroup struct {
	ID           uuid.UUID      `json:"id"`
	Position     int            `json:"position"`
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Context      string         `json:"context"`
	Config       map[string]any `json:"config"`
	ImageAssetID *uuid.UUID     `json:"imageAssetId"`
	Questions    []Question     `json:"questions"`
}

type Part struct {
	ID           uuid.UUID       `json:"id"`
	Position     int             `json:"position"`
	Title        string          `json:"title"`
	AudioAssetID *uuid.UUID      `json:"audioAssetId"`
	Groups       []QuestionGroup `json:"groups"`
}

type Test struct {
	ID                    uuid.UUID  `json:"id"`
	Slug                  string     `json:"slug"`
	ExamType              string     `json:"examType"`
	Status                string     `json:"status"`
	Revision              int64      `json:"revision"`
	Title                 string     `json:"title"`
	Description           string     `json:"description"`
	DurationMinutes       int        `json:"durationMinutes"`
	CurrentVersionNumber  int        `json:"currentVersionNumber"`
	HasUnpublishedChanges bool       `json:"hasUnpublishedChanges"`
	PublishedAt           *time.Time `json:"publishedAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	Parts                 []Part     `json:"parts,omitempty"`
}

type SaveInput struct {
	Slug            string `json:"slug"`
	ExamType        string `json:"examType"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	DurationMinutes int    `json:"durationMinutes"`
	Parts           []Part `json:"parts"`
	Revision        int64  `json:"revision,omitempty"`
}

type PublicQuestion struct {
	ID       uuid.UUID      `json:"id"`
	Position int            `json:"position"`
	Number   int            `json:"number"`
	Prompt   string         `json:"prompt"`
	Content  map[string]any `json:"content"`
	Points   int            `json:"points"`
}

type PublicQuestionGroup struct {
	ID           uuid.UUID        `json:"id"`
	Position     int              `json:"position"`
	Type         string           `json:"type"`
	Instructions string           `json:"instructions"`
	Context      string           `json:"context"`
	Config       map[string]any   `json:"config"`
	ImageAssetID *uuid.UUID       `json:"imageAssetId"`
	Questions    []PublicQuestion `json:"questions"`
}

type PublicPart struct {
	ID           uuid.UUID             `json:"id"`
	Position     int                   `json:"position"`
	Title        string                `json:"title"`
	AudioAssetID *uuid.UUID            `json:"audioAssetId"`
	Groups       []PublicQuestionGroup `json:"groups"`
}

type PublicTest struct {
	ID              uuid.UUID    `json:"id"`
	Slug            string       `json:"slug"`
	ExamType        string       `json:"examType"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	DurationMinutes int          `json:"durationMinutes"`
	Parts           []PublicPart `json:"parts"`
}

type ImportParseInput struct {
	Source   string `json:"source"`
	ExamType string `json:"examType"`
}

type ImportIssue struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Line           int    `json:"line,omitempty"`
	Part           int    `json:"part,omitempty"`
	QuestionNumber int    `json:"questionNumber,omitempty"`
}

type ImportResult struct {
	FormatVersion string        `json:"formatVersion"`
	Test          SaveInput     `json:"test"`
	Errors        []ImportIssue `json:"errors"`
	Warnings      []ImportIssue `json:"warnings"`
	Info          []ImportIssue `json:"info"`
}
