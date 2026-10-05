package analytics

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestNormalizePathDropsQueryAndCollapsesIDs(t *testing.T) {
	id := uuid.NewString()
	for input, want := range map[string]string{
		"":                                     "/",
		"/reading/" + id + "?token=secret#top": "/reading/:id",
		"exam/full-mock-sessions/" + id + "/sections/3": "/exam/full-mock-sessions/:id/sections/:n",
		"/admin/analytics/":                             "/admin/analytics",
	} {
		if got := normalizePath(input); got != want {
			t.Fatalf("normalizePath(%q) = %q, want %q", input, got, want)
		}
	}
	if got := normalizePath("/" + strings.Repeat("a", 500)); len(got) != maxPathLength {
		t.Fatalf("path not bounded: %d", len(got))
	}
}

func TestNilTrackerIsSafe(t *testing.T) {
	var tracker *Tracker
	tracker.TouchUser(uuid.New())
	if err := tracker.Ping(context.Background(), uuid.New(), "/", true); err != nil {
		t.Fatal(err)
	}
	visitors, views := tracker.DailyVisitors(context.Background(), []time.Time{time.Now()})
	if len(visitors) != 1 || len(views) != 1 {
		t.Fatal("daily visitors must keep the requested length")
	}
}

func TestOverviewAndExport(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	registered := testdb.User(t, pool)
	lead := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, role, status, source, referral_code, created_at)
		VALUES ($1, $2, '+77000000000', 'STUDENT', 'WAITING', 'Instagram', 'REF12345', CURRENT_TIMESTAMP)
	`, lead, lead.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO attempts (id, user_id, material_type, material_id, material_version_id, status, score, max_score, band, started_at, submitted_at)
		VALUES ($1, $2, 'reading', $3, $3, 'SUBMITTED', 30, 40, 7.0, CURRENT_TIMESTAMP - interval '20 minutes', CURRENT_TIMESTAMP),
		       ($4, $2, 'listening', $3, $3, 'IN_PROGRESS', NULL, NULL, NULL, CURRENT_TIMESTAMP, NULL)
	`, uuid.New(), registered, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_activity_days (day, user_id) VALUES ($1, $2)`,
		now.In(location).Format(time.DateOnly), registered); err != nil {
		t.Fatal(err)
	}

	overview, err := NewRepository(pool).Overview(ctx, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	totals := overview.Totals
	if totals.RegisteredUsers != 1 || totals.WaitlistPending != 1 || totals.RegisteredToday != 1 || totals.WaitlistToday != 1 {
		t.Fatalf("user totals: %+v", totals)
	}
	if totals.DAU != 1 || totals.WAU != 1 || totals.MAU != 1 || totals.AttemptsToday != 2 || totals.SubmittedToday != 1 {
		t.Fatalf("activity totals: %+v", totals)
	}
	if len(overview.Daily) != 7 || overview.Daily[6].Registrations != 1 || overview.Daily[6].Attempts != 2 {
		t.Fatalf("daily series: %+v", overview.Daily)
	}
	if len(overview.Skills) != 2 || overview.Skills[1].Skill != "reading" || overview.Skills[1].AverageBand == nil || *overview.Skills[1].AverageBand != 7 {
		t.Fatalf("skills: %+v", overview.Skills)
	}
	if overview.Funnel[1].Users != 1 || overview.Funnel[3].Users != 1 || len(overview.Cohorts) != 1 || overview.Cohorts[0].Size != 1 {
		t.Fatalf("funnel/cohorts: %+v %+v", overview.Funnel, overview.Cohorts)
	}
	if len(overview.Sources) != 2 || len(overview.Heatmap) == 0 {
		t.Fatalf("sources/heatmap: %+v %+v", overview.Sources, overview.Heatmap)
	}

	var out bytes.Buffer
	if err := NewRepository(pool).Export(ctx, &out, "users", now.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	csv := out.String()
	if !strings.HasPrefix(csv, "id,role,status,source,from_waitlist") || strings.Count(csv, "\n") != 3 {
		t.Fatalf("users export: %q", csv)
	}
	if strings.Contains(csv, "@example.test") || strings.Contains(csv, "+7700") {
		t.Fatal("export must not contain personal data")
	}
	for dataset := range exportQueries {
		if err := NewRepository(pool).Export(ctx, &bytes.Buffer{}, dataset, time.Unix(0, 0)); err != nil {
			t.Fatalf("export %s: %v", dataset, err)
		}
	}
}

func TestTrackerRealtime(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	ctx := context.Background()
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(client, nil, nil)
	first, second := uuid.New(), uuid.New()
	for _, ping := range []struct {
		id   uuid.UUID
		path string
		view bool
	}{{first, "/reading", true}, {first, "/reading", false}, {second, "/reading", true}, {second, "/writing/" + uuid.NewString(), true}} {
		if err := tracker.Ping(ctx, ping.id, ping.path, ping.view); err != nil {
			t.Fatal(err)
		}
	}
	tracker.touchUser(uuid.New(), time.Now())

	realtime, err := tracker.Realtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !realtime.Available || realtime.OnlineVisitors != 2 || realtime.OnlineUsers != 1 || realtime.VisitorsToday != 2 || realtime.PageViewsToday != 3 {
		t.Fatalf("realtime: %+v", realtime)
	}
	if len(realtime.ActivePages) != 2 || len(realtime.TopPagesToday) != 2 || realtime.TopPagesToday[0].Path != "/reading" || realtime.TopPagesToday[0].Count != 2 {
		t.Fatalf("pages: %+v", realtime)
	}

	// A visitor whose last heartbeat is outside the window drops off.
	tracker.now = func() time.Time { return time.Now().Add(OnlineWindow + time.Minute) }
	realtime, err = tracker.Realtime(ctx)
	if err != nil || realtime.OnlineVisitors != 0 || realtime.OnlineUsers != 0 {
		t.Fatalf("stale presence kept: %+v %v", realtime, err)
	}
}
