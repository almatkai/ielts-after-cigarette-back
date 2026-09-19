package writing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
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
	Archive(context.Context, uuid.UUID, uuid.UUID, int64) (Material, error)
	CreateMedia(context.Context, uuid.UUID, Media) (Media, error)
	GetMedia(context.Context, uuid.UUID, bool) (Media, error)
}

type Service struct {
	repository Repository
	mediaStore objectstorage.Store
}

func NewService(repository Repository, stores ...objectstorage.Store) *Service {
	var mediaStore objectstorage.Store
	if len(stores) > 0 {
		mediaStore = stores[0]
	}
	return &Service{repository: repository, mediaStore: mediaStore}
}

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
		input.Slug = "writing-" + uuid.NewString()[:8]
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
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return Material{}, nil, err
	}
	if details := validateForPublish(current); len(details) > 0 {
		return Material{}, details, nil
	}
	if current.ExamType == "academic" && current.Tasks[0].VisualAssetID != nil {
		if _, err := s.repository.GetMedia(ctx, *current.Tasks[0].VisualAssetID, false); err != nil {
			if errors.Is(err, ErrMediaNotFound) {
				return Material{}, map[string]string{
					"tasks[0].visualAssetId": "uploaded visual was not found; upload it again",
				}, nil
			}
			return Material{}, nil, err
		}
	}
	material, err := s.repository.Publish(ctx, id, actorID, revision)
	return material, nil, err
}

func (s *Service) Archive(ctx context.Context, id, actorID uuid.UUID, revision int64) (Material, map[string]string, error) {
	if revision < 1 {
		return Material{}, map[string]string{"revision": "must be a positive integer"}, nil
	}
	material, err := s.repository.Archive(ctx, id, actorID, revision)
	return material, nil, err
}

func (s *Service) ParseImport(input ImportParseInput) ImportResult {
	result := ImportResult{Materials: []SaveInput{}, Errors: []ImportIssue{}}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		result.Errors = append(result.Errors, ImportIssue{Code: "EMPTY_SOURCE", Message: "source is required"})
		return result
	}
	var envelope struct {
		Materials []SaveInput `json:"materials"`
	}
	if strings.HasPrefix(source, "[") {
		if err := json.Unmarshal([]byte(source), &envelope.Materials); err != nil {
			result.Errors = append(result.Errors, ImportIssue{Code: "INVALID_JSON", Message: "source must be valid JSON"})
			return result
		}
	} else if err := json.Unmarshal([]byte(source), &envelope); err != nil {
		result.Errors = append(result.Errors, ImportIssue{Code: "INVALID_JSON", Message: "source must be a JSON object with materials"})
		return result
	}
	if len(envelope.Materials) == 0 {
		result.Errors = append(result.Errors, ImportIssue{Code: "EMPTY_IMPORT", Message: "materials must contain at least one item"})
		return result
	}
	for index, material := range envelope.Materials {
		material = normalizeInput(material)
		if material.Slug == "" {
			material.Slug = "writing-" + uuid.NewString()[:8]
		}
		if details := validateInput(material, false); len(details) > 0 {
			for field, message := range details {
				result.Errors = append(result.Errors, ImportIssue{Code: "VALIDATION_ERROR", Message: field + ": " + message, Item: index + 1})
			}
			continue
		}
		result.Materials = append(result.Materials, material)
	}
	return result
}

func (s *Service) BulkCreate(ctx context.Context, actorID uuid.UUID, inputs []SaveInput) ([]Material, map[string]string, error) {
	if len(inputs) == 0 || len(inputs) > 20 {
		return nil, map[string]string{"materials": "must contain between 1 and 20 materials"}, nil
	}
	normalized := make([]SaveInput, len(inputs))
	for index, input := range inputs {
		input = normalizeInput(input)
		if input.Slug == "" {
			input.Slug = "writing-" + uuid.NewString()[:8]
		}
		if details := validateInput(input, false); len(details) > 0 {
			prefixed := map[string]string{}
			for field, message := range details {
				prefixed["materials["+strconv.Itoa(index)+"]."+field] = message
			}
			return nil, prefixed, nil
		}
		normalized[index] = input
	}
	items, err := s.repository.CreateMany(ctx, actorID, normalized)
	return items, nil, err
}

func publicMaterial(material Material) PublicMaterial {
	tasks := make([]PublicTask, 0, len(material.Tasks))
	for _, task := range material.Tasks {
		tasks = append(tasks, PublicTask{
			ID: task.ID, Position: task.Position, Type: task.Type, Prompt: task.Prompt,
			MinimumWords: task.MinimumWords, VisualType: task.VisualType,
			VisualURL: task.VisualURL, VisualAssetID: task.VisualAssetID,
			EssayType: task.EssayType, LetterTone: task.LetterTone,
		})
	}
	return PublicMaterial{
		ID: material.ID, Slug: material.Slug, ExamType: material.ExamType,
		Difficulty: material.Difficulty, Title: material.Title,
		Description: material.Description, DurationMinutes: material.DurationMinutes, Tasks: tasks,
	}
}

