package studyplan

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type contentOrigin struct {
	Section, MaterialID, VersionID, Stimulus, AudioURL string
	StartedAt                                          time.Time
}

func normalizedStimulus(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(plainLearningText(v)), " "))
}
func sameOrigin(a, b Drill) bool {
	if a.Section != b.Section {
		return false
	}
	if a.ID == b.ID {
		return true
	}
	if sameKnown(a.SourceMaterialID, b.SourceMaterialID) || sameKnown(a.SourceVersionID, b.SourceVersionID) || sameKnown(a.SourceQuestionID, b.SourceQuestionID) || sameKnown(a.AudioURL, b.AudioURL) {
		return true
	}
	return normalizedStimulus(a.Stimulus) != "" && normalizedStimulus(a.Stimulus) == normalizedStimulus(b.Stimulus)
}
func sameKnown(a, b *string) bool { return a != nil && b != nil && *a != "" && *a == *b }
func sourceOriginMatches(c Cycle, d Drill) bool {
	if c.Section != d.Section {
		return false
	}
	e := c.Source
	if e.MaterialID == d.ID || sameKnown(ptr(e.MaterialID), d.SourceMaterialID) || sameKnown(ptr(e.VersionID), d.SourceVersionID) || sameKnown(e.QuestionID, d.SourceQuestionID) || sameKnown(e.AudioURL, d.AudioURL) {
		return true
	}
	// Catalog version IDs are UUIDs. Built-in semantic versions (draft-1) are
	// intentionally not globally unique and cannot identify a shared source.
	if _, err := uuid.Parse(e.VersionID); err == nil && e.VersionID == d.Version {
		return true
	}
	return normalizedStimulus(e.Stimulus) != "" && normalizedStimulus(e.Stimulus) == normalizedStimulus(d.Stimulus)
}
func knownSessionOrigin(st *state, d Drill) bool {
	for _, s := range st.Learning.Sessions {
		if sameOrigin(s.Session.Drill, d) {
			return true
		}
	}
	for _, c := range st.Learning.Cycles {
		if sourceOriginMatches(c.Cycle, d) {
			return true
		}
	}
	return false
}
func priorAttemptOrigin(in inputs, d Drill, cutoff time.Time) bool {
	for _, r := range in.Results {
		if r.Skill != d.Section || (!cutoff.IsZero() && r.StartedAt.After(cutoff)) {
			continue
		}
		if (d.SourceMaterialID != nil && r.MaterialID == *d.SourceMaterialID) || (d.SourceVersionID != nil && r.VersionID == *d.SourceVersionID) || (r.VersionID != "" && r.VersionID == d.Version) {
			return true
		}
		if d.SourceQuestionID != nil {
			for _, q := range r.Questions {
				if q.ID == *d.SourceQuestionID {
					return true
				}
			}
		}
	}
	for _, o := range in.Origins {
		if o.Section != d.Section || (!cutoff.IsZero() && o.StartedAt.After(cutoff)) {
			continue
		}
		if d.AudioURL != nil && o.AudioURL != "" && *d.AudioURL == o.AudioURL {
			return true
		}
		if normalizedStimulus(o.Stimulus) != "" && normalizedStimulus(o.Stimulus) == normalizedStimulus(d.Stimulus) {
			return true
		}
	}
	return false
}
func loadContentOrigins(ctx context.Context, tx pgx.Tx, user uuid.UUID) ([]contentOrigin, error) {
	rows, err := tx.Query(ctx, `SELECT a.material_type,a.material_id,a.material_version_id,v.body,'' AS audio,a.started_at FROM attempts a JOIN reading_material_versions v ON v.id=a.material_version_id WHERE a.user_id=$1 AND a.material_type='reading'
 UNION ALL SELECT a.material_type,a.material_id,a.material_version_id,v.body,'',a.started_at FROM attempts a JOIN reading_test_passages tp ON tp.test_material_version_id=a.material_version_id JOIN reading_material_versions v ON v.id=tp.passage_material_version_id WHERE a.user_id=$1 AND a.material_type='reading'
 UNION ALL SELECT a.material_type,a.material_id,a.material_version_id,'','/listening/media/'||p.audio_asset_id::text,a.started_at FROM attempts a JOIN listening_parts p ON p.test_version_id=a.material_version_id WHERE a.user_id=$1 AND a.material_type='listening' AND p.audio_asset_id IS NOT NULL`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contentOrigin{}
	for rows.Next() {
		var o contentOrigin
		if err = rows.Scan(&o.Section, &o.MaterialID, &o.VersionID, &o.Stimulus, &o.AudioURL, &o.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
