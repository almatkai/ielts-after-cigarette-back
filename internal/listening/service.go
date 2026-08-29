package listening

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Service struct {
	repository Repository
	mediaDir   string
}

func NewService(repository Repository, mediaDir string) *Service {
	return &Service{repository: repository, mediaDir: mediaDir}
}

func (s *Service) ListAdmin(ctx context.Context) ([]Test, error) {
	return s.repository.List(ctx, false)
}
func (s *Service) GetAdmin(ctx context.Context, id uuid.UUID) (Test, error) {
	return s.repository.Get(ctx, id, false)
}
func (s *Service) ListPublic(ctx context.Context) ([]PublicTest, error) {
	items, err := s.repository.List(ctx, true)
	if err != nil {
		return nil, err
	}
	result := make([]PublicTest, len(items))
	for i, item := range items {
		result[i] = publicTest(item)
	}
	return result, nil
}
func (s *Service) GetPublic(ctx context.Context, id uuid.UUID) (PublicTest, error) {
	item, err := s.repository.Get(ctx, id, true)
	if err != nil {
		return PublicTest{}, err
	}
	return publicTest(item), nil
}

// GetVersion returns the full structure (with answers) of a specific version.
func (s *Service) GetVersion(ctx context.Context, id, versionID uuid.UUID) (Test, error) {
	return s.repository.GetVersion(ctx, id, versionID)
}

// GetVersionPublic returns the public structure (without answers) of a
// specific version.
func (s *Service) GetVersionPublic(ctx context.Context, id, versionID uuid.UUID) (PublicTest, error) {
	item, err := s.repository.GetVersion(ctx, id, versionID)
	if err != nil {
		return PublicTest{}, err
	}
	return publicTest(item), nil
}

// PublishedVersionID returns the published version of a PUBLISHED test.
func (s *Service) PublishedVersionID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.repository.PublishedVersionID(ctx, id)
}

func (s *Service) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Test, map[string]string, error) {
	input = normalize(input)
	if input.Slug == "" {
		input.Slug = "listening-" + uuid.NewString()[:8]
	}
	if details := validate(input, false); len(details) > 0 {
		return Test{}, details, nil
	}
	item, err := s.repository.Create(ctx, actorID, input)
	return item, nil, err
}
func (s *Service) Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Test, map[string]string, error) {
	input = normalize(input)
	if details := validate(input, true); len(details) > 0 {
		return Test{}, details, nil
	}
	item, err := s.repository.Update(ctx, id, actorID, input)
	return item, nil, err
}
func (s *Service) Publish(ctx context.Context, id, actorID uuid.UUID, revision int64) (Test, map[string]string, error) {
	if revision < 1 {
		return Test{}, map[string]string{"revision": "must be positive"}, nil
	}
	item, err := s.repository.Publish(ctx, id, actorID, revision)
	return item, nil, err
}

func normalize(input SaveInput) SaveInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	input.Title, input.Description = strings.TrimSpace(input.Title), strings.TrimSpace(input.Description)
	if input.DurationMinutes == 0 {
		input.DurationMinutes = 40
	}
	for pi := range input.Parts {
		part := &input.Parts[pi]
		part.Position = pi + 1
		part.Title = strings.TrimSpace(part.Title)
		for gi := range part.Groups {
			group := &part.Groups[gi]
			group.Position = gi + 1
			group.Type = strings.ToLower(strings.TrimSpace(group.Type))
			group.Instructions = strings.TrimSpace(group.Instructions)
			group.Context = strings.TrimSpace(group.Context)
			if group.Config == nil {
				group.Config = map[string]any{}
			}
			for qi := range group.Questions {
				q := &group.Questions[qi]
				q.Position = qi + 1
				q.Prompt = strings.TrimSpace(q.Prompt)
				q.Explanation = strings.TrimSpace(q.Explanation)
				if q.Content == nil {
					q.Content = map[string]any{}
				}
				if q.Answer == nil {
					q.Answer = map[string]any{}
				}
				if q.Points < 1 {
					q.Points = 1
				}
			}
		}
	}
	return input
}

