package studyplan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Count real SQL operations through the authenticated handler, without timing
// thresholds or dependence on PostgreSQL's execution plan.
type planQueryTrace struct {
	mu              sync.Mutex
	total           int
	performance     int
	writingSpeaking int
}

func (tr *planQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, q pgx.TraceQueryStartData) context.Context {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.total++
	if strings.Contains(q.SQL, "q.stimulus,a.submitted_at") {
		tr.performance++
	}
	if strings.Contains(q.SQL, "COALESCE(w.model,s.model") {
		tr.writingSpeaking++
	}
	return ctx
}

func (*planQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func tracePlanQueries(t *testing.T, h *harness) *planQueryTrace {
	t.Helper()
	cfg := h.pool.Config().Copy()
	tr := &planQueryTrace{}
	cfg.ConnConfig.Tracer = tr
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h.service.pool = pool
	return tr
}

func (tr *planQueryTrace) reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.total, tr.performance, tr.writingSpeaking = 0, 0, 0
}

func seedPerformanceWS(h *harness, section string, count int) (uuid.UUID, uuid.UUID, uuid.UUID) {
	m, v, q := uuid.New(), uuid.New(), uuid.New()
	if section == "writing" {
		h.sql(`INSERT INTO writing_materials(id,slug) VALUES($1,$2)`, m, "perf-"+m.String())
		tasks, _ := json.Marshal([]map[string]any{{"id": q, "type": "task2", "prompt": "Fixture prompt"}})
		h.sql(`INSERT INTO writing_material_versions(id,material_id,version_number,exam_type,difficulty,title,tasks) VALUES($1,$2,1,'academic','intermediate','Fixture',$3)`, v, m, tasks)
	} else {
		h.sql(`INSERT INTO speaking_materials(id,slug) VALUES($1,$2)`, m, "perf-"+m.String())
		parts, _ := json.Marshal([]map[string]any{{"id": q, "prompt": "Fixture prompt"}})
		h.sql(`INSERT INTO speaking_material_versions(id,material_id,version_number,exam_type,difficulty,title,parts) VALUES($1,$2,1,'academic','intermediate','Fixture',$3)`, v, m, parts)
	}
	for i := 0; i < count; i++ {
		performanceAttempt(h, h.user, section, m, v, h.now.Add(-time.Duration(i+1)*time.Hour), "SUBMITTED", nil)
	}
	return m, v, q
}

func TestCalendarHTTPQueryCountBoundedWithWritingSpeakingHistory(t *testing.T) {
	for _, section := range []string{"writing", "speaking"} {
		t.Run(section, func(t *testing.T) {
			h := setup(t)
			h.create(30)
			tr := tracePlanQueries(t, h)
			baseline := 0
			for _, count := range []int{0, 20, 100} {
				if count > 0 {
					previous := 0
					if count == 100 {
						previous = 20
					}
					seedPerformanceWS(h, section, count-previous)
				}
				// Reconcile the changed history before measuring an unchanged GET.
				h.call("GET", "/study-plan", nil, 200)
				tr.reset()
				r := h.call("GET", "/study-plan", nil, 200)
				if r.Plan == nil || len(r.Plan.Days) != 30 || len(r.Progress) != 30 {
					t.Fatal("calendar contract changed")
				}
				if count == 0 {
					baseline = tr.total
				}
				t.Logf("%s attempts=%d SQL=%d W/S source queries=%d", section, count, tr.total, tr.writingSpeaking)
				if tr.total > baseline+1 || tr.writingSpeaking > 1 {
					t.Errorf("history causes serial SQL growth: baseline=%d attempts=%d SQL=%d source queries=%d", baseline, count, tr.total, tr.writingSpeaking)
				}
			}
		})
	}
}

