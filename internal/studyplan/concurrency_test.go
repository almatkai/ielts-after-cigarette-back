package studyplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/google/uuid"
)

type starterFunc func(context.Context, uuid.UUID, string, uuid.UUID) (attempts.Attempt, any, bool, error)

func (f starterFunc) Start(ctx context.Context, user uuid.UUID, skill string, id uuid.UUID) (attempts.Attempt, any, bool, error) {
	return f(ctx, user, skill, id)
}

func TestConcurrentCreateHasOnePersistedCycle(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	var wg sync.WaitGroup
	out := make(chan *httptest.ResponseRecorder, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- h.request("POST", "/study-plan", allDays(30), h.token) }()
	}
	wg.Wait()
	close(out)
	id := ""
	for w := range out {
		if w.Code != 200 {
			t.Fatalf("create %d %s", w.Code, w.Body)
		}
		var r Response
		if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		if id == "" {
			id = r.Plan.ID
		}
		if id != r.Plan.ID || r.Plan.Revision != 1 {
			t.Fatal("competing cycles")
		}
	}
	var count int
	if e := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM study_plans WHERE user_id=$1`, h.user).Scan(&count); e != nil || count != 1 {
		t.Fatalf("%d cycles %v", count, e)
	}
}
func TestStartFailuresAndUniqueAttemptClaimRollback(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "")
	path := "/study-plan/assignments/" + a.ID + "/start"
	h.service.attempts = starterFunc(func(context.Context, uuid.UUID, string, uuid.UUID) (attempts.Attempt, any, bool, error) {
		return attempts.Attempt{}, nil, false, errors.New("controlled unavailable dependency")
	})
	h.call("POST", path, mutationInput{ExpectedRevision: r.Plan.Revision}, 500)
	next := h.call("GET", "/study-plan", nil, 200)
	if next.Plan.Revision != r.Plan.Revision || assignmentOn(t, next, next.Today, "").AttemptID != nil {
		t.Fatal("failure committed partial state")
	}
	// A misbehaving dependency cannot attach somebody else's attempt.
	foreign := uuid.New()
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status) VALUES($1,$2,'reading',$3,$4,'IN_PROGRESS')`, foreign, h.other, *a.MaterialID, uuid.New())
	h.service.attempts = starterFunc(func(context.Context, uuid.UUID, string, uuid.UUID) (attempts.Attempt, any, bool, error) {
		return attempts.Attempt{ID: foreign}, nil, false, nil
	})
	h.call("POST", path, mutationInput{ExpectedRevision: r.Plan.Revision}, 409)
	h.service.attempts = h.attempts
	w := h.request("POST", path, mutationInput{ExpectedRevision: r.Plan.Revision}, h.token)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var start StartResponse
	if e := json.Unmarshal(w.Body.Bytes(), &start); e != nil {
		t.Fatal(e)
	}
	if start.AttemptID == nil {
		t.Fatal("fresh start has no persisted attempt")
	}
	// Deliberately construct a conflicting aggregate row in this isolated test
	// schema. Even corrupted/repeated assignments must not consume one attempt
	// twice, and a failed claim must roll back the aggregate revision.
	var st state
	var raw []byte
	if e := h.pool.QueryRow(context.Background(), `SELECT state FROM study_plans WHERE user_id=$1`, h.user).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(raw, &st); e != nil {
		t.Fatal(e)
	}
	duplicate := a
	duplicate.ID = uuid.NewString()
	duplicate.Reason = "Test duplicate claim"
	st.Plan.Days[0].Assignments = append(st.Plan.Days[0].Assignments, duplicate)
	raw, _ = json.Marshal(st)
	h.sql(`UPDATE study_plans SET state=$2 WHERE user_id=$1`, h.user, raw)
	h.call("POST", "/study-plan/assignments/"+duplicate.ID+"/start", mutationInput{ExpectedRevision: start.PlanRevision}, 409)
	var claims int
	if e := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM study_plan_attempts WHERE plan_id=$1`, r.Plan.ID).Scan(&claims); e != nil || claims != 1 {
		t.Fatalf("claim uniqueness %d %v", claims, e)
	}
	var rev int64
	if e := h.pool.QueryRow(context.Background(), `SELECT revision FROM study_plans WHERE user_id=$1`, h.user).Scan(&rev); e != nil || rev != start.PlanRevision {
		t.Fatal("failed claim committed revision")
	}
}
func TestConcurrentStartAndSkipCannotBothCommit(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "")
	base := "/study-plan/assignments/" + a.ID
	out := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"start", "skip"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			out <- h.request("POST", base+"/"+action, map[string]any{"expectedRevision": r.Plan.Revision, "reason": "no_time"}, h.token)
		}(action)
	}
	wg.Wait()
	close(out)
	successes, conflicts := 0, 0
	for w := range out {
		switch w.Code {
		case 200:
			successes++
		case 409:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent status %d %s", w.Code, w.Body)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("start/skip race: %d/%d", successes, conflicts)
	}
	r = h.call("GET", "/study-plan", nil, 200)
	a = assignmentOn(t, r, r.Today, "")
	if a.Status != "SKIPPED" && a.Status != "IN_PROGRESS" {
		t.Fatalf("invalid atomic state %+v", a)
	}
}
func TestNewPublishedResultsAdaptFutureOnlyAndMissingMaterialStartFails(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	r := h.create(30)
	today := r.Plan.Days[0]
	h.seedReading(20)
	r = h.call("GET", "/study-plan", nil, 200)
	if r.Plan.Revision != 2 || r.Plan.Days[0].Assignments[0].ID != today.Assignments[0].ID {
		t.Fatal("new bank rewrote today")
	}
	rev := r.Plan.Revision
	r = h.call("GET", "/study-plan", nil, 200)
	if r.Plan.Revision != rev {
		t.Fatal("unstable bank fingerprint")
	}
	material := *r.Plan.Days[0].Assignments[0].MaterialID
	h.sql(`UPDATE reading_materials SET status='ARCHIVED' WHERE id=$1`, material)
	r = h.call("GET", "/study-plan", nil, 200)
	h.call("POST", fmt.Sprintf("/study-plan/assignments/%s/start", r.Plan.Days[0].Assignments[0].ID), mutationInput{ExpectedRevision: r.Plan.Revision}, 409)
}

// A saturated pool must still let the lock owner query full-mock availability
// and start an attempt. Waiters must not reserve every remaining connection.
func TestConcurrentPlanMutationsWithSmallPool(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "4")
	parsed.RawQuery = query.Encode()
	t.Setenv("TEST_DATABASE_URL", parsed.String())
	h := setup(t)
	h.seedReading(20)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const callers = 10
	var wg sync.WaitGroup
	responses := make(chan Response, callers)
	failures := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := h.service.Create(ctx, h.user, allDays(30))
			responses <- response
			failures <- err
		}()
	}
	wg.Wait()
	for i := 0; i < callers; i++ {
		if err := <-failures; err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	response := <-responses
	for i := 1; i < callers; i++ {
		other := <-responses
		if other.Plan.ID != response.Plan.ID || other.Plan.Revision != 1 {
			t.Fatal("concurrent creates persisted competing cycles")
		}
	}
	assignment := assignmentOn(t, response, response.Today, "PRACTICE")
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.service.Start(ctx, h.user, assignment.ID, response.Plan.Revision)
			failures <- err
		}()
	}
	wg.Wait()
	started := 0
	for i := 0; i < callers; i++ {
		err := <-failures
		if err == nil {
			started++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent start: %v", err)
		}
	}
	if started != 1 {
		t.Fatalf("started %d attempts; want exactly one", started)
	}
	var count int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM study_plan_attempts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("attempt claims: %d, error %v", count, err)
	}
}
