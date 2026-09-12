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
				questions = append(questions, GradingQuestion{
					ID: question.ID, Number: question.Number, Prompt: question.Prompt,
					Answer: question.Answer, Explanation: question.Explanation, Points: question.Points,
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
	questions := []GradingQuestion{}
	for _, group := range material.QuestionGroups {
		for _, question := range group.Questions {
			questions = append(questions, GradingQuestion{
				// Reading questions have no number; position identifies them.
				ID: question.ID, Number: question.Position, Prompt: question.Prompt,
				Answer: question.Answer, Explanation: question.Explanation, Points: question.Points,
			})
		}
	}
	return GradingMaterial{ExamType: material.ExamType, Questions: questions}, nil
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
		})
	}
	return GradingMaterial{ExamType: material.ExamType, WritingTasks: tasks}, nil
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
