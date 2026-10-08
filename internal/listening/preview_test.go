package listening

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type previewRepository struct {
	Repository
	draft, published Test
	reads            int
}

func (r *previewRepository) Get(_ context.Context, _ uuid.UUID, published bool) (Test, error) {
	r.reads++
	if published {
		if r.published.ID == uuid.Nil {
			return Test{}, ErrNotFound
		}
		return r.published, nil
	}
	return r.draft, nil
}

func TestPreviewListeningVersionsAndAnswerProjection(t *testing.T) {
	id, audio, image := uuid.New(), uuid.New(), uuid.New()
	parts := []Part{{Position: 1, AudioAssetID: &audio, Groups: []QuestionGroup{{Type: TypeMapLabelling, Config: map[string]any{"nested": []any{map[string]any{"explanation": "config-secret", "answer": "config-answer"}}}, ImageAssetID: &image, Questions: []Question{{ID: uuid.New(), Number: 1, Prompt: "Label the map", Content: map[string]any{"wordLimit": 2, "quote": "Turn left.", "nested": []any{map[string]any{"answer": "nested-secret"}}}, Answer: map[string]any{"value": "secret-answer"}, Explanation: "secret-explanation", Points: 1}}}}}}
	repo := &previewRepository{
		draft:     Test{ID: id, Title: "Draft", Parts: parts, CurrentVersionNumber: 2, Revision: 4, Status: StatusPublished, HasUnpublishedChanges: true},
		published: Test{ID: id, Title: "Published", Parts: parts, CurrentVersionNumber: 1, Revision: 4, Status: StatusPublished},
	}
	service := NewServiceWithStorage(repo, nil)
	for _, published := range []bool{false, true} {
		preview, err := service.Preview(context.Background(), id, published)
		if err != nil {
			t.Fatal(err)
		}
		wantTitle, wantVersion := "Draft", 2
		if published {
			wantTitle, wantVersion = "Published", 1
		}
		if preview.Material.Title != wantTitle || preview.VersionNumber != wantVersion || preview.Revision != 4 {
			t.Fatalf("wrong version: %#v", preview)
		}
		if *preview.Material.Parts[0].AudioAssetID != audio || *preview.Material.Parts[0].Groups[0].ImageAssetID != image {
			t.Fatal("preview lost media references")
		}
		questionID := parts[0].Groups[0].Questions[0].ID.String()
		key, ok := preview.AnswerKeys[questionID]
		if !ok || key.Answer["value"] != "secret-answer" || key.Explanation != "secret-explanation" || key.Quote != "Turn left." {
			t.Fatalf("administrator preview lost its answer key: %#v", preview.AnswerKeys)
		}
		encoded, err := json.Marshal(preview.Material)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"secret-answer", "secret-explanation", "config-secret", "config-answer", "nested-secret", `"answer":`, `"explanation":`, `"quote":`} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("student projection leaked %s", secret)
			}
		}
	}
	if repo.reads != 2 {
		t.Fatalf("reads = %d", repo.reads)
	}
}

func TestListeningPreviewKeysFollowSelectedVersion(t *testing.T) {
	id, questionID := uuid.New(), uuid.New()
	makeTest := func(option string, version int) Test {
		return Test{ID: id, CurrentVersionNumber: version, Parts: []Part{{Groups: []QuestionGroup{{Questions: []Question{
			{ID: questionID, Answer: map[string]any{"optionIds": []string{option}}, Explanation: option + " explanation", Content: map[string]any{"quote": option + " quote"}},
		}}}}}}
	}
	repo := &previewRepository{draft: makeTest("A", 2), published: makeTest("B", 1)}
	service := NewServiceWithStorage(repo, nil)
	for _, published := range []bool{false, true} {
		preview, err := service.Preview(context.Background(), id, published)
		if err != nil {
			t.Fatal(err)
		}
		want := "A"
		if published {
			want = "B"
		}
		key := preview.AnswerKeys[questionID.String()]
		if key.Answer["optionIds"].([]string)[0] != want || key.Explanation != want+" explanation" || key.Quote != want+" quote" {
			t.Fatalf("key does not match selected version: %#v", key)
		}
	}
}

func TestListeningPreviewEndpointPermissionsAndVersions(t *testing.T) {
	id := uuid.New()
	repo := &previewRepository{draft: Test{ID: id, Title: "Draft", Status: StatusDraft}}
	handler := NewHandler(NewServiceWithStorage(repo, nil), slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20, 1<<20)
	tokens := auth.NewTokenManager("preview-tests-secret", "test", "test", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Use(auth.Authenticate(tokens))
	router.Get("/tests/{testID}/preview", handler.Preview)
	for _, tc := range []struct {
		role, query string
		status      int
	}{
		{"", "", http.StatusUnauthorized},
		{auth.RoleStudent, "", http.StatusForbidden},
		{auth.RoleEditor, "", http.StatusForbidden},
		{auth.RoleAdmin, "", http.StatusOK},
		{auth.RoleAdmin, "?version=draft", http.StatusOK},
		{auth.RoleAdmin, "?version=published", http.StatusNotFound},
		{auth.RoleAdmin, "?version=invalid", http.StatusBadRequest},
	} {
		t.Run(tc.role+tc.query, func(t *testing.T) {
			before := repo.reads
			req := httptest.NewRequest(http.MethodGet, "/tests/"+id.String()+"/preview"+tc.query, nil)
			if tc.role != "" {
				token, _, err := tokens.NewAccessToken(uuid.New(), tc.role)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status == http.StatusForbidden && repo.reads != before {
				t.Fatal("unauthorized preview reached the repository")
			}
			if tc.role == auth.RoleAdmin && response.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("preview may be cached")
			}
		})
	}
}
