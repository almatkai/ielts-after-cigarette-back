package fullmock

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PrepareGuestMock reserves once, before public catalogs can expose its contents.
// Every visitor receives these exact versions even after new versions are published.
func (r *PostgresRepository) PrepareGuestMock(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = prepareGuestMock(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func prepareGuestMock(ctx context.Context, tx pgx.Tx) ([]candidate, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('fixed-guest-mock',0))`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT skill,material_id,material_version_id FROM guest_mock_materials ORDER BY skill`)
	if err != nil {
		return nil, err
	}
	items := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.skill, &item.materialID, &item.versionID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 4 {
		return items, nil
	}
	// A partial reservation is invalid; do not silently replace an existing section.
	if len(items) != 0 {
		return nil, ErrBankIncomplete
	}
	rows, err = tx.Query(ctx, `WITH eligible AS (`+eligibleMaterials+`)
 SELECT DISTINCT ON (skill) skill,material_id,version_id FROM eligible
 WHERE $1::uuid IS NOT NULL ORDER BY skill,material_id`, uuid.Nil, "academic")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.skill, &item.materialID, &item.versionID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, ready := bankStatuses(items); !ready {
		return nil, ErrBankIncomplete
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO guest_mock_materials (skill,material_id,material_version_id) VALUES ($1,$2,$3)`, item.skill, item.materialID, item.versionID); err != nil {
			return nil, err
		}
	}
	return items, nil
}
