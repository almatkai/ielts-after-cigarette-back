package reading

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Repository interface {
	List(context.Context) ([]Material, error)
	Get(context.Context, uuid.UUID) (Material, error)
	ListPublished(context.Context) ([]MaterialSummary, error)
	GetPublished(context.Context, uuid.UUID) (Material, error)
	GetVersion(context.Context, uuid.UUID, uuid.UUID) (Material, error)
	PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error)
	Create(context.Context, uuid.UUID, SaveInput) (Material, error)
	CreateMany(context.Context, uuid.UUID, []SaveInput) ([]Material, error)
	Update(context.Context, uuid.UUID, uuid.UUID, SaveInput) (Material, error)
	Publish(context.Context, uuid.UUID, uuid.UUID, int64) (Material, error)
}

func (s *Service) ParseImport(input ImportParseInput) ImportResult {
	return ParseImport(input)
}

func (s *Service) BulkCreate(ctx context.Context, actorID uuid.UUID, inputs []SaveInput) ([]Material, map[string]string, error) {
	if len(inputs) == 0 || len(inputs) > 20 {
		return nil, map[string]string{"passages": "must contain between 1 and 20 passages"}, nil
	}
	normalized := make([]SaveInput, len(inputs))
	for index, input := range inputs {
		input = normalizeInput(input)
		if input.Slug == "" {
			input.Slug = "reading-" + uuid.NewString()[:8]
		}
		if details := validateInput(input, false); len(details) > 0 {
			prefixed := map[string]string{}
			for field, message := range details {
				prefixed["passages["+strconv.Itoa(index)+"]."+field] = message
			}
			return nil, prefixed, nil
		}
		normalized[index] = input
	}
	items, err := s.repository.CreateMany(ctx, actorID, normalized)
	return items, nil, err
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) List(ctx context.Context) ([]Material, error) {
	materials, err := s.repository.List(ctx)
	if materials == nil {
		materials = []Material{}
	}
	return materials, err
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Material, error) {
	return s.repository.Get(ctx, id)
}

func (s *Service) ListPublic(ctx context.Context) ([]MaterialSummary, error) {
	items, err := s.repository.ListPublished(ctx)
	if items == nil {
		items = []MaterialSummary{}
	}
	return items, err
}

func (s *Service) GetPublic(ctx context.Context, id uuid.UUID) (PublicMaterial, error) {
	material, err := s.repository.GetPublished(ctx, id)
	if err != nil {
		return PublicMaterial{}, err
	}
	return publicMaterial(material), nil
}

// GetVersion returns the full structure (with answers) of a specific version.
func (s *Service) GetVersion(ctx context.Context, id, versionID uuid.UUID) (Material, error) {
	return s.repository.GetVersion(ctx, id, versionID)
}

// GetVersionPublic returns the public structure (without answers) of a
// specific version.
func (s *Service) GetVersionPublic(ctx context.Context, id, versionID uuid.UUID) (PublicMaterial, error) {
	material, err := s.repository.GetVersion(ctx, id, versionID)
	if err != nil {
		return PublicMaterial{}, err
	}
	return publicMaterial(material), nil
}

// PublishedVersionID returns the published version of a PUBLISHED material.
func (s *Service) PublishedVersionID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.repository.PublishedVersionID(ctx, id)
}

func publicMaterial(material Material) PublicMaterial {
	result := PublicMaterial{
		ID: material.ID, Slug: material.Slug, ExamType: material.ExamType,
		Difficulty: material.Difficulty, Title: material.Title,
		Description: material.Description, Body: material.Body,
		QuestionGroups: []PublicQuestionGroup{},
	}
	for _, group := range material.QuestionGroups {
		publicGroup := PublicQuestionGroup{
			ID: group.ID, Position: group.Position, Type: group.Type,
			Instructions: group.Instructions, Questions: []PublicQuestion{},
		}
		for _, question := range group.Questions {
			publicGroup.Questions = append(publicGroup.Questions, PublicQuestion{
				ID: question.ID, Position: question.Position, Prompt: question.Prompt,
				Content: question.Content, Points: question.Points,
			})
		}
		result.QuestionGroups = append(result.QuestionGroups, publicGroup)
	}
	return result
}

func (s *Service) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Material, map[string]string, error) {
	input = normalizeInput(input)
	if input.Slug == "" {
		input.Slug = "reading-" + uuid.NewString()[:8]
	}
	if details := validateInput(input, false); len(details) > 0 {
		return Material{}, details, nil
	}
	material, err := s.repository.Create(ctx, actorID, input)
	return material, nil, err
}

func (s *Service) Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Material, map[string]string, error) {
	input = normalizeInput(input)
	if details := validateInput(input, true); len(details) > 0 {
		return Material{}, details, nil
	}
	material, err := s.repository.Update(ctx, id, actorID, input)
	return material, nil, err
}

func (s *Service) Publish(ctx context.Context, id, actorID uuid.UUID, revision int64) (Material, map[string]string, error) {
	if revision < 1 {
		return Material{}, map[string]string{"revision": "must be a positive integer"}, nil
	}
	material, err := s.repository.Publish(ctx, id, actorID, revision)
	return material, nil, err
}

