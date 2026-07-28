package user

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Handler struct {
	service  *Service
	logger   *slog.Logger
	maxBytes int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBytes int64) *Handler {
	return &Handler{service: service, logger: logger, maxBytes: maxBytes}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required", nil)
		return
	}
	profile, err := h.service.Get(r.Context(), userID)
	if err != nil {
		h.handleError(w, r, "get profile", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, profile)
}

type profilePatchRequest struct {
	DisplayName *string `json:"displayName"`
	Timezone    *string `json:"timezone"`
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required", nil)
		return
	}
	var request profilePatchRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	profile, details, err := h.service.UpdateProfile(r.Context(), userID, ProfilePatch{
		DisplayName: request.DisplayName,
		Timezone:    request.Timezone,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.handleError(w, r, "update profile", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, profile)
}

type goalRequest struct {
	TargetBand *float64 `json:"targetBand"`
	ExamDate   *string  `json:"examDate"`
	ExamType   *string  `json:"examType"`
}

func (h *Handler) UpdateGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required", nil)
		return
	}
	var request goalRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	profile, details, err := h.service.UpdateGoal(r.Context(), userID, GoalInput{
		TargetBand: request.TargetBand,
		ExamDate:   request.ExamDate,
		ExamType:   request.ExamType,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.handleError(w, r, "update profile goal", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, profile)
}

func (h *Handler) handleError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, "PROFILE_NOT_FOUND", "Profile was not found", nil)
		return
	}
	h.logger.ErrorContext(r.Context(), operation,
		"request_id", httpx.RequestID(r.Context()),
		"error", err,
	)
	httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
}