func normalizeInput(input SaveInput) SaveInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	input.Difficulty = strings.ToLower(strings.TrimSpace(input.Difficulty))
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.DurationMinutes == 0 {
		input.DurationMinutes = 60
	}
	for index := range input.Tasks {
		task := &input.Tasks[index]
		if task.ID == uuid.Nil {
			task.ID = uuid.New()
		}
		task.Position = index + 1
		task.Type = strings.ToLower(strings.TrimSpace(task.Type))
		task.Prompt = strings.TrimSpace(task.Prompt)
		task.VisualType = normalizeOptional(task.VisualType)
		task.VisualURL = normalizeOptional(task.VisualURL)
		task.AssessmentNotes = strings.TrimSpace(task.AssessmentNotes)
		task.EssayType = normalizeOptional(task.EssayType)
		task.LetterTone = normalizeOptional(task.LetterTone)
		if task.MinimumWords < 1 {
			if index == 0 {
				task.MinimumWords = 150
			} else {
				task.MinimumWords = 250
			}
		}
	}
	return input
}

func normalizeOptional(value *string) *string {
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
	if input.Difficulty != "foundation" && input.Difficulty != "intermediate" && input.Difficulty != "advanced" {
		details["difficulty"] = "must be foundation, intermediate, or advanced"
	}
	if length := utf8.RuneCountInString(input.Title); length < 3 || length > 200 {
		details["title"] = "must contain between 3 and 200 characters"
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		details["description"] = "must contain at most 1000 characters"
	}
	if input.DurationMinutes < 5 || input.DurationMinutes > 180 {
		details["durationMinutes"] = "must be between 5 and 180"
	}
	if requireRevision && input.Revision < 1 {
		details["revision"] = "must be a positive integer"
	}
	if len(input.Tasks) != 2 {
		details["tasks"] = "must contain exactly Task 1 and Task 2"
		return details
	}
	for index, task := range input.Tasks {
		expectedType := TaskOne
		if index == 1 {
			expectedType = TaskTwo
		}
		prefix := "tasks[" + strconv.Itoa(index) + "]"
		if task.Type != expectedType {
			details[prefix+".type"] = "must be " + expectedType
		}
		if length := utf8.RuneCountInString(task.Prompt); length < 20 || length > 20000 {
			details[prefix+".prompt"] = "must contain between 20 and 20000 characters"
		}
		if task.MinimumWords < 1 || task.MinimumWords > 1000 {
			details[prefix+".minimumWords"] = "must be between 1 and 1000"
		}
		if index == 0 && input.ExamType == "academic" {
			if task.VisualType == nil || !oneOf(*task.VisualType, "bar_chart", "line_graph", "pie_chart", "table", "diagram", "process", "map", "mixed") {
				details[prefix+".visualType"] = "Academic Task 1 needs a supported visualType"
			}
			if task.VisualURL != nil {
				if parsed, err := url.ParseRequestURI(*task.VisualURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
					details[prefix+".visualUrl"] = "must be a valid https URL"
				}
			}
		}
		if index == 1 && task.EssayType != nil && !oneOf(*task.EssayType,
			"opinion", "discussion", "advantages_disadvantages", "problem_solution", "two_part") {
			details[prefix+".essayType"] = "must be a supported IELTS essay type"
		}
		if index == 0 && input.ExamType == "general" {
			if task.LetterTone == nil || !oneOf(*task.LetterTone, "formal", "semi-formal", "informal") {
				details[prefix+".letterTone"] = "General Task 1 needs formal, semi-formal, or informal letterTone"
			}
		}
	}
	return details
}

func validateForPublish(material Material) map[string]string {
	details := map[string]string{}
	if len(material.Tasks) != 2 {
		details["tasks"] = "must contain exactly Task 1 and Task 2"
		return details
	}
	first := material.Tasks[0]
	if material.ExamType == "academic" {
		if first.VisualAssetID == nil && first.VisualURL == nil {
			details["tasks[0].visualAssetId"] = "Academic Task 1 needs an uploaded visual"
		}
		if utf8.RuneCountInString(first.AssessmentNotes) < 20 {
			details["tasks[0].assessmentNotes"] = "describe the visual's key facts and comparisons for AI assessment"
		}
	}
	return details
}

func (s *Service) StoreMedia(ctx context.Context, actorID uuid.UUID, header *multipart.FileHeader, source io.Reader) (Media, error) {
	if s.mediaStore == nil {
		return Media{}, errors.New("writing media storage is not configured")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	mimeTypes := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	}
	mimeType, ok := mimeTypes[ext]
	if !ok {
		return Media{}, fmt.Errorf("%w: use PNG, JPG, or WebP", ErrUnsupportedMedia)
	}
	key := "writing/" + uuid.NewString() + ext
	written, err := s.mediaStore.Put(ctx, key, mimeType, source, header.Size)
	if err != nil {
		return Media{}, err
	}
	media, err := s.repository.CreateMedia(ctx, actorID, Media{
		Kind: "image", OriginalName: filepath.Base(header.Filename), MimeType: mimeType,
		StorageKey: key, ByteSize: written,
	})
	if err != nil {
		_ = s.mediaStore.Delete(ctx, key)
		return Media{}, err
	}
	return media, nil
}

func (s *Service) Media(ctx context.Context, id uuid.UUID, publishedOnly bool) (Media, objectstorage.ReadSeekCloser, error) {
	if s.mediaStore == nil {
		return Media{}, nil, errors.New("writing media storage is not configured")
	}
	media, err := s.repository.GetMedia(ctx, id, publishedOnly)
	if err != nil {
		return Media{}, nil, err
	}
	object, err := s.mediaStore.Open(ctx, media.StorageKey)
	if err != nil {
		return Media{}, nil, err
	}
	return media, object, nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
