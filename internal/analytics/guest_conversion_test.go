package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/guest"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGuestConversionCohorts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, location)
	from := time.Date(2026, 10, 10, 0, 0, 0, 0, location)
	type scenario struct {
		name       string
		created    time.Time
		status     string
		registered time.Time
		completed  time.Time
		retakes    int
		want       GuestConversionPoint
	}
	cases := []scenario{
		// Midnight in Almaty is the previous UTC day; must still appear on Oct 10.
		{name: "opened", created: from, want: GuestConversionPoint{Guests: 1}},
		{name: "running", created: from.Add(time.Hour), status: "IN_PROGRESS", want: GuestConversionPoint{Guests: 1, Started: 1}},
		{name: "completed anonymously", created: from.Add(2 * time.Hour), status: "SUBMITTED", want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1}},
		{name: "registered later", created: from.Add(3 * time.Hour), status: "SUBMITTED", registered: now.Add(-time.Hour), want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1, Registered: 1}},
		{name: "existing account login", created: from.Add(4 * time.Hour), status: "SUBMITTED", registered: from.Add(-time.Hour), want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1}},
		{name: "registered before completion", created: from.Add(5 * time.Hour), status: "SUBMITTED", registered: from.Add(5*time.Hour + time.Minute), want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1}},
		{name: "retakes counted once", created: from.Add(6 * time.Hour), status: "SUBMITTED", registered: now.Add(-time.Hour), retakes: 2, want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1, Registered: 1}},
		{name: "future signup excluded", created: from.Add(7 * time.Hour), status: "SUBMITTED", registered: now.Add(time.Hour), want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1}},
		{name: "completion after observation excluded", created: from.Add(8 * time.Hour), status: "SUBMITTED", completed: now.Add(time.Hour), want: GuestConversionPoint{Guests: 1, Started: 1}},
		{name: "today", created: now.Add(-time.Hour), status: "SUBMITTED", registered: now, want: GuestConversionPoint{Guests: 1, Started: 1, Completed: 1, Registered: 1}},
		{name: "old cohort excluded despite recent conversion", created: from.Add(-time.Hour), status: "SUBMITTED", registered: now},
		{name: "future cohort excluded", created: now.Add(time.Hour), status: "IN_PROGRESS"},
	}
	want := GuestConversion{Daily: make([]GuestConversionPoint, 7)}
	for i := range want.Daily {
		want.Daily[i].Day = from.AddDate(0, 0, i).Format(time.DateOnly)
	}
	for _, tc := range cases {
		trial, err := guest.NewPostgresRepository(pool).Create(ctx, []byte(tc.name), "academic", now.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE guest_trials SET created_at=$2 WHERE user_id=$1`, trial.UserID, tc.created); err != nil {
			t.Fatal(err)
		}
		if tc.status != "" {
			owner := trial.UserID
			var account uuid.UUID
			if !tc.registered.IsZero() {
				account = testdb.User(t, pool)
				// A previous waitlist account can be created before the mock;
				// terms_accepted_at is when it actually finished registration.
				if _, err := pool.Exec(ctx, `UPDATE users SET created_at=$2,terms_accepted_at=$3 WHERE id=$1`, account, from.AddDate(0, 0, -30), tc.registered); err != nil {
					t.Fatal(err)
				}
				owner = account // Completed sessions have already been transferred by Claim.
			}
			var last uuid.UUID
			for i := 0; i <= tc.retakes; i++ {
				completed := tc.completed
				if completed.IsZero() {
					completed = tc.created.Add(30 * time.Minute)
				}
				last = seedConversionMock(t, pool, owner, tc.status, tc.created.Add(time.Minute), completed)
			}
			if account != uuid.Nil {
				if _, err := pool.Exec(ctx, `UPDATE guest_trials SET claimed_by=$2,claimed_session_id=$3 WHERE user_id=$1`, trial.UserID, account, last); err != nil {
					t.Fatal(err)
				}
				// A later account mock must not affect the guest stages.
				seedConversionMock(t, pool, account, "SUBMITTED", now.Add(-time.Minute), now)
			}
		}
		want.Guests += tc.want.Guests
		want.Started += tc.want.Started
		want.Completed += tc.want.Completed
		want.Registered += tc.want.Registered
		if tc.want.Guests > 0 {
			index := 0
			if tc.created.In(location).Format(time.DateOnly) == now.In(location).Format(time.DateOnly) {
				index = 6
			}
			p := &want.Daily[index]
			p.Guests += tc.want.Guests
			p.Started += tc.want.Started
			p.Completed += tc.want.Completed
			p.Registered += tc.want.Registered
		}
	}
	got, err := NewRepository(pool).Overview(ctx, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	g := got.GuestConversion
	if g.Guests != want.Guests || g.Started != want.Started || g.Completed != want.Completed || g.Registered != want.Registered {
		t.Fatalf("guest totals = %+v, want %+v", g, want)
	}
	if len(g.Daily) != len(want.Daily) {
		t.Fatalf("daily rows = %d, want %d", len(g.Daily), len(want.Daily))
	}
	for i, p := range g.Daily {
		if p != want.Daily[i] {
			t.Errorf("daily[%d] = %+v, want %+v", i, p, want.Daily[i])
		}
	}
}

func TestGuestConversionEmpty(t *testing.T) {
	pool := testdb.Open(t)
	got, err := NewRepository(pool).Overview(context.Background(), time.Now(), 7)
	if err != nil {
		t.Fatal(err)
	}
	g := got.GuestConversion
	if g.Guests != 0 || g.Started != 0 || g.Completed != 0 || g.Registered != 0 || len(g.Daily) != 7 {
		t.Fatalf("empty conversion: %+v", g)
	}
	for _, p := range g.Daily {
		if p.Day == "" || p.Guests != 0 || p.Started != 0 || p.Completed != 0 || p.Registered != 0 {
			t.Fatalf("empty day: %+v", p)
		}
	}
}

func TestGuestConversionSurvivesOwnershipTransfer(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	repo := guest.NewPostgresRepository(pool)
	trial, err := repo.Create(ctx, []byte("conversion-claim"), "academic", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guest_trials SET created_at=$2 WHERE user_id=$1`, trial.UserID, now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	session := seedConversionMock(t, pool, trial.UserID, "SUBMITTED", now.Add(-2*time.Hour), now.Add(-time.Hour))
	account := testdb.User(t, pool)
	for range 2 {
		claimed, err := repo.Claim(ctx, []byte("conversion-claim"), account)
		if err != nil || claimed == nil || *claimed != session {
			t.Fatalf("claim = %v, err = %v", claimed, err)
		}
	}
	got, err := NewRepository(pool).Overview(ctx, time.Now(), 7)
	if err != nil {
		t.Fatal(err)
	}
	g := got.GuestConversion
	if g.Guests != 1 || g.Started != 1 || g.Completed != 1 || g.Registered != 1 {
		t.Fatalf("conversion after repeated claim: %+v", g)
	}
}

func seedConversionMock(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID, status string, started, completed time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var submitted *time.Time
	if status == "SUBMITTED" {
		submitted = &completed
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO full_mock_sessions
		(id,user_id,exam_type,title,duration_minutes,status,started_at,submitted_at)
		VALUES ($1,$2,'academic','Conversion test',165,$3,$4,$5)`, id, owner, status, started, submitted)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
