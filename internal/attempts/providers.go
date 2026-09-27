package attempts

import (
	"context"
	"errors"

	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
	"github.com/almatkai/ielts-after-cigarette-back/internal/reading"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speaking"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writing"
	"github.com/google/uuid"
)

// MaterialProvider adapts a material module (listening, reading) to the
// attempts service. Module-specific not-found errors are translated to
// ErrMaterialNotFound so the handler has a single mapping.
type MaterialProvider interface {
	// PublishedVersionID returns the published version of a PUBLISHED material.
	PublishedVersionID(ctx context.Context, materialID uuid.UUID) (uuid.UUID, error)
	// PublicStructure returns the version structure without correct answers.
	PublicStructure(ctx context.Context, materialID, versionID uuid.UUID) (any, error)
	// GradingStructure returns the version with correct answers for grading.
	GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error)
}

// MaterialRef identifies a pinned material version inside a material module.
type MaterialRef struct {
	MaterialID uuid.UUID
	VersionID  uuid.UUID
}

// BulkMaterialProvider is implemented by material modules that can load the
// grading structures of many versions with a bounded number of queries. The
// mistakes page asks for the structure of every submitted attempt at once, so
// without it the page pays one query fan-out per attempt.
type BulkMaterialProvider interface {
	GradingStructures(ctx context.Context, refs []MaterialRef) (map[MaterialRef]GradingMaterial, error)
}

type listeningProvider struct {
	service *listening.Service
}

func NewListeningProvider(service *listening.Service) MaterialProvider {
	return listeningProvider{service: service}
}

func (p listeningProvider) PublishedVersionID(ctx context.Context, materialID uuid.UUID) (uuid.UUID, error) {
	versionID, err := p.service.PublishedVersionID(ctx, materialID)
	if errors.Is(err, listening.ErrNotFound) {
		return uuid.Nil, ErrMaterialNotFound
	}
	return versionID, err
}

func (p listeningProvider) PublicStructure(ctx context.Context, materialID, versionID uuid.UUID) (any, error) {
	test, err := p.service.GetVersionPublic(ctx, materialID, versionID)
	if errors.Is(err, listening.ErrNotFound) {
		return nil, ErrMaterialNotFound
	}
	return test, err
}

func (p listeningProvider) GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error) {
	test, err := p.service.GetVersion(ctx, materialID, versionID)
	if errors.Is(err, listening.ErrNotFound) {
		return GradingMaterial{}, ErrMaterialNotFound
	}
	if err != nil {
		return GradingMaterial{}, err
	}
	questions := []GradingQuestion{}
	for _, part := range test.Parts {
		for _, group := range part.Groups {
			for _, question := range group.Questions {
				quote, _ := question.Content["quote"].(string)
				hint, _ := question.Content["hint"].(string)
				var tStart, tEnd *float64
				if s, ok := question.Content["timestampStart"].(float64); ok {
					tStart = &s
				}
				if e, ok := question.Content["timestampEnd"].(float64); ok {
					tEnd = &e
				}
				questions = append(questions, GradingQuestion{
					ID:             question.ID,
					Number:         question.Number,
					Prompt:         question.Prompt,
					Type:           group.Type,
					Content:        question.Content,
					Answer:         question.Answer,
					Explanation:    question.Explanation,
					Quote:          quote,
					Hint:           hint,
					Points:         question.Points,
					TimestampStart: tStart,
					TimestampEnd:   tEnd,
					AudioAssetID:   part.AudioAssetID,
					Transcript:     part.Transcript,
				})
			}
		}
	}
	return GradingMaterial{ExamType: test.ExamType, Questions: questions}, nil
}

type readingProvider struct {
	service *reading.Service
}

func NewReadingProvider(service *reading.Service) MaterialProvider {
	return readingProvider{service: service}
}

func (p readingProvider) PublishedVersionID(ctx context.Context, materialID uuid.UUID) (uuid.UUID, error) {
	versionID, err := p.service.PublishedVersionID(ctx, materialID)
	if errors.Is(err, reading.ErrNotFound) {
		return uuid.Nil, ErrMaterialNotFound
	}
	return versionID, err
}

func (p readingProvider) PublicStructure(ctx context.Context, materialID, versionID uuid.UUID) (any, error) {
	material, err := p.service.GetVersionPublic(ctx, materialID, versionID)
	if errors.Is(err, reading.ErrNotFound) {
		return nil, ErrMaterialNotFound
	}
	return material, err
}

func (p readingProvider) GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error) {
	material, err := p.service.GetVersion(ctx, materialID, versionID)
	if errors.Is(err, reading.ErrNotFound) {
		return GradingMaterial{}, ErrMaterialNotFound
	}
	if err != nil {
		return GradingMaterial{}, err
	}
	return gradingMaterialFromReading(material), nil
}

// GradingStructures loads many reading versions with a bounded number of
// queries, which is what the mistakes page needs for historical attempts.
func (p readingProvider) GradingStructures(ctx context.Context, refs []MaterialRef) (map[MaterialRef]GradingMaterial, error) {
	versionRefs := make([]reading.VersionRef, 0, len(refs))
	for _, ref := range refs {
		versionRefs = append(versionRefs, reading.VersionRef{
			MaterialID: ref.MaterialID,
			VersionID:  ref.VersionID,
		})
	}
	materials, err := p.service.GradingStructures(ctx, versionRefs)
	if errors.Is(err, reading.ErrNotFound) {
		return nil, ErrMaterialNotFound
	}
	if err != nil {
		return nil, err
	}
	items := make(map[MaterialRef]GradingMaterial, len(refs))
	for _, ref := range refs {
		material, ok := materials[reading.VersionRef{MaterialID: ref.MaterialID, VersionID: ref.VersionID}]
		if !ok {
			return nil, ErrMaterialNotFound
		}
		items[ref] = gradingMaterialFromReading(material)
	}
	return items, nil
}

