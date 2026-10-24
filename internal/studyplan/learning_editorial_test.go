package studyplan

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestEditorialAdmissionBindsKeyAndContent(t *testing.T) {
	original := drillUnit{Drill: Drill{ID: "original", Version: "1", Section: "reading", Modality: "objective", EditorialStatus: "draft", FamilyID: ptr("draft-family"), Task: "Choose TRUE or FALSE", Stimulus: "A ferry carries 40 people."}, Key: json.RawMessage(`{"value":"TRUE"}`), Feedback: "The passage confirms it."}
	key := editorialKey("drill", original.Drill.ID, original.Drill.Version)
	metadata := map[string]editorialMetadata{key: {Status: "reviewed", FamilyID: "checked-family", ContentHash: drillContentHash(original)}}
	admitted := applyEditorialMetadata([]drillUnit{original}, metadata)
	if len(admitted) != 1 || !admitted[0].Drill.MasteryEligible || *admitted[0].Drill.FamilyID != "checked-family" {
		t.Fatalf("reviewed exact content not admitted: %+v", admitted)
	}
	for _, change := range []func(*drillUnit){func(u *drillUnit) { u.Key = json.RawMessage(`{"value":"FALSE"}`) }, func(u *drillUnit) { u.Drill.Stimulus = "Changed source text" }, func(u *drillUnit) { u.Feedback = "Changed teacher explanation" }} {
		revised := original
		change(&revised)
		if applyEditorialMetadata([]drillUnit{revised}, metadata)[0].Drill.MasteryEligible {
			t.Fatal("changed content inherited old approval")
		}
	}
	for _, modality := range []string{"typed", "script"} {
		subjective := original
		subjective.Drill.Modality = modality
		metadata[key] = editorialMetadata{Status: "reviewed", FamilyID: "checked-family", ContentHash: drillContentHash(subjective)}
		if applyEditorialMetadata([]drillUnit{subjective}, metadata)[0].Drill.MasteryEligible {
			t.Fatalf("%s admitted as objective transfer", modality)
		}
	}
	metadata[key] = editorialMetadata{Status: "withdrawn", ContentHash: "stale-hash"}
	if len(applyEditorialMetadata([]drillUnit{original}, metadata)) != 0 {
		t.Fatal("withdrawn version offered for practice")
	}
}

