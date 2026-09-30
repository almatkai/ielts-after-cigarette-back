package blog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const postColumns = `p.id, p.slug, p.title, p.description, p.cover_media_id, p.body_html, p.body_json,
	p.reading_time_minutes, p.status, p.published_at, p.content_updated_at, p.created_at, p.updated_at,
	u.id, COALESCE(up.display_name, split_part(u.email, '@', 1)), u.role`

const summaryColumns = `p.id, p.slug, p.title, p.description, p.cover_media_id,
	p.reading_time_minutes, p.status, p.published_at, p.content_updated_at,
	u.id, COALESCE(up.display_name, split_part(u.email, '@', 1)), u.role`

func (r *PostgresRepository) List(ctx context.Context) ([]Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postColumns+`
		FROM blog_posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		ORDER BY COALESCE(p.published_at, p.updated_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("list blog posts: %w", err)
	}
	defer rows.Close()
	return collectPosts(rows)
}

func (r *PostgresRepository) ListPublished(ctx context.Context, limit, offset int) ([]PostSummary, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+summaryColumns+`
		FROM blog_posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE p.status = 'PUBLISHED'
		ORDER BY p.published_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list published blog posts: %w", err)
	}
	defer rows.Close()
	items := []PostSummary{}
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) CountPublished(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM blog_posts WHERE status = 'PUBLISHED'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count published blog posts: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Post, error) {
	post, err := scanPost(r.pool.QueryRow(ctx, `SELECT `+postColumns+`
		FROM blog_posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE p.id = $1`, id))
	return mapReadError(post, err, "get blog post")
}

func (r *PostgresRepository) GetPublished(ctx context.Context, slug string) (PublicPost, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postColumns+`
		FROM blog_posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE p.slug = $1 AND p.status = 'PUBLISHED'`, slug)
	if err != nil {
		return PublicPost{}, fmt.Errorf("get published blog post: %w", err)
	}
	defer rows.Close()
	posts, err := collectPosts(rows)
	if err != nil {
		return PublicPost{}, err
	}
	if len(posts) == 0 {
		return PublicPost{}, ErrNotFound
	}
	return toPublic(posts[0]), nil
}

func (r *PostgresRepository) ListByAuthor(ctx context.Context, authorID uuid.UUID) ([]Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postColumns+`
		FROM blog_posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE p.author_id = $1
		ORDER BY p.updated_at DESC`, authorID)
	if err != nil {
		return nil, fmt.Errorf("list blog posts by author: %w", err)
	}
	defer rows.Close()
	return collectPosts(rows)
}

func (r *PostgresRepository) Create(ctx context.Context, authorID uuid.UUID, input SaveInput) (Post, error) {
	bodyJSON, err := json.Marshal(input.BodyJSON)
	if err != nil {
		return Post{}, fmt.Errorf("encode blog body json: %w", err)
	}
	postID := uuid.New()
	if _, err := r.pool.Exec(ctx, `INSERT INTO blog_posts
		(id, author_id, slug, title, description, cover_media_id, body_html, body_json, reading_time_minutes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, 'DRAFT')`,
		postID, authorID, input.Slug, input.Title, input.Description,
		nullableUUID(input.CoverMediaID), input.BodyHTML, string(bodyJSON),
		estimateReadingTime(input.BodyHTML)); err != nil {
		return Post{}, mapWriteError(err)
	}
	return r.Get(ctx, postID)
}