// gradingMaterialFromReading flattens the reading structure (a test plus its
// passages) into the module-neutral view used for grading and review.
func gradingMaterialFromReading(material reading.Material) GradingMaterial {
	questions := []GradingQuestion{}
	materials := append([]reading.Material{material}, material.Passages...)
	for _, current := range materials {
		for _, group := range current.QuestionGroups {
			for _, question := range group.Questions {
				number := question.Position
				if contentNumber, ok := numericInt(question.Content["number"]); ok {
					number = contentNumber
				}
				quote, _ := question.Content["quote"].(string)
				if quote == "" {
					quote, _ = question.Content["textReference"].(string)
				}
				hint, _ := question.Content["hint"].(string)
				questions = append(questions, GradingQuestion{
					ID:           question.ID,
					Number:       number,
					Prompt:       question.Prompt,
					Type:         group.Type,
					Content:      question.Content,
					Answer:       question.Answer,
					Explanation:  question.Explanation,
					Quote:        quote,
					Hint:         hint,
					Points:       question.Points,
					PassageTitle: current.Title,
					PassageBody:  current.Body,
				})
			}
		}
	}
	return GradingMaterial{ExamType: material.ExamType, Questions: questions}
}

func numericInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		return 0, false
	}
}

type writingProvider struct {
	service *writing.Service
}

func NewWritingProvider(service *writing.Service) MaterialProvider {
	return writingProvider{service: service}
}

func (p writingProvider) PublishedVersionID(ctx context.Context, materialID uuid.UUID) (uuid.UUID, error) {
	versionID, err := p.service.PublishedVersionID(ctx, materialID)
	if errors.Is(err, writing.ErrNotFound) {
		return uuid.Nil, ErrMaterialNotFound
	}
	return versionID, err
}

func (p writingProvider) PublicStructure(ctx context.Context, materialID, versionID uuid.UUID) (any, error) {
	material, err := p.service.GetVersionPublic(ctx, materialID, versionID)
	if errors.Is(err, writing.ErrNotFound) {
		return nil, ErrMaterialNotFound
	}
	return material, err
}

func (p writingProvider) GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error) {
	material, err := p.service.GetVersion(ctx, materialID, versionID)
	if errors.Is(err, writing.ErrNotFound) {
		return GradingMaterial{}, ErrMaterialNotFound
	}
	if err != nil {
		return GradingMaterial{}, err
	}
	tasks := make([]WritingTask, 0, len(material.Tasks))
	for _, task := range material.Tasks {
		tasks = append(tasks, WritingTask{
			ID: task.ID, Position: task.Position, Type: task.Type,
			Prompt: task.Prompt, MinimumWords: task.MinimumWords,
			VisualType: optionalString(task.VisualType), EssayType: optionalString(task.EssayType),
			AssessmentNotes: task.AssessmentNotes,
		})
	}
	return GradingMaterial{ExamType: material.ExamType, WritingTasks: tasks}, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type speakingProvider struct {
	service *speaking.Service
}

func NewSpeakingProvider(service *speaking.Service) MaterialProvider {
	return speakingProvider{service: service}
}

func (p speakingProvider) PublishedVersionID(ctx context.Context, materialID uuid.UUID) (uuid.UUID, error) {
	versionID, err := p.service.PublishedVersionID(ctx, materialID)
	if errors.Is(err, speaking.ErrNotFound) {
		return uuid.Nil, ErrMaterialNotFound
	}
	return versionID, err
}

func (p speakingProvider) PublicStructure(ctx context.Context, materialID, versionID uuid.UUID) (any, error) {
	material, err := p.service.GetVersionPublic(ctx, materialID, versionID)
	if errors.Is(err, speaking.ErrNotFound) {
		return nil, ErrMaterialNotFound
	}
	return material, err
}

func (p speakingProvider) GradingStructure(ctx context.Context, materialID, versionID uuid.UUID) (GradingMaterial, error) {
	material, err := p.service.GetVersion(ctx, materialID, versionID)
	if errors.Is(err, speaking.ErrNotFound) {
		return GradingMaterial{}, ErrMaterialNotFound
	}
	if err != nil {
		return GradingMaterial{}, err
	}
	parts := make([]SpeakingPart, 0, len(material.Parts))
	for _, part := range material.Parts {
		questions := make([]string, 0, len(part.Questions))
		for _, question := range part.Questions {
			questions = append(questions, question.Prompt)
		}
		parts = append(parts, SpeakingPart{
			ID: part.ID, Position: part.Position, Type: part.Type, Title: part.Title,
			Instructions: part.Instructions, CueCard: part.CueCard,
			PreparationSeconds: part.PreparationSeconds, ResponseSeconds: part.ResponseSeconds,
			Questions: questions,
		})
	}
	return GradingMaterial{ExamType: material.ExamType, SpeakingParts: parts}, nil
}
