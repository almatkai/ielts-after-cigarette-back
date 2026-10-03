package listening

import (
	"context"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testcontent"
	"github.com/google/uuid"
)

// Preview preserves the student projection and supplies an administrator-only
// answer-key sidecar from the same saved version, without creating an attempt.
type Preview struct {
	Material              PublicTest                       `json:"material"`
	AnswerKeys            map[string]testcontent.AnswerKey `json:"answerKeys"`
	VersionNumber         int                              `json:"versionNumber"`
	Revision              int64                            `json:"revision"`
	Status                string                           `json:"status"`
	HasUnpublishedChanges bool                             `json:"hasUnpublishedChanges"`
}

func (s *Service) Preview(ctx context.Context, id uuid.UUID, published bool) (Preview, error) {
	item, err := s.repository.Get(ctx, id, published)
	if err != nil {
		return Preview{}, err
	}
	keys := make(map[string]testcontent.AnswerKey)
	for _, part := range item.Parts {
		for _, group := range part.Groups {
			for _, question := range group.Questions {
				keys[question.ID.String()] = testcontent.NewAnswerKey(question.Answer, question.Explanation, question.Content)
			}
		}
	}
	return Preview{
		Material: publicTest(item), AnswerKeys: keys, VersionNumber: item.CurrentVersionNumber,
		Revision: item.Revision, Status: item.Status,
		HasUnpublishedChanges: item.HasUnpublishedChanges,
	}, nil
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if auth.Role(r.Context()) != auth.RoleAdmin {
		httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "Only administrators can preview tests", nil)
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	version := r.URL.Query().Get("version")
	if version != "" && version != "draft" && version != "published" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_VERSION", "version must be draft or published", nil)
		return
	}
	preview, err := h.service.Preview(r.Context(), id, version == "published")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, preview)
}