func validate(input SaveInput, revision bool) map[string]string {
	d := map[string]string{}
	if !slugPattern.MatchString(input.Slug) || len(input.Slug) > 160 {
		d["slug"] = "must use lowercase Latin letters, numbers and hyphens"
	}
	if input.ExamType != "academic" && input.ExamType != "general" {
		d["examType"] = "must be academic or general"
	}
	if n := utf8.RuneCountInString(input.Title); n < 3 || n > 200 {
		d["title"] = "must contain 3-200 characters"
	}
	if input.DurationMinutes < 1 || input.DurationMinutes > 180 {
		d["durationMinutes"] = "must be between 1 and 180"
	}
	if revision && input.Revision < 1 {
		d["revision"] = "must be positive"
	}
	if len(input.Parts) < 1 || len(input.Parts) > 10 {
		d["parts"] = "must contain 1-10 parts"
		return d
	}
	numbers := map[int]bool{}
	for pi, part := range input.Parts {
		if len(part.Groups) < 1 {
			d[fmt.Sprintf("parts[%d].groups", pi)] = "must not be empty"
		}
		for gi, group := range part.Groups {
			if !supported(group.Type) {
				d[fmt.Sprintf("parts[%d].groups[%d].type", pi, gi)] = "unsupported type"
			}
			if len(group.Questions) < 1 || len(group.Questions) > 50 {
				d[fmt.Sprintf("parts[%d].groups[%d].questions", pi, gi)] = "must contain 1-50 questions"
			}
			for qi, q := range group.Questions {
				path := fmt.Sprintf("parts[%d].groups[%d].questions[%d]", pi, gi, qi)
				if q.Number < 1 || numbers[q.Number] {
					d[path+".number"] = "must be a unique positive number"
				}
				numbers[q.Number] = true
				if q.Prompt == "" {
					d[path+".prompt"] = "is required"
				}
				if len(q.Answer) == 0 {
					d[path+".answer"] = "is required"
				}
			}
		}
	}
	return d
}
func supported(value string) bool {
	for _, v := range SupportedQuestionTypes {
		if v == value {
			return true
		}
	}
	return false
}

func publicTest(item Test) PublicTest {
	result := PublicTest{ID: item.ID, Slug: item.Slug, ExamType: item.ExamType, Title: item.Title, Description: item.Description, DurationMinutes: item.DurationMinutes, Parts: []PublicPart{}}
	for _, part := range item.Parts {
		pp := PublicPart{ID: part.ID, Position: part.Position, Title: part.Title, AudioAssetID: part.AudioAssetID, Groups: []PublicQuestionGroup{}}
		for _, group := range part.Groups {
			pg := PublicQuestionGroup{ID: group.ID, Position: group.Position, Type: group.Type, Instructions: group.Instructions, Context: group.Context, Config: group.Config, ImageAssetID: group.ImageAssetID, Questions: []PublicQuestion{}}
			for _, q := range group.Questions {
				pg.Questions = append(pg.Questions, PublicQuestion{ID: q.ID, Position: q.Position, Number: q.Number, Prompt: q.Prompt, Content: q.Content, Points: q.Points})
			}
			pp.Groups = append(pp.Groups, pg)
		}
		result.Parts = append(result.Parts, pp)
	}
	return result
}

func (s *Service) StoreMedia(ctx context.Context, actorID uuid.UUID, kind string, header *multipart.FileHeader, source io.Reader) (Media, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]map[string]bool{"audio": {".mp3": true, ".m4a": true, ".wav": true, ".ogg": true, ".webm": true}, "image": {".png": true, ".jpg": true, ".jpeg": true, ".webp": true}}
	if !allowed[kind][ext] {
		return Media{}, fmt.Errorf("unsupported %s file type", kind)
	}
	mimeTypes := map[string]string{".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".wav": "audio/wav", ".ogg": "audio/ogg", ".webm": "audio/webm", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}
	if err := os.MkdirAll(s.mediaDir, 0o750); err != nil {
		return Media{}, err
	}
	key := uuid.NewString() + ext
	target := filepath.Join(s.mediaDir, key)
	temporary := target + ".upload"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return Media{}, err
	}
	written, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return Media{}, copyErr
		}
		return Media{}, closeErr
	}
	if err = os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return Media{}, err
	}
	media, err := s.repository.CreateMedia(ctx, actorID, Media{Kind: kind, OriginalName: filepath.Base(header.Filename), MimeType: mimeTypes[ext], StorageKey: key, ByteSize: written})
	if err != nil {
		_ = os.Remove(target)
		return Media{}, err
	}
	return media, nil
}
func (s *Service) Media(ctx context.Context, id uuid.UUID, publishedOnly bool) (Media, string, error) {
	media, err := s.repository.GetMedia(ctx, id, publishedOnly)
	if err != nil {
		return Media{}, "", err
	}
	return media, filepath.Join(s.mediaDir, media.StorageKey), nil
}
