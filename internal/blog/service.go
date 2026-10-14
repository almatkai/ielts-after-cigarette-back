package blog

import (
	"context"
	"math"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// htmlTagPattern strips tags to estimate reading time from the body.
var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

type Service struct {
	repository Repository
	mediaStore objectstorage.Store
	now        func() time.Time
}

func NewService(repository Repository, mediaStore objectstorage.Store) *Service {
	return &Service{repository: repository, mediaStore: mediaStore, now: time.Now}
}

// List returns every post (drafts included) for the admin blog console.
func (s *Service) List(ctx context.Context) ([]Post, error) {
	items, err := s.repository.List(ctx)
	if items == nil {
		items = []Post{}
	}
	return items, err
}

// ListPublished returns a page of published posts for the public blog index.
func (s *Service) ListPublished(ctx context.Context, limit, offset int) ([]PostSummary, int, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, err := s.repository.ListPublished(ctx, limit, offset)
	if items == nil {
		items = []PostSummary{}
	}
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repository.CountPublished(ctx)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Post, error) {
	return s.repository.Get(ctx, id)
}

// GetPublic returns a published post by slug for the public site.
func (s *Service) GetPublic(ctx context.Context, slug string) (PublicPost, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" || !slugPattern.MatchString(slug) {
		return PublicPost{}, ErrNotFound
	}
	return s.repository.GetPublished(ctx, slug)
}

// ListMine returns posts owned by the requesting writer.
func (s *Service) ListMine(ctx context.Context, authorID uuid.UUID) ([]Post, error) {
	items, err := s.repository.ListByAuthor(ctx, authorID)
	if items == nil {
		items = []Post{}
	}
	return items, err
}

func (s *Service) Create(ctx context.Context, authorID uuid.UUID, input SaveInput) (Post, map[string]string, error) {
	input = s.normalizeInput(input)
	if input.Slug == "" {
		input.Slug = "post-" + uuid.NewString()[:8]
	}
	if details := s.validateInput(input); len(details) > 0 {
		return Post{}, details, nil
	}
	post, err := s.repository.Create(ctx, authorID, input)
	return post, nil, err
}

// Update lets the author edit their own post, or an editor/admin edit any
// post. Only unpublished posts accept body edits; published posts move to
// archive instead.
func (s *Service) Update(ctx context.Context, id, actorID, actorRole uuid.UUID, role string, input SaveInput) (Post, map[string]string, error) {
	input = s.normalizeInput(input)
	if details := s.validateInput(input); len(details) > 0 {
		return Post{}, details, nil
	}
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return Post{}, nil, err
	}
	if role != "EDITOR" && role != "ADMIN" && current.Author.ID != actorID {
		return Post{}, nil, ErrNotAuthor
	}
	if current.Status == StatusPublished && input.BodyHTML != current.BodyHTML {
		return Post{}, nil, ErrAlreadyPublished
	}
	post, err := s.repository.Update(ctx, id, actorID, input)
	return post, nil, err
}

func (s *Service) Publish(ctx context.Context, id, actorID uuid.UUID, role string, input PublishInput) (Post, map[string]string, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return Post{}, nil, err
	}
	if role != "EDITOR" && role != "ADMIN" && current.Author.ID != actorID {
		return Post{}, nil, ErrNotAuthor
	}
	if details := s.validateForPublish(current); len(details) > 0 {
		return Post{}, details, nil
	}
	post, err := s.repository.Publish(ctx, id, input.Revision)
	if err == nil && current.Status != StatusPublished {
		_ = post
	}
	return post, nil, err
}

func (s *Service) Archive(ctx context.Context, id, actorID uuid.UUID, role string) (Post, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return Post{}, err
	}
	if role != "EDITOR" && role != "ADMIN" && current.Author.ID != actorID {
		return Post{}, ErrNotAuthor
	}
	return s.repository.Archive(ctx, id)
}

func (s *Service) validateForPublish(post Post) map[string]string {
	details := map[string]string{}
	if utf8.RuneCountInString(strings.TrimSpace(post.Title)) < 3 {
		details["title"] = "must contain at least 3 characters"
	}
	if utf8.RuneCountInString(strings.TrimSpace(stripHTML(post.BodyHTML))) < 100 {
		details["bodyHtml"] = "publish a body with at least 100 characters of plain text"
	}
	return details
}

func stripHTML(value string) string {
	return htmlTagPattern.ReplaceAllString(value, " ")
}

