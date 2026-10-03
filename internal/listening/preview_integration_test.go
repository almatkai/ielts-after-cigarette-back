package listening

import (
	"context"
	"errors"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func TestListeningPreviewDoesNotPublishAndDraftMediaRemainPrivate(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	service := NewService(repo, t.TempDir())
	actor := testdb.User(t, pool)
	media, err := repo.CreateMedia(ctx, actor, Media{Kind: "audio", OriginalName: "preview.wav", MimeType: "audio/wav", StorageKey: "preview.wav", ByteSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := SaveInput{Slug: "preview-listening", ExamType: "academic", Title: "Original listening", DurationMinutes: 40,
		Parts: []Part{{Title: "Part one", AudioAssetID: &media.ID, Groups: []QuestionGroup{{Type: TypeShortAnswer, Questions: []Question{{Number: 1, Prompt: "Answer", Answer: map[string]any{"value": "test"}, Points: 1}}}}}}}
	seeded, details, err := service.Create(ctx, actor, input)
	if err != nil || len(details) != 0 {
		t.Fatalf("create: %v %v", err, details)
	}
	if _, err := service.Preview(ctx, seeded.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetPublic(ctx, seeded.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft is publicly readable: %v", err)
	}
	items, err := service.ListPublic(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("draft appears in public library: %#v %v", items, err)
	}
	if _, err := repo.GetMedia(ctx, media.ID, true); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("draft audio is public: %v", err)
	}
	if _, err := repo.GetMedia(ctx, media.ID, false); err != nil {
		t.Fatalf("admin cannot load draft audio: %v", err)
	}

	published, details, err := service.Publish(ctx, seeded.ID, actor, seeded.Revision)
	if err != nil || len(details) != 0 {
		t.Fatalf("publish: %v %v", err, details)
	}
	if _, err := repo.GetMedia(ctx, media.ID, true); err != nil {
		t.Fatalf("published audio is unavailable: %v", err)
	}
	newMedia, err := repo.CreateMedia(ctx, actor, Media{Kind: "audio", OriginalName: "new.wav", MimeType: "audio/wav", StorageKey: "new.wav", ByteSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	input.Parts[0].AudioAssetID = &newMedia.ID
	input.Title, input.Revision = "Changed draft listening", published.Revision
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
	if _, err := repo.GetMedia(ctx, newMedia.ID, true); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("unpublished replacement audio is public: %v", err)
	}
	if *public.Parts[0].AudioAssetID != media.ID {
		t.Fatal("public audio changed before republishing")
	}
	latest, err := service.GetAdmin(ctx, seeded.ID)
	if err != nil || latest.Revision != updated.Revision {
		t.Fatal("preview changed a saved version")
	}
}