func TestEditorialAuthorizationDoesNotExposeKeys(t *testing.T) {
	h := NewEditorialHandler(nil, 1<<20)
	router := chi.NewRouter()
	h.Routes(router)
	for _, role := range []string{"", auth.RoleStudent, auth.RoleEditor} {
		req := httptest.NewRequest(http.MethodGet, "/study-learning/content", nil)
		expected := 401
		if role != "" {
			req = req.WithContext(auth.WithUser(req.Context(), uuid.New(), role))
			expected = 403
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != expected || bytes.Contains(w.Body.Bytes(), []byte("answerKey")) {
			t.Fatalf("role %q: %d %s", role, w.Code, w.Body)
		}
	}
}

func TestEditorialFamilyCannotSplitAnIdenticalSource(t *testing.T) {
	base := drillUnit{Drill: Drill{ID: "passage-q1", Version: "1", Section: "reading", Modality: "objective", EditorialStatus: "draft", Stimulus: "One shared passage"}, Key: json.RawMessage(`{"value":"TRUE"}`)}
	sibling := base
	sibling.Drill.ID = "passage-q2"
	sibling.Key = json.RawMessage(`{"value":"FALSE"}`)
	metadata := map[string]editorialMetadata{
		editorialKey("drill", base.Drill.ID, "1"):    {Status: "reviewed", FamilyID: "one-family", ContentHash: drillContentHash(base)},
		editorialKey("drill", sibling.Drill.ID, "1"): {Status: "reviewed", FamilyID: "different-family", ContentHash: drillContentHash(sibling)},
	}
	for _, u := range applyEditorialMetadata([]drillUnit{base, sibling}, metadata) {
		if u.Drill.MasteryEligible {
			t.Fatal("a conflicting sibling family qualified")
		}
	}
	derived := base
	derived.Drill.SourceMaterialID = ptr("original-test")
	derived.Drill.SourceVersionID = ptr("original-version")
	metadata = map[string]editorialMetadata{editorialKey("drill", derived.Drill.ID, "1"): {Status: "reviewed", FamilyID: "one-family", ContentHash: drillContentHash(derived)}}
	if applyEditorialMetadata([]drillUnit{derived}, metadata)[0].Drill.MasteryEligible {
		t.Fatal("derived item qualified without source review")
	}
	metadata[editorialKey("material", "original-test", "original-version")] = editorialMetadata{Status: "reviewed", FamilyID: "original-family", ContentHash: materialContentHash("original-test", "original-version")}
	if applyEditorialMetadata([]drillUnit{derived}, metadata)[0].Drill.MasteryEligible {
		t.Fatal("derived item qualified under a different source family")
	}
	m := metadata[editorialKey("drill", derived.Drill.ID, "1")]
	m.FamilyID = "original-family"
	metadata[editorialKey("drill", derived.Drill.ID, "1")] = m
	if !applyEditorialMetadata([]drillUnit{derived}, metadata)[0].Drill.MasteryEligible {
		t.Fatal("matching reviewed source and item failed admission")
	}
}

func TestEditorialHistoricalVersionCanBeWithdrawnAndNotesAreUnicode(t *testing.T) {
	h := setup(t)
	id, version, _ := h.seedReading(20)
	router := chi.NewRouter()
	NewEditorialHandler(h.pool, 1<<20).Routes(router)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req = req.WithContext(auth.WithUser(req.Context(), h.user, auth.RoleAdmin))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	input := editorialInput{Version: version.String(), ContentHash: materialContentHash(id.String(), version.String()), FamilyID: "historical-family", Status: "reviewed", ReviewNotes: strings.Repeat("Я", 2500), KeysChecked: true, FamilyChecked: true, SourceChecked: true}
	path := "/study-learning/content/material/" + id.String()
	if w := request("PUT", path, input); w.Code != 200 {
		t.Fatalf("unicode note rejected %d %s", w.Code, w.Body)
	}
	newVersion := uuid.New()
	h.sql(`INSERT INTO reading_material_versions(id,material_id,version_number,title,body,created_by,duration_minutes) VALUES($1,$2,2,'New published version','Another text',$3,20)`, newVersion, id, h.user)
	h.sql(`UPDATE reading_materials SET published_version_id=$2,current_version_id=$2 WHERE id=$1`, id, newVersion)
	input.ExpectedRevision = 1
	input.Status = "withdrawn"
	if w := request("PUT", path, input); w.Code != 200 {
		t.Fatalf("old version withdrawal failed %d %s", w.Code, w.Body)
	}
	w := request("GET", path+"/history?version="+version.String(), nil)
	var history struct {
		Items []json.RawMessage `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Items) != 2 {
		t.Fatalf("history %d %s", w.Code, w.Body)
	}
}

func TestEditorialHTTPReviewRevisionAuditAndWithdrawal(t *testing.T) {
	pool := testdb.Open(t)
	actor := testdb.User(t, pool)
	router := chi.NewRouter()
	NewEditorialHandler(pool, 1<<20).Routes(router)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(auth.WithUser(req.Context(), actor, auth.RoleAdmin))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := request("GET", "/study-learning/content", nil)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var listing struct {
		Items []editorialCandidate `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	var item editorialCandidate
	for _, c := range listing.Items {
		if c.Kind == "drill" && c.Section == "reading" && len(c.AnswerKey) > 0 {
			item = c
			break
		}
	}
	if item.ID == "" {
		t.Fatal("no original objective candidate available")
	}
	path := "/study-learning/content/drill/" + item.ID
	input := editorialInput{Version: item.Version, ContentHash: item.ContentHash, FamilyID: "independent-ferry-study", Status: "reviewed", ReviewNotes: "Key, source and independence checked against the full exercise.", KeysChecked: true, FamilyChecked: true, SourceChecked: true}
	bad := input
	bad.ContentHash = "changed"
	if w := request("PUT", path, bad); w.Code != 409 {
		t.Fatalf("hash: %d %s", w.Code, w.Body)
	}
	bad = input
	bad.KeysChecked = false
	if w := request("PUT", path, bad); w.Code != 422 {
		t.Fatalf("checklist: %d %s", w.Code, w.Body)
	}
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- request("PUT", path, input).Code }()
	}
	wg.Wait()
	close(codes)
	successes, conflicts := 0, 0
	for code := range codes {
		if code == 200 {
			successes++
		}
		if code == 409 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent result: successes=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM study_learning_content_reviews`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	meta, err := loadEditorialMetadata(context.Background(), tx)
	tx.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	admitted := applyEditorialMetadata(builtinDrills(), meta)
	found := false
	for _, u := range admitted {
		if u.Drill.ID == item.ID {
			found = u.Drill.MasteryEligible
		}
	}
	if !found {
		t.Fatal("saved approval did not enable the exact reviewed exercise")
	}
	input.ExpectedRevision = 1
	input.Status = "withdrawn"
	if w := request("PUT", path, input); w.Code != 200 {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body)
	}
	tx, _ = pool.Begin(context.Background())
	meta, err = loadEditorialMetadata(context.Background(), tx)
	tx.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range applyEditorialMetadata(builtinDrills(), meta) {
		if u.Drill.ID == item.ID {
			t.Fatal("withdrawn exercise remains offered")
		}
	}
}
