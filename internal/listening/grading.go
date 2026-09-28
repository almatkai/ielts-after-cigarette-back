package listening

import (
	"context"

	"github.com/google/uuid"
)

// VersionRef identifies one pinned test version.
type VersionRef struct {
	MaterialID uuid.UUID
	VersionID  uuid.UUID
}

// gradingVersionSelect reads the identity of many pinned versions. A version
// the query does not return is a missing material, which the caller reports as
// ErrNotFound like the single-version lookup does.
const gradingVersionSelect = `
	SELECT v.test_id, v.id, t.exam_type
	FROM listening_test_versions v
	JOIN listening_tests t ON t.id = v.test_id
	WHERE v.id = ANY($1)
`

// GradingStructures loads the full structure (with answers) of many pinned
// versions in two queries: one for the version identities and one for every
// part, group and question. Grading a full mock and the mistakes page both need
// the structure of every attempt at once, and on a distant database the round
// trips, not the queries, dominate the latency.
func (r *PostgresRepository) GradingStructures(ctx context.Context, refs []VersionRef) (map[VersionRef]Test, error) {
	result := make(map[VersionRef]Test, len(refs))
	if len(refs) == 0 {
		return result, nil
	}
	versionIDs := make([]uuid.UUID, 0, len(refs))
	seen := make(map[uuid.UUID]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.VersionID] {
			continue
		}
		seen[ref.VersionID] = true
		versionIDs = append(versionIDs, ref.VersionID)
	}
	identities, err := r.gradingVersionIdentities(ctx, versionIDs)
	if err != nil {
		return nil, err
	}
	parts, err := r.partsByVersion(ctx, versionIDs)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		identity, ok := identities[ref.VersionID]
		if !ok {
			return nil, ErrNotFound
		}
		versionParts, ok := parts[ref.VersionID]
		if !ok {
			versionParts = []Part{}
		}
		result[ref] = Test{
			ID:       identity.testID,
			ExamType: identity.examType,
			Parts:    versionParts,
		}
	}
	return result, nil
}

type versionIdentity struct {
	testID   uuid.UUID
	examType string
}

func (r *PostgresRepository) gradingVersionIdentities(ctx context.Context, versionIDs []uuid.UUID) (map[uuid.UUID]versionIdentity, error) {
	rows, err := r.pool.Query(ctx, gradingVersionSelect, versionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := make(map[uuid.UUID]versionIdentity, len(versionIDs))
	for rows.Next() {
		var versionID uuid.UUID
		var identity versionIdentity
		if err := rows.Scan(&identity.testID, &versionID, &identity.examType); err != nil {
			return nil, err
		}
		identities[versionID] = identity
	}
	return identities, rows.Err()
}