// EstimadeReadingTime approximates reading time at 200 words per minute.
func estimateReadingTime(bodyHTML string) int {
	plain := stripHTML(bodyHTML)
	words := strings.Fields(plain)
	estimate := len(words) / 200
	if estimate < 1 {
		return 1
	}
	if estimate > 120 {
		return 120
	}
	return estimate
}

// Apply collects a writer application from a student. The certificate file
// must already be uploaded through UploadMedia.
func (s *Service) Apply(ctx context.Context, userID uuid.UUID, input ApplyInput, certificateMediaID uuid.UUID) (Application, map[string]string, error) {
	input = normalizeApplyInput(input)
	if details := validateApplyInput(input); len(details) > 0 {
		return Application{}, details, nil
	}
	certificate, err := s.repository.GetCertificate(ctx, certificateMediaID, false)
	userIDMatches := err == nil && certificate.UploadedBy == userID
	if err != nil {
		if errors.Is(err, ErrMediaNotFound) {
			return Application{}, map[string]string{"certificateMediaId": "upload the certificate file first"}, nil
		}
		return Application{}, nil, err
	}
	if !userIDMatches {
		return Application{}, map[string]string{"certificateMediaId": "certificate was uploaded by another user"}, nil
	}
	application, err := s.repository.CreateApplication(ctx, userID, input, certificate)
	return application, nil, err
}

func (s *Service) MyApplication(ctx context.Context, userID uuid.UUID) (Application, bool, error) {
	return s.repository.MyApplication(ctx, userID)
}

// listApplications lists writer applications, optionally filtered by status.
func (s *Service) ListApplications(ctx context.Context, status string) ([]Application, error) {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "":
		status = ""
	case ApplicationPending, ApplicationApproved, ApplicationRejected:
	default:
		return nil, fmt.Errorf("status filter must be PENDING, APPROVED, or REJECTED")
	}
	items, err := s.repository.ListApplications(ctx, status)
	if items == nil {
		items = []Application{}
	}
	return items, err
}

func (s *Service) GetApplication(ctx context.Context, id uuid.UUID) (Application, error) {
	return s.repository.GetApplication(ctx, id)
}

// ReviewApplication approves or rejects a pending application. Approval
// grants the WRITER role inside the same transaction.
func (s *Service) ReviewApplication(ctx context.Context, applicationID, reviewerID uuid.UUID, approve bool, notes string) (Application, map[string]string, error) {
	if utf8.RuneCountInString(notes) > 2000 {
		return Application{}, map[string]string{"notes": "must contain at most 2000 characters"}, nil
	}
	status := ApplicationRejected
	if approve {
		status = ApplicationApproved
	}
	application, err := s.repository.ReviewApplication(ctx, applicationID, reviewerID, status, notes)
	return application, nil, err
}

// Certificate returns the stored certificate file for admins reviewing an
// application, or for the applicant themselves.
func (s *Service) Certificate(ctx context.Context, applicationID, requesterID uuid.UUID, isAdmin bool) (CertificateFile, error) {
	application, err := s.repository.GetApplication(ctx, applicationID)
	if err != nil {
		return CertificateFile{}, err
	}
	if !isAdmin && application.UserID != requesterID {
		return CertificateFile{}, ErrNotFound
	}
	return s.repository.GetCertificate(ctx, application.CertificateMediaID, false)
}

func (s *Service) StoreMedia(ctx context.Context, uploaderID uuid.UUID, header *multipart.FileHeader, source io.Reader) (Media, error) {
	if s.mediaStore == nil {
		return Media{}, errors.New("blog media storage is not configured")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	mimeTypes := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
		".gif": "image/gif", ".pdf": "application/pdf",
	}
	mimeType, ok := mimeTypes[ext]
	if !ok {
		return Media{}, fmt.Errorf("%w: use PNG, JPG, WebP, GIF, or PDF", ErrUnsupportedMedia)
	}
	key := "blog/" + uuid.NewString() + ext
	written, err := s.mediaStore.Put(ctx, key, mimeType, source, header.Size)
	if err != nil {
		return Media{}, err
	}
	media, err := s.repository.CreateMedia(ctx, uploaderID, Media{
		Kind:         "image",
		OriginalName: filepath.Base(header.Filename),
		MimeType:     mimeType,
		StorageKey:   key,
		ByteSize:     written,
	})
	if err != nil {
		_ = s.mediaStore.Delete(ctx, key)
		return Media{}, err
	}
	return media, nil
}

