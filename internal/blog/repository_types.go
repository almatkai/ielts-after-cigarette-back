package blog

import (
	"context"

	"github.com/google/uuid"
)

// CertificateFile is a media object uploaded as an IELTS certificate. The
// uploader must match the applicant; admins review the file contents.
type CertificateFile struct {
	MediaID    uuid.UUID
	StorageKey string
	MimeType   string
	UploadedBy uuid.UUID
}

type Repository interface {
	// Posts
	List(ctx context.Context) ([]Post, error)
	ListPublished(ctx context.Context, limit, offset int) ([]PostSummary, error)
	CountPublished(ctx context.Context) (int, error)
	Get(ctx context.Context, id uuid.UUID) (Post, error)
	GetPublished(ctx context.Context, slug string) (PublicPost, error)
	ListByAuthor(ctx context.Context, authorID uuid.UUID) ([]Post, error)
	Create(ctx context.Context, authorID uuid.UUID, input SaveInput) (Post, error)
	Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Post, error)
	Publish(ctx context.Context, id uuid.UUID, expectedRevision *int64) (Post, error)
	Archive(ctx context.Context, id uuid.UUID) (Post, error)

	// Media (cover images, inline images, and certificates)
	CreateMedia(ctx context.Context, uploadedBy uuid.UUID, media Media) (Media, error)
	GetMedia(ctx context.Context, id uuid.UUID) (Media, error)

	// Writer applications
	CreateApplication(ctx context.Context, userID uuid.UUID, input ApplyInput, certificate CertificateFile) (Application, error)
	MyApplication(ctx context.Context, userID uuid.UUID) (Application, bool, error)
	ListApplications(ctx context.Context, status string) ([]Application, error)
	GetApplication(ctx context.Context, id uuid.UUID) (Application, error)
	ReviewApplication(ctx context.Context, id, reviewerID uuid.UUID, status, notes string) (Application, error)
	GetCertificate(ctx context.Context, mediaID uuid.UUID, onlyPending bool) (CertificateFile, error)

	// Writer role
	HasRole(ctx context.Context, userID uuid.UUID, role string) (bool, error)
}