func normalizeInput(input SaveInput) SaveInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	input.Difficulty = strings.ToLower(strings.TrimSpace(input.Difficulty))
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Body = strings.TrimSpace(input.Body)
	input.SourceTitle = normalizedOptional(input.SourceTitle)
	input.SourceURL = normalizedOptional(input.SourceURL)
	for groupIndex := range input.QuestionGroups {
		group := &input.QuestionGroups[groupIndex]
		group.Type = strings.ToLower(strings.TrimSpace(group.Type))
		group.Instructions = strings.TrimSpace(group.Instructions)
		// The request order is canonical; never allow duplicate positions from a
		// stale client to turn into a database error.
		group.Position = groupIndex + 1
		for questionIndex := range group.Questions {
			question := &group.Questions[questionIndex]
			question.Prompt = strings.TrimSpace(question.Prompt)
			question.Explanation = strings.TrimSpace(question.Explanation)
			question.Position = questionIndex + 1
			if question.Points < 1 {
				question.Points = 1
			}
			if question.Content == nil {
				question.Content = map[string]any{}
			}
			if question.Answer == nil {
				question.Answer = map[string]any{}
			}
		}
	}
	return input
}

func normalizedOptional(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func validateInput(input SaveInput, requireRevision bool) map[string]string {
	details := map[string]string{}
	if len(input.Slug) > 160 || !slugPattern.MatchString(input.Slug) {
		details["slug"] = "must contain lowercase Latin letters, numbers, and single hyphens"
	}
	if input.ExamType != "academic" && input.ExamType != "general" {
		details["examType"] = "must be academic or general"
	}
	switch input.Difficulty {
	case "foundation", "intermediate", "advanced":
	default:
		details["difficulty"] = "must be foundation, intermediate, or advanced"
	}
	if length := utf8.RuneCountInString(input.Title); length < 3 || length > 200 {
		details["title"] = "must contain between 3 and 200 characters"
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		details["description"] = "must contain at most 1000 characters"
	}
	if length := utf8.RuneCountInString(input.Body); length < 50 || length > 100000 {
		details["body"] = "must contain between 50 and 100000 characters"
	}
	if input.SourceTitle != nil && utf8.RuneCountInString(*input.SourceTitle) > 200 {
		details["sourceTitle"] = "must contain at most 200 characters"
	}
	if input.SourceURL != nil {
		parsed, err := url.ParseRequestURI(*input.SourceURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			details["sourceUrl"] = "must be a valid https URL"
		}
	}
	if requireRevision && input.Revision < 1 {
		details["revision"] = "must be a positive integer"
	}
	if len(input.QuestionGroups) > 20 {
		details["questionGroups"] = "must contain at most 20 groups"
	}
	for groupIndex, group := range input.QuestionGroups {
		if !containsQuestionType(group.Type) {
			details["questionGroups"] = "group " + strconv.Itoa(groupIndex+1) + " has an unsupported question type"
			continue
		}
		if len(group.Questions) == 0 || len(group.Questions) > 50 {
			details["questionGroups"] = "each group must contain between 1 and 50 questions"
			continue
		}
		for questionIndex, question := range group.Questions {
			if strings.TrimSpace(question.Prompt) == "" {
				details["questionGroups"] = "question " + strconv.Itoa(questionIndex+1) + " in group " + strconv.Itoa(groupIndex+1) + " needs a prompt"
			}
			if question.Points < 1 || question.Points > 10 {
				details["questionGroups"] = "question points must be between 1 and 10"
			}
			if questionDetails := validateQuestion(group.Type, question); questionDetails != "" {
				details["questionGroups"] = "group " + strconv.Itoa(groupIndex+1) + ", question " + strconv.Itoa(questionIndex+1) + ": " + questionDetails
			}
		}
	}
	return details
}

func validateQuestion(questionType string, question Question) string {
	if questionType == QuestionMultipleChoice {
		options, ok := question.Content["options"].([]any)
		if !ok || len(options) < 2 {
			return "multiple choice needs at least two content.options"
		}
		if _, ok := question.Answer["optionId"]; !ok {
			if ids, multiple := question.Answer["optionIds"].([]any); !multiple || len(ids) == 0 {
				return "answer needs optionId or optionIds"
			}
		}
	}
	if questionType == QuestionTrueFalseNotGiven || questionType == QuestionYesNoNotGiven {
		value, ok := question.Answer["value"].(string)
		if !ok || (questionType == QuestionTrueFalseNotGiven && !oneOf(value, "TRUE", "FALSE", "NOT_GIVEN")) || (questionType == QuestionYesNoNotGiven && !oneOf(value, "YES", "NO", "NOT_GIVEN")) {
			return "answer.value must be one of the allowed IELTS values"
		}
	}
	if strings.HasPrefix(questionType, "matching_") {
		if _, ok := question.Answer["optionId"]; !ok {
			return "matching question needs answer.optionId"
		}
	}
	if strings.HasSuffix(questionType, "completion") || questionType == QuestionShortAnswer {
		if accepted, ok := question.Answer["accepted"].([]any); !ok || len(accepted) == 0 {
			return "answer.accepted must be a non-empty array"
		}
	}
	return ""
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsQuestionType(value string) bool {
	for _, supported := range SupportedQuestionTypes {
		if value == supported {
			return true
		}
	}
	return false
}
