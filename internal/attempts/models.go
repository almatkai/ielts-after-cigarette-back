package attempts

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("attempt not found")
	ErrMaterialNotFound    = errors.New("material not found or not published")
	ErrUnsupportedMaterial = errors.New("unsupported material type")
	ErrAlreadySubmitted    = errors.New("attempt already submitted")
)

const (
	StatusInProgress = "IN_PROGRESS"
	StatusSubmitted  = "SUBMITTED"

	MaterialListening = "listening"
	MaterialReading   = "reading"
)

type Attempt struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"-"`
	MaterialType      string     `json:"materialType"`
	MaterialID        uuid.UUID  `json:"materialId"`
	MaterialVersionID uuid.UUID  `json:"materialVersionId"`
	Status            string     `json:"status"`
	Score             *int       `json:"score"`
	MaxScore          *int       `json:"maxScore"`
	Band              *float64   `json:"band"`
	StartedAt         time.Time  `json:"startedAt"`
	SubmittedAt       *time.Time `json:"submittedAt"`
}

type AnswerInput struct {
	QuestionID uuid.UUID      `json:"questionId"`
	Answer     map[string]any `json:"answer"`
}

type SaveAnswersInput struct {
	Answers []AnswerInput `json:"answers"`
}

type Answer struct {
	QuestionID    uuid.UUID      `json:"questionId"`
	Answer        map[string]any `json:"answer"`
	IsCorrect     *bool          `json:"isCorrect,omitempty"`
	PointsAwarded *int           `json:"pointsAwarded,omitempty"`
}

// GradingMaterial is the module-neutral view of a material version used for
// grading and review: listening flattens parts/groups, reading flattens
// question groups. Number is the listening question number; reading
// questions have no number, so their position is used.
type GradingQuestion struct {
	ID          uuid.UUID
	Number      int
	Prompt      string
	Answer      map[string]any
	Explanation string
	Points      int
}

type GradingMaterial struct {
	ExamType  string
	Questions []GradingQuestion
}

type Summary struct {
	Attempt
	TestTitle string `json:"testTitle"`
	TestSlug  string `json:"testSlug"`
}

type ReviewAnswer struct {
	QuestionID    uuid.UUID      `json:"questionId"`
	Number        int            `json:"number"`
	Prompt        string         `json:"prompt"`
	Answer        map[string]any `json:"answer"`
	IsCorrect     bool           `json:"isCorrect"`
	PointsAwarded int            `json:"pointsAwarded"`
	CorrectAnswer map[string]any `json:"correctAnswer"`
	Explanation   string         `json:"explanation"`
}

type Detail struct {
	Attempt
	Answers []Answer       `json:"answers,omitempty"`
	Review  []ReviewAnswer `json:"review,omitempty"`
}
