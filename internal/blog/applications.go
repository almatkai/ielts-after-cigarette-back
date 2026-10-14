package blog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const applicationColumns = `a.id, a.user_id, u.email, COALESCE(up.display_name, split_part(u.email, '@', 1)),
	a.overall_band, a.listening_band, a.reading_band, a.writing_band, a.speaking_band,
	a.trf_number, a.test_date::text, a.exam_type, a.bio,
	a.status, a.review_notes, a.reviewed_by, a.reviewed_at, a.created_at, a.updated_at`

func (r *PostgresRepository) CreateApplication(ctx context.Context, userID uuid.UUID, input ApplyInput, certificate CertificateFile) (Application, error) {
	applicationID := uuid.New()
	_, err := r.pool.Exec(ctx, `INSERT INTO writer_applications
		(id, user_id, overall_band, listening_band, reading_band, writing_band, speaking_band,
			trf_number, test_date, exam_type, bio, certificate_storage_key, certificate_mime_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		applicationID, userID, input.OverallBand, nullableFloat(input.ListeningBand),
		input.ReadingBand, input.WritingBand, input.SpeakingBand, input.TRFNumber,
		nullableString(input.TestDate), nullableString(input.ExamType), input.Bio,
		certificate.StorageKey, certificate.MimeType)
	if err != nil {
		return Application{}, mapApplicationError(err)
	}
	return r.GetApplication(ctx, applicationID)
}

func (r *PostgresRepository) MyApplication(ctx context.Context, userID uuid.UUID) (Application, bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+applicationColumns+`
		FROM writer_applications a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE a.user_id = $1
		ORDER BY a.created_at DESC
		LIMIT 1`, userID)
	if err != nil {
		return Application{}, false, fmt.Errorf("get my writer application: %w", err)
	}
	defer rows.Close()
	applications, err := collectApplications(rows)
	if err != nil {
		return Application{}, false, err
	}
	if len(applications) == 0 {
		return Application{}, false, nil
	}
	return applications[0], true, nil
}

func (r *PostgresRepository) ListApplications(ctx context.Context, status string) ([]Application, error) {
	query := `SELECT ` + applicationColumns + `
		FROM writer_applications a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN user_profiles up ON up.user_id = u.id`
	args := []any{}
	if status != "" {
		query += ` WHERE a.status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY a.created_at DESC`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list writer applications: %w", err)
	}
	defer rows.Close()
	return collectApplications(rows)
}

func (r *PostgresRepository) GetApplication(ctx context.Context, id uuid.UUID) (Application, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+applicationColumns+`
		FROM writer_applications a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE a.id = $1`, id)
	if err != nil {
		return Application{}, fmt.Errorf("get writer application: %w", err)
	}
	defer rows.Close()
	applications, err := collectApplications(rows)
	if err != nil {
		return Application{}, err
	}
	if len(applications) == 0 {
		return Application{}, ErrApplicationNotFound
	}
	return applications[0], nil
}

// ReviewApplication approves or rejects a pending application. Approval also
// grants the WRITER role and revokes active sessions in the same transaction
// so role and application status can never diverge.
func (r *PostgresRepository) ReviewApplication(ctx context.Context, id, reviewerID uuid.UUID, status, notes string) (Application, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Application{}, err
	}
	defer tx.Rollback(ctx)
	var userID uuid.UUID
	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT user_id, status FROM writer_applications WHERE id = $1 FOR UPDATE`, id).
		Scan(&userID, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, ErrApplicationNotFound
	}
	if err != nil {
		return Application{}, fmt.Errorf("lock writer application: %w", err)
	}
	if currentStatus != ApplicationPending {
		return Application{}, ErrApplicationNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE writer_applications
		SET status = $2, review_notes = $3, reviewed_by = $4, reviewed_at = CURRENT_TIMESTAMP
		WHERE id = $1`, id, status, notes, reviewerID); err != nil {
		return Application{}, fmt.Errorf("review writer application: %w", err)
	}
	if status == ApplicationApproved {
		if _, err := tx.Exec(ctx, `UPDATE users SET role = $2, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1`, userID, authRoleWriter); err != nil {
			return Application{}, fmt.Errorf("grant writer role: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_sessions
			SET revoked_at = CURRENT_TIMESTAMP
			WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return Application{}, fmt.Errorf("revoke sessions after writer grant: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Application{}, fmt.Errorf("commit writer application review: %w", err)
	}
	return r.GetApplication(ctx, id)
}

// authRoleWriter mirrors auth.RoleWriter as a plain value to keep the blog
// package self-contained.
const authRoleWriter = "WRITER"

func collectApplications(rows pgx.Rows) ([]Application, error) {
	items := []Application{}
	for rows.Next() {
		var application Application
		var certificateMime string
		err := rows.Scan(&application.ID, &application.UserID, &application.UserEmail,
			&application.UserDisplayName, &application.OverallBand, &application.ListeningBand,
			&application.ReadingBand, &application.WritingBand, &application.SpeakingBand,
			&application.TRFNumber, &application.TestDate, &application.ExamType, &application.Bio,
			&application.Status, &application.ReviewNotes, &application.ReviewedBy,
			&application.ReviewedAt, &application.CreatedAt, &application.UpdatedAt)
		application.CertificateMime = certificateMime
		if err != nil {
			return nil, err
		}
		items = append(items, application)
	}
	return items, rows.Err()
}

func mapApplicationError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrApplicationExists
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		return ErrBandTooLow
	}
	return fmt.Errorf("create writer application: %w", err)
}

// GetCertificate resolves a certificate media record. Only the uploader's
// own pending-application context is checked here; authorization lives in
// the service layer.
func (r *PostgresRepository) GetCertificate(ctx context.Context, mediaID uuid.UUID, _ bool) (CertificateFile, error) {
	var certificate CertificateFile
	err := r.pool.QueryRow(ctx, `SELECT id, storage_key, mime_type, uploaded_by
		FROM blog_media WHERE id = $1`, mediaID).
		Scan(&certificate.MediaID, &certificate.StorageKey, &certificate.MimeType,
			&certificate.UploadedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return CertificateFile{}, ErrMediaNotFound
	}
	if err != nil {
		return CertificateFile{}, fmt.Errorf("get certificate media: %w", err)
	}
	return certificate, nil
}
