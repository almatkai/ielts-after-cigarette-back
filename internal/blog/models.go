package blog

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("blog post not found")
	ErrApplicationNotFound = errors.New("writer application not found")
	ErrSlugExists          = errors.New("blog post slug already exists")
	ErrMediaNotFound       = errors.New("blog media not found")
	ErrUnsupportedMedia    = errors.New("unsupported blog media type")
	ErrNotAuthor           = errors.New("user is not the author of this post")
	ErrAlreadyPublished    = errors.New("blog post is already published")
	ErrApplicationExists   = errors.New("a writer application is already pending")
	ErrWriterRoleHeld      = errors.New("user already has the writer role")
	ErrBandTooLow          = errors.New("overall band must be at least 7.5")
)

const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"

	ApplicationPending  = "PENDING"
	ApplicationApproved = "APPROVED"
	ApplicationRejected = "REJECTED"
)

// MinimumAttestedBand is the lowest official IELTS overall band a writer may
// attest in an application.
const MinimumAttestedBand = 7.5

type Author struct {
	ID            uuid.UUID `json:"id"`
	DisplayName   string    `json:"displayName"`
	AvatarInitial string    `json:"avatarInitial"`
	Role          string    `json:"role"`
}

type Post struct {
	ID                 uuid.UUID      `json:"id"`
	Author             Author         `json:"author"`
	Slug               string         `json:"slug"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	CoverMediaID       *uuid.UUID     `json:"coverMediaId,omitempty"`
	BodyHTML           string         `json:"bodyHtml"`
	BodyJSON           map[string]any `json:"bodyJson"`
	ReadingTimeMinutes int            `json:"readingTimeMinutes"`
	Status             string         `json:"status"`
	PublishedAt        *time.Time     `json:"publishedAt"`
	ContentUpdatedAt   *time.Time     `json:"contentUpdatedAt"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

// PostSummary is the list-view projection: everything a blog index card
// needs, without the body.
type PostSummary struct {
	ID                 uuid.UUID  `json:"id"`
	Author             Author     `json:"author"`
	Slug               string     `json:"slug"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	CoverMediaID       *uuid.UUID `json:"coverMediaId,omitempty"`
	ReadingTimeMinutes int        `json:"readingTimeMinutes"`
	Status             string     `json:"status"`
	PublishedAt        *time.Time `json:"publishedAt"`
	ContentUpdatedAt   *time.Time `json:"contentUpdatedAt"`
}

type PublicPost struct {
	ID                 uuid.UUID  `json:"id"`
	Author             Author     `json:"author"`
	Slug               string     `json:"slug"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	CoverMediaID       *uuid.UUID `json:"coverMediaId,omitempty"`
	BodyHTML           string     `json:"bodyHtml"`
	ReadingTimeMinutes int        `json:"readingTimeMinutes"`
	PublishedAt        *time.Time `json:"publishedAt"`
	ContentUpdatedAt   *time.Time `json:"contentUpdatedAt"`
}

// SaveInput carries editor changes. Revision enables optimistic locking used
// by the admin editor; it is ignored for writer-owned saves.
type SaveInput struct {
	Slug         string         `json:"slug"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	CoverMediaID *uuid.UUID     `json:"coverMediaId"`
	BodyHTML     string         `json:"bodyHtml"`
	BodyJSON     map[string]any `json:"bodyJson"`
	Revision     *int64         `json:"revision"`
}

type PublishInput struct {
	Revision *int64 `json:"revision"`
}

type ReviewInput struct {
	Notes string `json:"notes"`
}

type Application struct {
	ID                 uuid.UUID  `json:"id"`
	UserID             uuid.UUID  `json:"userId"`
	UserEmail          string     `json:"userEmail"`
	UserDisplayName    string     `json:"userDisplayName"`
	OverallBand        float64    `json:"overallBand"`
	ListeningBand      *float64   `json:"listeningBand"`
	ReadingBand        float64    `json:"readingBand"`
	WritingBand        float64    `json:"writingBand"`
	SpeakingBand       float64    `json:"speakingBand"`
	TRFNumber          string     `json:"trfNumber"`
	TestDate           *string    `json:"testDate"`
	ExamType           *string    `json:"examType"`
	Bio                string     `json:"bio"`
	CertificateMediaID uuid.UUID  `json:"certificateMediaId"`
	CertificateMime    string     `json:"certificateMime"`
	Status             string     `json:"status"`
	ReviewNotes        string     `json:"reviewNotes"`
	ReviewedBy         *uuid.UUID `json:"reviewedBy"`
	ReviewedAt         *time.Time `json:"reviewedAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type ApplyInput struct {
	OverallBand        float64   `json:"overallBand"`
	ListeningBand      *float64  `json:"listeningBand"`
	ReadingBand        float64   `json:"readingBand"`
	WritingBand        float64   `json:"writingBand"`
	SpeakingBand       float64   `json:"speakingBand"`
	TRFNumber          string    `json:"trfNumber"`
	TestDate           *string   `json:"testDate"`
	ExamType           *string   `json:"examType"`
	Bio                string    `json:"bio"`
	CertificateMediaID uuid.UUID `json:"certificateMediaId"`
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