func TestTaskPerformanceSQLOnlyForLearningGet(t *testing.T) {
	h := setup(t)
	h.create(30)
	c := sourceCycle(t, h)
	session := startDrill(t, h, "iac-r-library", "performance-trace", nil)
	tr := tracePlanQueries(t, h)
	check := func(name string, want int, action func()) {
		t.Helper()
		tr.reset()
		action()
		if tr.total == 0 || tr.performance != want {
			t.Errorf("%s: SQL=%d task-performance queries=%d want=%d", name, tr.total, tr.performance, want)
		}
	}
	check("learning get", 1, func() { learningGet(t, h) })
	check("calendar get", 0, func() { h.call("GET", "/study-plan", nil, 200) })
	check("inventory", 0, func() { learningCall[Inventory](t, h, "GET", "/inventory", nil, 200) })
	check("cycle", 0, func() { learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200) })
	check("session", 0, func() { learningCall[Session](t, h, "GET", "/sessions/"+session.ID, nil, 200) })
	check("view", 0, func() {
		c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/view", map[string]any{"expectedRevision": c.Revision}, 200)
	})
	check("correction", 0, func() {
		c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "performance-correction", "TRUE"), 200)
	})
	check("cycle skip", 0, func() {
		learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/skip", map[string]any{"expectedRevision": c.Revision, "idempotencyKey": "performance-skip"}, 200)
	})
	r := h.call("GET", "/study-plan", nil, 200)
	check("drill start", 0, func() {
		learningCall[Session](t, h, "POST", "/drills/iac-r-library/start", map[string]any{"expectedRevision": r.Plan.Revision, "idempotencyKey": "performance-start-2"}, 200)
	})
	check("drill submit", 0, func() { submitDrill(t, h, session, "performance-submit", "FALSE", false) })
	check("calendar create", 0, func() { h.create(30) })
	check("calendar replan", 0, func() {
		r := h.call("GET", "/study-plan", nil, 200)
		h.call("POST", "/study-plan/replan", map[string]any{"expectedRevision": r.Plan.Revision}, 200)
	})
	check("calendar settings", 0, func() {
		r := h.call("GET", "/study-plan", nil, 200)
		h.call("PUT", "/study-plan/settings", map[string]any{"expectedRevision": r.Plan.Revision, "dailyMinutes": 45, "studyDays": []int{1, 2, 3, 4, 5, 6, 7}}, 200)
	})
	check("calendar mock", 0, func() {
		r := h.call("GET", "/study-plan", nil, 200)
		// The week's offer cannot be declined before its decision day.
		h.call("POST", "/study-plan/weeks/1/mock", map[string]any{"expectedRevision": r.Plan.Revision, "action": "decline"}, 409)
	})
	// Even rejected mutation paths load/reconcile inputs before validating IDs.
	for _, action := range []string{"start", "skip", "review"} {
		check("calendar "+action, 0, func() {
			r := h.call("GET", "/study-plan", nil, 200)
			h.call("POST", fmt.Sprintf("/study-plan/assignments/%s/%s", uuid.NewString(), action), map[string]any{"expectedRevision": r.Plan.Revision, "reason": "no_time"}, 404)
		})
	}
}

func TestCalendarStartReloadDoesNotLoadTaskPerformance(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "")
	tr := tracePlanQueries(t, h)
	w := h.request("POST", "/study-plan/assignments/"+a.ID+"/start", mutationInput{ExpectedRevision: r.Plan.Revision}, h.token)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if tr.performance != 0 || tr.total == 0 {
		t.Fatalf("start and fresh draft reload ran %d performance queries (%d total SQL)", tr.performance, tr.total)
	}
}

func TestSyncLearningPreservesSelectionAndRefreshesFamilyIndex(t *testing.T) {
	units := []drillUnit{}
	for _, d := range []Drill{
		{ID: "00-reserved", FamilyID: ptr("reserved"), MasteryEligible: true},
		{ID: "00-script", FamilyID: ptr("script"), Modality: "script"},
		{ID: "00-source", FamilyID: ptr("different"), Stimulus: "Original source"},
		{ID: "a-seen", FamilyID: ptr("seen")},
		{ID: "z-unseen", FamilyID: ptr("new")},
		{ID: "zz-unknown"},
	} {
		d.Section, d.SkillID = "reading", "ms-r-tfng"
		units = append(units, drillUnit{Drill: d})
	}
	sources := []sourceMistake{}
	for i := 0; i < 150; i++ {
		sources = append(sources, sourceMistake{Identity: fmt.Sprint(i), SkillID: "ms-r-tfng", Source: SourceEvidence{MaterialID: fmt.Sprint(i), Stimulus: "Original source", FamilyID: ptr("seen"), FamilyKnown: true}, Key: json.RawMessage(`{"value":"TRUE"}`)})
	}
	st := state{}
	// Duplicate source records must retain the same identity and original snapshot.
	syncLearning(&st, append(sources, sources[0]), units, "2026-11-04")
	if len(st.Learning.Cycles) != len(sources) {
		t.Fatal("duplicate/missing source cycles")
	}
	ids := []string{}
	for _, c := range st.Learning.Cycles {
		ids = append(ids, c.Cycle.ID)
		if c.Cycle.NextDrillID == nil || *c.Cycle.NextDrillID != "z-unseen" {
			t.Fatalf("unseen remedial choice changed: %+v", c.Cycle)
		}
	}
	before, _ := json.Marshal(st.Learning)
	// Input bank order cannot change a stable choice or churn cycle revisions.
	for i, j := 0, len(units)-1; i < j; i, j = i+1, j-1 {
		units[i], units[j] = units[j], units[i]
	}
	syncLearning(&st, sources, units, "2026-11-04")
	after, _ := json.Marshal(st.Learning)
	if string(before) != string(after) {
		t.Fatal("unchanged sources/bank churn learning state")
	}
	for i := range sources {
		sources[i].Source.FamilyID = ptr("new")
		sources[i].Source.Feedback = "Changed snapshot is not a new mistake"
		sources[i].Key = json.RawMessage(`{"value":"FALSE"}`)
	}
	syncLearning(&st, sources, units, "2026-11-04")
	for i, c := range st.Learning.Cycles {
		if c.Cycle.ID != ids[i] || c.Cycle.Revision != 2 || c.Cycle.NextDrillID == nil || *c.Cycle.NextDrillID != "a-seen" || c.Cycle.Source.Feedback != "" || string(c.Key) != `{"value":"TRUE"}` {
			t.Fatalf("family refresh lost stable identity/immutable evidence/selection: %+v", c)
		}
	}
}
