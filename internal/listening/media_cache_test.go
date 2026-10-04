package listening

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type cachedMediaRepository struct {
	Repository
	media     Media
	published bool
}

func (r *cachedMediaRepository) GetMedia(_ context.Context, id uuid.UUID, publishedOnly bool) (Media, error) {
	if id != r.media.ID || (publishedOnly && !r.published) {
		return Media{}, ErrMediaNotFound
	}
	return r.media, nil
}

type countingMediaStore struct {
	objectstorage.Store
	opens int
}
type cachedMediaReader struct{ *bytes.Reader }

func (*cachedMediaReader) Close() error { return nil }
func (s *countingMediaStore) Open(context.Context, string) (objectstorage.ReadSeekCloser, error) {
	s.opens++
	return &cachedMediaReader{bytes.NewReader([]byte("abcdef"))}, nil
}

func TestMediaRevalidationKeepsDraftAndArchiveAccessChecks(t *testing.T) {
	id := uuid.New()
	repo := &cachedMediaRepository{media: Media{ID: id, MimeType: "audio/wav", OriginalName: "test.wav", StorageKey: "test.wav", CreatedAt: time.Now()}, published: true}
	store := &countingMediaStore{}
	handler := NewHandler(NewServiceWithStorage(repo, store), slog.New(slog.NewTextHandler(io.Discard, nil)), 1024, 1024)
	tokens := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Use(auth.Authenticate(tokens))
	router.Get("/media/{mediaID}", handler.Media)
	request := func(role, etag, rng string) *httptest.ResponseRecorder {
		token, _, err := tokens.NewAccessToken(uuid.New(), role)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/media/"+id.String(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("If-None-Match", etag)
		req.Header.Set("Range", rng)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	first := request(auth.RoleStudent, "", "bytes=1-2")
	if first.Code != http.StatusPartialContent || first.Body.String() != "bc" {
		t.Fatalf("range response: %d %s", first.Code, first.Body)
	}
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatal("published media must revalidate")
	}
	second := request(auth.RoleStudent, "W/"+etag, "")
	if second.Code != http.StatusNotModified || store.opens != 1 || second.Body.Len() != 0 {
		t.Fatalf("revalidation reopened storage: status=%d opens=%d", second.Code, store.opens)
	}
	draft := request(auth.RoleAdmin, etag, "")
	if draft.Code != http.StatusOK || draft.Header().Get("Cache-Control") != "private, no-store" || draft.Header().Get("ETag") != "" || store.opens != 2 {
		t.Fatal("draft media used published cache")
	}
	repo.published = false
	archived := request(auth.RoleStudent, etag, "")
	if archived.Code != http.StatusNotFound || store.opens != 2 {
		t.Fatal("cached media bypassed current publication check")
	}
}
