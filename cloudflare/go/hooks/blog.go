package blog

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The selected snapshot stores certificate keys in writer_applications but its
// DTO does not populate CertificateMediaID. Resolve the existing relationship
// after the unchanged service ownership check, without altering source/data.
func cloudflareCertificate(ctx context.Context, repository Repository, applicationID, mediaID uuid.UUID) (CertificateFile, error) {
	if native, ok := repository.(interface {
		cloudflareApplicationCertificate(context.Context, uuid.UUID) (CertificateFile, error)
	}); ok {
		return native.cloudflareApplicationCertificate(ctx, applicationID)
	}
	return repository.GetCertificate(ctx, mediaID, false)
}
func (r *PostgresRepository) cloudflareApplicationCertificate(ctx context.Context, id uuid.UUID) (CertificateFile, error) {
	var out CertificateFile
	err := r.pool.QueryRow(ctx, `SELECT m.id,m.storage_key,m.mime_type,m.uploaded_by FROM writer_applications a JOIN blog_media m ON m.storage_key=a.certificate_storage_key AND m.uploaded_by=a.user_id WHERE a.id=$1`, id).Scan(&out.MediaID, &out.StorageKey, &out.MimeType, &out.UploadedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return CertificateFile{}, ErrMediaNotFound
	}
	return out, err
}
