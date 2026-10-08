package reading

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
	draft, published Material
	reads            int
}

func (r *previewRepository) Get(context.Context, uuid.UUID) (Material, error) {
	r.reads++
	return r.draft, nil
}
func (r *previewRepository) GetPublished(context.Context, uuid.UUID) (Material, error) {
	r.reads++
	if r.published.ID == uuid.Nil {
		return Material{}, ErrNotFound
	}
	return r.published, nil
}

func TestPreviewReadingVersionsAndNestedAnswerProjection(t *testing.T) {
	id := uuid.New()
	question := Question{ID: uuid.New(), Prompt: "Choose", Content: map[string]any{"options": []string{"A", "B"}, "quote": "It&#8217;s safe &lt;script&gt;", "hint": "First paragraph", "nested": []any{map[string]any{"answer": "nested-secret", "explanation": "nested-explanation"}}}, Answer: map[string]any{"value": "secret-answer"}, Explanation: "secret-explanation", Points: 1}
	group := QuestionGroup{ID: uuid.New(), Type: QuestionMultipleChoice, Questions: []Question{question}}
	repo := &previewRepository{
		draft: Material{ID: id, Title: "Draft", Kind: KindTest, CurrentVersionNumber: 2, Revision: 4, Status: StatusPublished, HasUnpublishedChanges: true,
			Passages: []Material{{ID: uuid.New(), Body: "It&#8217;s safe &lt;script&gt;", QuestionGroups: []QuestionGroup{group}}}},
		published: Material{ID: id, Title: "Published", Kind: KindTest, CurrentVersionNumber: 1, Revision: 4, Status: StatusPublished, QuestionGroups: []QuestionGroup{group}},
	}
	service := NewService(repo)
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
		if !published && preview.Material.Passages[0].Body != "It’s safe <script>" {
			t.Fatal("HTML entities were not decoded as text")
		}
		key, ok := preview.AnswerKeys[question.ID.String()]
		if !ok || key.Answer["value"] != "secret-answer" || key.Explanation != "secret-explanation" || key.Quote != "It’s safe <script>" || key.Hint != "First paragraph" {
			t.Fatalf("administrator preview lost its answer key: %#v", preview.AnswerKeys)
		}
		encoded, err := json.Marshal(preview.Material)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"secret-answer", "secret-explanation", "nested-secret", "nested-explanation", `"answer":`, `"explanation":`, `"quote":`, `"hint":`} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("student projection leaked %s", secret)
			}
		}
	}
	if repo.reads != 2 {
		t.Fatalf("reads = %d", repo.reads)
	}
}

func TestReadingPreviewKeysFollowPinnedPassageVersion(t *testing.T) {
	id, questionID := uuid.New(), uuid.New()
	makeMaterial := func(answer string, version int) Material {
		return Material{ID: id, Kind: KindTest, CurrentVersionNumber: version, Passages: []Material{
			{ID: uuid.New(), QuestionGroups: []QuestionGroup{{Questions: []Question{
				{ID: questionID, Answer: map[string]any{"value": answer}, Explanation: answer + " explanation", Content: map[string]any{"quote": answer + " quote"}},
			}}}},
		}}
	}
	repo := &previewRepository{draft: makeMaterial("draft", 2), published: makeMaterial("published", 1)}
	service := NewService(repo)
	for _, published := range []bool{false, true} {
		preview, err := service.Preview(context.Background(), id, published)
		if err != nil {
			t.Fatal(err)
		}
		want := "draft"
		if published {
			want = "published"
		}
		key := preview.AnswerKeys[questionID.String()]
		if key.Answer["value"] != want || key.Explanation != want+" explanation" || key.Quote != want+" quote" {
			t.Fatalf("key does not match selected pinned passage: %#v", key)
		}
	}
}

func TestReadingPreviewEndpointPermissionsAndVersions(t *testing.T) {
	id := uuid.New()
	repo := &previewRepository{draft: Material{ID: id, Title: "Draft", Status: StatusDraft}}
	handler := NewHandler(NewService(repo), slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	tokens := auth.NewTokenManager("preview-tests-secret", "test", "test", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Use(auth.Authenticate(tokens))
	// Intentionally no role middleware: the endpoint must also enforce its own role.
	router.Get("/materials/{materialID}/preview", handler.Preview)
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
			req := httptest.NewRequest(http.MethodGet, "/materials/"+id.String()+"/preview"+tc.query, nil)
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