// OpenMedia opens a media record against object storage.
func (s *Service) OpenMedia(ctx context.Context, media Media) (objectstorage.ReadSeekCloser, error) {
	if s.mediaStore == nil {
		return nil, errors.New("blog media storage is not configured")
	}
	return s.mediaStore.Open(ctx, media.StorageKey)
}

// Media opens a stored media object. publishedOnly restricts viewers to media
// referenced by published posts or certificates awaiting review.
func (s *Service) Media(ctx context.Context, id uuid.UUID, publishedOnly bool) (Media, objectstorage.ReadSeekCloser, error) {
	if s.mediaStore == nil {
		return Media{}, nil, errors.New("blog media storage is not configured")
	}
	media, err := s.repository.GetMedia(ctx, id)
	if err != nil {
		return Media{}, nil, err
	}
	object, err := s.mediaStore.Open(ctx, media.StorageKey)
	if err != nil {
		return Media{}, nil, err
	}
	return media, object, nil
}

func (s *Service) normalizeInput(input SaveInput) SaveInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.BodyHTML = strings.TrimSpace(sanitizeBody(input.BodyHTML))
	if input.BodyJSON == nil {
		input.BodyJSON = map[string]any{}
	}
	return input
}

func (s *Service) validateInput(input SaveInput) map[string]string {
	details := map[string]string{}
	if len(input.Slug) > 255 || !slugPattern.MatchString(input.Slug) {
		details["slug"] = "must contain lowercase Latin letters, numbers, and single hyphens"
	}
	if length := utf8.RuneCountInString(input.Title); length < 3 || length > 300 {
		details["title"] = "must contain between 3 and 300 characters"
	}
	if utf8.RuneCountInString(input.Description) > 2000 {
		details["description"] = "must contain at most 2000 characters"
	}
	if bodyLength := utf8.RuneCountInString(input.BodyHTML); bodyLength == 0 || bodyLength > 200_000 {
		details["bodyHtml"] = "must contain between 1 and 200000 characters"
	}
	return details
}

func normalizeApplyInput(input ApplyInput) ApplyInput {
	input.TRFNumber = strings.TrimSpace(input.TRFNumber)
	input.Bio = strings.TrimSpace(input.Bio)
	if input.ExamType != nil {
		examType := strings.ToLower(strings.TrimSpace(*input.ExamType))
		if examType == "" {
			input.ExamType = nil
		} else {
			input.ExamType = &examType
		}
	}
	if input.TestDate != nil {
		testDate := strings.TrimSpace(*input.TestDate)
		if testDate == "" {
			input.TestDate = nil
		} else {
			input.TestDate = &testDate
		}
	}
	return input
}

func validateApplyInput(input ApplyInput) map[string]string {
	details := map[string]string{}
	if input.OverallBand < MinimumAttestedBand || input.OverallBand > 9 || input.OverallBand*2 != math.Trunc(input.OverallBand*2) {
		details["overallBand"] = "an official IELTS overall band of at least 7.5 is required"
	}
	if input.ListeningBand != nil && !validHalfBand(*input.ListeningBand) {
		details["listeningBand"] = "must be between 0 and 9 in half-band steps"
	}
	if !validHalfBand(input.ReadingBand) {
		details["readingBand"] = "must be between 0 and 9 in half-band steps"
	}
	if !validHalfBand(input.WritingBand) {
		details["writingBand"] = "must be between 0 and 9 in half-band steps"
	}
	if !validHalfBand(input.SpeakingBand) {
		details["speakingBand"] = "must be between 0 and 9 in half-band steps"
	}
	if length := utf8.RuneCountInString(input.TRFNumber); length < 6 || length > 64 {
		details["trfNumber"] = "must contain between 6 and 64 characters"
	}
	if input.TestDate != nil && !datePattern.MatchString(*input.TestDate) {
		details["testDate"] = "must be a date in YYYY-MM-DD format"
	}
	if input.ExamType != nil && *input.ExamType != "academic" && *input.ExamType != "general" {
		details["examType"] = "must be academic or general"
	}
	if utf8.RuneCountInString(input.Bio) > 2000 {
		details["bio"] = "must contain at most 2000 characters"
	}
	return details
}

func validHalfBand(value float64) bool {
	return value >= 0 && value <= 9 && value*2 == math.Trunc(value*2)
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
