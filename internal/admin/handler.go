package admin

import (
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type Handler struct{}

type AccessResponse struct {
	UserID uuid.UUID `json:"userId"`
	Role   string    `json:"role"`
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Access(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Bearer token is required", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AccessResponse{
		UserID: userID,
		Role:   auth.Role(r.Context()),
	})
}
