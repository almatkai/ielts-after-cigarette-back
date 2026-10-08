package reading

import (
	"context"
	"errors"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func TestReadingPreviewDoesNotPublishOrChangeSnapshots(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	service := NewService(repo)
	actor := testdb.User(t, pool)
	seeded := seedReadingTest(t, ctx, service, actor)

	preview, err := service.Preview(ctx, seeded.ID, false)
	if err != nil || len(preview.Material.Passages) != 2 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	if _, err := service.GetPublic(ctx, seeded.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft is publicly readable: %v", err)
	}
	items, err := service.ListPublic(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("draft appears in public library: %#v %v", items, err)
	}

	published, details, err := service.Publish(ctx, seeded.ID, actor, seeded.Revision)
	if err != nil || len(details) != 0 {
		t.Fatalf("publish: %v %v", err, details)
	}
	input := SaveInput{Kind: published.Kind, Slug: published.Slug, ExamType: published.ExamType, Difficulty: published.Difficulty,
		Title: "Changed draft title", Body: published.Body, DurationMinutes: published.DurationMinutes, Revision: published.Revision}
	updated, details, err := service.Update(ctx, seeded.ID, actor, input)
	if err != nil || len(details) != 0 {
		t.Fatalf("update: %v %v", err, details)
	}
	draft, err := service.Preview(ctx, seeded.ID, false)
	if err != nil || draft.Material.Title != input.Title || draft.VersionNumber != updated.CurrentVersionNumber {
		t.Fatalf("draft: %#v %v", draft, err)
	}
	old, err := service.Preview(ctx, seeded.ID, true)
	if err != nil || old.Material.Title != seeded.Title || old.VersionNumber != seeded.CurrentVersionNumber {
		t.Fatalf("published: %#v %v", old, err)
	}
	public, err := service.GetPublic(ctx, seeded.ID)
	if err != nil || public.Title != seeded.Title {
		t.Fatalf("public snapshot changed: %#v %v", public, err)
	}
	latest, err := service.Get(ctx, seeded.ID)
	if err != nil || latest.Revision != updated.Revision || latest.CurrentVersionID != updated.CurrentVersionID {
		t.Fatal("preview changed a saved version")
	}
}
