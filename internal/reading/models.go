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

// IELTS Reading question types. Groups contain one type of question, which
// mirrors the official test layout and keeps rendering/grading deterministic.
const (
	QuestionMultipleChoice         = "multiple_choice"
	QuestionTrueFalseNotGiven      = "true_false_not_given"
	QuestionYesNoNotGiven          = "yes_no_not_given"
	QuestionMatchingInformation    = "matching_information"
	QuestionMatchingHeadings       = "matching_headings"
	QuestionMatchingFeatures       = "matching_features"
	QuestionMatchingSentenceEnds   = "matching_sentence_endings"
	QuestionSentenceCompletion     = "sentence_completion"
	QuestionSummaryCompletion      = "summary_completion"
	QuestionNoteCompletion         = "note_completion"
	QuestionTableCompletion        = "table_completion"
	QuestionFlowChartCompletion    = "flow_chart_completion"
	QuestionDiagramLabelCompletion = "diagram_label_completion"
	QuestionShortAnswer            = "short_answer"
)

var SupportedQuestionTypes = []string{
	QuestionMultipleChoice, QuestionTrueFalseNotGiven, QuestionYesNoNotGiven,
	QuestionMatchingInformation, QuestionMatchingHeadings, QuestionMatchingFeatures,
	QuestionMatchingSentenceEnds, QuestionSentenceCompletion, QuestionSummaryCompletion,
	QuestionNoteCompletion, QuestionTableCompletion, QuestionFlowChartCompletion,
	QuestionDiagramLabelCompletion, QuestionShortAnswer,
}

type QuestionGroup struct {
	ID           uuid.UUID  `json:"id"`
	Position     int        `json:"position"`
	Type         string     `json:"type"`
	Instructions string     `json:"instructions"`
	Questions    []Question `json:"questions"`
}

type Question struct {
	ID          uuid.UUID      `json:"id"`
	Position    int            `json:"position"`
	Prompt      string         `json:"prompt"`
	Content     map[string]any `json:"content"`
	Answer      map[string]any `json:"answer"`
	Explanation string         `json:"explanation"`
	Points      int            `json:"points"`
}

type Material struct {
	ID                    uuid.UUID       `json:"id"`
	Slug                  string          `json:"slug"`
	ExamType              string          `json:"examType"`
	Difficulty            string          `json:"difficulty"`
	Status                string          `json:"status"`
	Revision              int64           `json:"revision"`
	Title                 string          `json:"title"`
	Description           string          `json:"description"`
	Body                  string          `json:"body"`
	SourceTitle           *string         `json:"sourceTitle"`
	SourceURL             *string         `json:"sourceUrl"`
	CurrentVersionNumber  int             `json:"currentVersionNumber"`
	PublishedVersionID    *uuid.UUID      `json:"publishedVersionId"`
	HasUnpublishedChanges bool            `json:"hasUnpublishedChanges"`
	PublishedAt           *time.Time      `json:"publishedAt"`
	CreatedAt             time.Time       `json:"createdAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
	QuestionGroups        []QuestionGroup `json:"questionGroups,omitempty"`
}

type SaveInput struct {
	Slug           string          `json:"slug"`
	ExamType       string          `json:"examType"`
	Difficulty     string          `json:"difficulty"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Body           string          `json:"body"`
	SourceTitle    *string         `json:"sourceTitle"`
	SourceURL      *string         `json:"sourceUrl"`
	QuestionGroups []QuestionGroup `json:"questionGroups,omitempty"`
	Revision       int64           `json:"revision,omitempty"`
}

// MaterialSummary is the public list shape: no passage body, no questions.
type MaterialSummary struct {
	ID          uuid.UUID  `json:"id"`
	Slug        string     `json:"slug"`
	ExamType    string     `json:"examType"`
	Difficulty  string     `json:"difficulty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	PublishedAt *time.Time `json:"publishedAt"`
}

// Public* types mirror Material without correct answers and explanations.
type PublicQuestion struct {
	ID       uuid.UUID      `json:"id"`
	Position int            `json:"position"`
	Prompt   string         `json:"prompt"`
	Content  map[string]any `json:"content"`
	Points   int            `json:"points"`
}

type PublicQuestionGroup struct {
	ID           uuid.UUID        `json:"id"`
	Position     int              `json:"position"`
	Type         string           `json:"type"`
	Instructions string           `json:"instructions"`
	Questions    []PublicQuestion `json:"questions"`
}

type PublicMaterial struct {
	ID             uuid.UUID             `json:"id"`
	Slug           string                `json:"slug"`
	ExamType       string                `json:"examType"`
	Difficulty     string                `json:"difficulty"`
	Title          string                `json:"title"`
	Description    string                `json:"description"`
	Body           string                `json:"body"`
	QuestionGroups []PublicQuestionGroup `json:"questionGroups"`
}

type PublishInput struct {
	Revision int64 `json:"revision"`
}

type ImportParseInput struct {
	Source     string `json:"source"`
	ExamType   string `json:"examType"`
	Difficulty string `json:"difficulty"`
}

type ImportIssue struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Line           int    `json:"line,omitempty"`
	Passage        int    `json:"passage,omitempty"`
	QuestionNumber int    `json:"questionNumber,omitempty"`
}

type ImportPassage struct {
	Number   int       `json:"number"`
	Material SaveInput `json:"material"`
}

type ImportResult struct {
	FormatVersion   string          `json:"formatVersion"`
	Title           string          `json:"title"`
	DurationMinutes int             `json:"durationMinutes,omitempty"`
	Passages        []ImportPassage `json:"passages"`
	Warnings        []ImportIssue   `json:"warnings"`
	Errors          []ImportIssue   `json:"errors"`
	Info            []ImportIssue   `json:"info"`
}

type BulkCreateInput struct {
	Passages []SaveInput `json:"passages"`
}

type BulkCreateResult struct {
	Items []Material `json:"items"`
}