func (r *PostgresRepository) Update(ctx context.Context, id, _ uuid.UUID, input SaveInput) (Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM blog_posts WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("lock blog post: %w", err)
	}
	bodyJSON, err := json.Marshal(input.BodyJSON)
	if err != nil {
		return Post{}, fmt.Errorf("encode blog body json: %w", err)
	}
	command, err := tx.Exec(ctx, `UPDATE blog_posts
		SET slug = $2, title = $3, description = $4, cover_media_id = $5,
			body_html = $6, body_json = $7::jsonb, reading_time_minutes = $8,
			content_updated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, id, input.Slug, input.Title, input.Description,
		nullableUUID(input.CoverMediaID), input.BodyHTML, string(bodyJSON),
		estimateReadingTime(input.BodyHTML))
	if err != nil {
		return Post{}, mapWriteError(err)
	}
	if command.RowsAffected() != 1 {
		return Post{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, fmt.Errorf("commit blog post update: %w", err)
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) Publish(ctx context.Context, id uuid.UUID, _ *int64) (Post, error) {
	command, err := r.pool.Exec(ctx, `UPDATE blog_posts
		SET status = 'PUBLISHED', published_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'DRAFT'`, id)
	if err != nil {
		return Post{}, fmt.Errorf("publish blog post: %w", err)
	}
	if command.RowsAffected() != 1 {
		if _, err := r.Get(ctx, id); errors.Is(err, ErrNotFound) {
			return Post{}, ErrNotFound
		}
		return Post{}, ErrAlreadyPublished
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) Archive(ctx context.Context, id uuid.UUID) (Post, error) {
	command, err := r.pool.Exec(ctx, `UPDATE blog_posts
		SET status = 'ARCHIVED', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, id)
	if err != nil {
		return Post{}, fmt.Errorf("archive blog post: %w", err)
	}
	if command.RowsAffected() != 1 {
		return Post{}, ErrNotFound
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) CreateMedia(ctx context.Context, uploadedBy uuid.UUID, media Media) (Media, error) {
	media.ID = uuid.New()
	err := r.pool.QueryRow(ctx, `INSERT INTO blog_media
		(id, original_name, mime_type, storage_key, byte_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		media.ID, media.OriginalName, media.MimeType, media.StorageKey,
		media.ByteSize, uploadedBy).Scan(&media.CreatedAt)
	if err != nil {
		return Media{}, fmt.Errorf("create blog media: %w", err)
	}
	return media, nil
}

func (r *PostgresRepository) GetMedia(ctx context.Context, id uuid.UUID) (Media, error) {
	var media Media
	err := r.pool.QueryRow(ctx, `SELECT id, kind, original_name, mime_type, storage_key, byte_size, created_at
		FROM blog_media WHERE id = $1`, id).
		Scan(&media.ID, &media.Kind, &media.OriginalName, &media.MimeType,
			&media.StorageKey, &media.ByteSize, &media.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrMediaNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("get blog media: %w", err)
	}
	return media, nil
}

// HasRole reports whether the user currently holds the role.
func (r *PostgresRepository) HasRole(ctx context.Context, userID uuid.UUID, role string) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE id = $1 AND role = $2`,
		userID, role).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check user role: %w", err)
	}
	return count > 0, nil
}

func nullableUUID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func toPublic(post Post) PublicPost {
	return PublicPost{
		ID: post.ID, Author: post.Author, Slug: post.Slug, Title: post.Title,
		Description: post.Description, CoverMediaID: post.CoverMediaID,
		BodyHTML: post.BodyHTML, ReadingTimeMinutes: post.ReadingTimeMinutes,
		PublishedAt: post.PublishedAt, ContentUpdatedAt: post.ContentUpdatedAt,
	}
}

type rowScanner interface {
	Scan(...any) error
}

func scanPost(row rowScanner) (Post, error) {
	var post Post
	var bodyJSON []byte
	var coverMediaID *uuid.UUID
	err := row.Scan(&post.ID, &post.Slug, &post.Title, &post.Description, &coverMediaID,
		&post.BodyHTML, &bodyJSON, &post.ReadingTimeMinutes, &post.Status,
		&post.PublishedAt, &post.ContentUpdatedAt, &post.CreatedAt, &post.UpdatedAt,
		&post.Author.ID, &post.Author.DisplayName, &post.Author.Role)
	if err != nil {
		return Post{}, err
	}
	post.CoverMediaID = coverMediaID
	if err := json.Unmarshal(bodyJSON, &post.BodyJSON); err != nil {
		return Post{}, fmt.Errorf("decode blog body json: %w", err)
	}
	if post.BodyJSON == nil {
		post.BodyJSON = map[string]any{}
	}
	return post, nil
}

func scanSummary(row rowScanner) (PostSummary, error) {
	var summary PostSummary
	var coverMediaID *uuid.UUID
	err := row.Scan(&summary.ID, &summary.Slug, &summary.Title, &summary.Description, &coverMediaID,
		&summary.ReadingTimeMinutes, &summary.Status, &summary.PublishedAt, &summary.ContentUpdatedAt,
		&summary.Author.ID, &summary.Author.DisplayName, &summary.Author.Role)
	if err != nil {
		return PostSummary{}, err
	}
	summary.CoverMediaID = coverMediaID
	return summary, nil
}

func collectPosts(rows pgx.Rows) ([]Post, error) {
	items := []Post{}
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, post)
	}
	return items, rows.Err()
}

func mapReadError(post Post, err error, operation string) (Post, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("%s: %w", operation, err)
	}
	return post, nil
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrSlugExists
	}
	return fmt.Errorf("write blog post: %w", err)
}
