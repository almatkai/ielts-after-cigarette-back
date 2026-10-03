package listening

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mediaRepository stubs only the media surface: the storage key points at an
// object that was never uploaded to this environment's storage.
type mediaRepository struct {
	Repository
	media Media
}

func (r *mediaRepository) GetMedia(_ context.Context, id uuid.UUID, _ bool) (Media, error) {
	if id != r.media.ID {
		return Media{}, ErrMediaNotFound
	}
	return r.media, nil
}

func TestMediaMissingObjectAnswers404Not500(t *testing.T) {
	mediaID := uuid.New()
	repo := &mediaRepository{media: Media{
		ID: mediaID, Kind: "audio", OriginalName: "part1.mp3",
		MimeType: "audio/mpeg", StorageKey: "listening/never-uploaded.mp3",
	}}
	service := NewServiceWithStorage(repo, objectstorage.NewFileStore(t.TempDir()))

	if _, _, err := service.Media(context.Background(), mediaID, false); !errors.Is(err, ErrMediaObjectMissing) {
		t.Fatalf("service error = %v, want ErrMediaObjectMissing", err)
	}

	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20, 1<<20)
	tokens := auth.NewTokenManager("media-tests-secret", "test", "test", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Use(auth.Authenticate(tokens))
	router.Get("/listening/media/{mediaID}", handler.Media)

	token, _, err := tokens.NewAccessToken(uuid.New(), auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/listening/media/"+mediaID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, "MEDIA_NOT_FOUND") {
		t.Fatalf("body %q lacks MEDIA_NOT_FOUND", body)
	}
}
