package attempts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// One skill branch supplies metadata per attempt, rather than eight independent
// left joins. Missing metadata still produces the legacy empty title/slug.
// full_mock_session_sections.attempt_id is UNIQUE, so the join cannot
// duplicate rows; NULL marks attempts that belong to no Full Mock session.
const summarySelect = `SELECT a.id, a.user_id, a.material_type, a.material_id,
	a.material_version_id, a.status,
 CASE WHEN fm.status <> 'SUBMITTED' THEN NULL ELSE a.score END,
 CASE WHEN fm.status <> 'SUBMITTED' THEN NULL ELSE a.max_score END,
 CASE WHEN fm.status <> 'SUBMITTED' THEN NULL ELSE a.band::double precision END,
	a.started_at, a.submitted_at, COALESCE(metadata.title,''), COALESCE(metadata.slug,''), fmss.session_id
	FROM selected a
	LEFT JOIN full_mock_session_sections fmss ON fmss.attempt_id = a.id
 LEFT JOIN full_mock_sessions fm ON fm.id=fmss.session_id
	LEFT JOIN LATERAL (
		SELECT (SELECT title FROM listening_test_versions WHERE id=a.material_version_id) AS title,
			(SELECT slug FROM listening_tests WHERE id=a.material_id) AS slug
		WHERE a.material_type='listening'
		UNION ALL
		SELECT (SELECT title FROM reading_material_versions WHERE id=a.material_version_id),
			(SELECT slug FROM reading_materials WHERE id=a.material_id)
		WHERE a.material_type='reading'
		UNION ALL
		SELECT (SELECT title FROM writing_material_versions WHERE id=a.material_version_id),
			(SELECT slug FROM writing_materials WHERE id=a.material_id)
		WHERE a.material_type='writing'
		UNION ALL
		SELECT (SELECT title FROM speaking_material_versions WHERE id=a.material_version_id),
			(SELECT slug FROM speaking_materials WHERE id=a.material_id)
		WHERE a.material_type='speaking'
	) metadata ON TRUE`

type HistoryCursor struct {
	At time.Time `json:"at"`
	ID uuid.UUID `json:"id"`
}

type HistoryPage struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

func ParseHistoryCursor(raw string) (HistoryCursor, error) {
	if raw == "" {
		return HistoryCursor{}, nil
	}
	if len(raw) > 256 {
		return HistoryCursor{}, fmt.Errorf("invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return HistoryCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor HistoryCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.At.IsZero() || cursor.ID == uuid.Nil {
		return HistoryCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func (s *Service) History(ctx context.Context, userID uuid.UUID, materialType string, limit int, cursor HistoryCursor) (HistoryPage, error) {
	repository, ok := s.repository.(interface {
		ListHistory(context.Context, uuid.UUID, string, int, HistoryCursor) (HistoryPage, error)
	})
	if !ok {
		return HistoryPage{}, ErrUnsupportedMaterial
	}
	return repository.ListHistory(ctx, userID, materialType, limit, cursor)
}

func (r *PostgresRepository) ListHistory(ctx context.Context, userID uuid.UUID, materialType string, limit int, cursor HistoryCursor) (HistoryPage, error) {
	where := "user_id=$1"
	args := []any{userID}
	if materialType != "" {
		args = append(args, materialType)
		where += fmt.Sprintf(" AND material_type=$%d", len(args))
	}
	if !cursor.At.IsZero() {
		args = append(args, cursor.At, cursor.ID)
		where += fmt.Sprintf(" AND (COALESCE(submitted_at,started_at),id) < ($%d,$%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`WITH selected AS MATERIALIZED (
		SELECT * FROM attempts WHERE %s
		ORDER BY COALESCE(submitted_at,started_at) DESC, id DESC LIMIT $%d
	) `, where, len(args)) + summarySelect + ` ORDER BY COALESCE(a.submitted_at,a.started_at) DESC, a.id DESC`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	result := HistoryPage{Items: []Summary{}}
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return HistoryPage{}, err
		}
		if len(result.Items) == limit {
			last := result.Items[len(result.Items)-1]
			at := last.StartedAt
			if last.SubmittedAt != nil {
				at = *last.SubmittedAt
			}
			encoded, _ := json.Marshal(HistoryCursor{At: at, ID: last.ID})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
			break
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func scanSummary(row rowScanner) (Summary, error) {
	var item Summary
	err := row.Scan(&item.ID, &item.UserID, &item.MaterialType, &item.MaterialID,
		&item.MaterialVersionID, &item.Status, &item.Score, &item.MaxScore,
		&item.Band, &item.StartedAt, &item.SubmittedAt, &item.TestTitle, &item.TestSlug,
		&item.FullMockSessionID)
	return item, err
}
