package reading

import (
	"context"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testcontent"
	"github.com/google/uuid"
)

// Preview is a read-only projection of a saved version. AnswerKeys is an
// administrator-only sidecar; Material retains the safe student projection.
type Preview struct {
	Material              PublicMaterial                   `json:"material"`
	AnswerKeys            map[string]testcontent.AnswerKey `json:"answerKeys"`
	VersionNumber         int                              `json:"versionNumber"`
	Revision              int64                            `json:"revision"`
	Status                string                           `json:"status"`
	HasUnpublishedChanges bool                             `json:"hasUnpublishedChanges"`
}

func (s *Service) Preview(ctx context.Context, id uuid.UUID, published bool) (Preview, error) {
	var material Material
	var err error
	if published {
		material, err = s.repository.GetPublished(ctx, id)
	} else {
		material, err = s.repository.Get(ctx, id)
	}
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Material: publicMaterial(material), AnswerKeys: previewAnswerKeys(material), VersionNumber: material.CurrentVersionNumber,
		Revision: material.Revision, Status: material.Status,
		HasUnpublishedChanges: material.HasUnpublishedChanges,
	}, nil
}

func previewAnswerKeys(material Material) map[string]testcontent.AnswerKey {
	keys := make(map[string]testcontent.AnswerKey)
	var collect func(Material)
	collect = func(item Material) {
		for _, group := range item.QuestionGroups {
			for _, question := range group.Questions {
				keys[question.ID.String()] = testcontent.NewAnswerKey(question.Answer, question.Explanation, question.Content)
			}
		}
		for _, passage := range item.Passages {
			collect(passage)
		}
	}
	collect(material)
	return keys
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if auth.Role(r.Context()) != auth.RoleAdmin {
		httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "Only administrators can preview tests", nil)
		return
	}
	id, ok := h.materialID(w, r)
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
