package reading

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type readingQueryCounter struct{ count atomic.Int64 }

func (c *readingQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (*readingQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestReadingLoadQueryCountDoesNotGrowPerPassage(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	service := NewService(NewPostgresRepository(pool))
	seeded := seedReadingTest(t, ctx, service, actor)

	counter := &readingQueryCounter{}
	config := pool.Config()
	config.ConnConfig.Tracer = counter
	countedPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer countedPool.Close()
	repo := NewPostgresRepository(countedPool)
	check := func(wantPassages int) {
		t.Helper()
		counter.count.Store(0)
		loaded, err := repo.GetVersion(ctx, seeded.ID, seeded.CurrentVersionID)
		if err != nil {
			t.Fatal(err)
		}
		if got := counter.count.Load(); got != 6 {
			t.Fatalf("%d passages took %d queries, want 6 independent of passage count", wantPassages, got)
		}
		if len(loaded.Passages) != wantPassages {
			t.Fatalf("passages = %d, want %d", len(loaded.Passages), wantPassages)
		}
		for i, passage := range loaded.Passages {
			if len(passage.QuestionGroups) != 1 || len(passage.QuestionGroups[0].Questions) == 0 {
				t.Fatalf("passage %d lost questions: %+v", i, passage)
			}
		}
		if loaded.Passages[0].Title != "Passage one" || loaded.Passages[1].Title != "Passage two" {
			t.Fatal("passage order changed")
		}
	}
	check(2)
	input := saveInputForRefreshTest(seeded.Passages[1])
	input.Slug = "loading-third-passage"
	input.Title = "Passage three"
	third, details, err := service.Create(ctx, actor, input)
	if err != nil || len(details) > 0 {
		t.Fatalf("third passage: %v %v", err, details)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reading_test_passages
		(test_material_version_id, position, passage_material_id, passage_material_version_id)
		VALUES ($1,3,$2,$3)`, seeded.CurrentVersionID, third.ID, third.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	check(3)

	// The bulk loader must reject a valid version paired with another material.
	if _, err := repo.GradingStructures(ctx, []VersionRef{{MaterialID: uuid.New(), VersionID: third.CurrentVersionID}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched material/version error = %v", err)
	}
}
