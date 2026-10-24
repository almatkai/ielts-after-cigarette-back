package studyplan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type editorialMetadata struct {
	Kind        string `json:"kind"`
	ReferenceID string `json:"referenceId"`
	Version     string `json:"version"`
	Section     string `json:"section"`
	FamilyID    string `json:"familyId"`
	Status      string `json:"status"`
	ContentHash string `json:"contentHash"`
	Revision    int64  `json:"revision"`
	ReviewNotes string `json:"reviewNotes"`
	ReviewerID  string `json:"reviewerId"`
	ReviewedAt  string `json:"reviewedAt"`
}

func editorialKey(kind, id, version string) string { return kind + ":" + id + ":" + version }

func loadEditorialMetadata(ctx context.Context, tx pgx.Tx) (map[string]editorialMetadata, error) {
	rows, err := tx.Query(ctx, `SELECT kind,reference_id,version,section,family_id,status,content_hash,revision,review_notes,reviewer_id,reviewed_at FROM study_learning_content_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]editorialMetadata{}
	for rows.Next() {
		var m editorialMetadata
		var at time.Time
		if err := rows.Scan(&m.Kind, &m.ReferenceID, &m.Version, &m.Section, &m.FamilyID, &m.Status, &m.ContentHash, &m.Revision, &m.ReviewNotes, &m.ReviewerID, &at); err != nil {
			return nil, err
		}
		m.ReviewedAt = at.UTC().Format(time.RFC3339)
		out[editorialKey(m.Kind, m.ReferenceID, m.Version)] = m
	}
	return out, rows.Err()
}

// Approval is bound to the complete exercise, private key and explanation.
// A family or label change alone must not alter the exercise fingerprint.
func drillContentHash(u drillUnit) string {
	d := u.Drill
	d.FamilyID = nil
	d.EditorialStatus = ""
	d.MasteryEligible = false
	raw, _ := json.Marshal(struct {
		Drill    Drill
		Key      json.RawMessage
		Feedback string
	}{d, u.Key, u.Feedback})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func materialContentHash(id, version string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("material:"+id+":"+version)))
}

// Changing a label cannot make the same passage/audio into a new source.
func editorialOrigin(d Drill) string {
	if d.SourceMaterialID != nil {
		return d.Section + ":material:" + *d.SourceMaterialID
	}
	audio := ""
	if d.AudioURL != nil {
		audio = *d.AudioURL
	}
	return fmt.Sprintf("%s:stimulus:%x", d.Section, sha256.Sum256([]byte(strings.TrimSpace(d.Stimulus)+"\n"+audio)))
}
func editorialFamilyAllowed(d Drill, family string, units []drillUnit, metadata map[string]editorialMetadata) bool {
	if d.SourceMaterialID != nil {
		if d.SourceVersionID == nil {
			return false
		}
		m, ok := metadata[editorialKey("material", *d.SourceMaterialID, *d.SourceVersionID)]
		if !ok || m.Status != "reviewed" || m.ContentHash != materialContentHash(*d.SourceMaterialID, *d.SourceVersionID) || m.FamilyID != family {
			return false
		}
	}
	for _, sibling := range units {
		if sibling.Drill.ID == d.ID || editorialOrigin(sibling.Drill) != editorialOrigin(d) {
			continue
		}
		m, ok := metadata[editorialKey("drill", sibling.Drill.ID, sibling.Drill.Version)]
		if ok && m.Status == "reviewed" && m.ContentHash == drillContentHash(sibling) && m.FamilyID != family {
			return false
		}
	}
	return true
}

func applyEditorialMetadata(units []drillUnit, metadata map[string]editorialMetadata) []drillUnit {
	// Check siblings against their original stimuli before applying any labels.
	original := append([]drillUnit{}, units...)
	for i := range units {
		u := &units[i]
		d := &u.Drill
		m, ok := metadata[editorialKey("drill", d.ID, d.Version)]
		if ok && m.Status == "withdrawn" {
			d.EditorialStatus = "withdrawn"
			d.MasteryEligible = false
			continue
		}
		if !ok || m.ContentHash != drillContentHash(*u) {
			d.MasteryEligible = false
			continue
		}
		d.EditorialStatus = m.Status
		if m.FamilyID != "" {
			d.FamilyID = ptr(m.FamilyID)
		}
		objective := d.Modality == "objective" || (d.Section == "listening" && d.Modality == "audio" && d.AudioURL != nil)
		d.MasteryEligible = m.Status == "reviewed" && m.FamilyID != "" && objective && len(u.Key) > 0 && editorialFamilyAllowed(original[i].Drill, m.FamilyID, original, metadata)
	}
	usable := make([]drillUnit, 0, len(units))
	for _, u := range units {
		if u.Drill.EditorialStatus != "withdrawn" {
			usable = append(usable, u)
		}
	}
	return usable
}
func applySourceEditorialMetadata(sources []sourceMistake, metadata map[string]editorialMetadata) {
	for i := range sources {
		e := &sources[i].Source
		m, ok := metadata[editorialKey("material", e.MaterialID, e.VersionID)]
		if ok && m.Status == "reviewed" && m.FamilyID != "" && m.ContentHash == materialContentHash(e.MaterialID, e.VersionID) {
			e.FamilyID = ptr(m.FamilyID)
			e.FamilyKnown = true
		}
	}
}

type editorialCandidate struct {
	Kind        string             `json:"kind"`
	ID          string             `json:"id"`
	Version     string             `json:"version"`
	Section     string             `json:"section"`
	Title       string             `json:"title"`
	ContentHash string             `json:"contentHash"`
	Drill       *Drill             `json:"drill"`
	AnswerKey   json.RawMessage    `json:"answerKey"`
	Explanation string             `json:"explanation"`
	Metadata    *editorialMetadata `json:"metadata"`
}

func editorialCandidates(ctx context.Context, tx pgx.Tx) ([]editorialCandidate, error) {
	meta, err := loadEditorialMetadata(ctx, tx)
	if err != nil {
		return nil, err
	}
	units := builtinDrills()
	extra, err := loadShortListening(ctx, tx, "academic")
	if err != nil {
		return nil, err
	}
	units = append(units, extra...)
	out := []editorialCandidate{}
	for _, u := range units {
		d := u.Drill
		c := editorialCandidate{Kind: "drill", ID: d.ID, Version: d.Version, Section: d.Section, Title: d.Title, ContentHash: drillContentHash(u), Drill: &d, AnswerKey: u.Key, Explanation: u.Feedback}
		if m, ok := meta[editorialKey(c.Kind, c.ID, c.Version)]; ok {
			c.Metadata = &m
		}
		out = append(out, c)
	}
	rows, err := tx.Query(ctx, `SELECT m.id::text,v.id::text,'reading',v.title FROM reading_materials m JOIN reading_material_versions v ON v.material_id=m.id WHERE (m.status='PUBLISHED' AND v.id=m.published_version_id AND m.exam_type='academic') OR EXISTS(SELECT 1 FROM study_learning_content_metadata md WHERE md.kind='material' AND md.reference_id=m.id::text AND md.version=v.id::text AND md.section='reading')
 UNION ALL SELECT m.id::text,v.id::text,'listening',v.title FROM listening_tests m JOIN listening_test_versions v ON v.test_id=m.id WHERE (m.status='PUBLISHED' AND v.id=m.published_version_id AND m.exam_type='academic') OR EXISTS(SELECT 1 FROM study_learning_content_metadata md WHERE md.kind='material' AND md.reference_id=m.id::text AND md.version=v.id::text AND md.section='listening')
 UNION ALL SELECT m.id::text,v.id::text,'writing',v.title FROM writing_materials m JOIN writing_material_versions v ON v.material_id=m.id WHERE (m.status='PUBLISHED' AND v.id=m.published_version_id AND v.exam_type='academic') OR EXISTS(SELECT 1 FROM study_learning_content_metadata md WHERE md.kind='material' AND md.reference_id=m.id::text AND md.version=v.id::text AND md.section='writing')
 UNION ALL SELECT m.id::text,v.id::text,'speaking',v.title FROM speaking_materials m JOIN speaking_material_versions v ON v.material_id=m.id WHERE (m.status='PUBLISHED' AND v.id=m.published_version_id AND v.exam_type='academic') OR EXISTS(SELECT 1 FROM study_learning_content_metadata md WHERE md.kind='material' AND md.reference_id=m.id::text AND md.version=v.id::text AND md.section='speaking')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := editorialCandidate{Kind: "material", Explanation: "Разметка семейства опубликованной версии. Сама по себе не подтверждает шкалу IELTS или качество оценки."}
		if err := rows.Scan(&c.ID, &c.Version, &c.Section, &c.Title); err != nil {
			return nil, err
		}
		c.ContentHash = materialContentHash(c.ID, c.Version)
		if m, ok := meta[editorialKey(c.Kind, c.ID, c.Version)]; ok {
			c.Metadata = &m
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type EditorialHandler struct {
	pool    *pgxpool.Pool
	maxBody int64
}

func NewEditorialHandler(pool *pgxpool.Pool, maxBody int64) *EditorialHandler {
	return &EditorialHandler{pool, maxBody}
}

// Mount under the authenticated /admin group. Each handler rechecks ADMIN.
func (h *EditorialHandler) Routes(r chi.Router) {
	r.Get("/study-learning/content", h.List)
	r.Get("/study-learning/content/{kind}/{id}/history", h.History)
	r.Put("/study-learning/content/{kind}/{id}", h.Review)
}

func (h *EditorialHandler) History(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		editorialError(w, r, ErrValidation)
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT revision,decision,reviewer_id,created_at FROM study_learning_content_reviews WHERE kind=$1 AND reference_id=$2 AND version=$3 ORDER BY revision DESC LIMIT 100`, chi.URLParam(r, "kind"), chi.URLParam(r, "id"), version)
	if err != nil {
		editorialError(w, r, err)
		return
	}
	defer rows.Close()
	type event struct {
		Revision   int64           `json:"revision"`
		Decision   json.RawMessage `json:"decision"`
		ReviewerID string          `json:"reviewerId"`
		CreatedAt  string          `json:"createdAt"`
	}
	items := []event{}
	for rows.Next() {
		var e event
		var at time.Time
		if err := rows.Scan(&e.Revision, &e.Decision, &e.ReviewerID, &at); err != nil {
			editorialError(w, r, err)
			return
		}
		e.CreatedAt = at.UTC().Format(time.RFC3339)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		editorialError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items})
}
func (h *EditorialHandler) actor(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok || id == uuid.Nil {
		httpx.WriteError(w, r, 401, "UNAUTHENTICATED", "Необходимо войти", nil)
		return uuid.Nil, false
	}
	if auth.Role(r.Context()) != auth.RoleAdmin {
		httpx.WriteError(w, r, 403, "FORBIDDEN", "Редакторский допуск доступен администратору", nil)
		return uuid.Nil, false
	}
	return id, true
}
func editorialError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, 404, "NOT_FOUND", "Материал или версия не найдены", nil)
	case errors.Is(err, ErrConflict):
		httpx.WriteError(w, r, 409, "CONFLICT", "Материал или решение изменились; обновите и проверьте заново", nil)
	case errors.Is(err, ErrValidation):
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Укажите семейство, решение, заметки и результаты проверки", nil)
	default:
		httpx.WriteError(w, r, 500, "INTERNAL_ERROR", "Не удалось сохранить редакторскую проверку", nil)
	}
}
func (h *EditorialHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		editorialError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	items, err := editorialCandidates(r.Context(), tx)
	if err != nil {
		editorialError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items, "limitations": []string{"Редакторская проверка допускает учебное задание; она не калибрует балл IELTS.", "Материалы одного семейства не считаются независимыми проверками.", "Для Writing/Speaking нужен отдельный проверяющий; редакторский допуск не оценивает ответы ученика."}})
}

type editorialInput struct {
	Version          string `json:"version"`
	ContentHash      string `json:"contentHash"`
	FamilyID         string `json:"familyId"`
	Status           string `json:"status"`
	ExpectedRevision int64  `json:"expectedRevision"`
	ReviewNotes      string `json:"reviewNotes"`
	KeysChecked      bool   `json:"keysChecked"`
	FamilyChecked    bool   `json:"familyChecked"`
	SourceChecked    bool   `json:"sourceChecked"`
}

var validFamily = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:_-]{2,119}$`)

func (h *EditorialHandler) Review(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in editorialInput
	if httpx.DecodeJSON(w, r, h.maxBody, &in) != nil {
		editorialError(w, r, ErrValidation)
		return
	}
	in.ReviewNotes = strings.TrimSpace(in.ReviewNotes)
	in.FamilyID = strings.TrimSpace(in.FamilyID)
	if utf8.RuneCountInString(in.ReviewNotes) < 20 || utf8.RuneCountInString(in.ReviewNotes) > 4000 || in.ExpectedRevision < 0 || (in.Status != "draft" && in.Status != "reviewed" && in.Status != "withdrawn") || (in.FamilyID != "" && !validFamily.MatchString(in.FamilyID)) || (in.Status == "reviewed" && (in.FamilyID == "" || !in.KeysChecked || !in.FamilyChecked || !in.SourceChecked)) {
		editorialError(w, r, ErrValidation)
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		editorialError(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,44))`, "study-learning-editorial")
	if err != nil {
		editorialError(w, r, err)
		return
	}
	candidates, err := editorialCandidates(ctx, tx)
	if err != nil {
		editorialError(w, r, err)
		return
	}
	var found *editorialCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.Kind == kind && c.ID == id && c.Version == in.Version {
			found = c
			break
		}
	}
	if found == nil {
		editorialError(w, r, ErrNotFound)
		return
	}
	if found.ContentHash != in.ContentHash {
		editorialError(w, r, ErrConflict)
		return
	}
	rev := int64(0)
	if found.Metadata != nil {
		rev = found.Metadata.Revision
	}
	if rev != in.ExpectedRevision {
		editorialError(w, r, ErrConflict)
		return
	}
	if in.Status == "reviewed" && found.Drill != nil {
		metadata, e := loadEditorialMetadata(ctx, tx)
		if e != nil {
			editorialError(w, r, e)
			return
		}
		units := []drillUnit{}
		for _, candidate := range candidates {
			if candidate.Drill != nil {
				units = append(units, drillUnit{Drill: *candidate.Drill, Key: candidate.AnswerKey, Feedback: candidate.Explanation})
			}
		}
		if !editorialFamilyAllowed(*found.Drill, in.FamilyID, units, metadata) {
			httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Семейство должно совпадать с проверенным источником и вопросами того же текста или аудио", map[string]string{"familyId": "Сначала проверьте семейство исходного материала; связанные вопросы не являются независимыми"})
			return
		}
	}
	decision, _ := json.Marshal(in)
	_, err = tx.Exec(ctx, `INSERT INTO study_learning_content_metadata(kind,reference_id,version,section,family_id,status,content_hash,revision,review_notes,reviewer_id,checklist)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(kind,reference_id,version) DO UPDATE SET family_id=EXCLUDED.family_id,status=EXCLUDED.status,content_hash=EXCLUDED.content_hash,revision=EXCLUDED.revision,review_notes=EXCLUDED.review_notes,reviewer_id=EXCLUDED.reviewer_id,reviewed_at=CURRENT_TIMESTAMP,checklist=EXCLUDED.checklist`, kind, id, in.Version, found.Section, in.FamilyID, in.Status, in.ContentHash, rev+1, in.ReviewNotes, actor, decision)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO study_learning_content_reviews(kind,reference_id,version,revision,decision,reviewer_id) VALUES($1,$2,$3,$4,$5,$6)`, kind, id, in.Version, rev+1, decision, actor)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		editorialError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"revision": rev + 1, "status": in.Status, "familyId": in.FamilyID, "contentHash": in.ContentHash})
}
