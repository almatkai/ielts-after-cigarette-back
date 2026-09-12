package speaking

import (
	"context"
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
	Update(context.Context, uuid.UUID, uuid.UUID, SaveInput) (Material, error)
	Publish(context.Context, uuid.UUID, uuid.UUID, int64) (Material, error)
}

type Service struct{ repository Repository }

func NewService(repository Repository) *Service { return &Service{repository: repository} }

func (s *Service) List(ctx context.Context) ([]Material, error) {
	items, err := s.repository.List(ctx)
	if items == nil {
		items = []Material{}
	}
	return items, err
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

func (s *Service) GetVersionPublic(ctx context.Context, id, versionID uuid.UUID) (PublicMaterial, error) {
	material, err := s.repository.GetVersion(ctx, id, versionID)
	if err != nil {
		return PublicMaterial{}, err
	}
	return publicMaterial(material), nil
}

func (s *Service) GetVersion(ctx context.Context, id, versionID uuid.UUID) (Material, error) {
	return s.repository.GetVersion(ctx, id, versionID)
}

func (s *Service) PublishedVersionID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.repository.PublishedVersionID(ctx, id)
}

func (s *Service) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Material, map[string]string, error) {
	input = normalizeInput(input)
	if input.Slug == "" {
		input.Slug = "speaking-" + uuid.NewString()[:8]
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

func publicMaterial(material Material) PublicMaterial {
	return PublicMaterial{
		ID: material.ID, Slug: material.Slug, ExamType: material.ExamType,
		Difficulty: material.Difficulty, Title: material.Title,
		Description: material.Description, Parts: material.Parts,
	}
}

func normalizeInput(input SaveInput) SaveInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	input.Difficulty = strings.ToLower(strings.TrimSpace(input.Difficulty))
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	for partIndex := range input.Parts {
		part := &input.Parts[partIndex]
		if part.ID == uuid.Nil {
			part.ID = uuid.New()
		}
		part.Position = partIndex + 1
		part.Type = strings.ToLower(strings.TrimSpace(part.Type))
		part.Title = strings.TrimSpace(part.Title)
		part.Instructions = strings.TrimSpace(part.Instructions)
		if part.ResponseSeconds < 1 {
			switch part.Type {
			case PartOne, PartThree:
				part.ResponseSeconds = 300
			case PartTwo:
				part.ResponseSeconds = 120
			}
		}
		if part.Type == PartTwo && part.PreparationSeconds < 1 {
			part.PreparationSeconds = 60
		}
		if part.Type != PartTwo {
			part.PreparationSeconds = 0
		}
		if part.CueCard == nil {
			part.CueCard = []string{}
		}
		for cueIndex := range part.CueCard {
			part.CueCard[cueIndex] = strings.TrimSpace(part.CueCard[cueIndex])
		}
		if part.Questions == nil {
			part.Questions = []Question{}
		}
		for questionIndex := range part.Questions {
			question := &part.Questions[questionIndex]
			if question.ID == uuid.Nil {
				question.ID = uuid.New()
			}
			question.Position = questionIndex + 1
			question.Prompt = strings.TrimSpace(question.Prompt)
		}
	}
	return input
}

func validateInput(input SaveInput, requireRevision bool) map[string]string {
	details := map[string]string{}
	if len(input.Slug) > 160 || !slugPattern.MatchString(input.Slug) {
		details["slug"] = "must contain lowercase Latin letters, numbers, and single hyphens"
	}
	if input.ExamType != "academic" && input.ExamType != "general" {
		details["examType"] = "must be academic or general"
	}
	if input.Difficulty != "foundation" && input.Difficulty != "intermediate" && input.Difficulty != "advanced" {
		details["difficulty"] = "must be foundation, intermediate, or advanced"
	}
	if length := utf8.RuneCountInString(input.Title); length < 3 || length > 200 {
		details["title"] = "must contain between 3 and 200 characters"
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		details["description"] = "must contain at most 1000 characters"
	}
	if requireRevision && input.Revision < 1 {
		details["revision"] = "must be a positive integer"
	}
	if len(input.Parts) != 3 {
		details["parts"] = "must contain Speaking Parts 1, 2, and 3"
		return details
	}
	expected := []string{PartOne, PartTwo, PartThree}
	for index, part := range input.Parts {
		prefix := "parts[" + strconv.Itoa(index) + "]"
		if part.Type != expected[index] {
			details[prefix+".type"] = "must be " + expected[index]
		}
		if length := utf8.RuneCountInString(part.Title); length < 3 || length > 300 {
			details[prefix+".title"] = "must contain between 3 and 300 characters"
		}
		if utf8.RuneCountInString(part.Instructions) > 3000 {
			details[prefix+".instructions"] = "must contain at most 3000 characters"
		}
		if part.ResponseSeconds < 30 || part.ResponseSeconds > 600 {
			details[prefix+".responseSeconds"] = "must be between 30 and 600 seconds"
		}
		if index == 1 {
			if part.PreparationSeconds < 30 || part.PreparationSeconds > 120 {
				details[prefix+".preparationSeconds"] = "must be between 30 and 120 seconds"
			}
			if len(part.CueCard) < 3 || len(part.CueCard) > 6 {
				details[prefix+".cueCard"] = "must contain between 3 and 6 cue-card prompts"
			}
			for cueIndex, cue := range part.CueCard {
				if length := utf8.RuneCountInString(cue); length < 3 || length > 500 {
					details[prefix+".cueCard["+strconv.Itoa(cueIndex)+"]"] = "must contain between 3 and 500 characters"
				}
			}
			if len(part.Questions) != 0 {
				details[prefix+".questions"] = "Part 2 uses cueCard prompts and must not contain questions"
			}
			continue
		}
		if len(part.CueCard) != 0 {
			details[prefix+".cueCard"] = "only Part 2 may contain cue-card prompts"
		}
		if len(part.Questions) < 2 || len(part.Questions) > 15 {
			details[prefix+".questions"] = "must contain between 2 and 15 questions"
		}
		for questionIndex, question := range part.Questions {
			if length := utf8.RuneCountInString(question.Prompt); length < 3 || length > 1000 {
				details[prefix+".questions["+strconv.Itoa(questionIndex)+"].prompt"] = "must contain between 3 and 1000 characters"
			}
		}
	}
	return details
}
