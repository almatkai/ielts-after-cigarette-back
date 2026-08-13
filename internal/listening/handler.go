package listening

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service           *Service
	logger            *slog.Logger
	maxBody, maxMedia int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBody, maxMedia int64) *Handler {
	return &Handler{service: service, logger: logger, maxBody: maxBody, maxMedia: maxMedia}
}

func (h *Handler) ListAdmin(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListAdmin(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) GetAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	item, err := h.service.GetAdmin(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}
func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPublic(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	item, err := h.service.GetPublic(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	item, details, err := h.service.Create(r.Context(), actor, input)
	h.writeSave(w, r, http.StatusCreated, item, details, err)
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	item, details, err := h.service.Update(r.Context(), id, actor, input)
	h.writeSave(w, r, http.StatusOK, item, details, err)
}
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	item, details, err := h.service.Publish(r.Context(), id, actor, input.Revision)
	h.writeSave(w, r, http.StatusOK, item, details, err)
}
func (h *Handler) ParseImport(w http.ResponseWriter, r *http.Request) {
	var input ImportParseInput
	if !h.decode(w, r, &input) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ParseImport(input))
}
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	item, details, err := h.service.Create(r.Context(), actor, input)
	h.writeSave(w, r, http.StatusCreated, item, details, err)
}

func (h *Handler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxMedia+1<<20)
	if err := r.ParseMultipartForm(h.maxMedia); err != nil {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Media upload is too large", nil)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	if kind != "audio" && kind != "image" {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "kind must be audio or image", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "FILE_REQUIRED", "Multipart field file is required", nil)
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > h.maxMedia {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Media upload is too large", nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	media, err := h.service.StoreMedia(r.Context(), actor, kind, header, file)
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "MEDIA_INVALID", err.Error(), nil)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, media)
}
func (h *Handler) Media(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "mediaID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Media ID must be UUID", nil)
		return
	}
	role := auth.Role(r.Context())
	publishedOnly := role != auth.RoleEditor && role != auth.RoleAdmin
	media, path, err := h.service.Media(r.Context(), id, publishedOnly)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		h.logger.Error("open listening media", "error", err, "media_id", id)
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "Media is unavailable", nil)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", media.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, media.OriginalName, media.CreatedAt, file)
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, target); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return false
	}
	return true
}
func (h *Handler) id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "testID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Test ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}
func (h *Handler) writeSave(w http.ResponseWriter, r *http.Request, status int, item Test, details map[string]string, err error) {
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, item)
}
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrMediaNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource was not found", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "SLUG_EXISTS", "Slug already exists", nil)
	case errors.Is(err, ErrRevisionConflict):
		httpx.WriteError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Reload and retry", nil)
	default:
		h.logger.Error("listening request failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
	}
}
