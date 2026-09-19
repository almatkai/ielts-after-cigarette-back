package writing

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service  *Service
	logger   *slog.Logger
	maxBody  int64
	maxMedia int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBody int64, mediaLimits ...int64) *Handler {
	maxMedia := int64(10 << 20)
	if len(mediaLimits) > 0 && mediaLimits[0] > 0 && mediaLimits[0] < maxMedia {
		maxMedia = mediaLimits[0]
	}
	return &Handler{service: service, logger: logger, maxBody: maxBody, maxMedia: maxMedia}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	material, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
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
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	material, err := h.service.GetPublic(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Create(r.Context(), actor, input)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, material)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Update(r.Context(), id, actor, input)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input PublishInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Publish(r.Context(), id, actor, input.Revision)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input PublishInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Archive(r.Context(), id, actor, input.Revision)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) ParseImport(w http.ResponseWriter, r *http.Request) {
	var input ImportParseInput
	if !h.decode(w, r, &input) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.service.ParseImport(input))
}

func (h *Handler) BulkImport(w http.ResponseWriter, r *http.Request) {
	var input BulkCreateInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	items, details, err := h.service.BulkCreate(r.Context(), actor, input.Materials)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"items": items})
}

func (h *Handler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxMedia+(1<<20))
	if err := r.ParseMultipartForm(h.maxMedia); err != nil {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Writing visual is too large", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "FILE_REQUIRED", "Multipart field file is required", nil)
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > h.maxMedia {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Writing visual is too large", nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	media, err := h.service.StoreMedia(r.Context(), actor, header, file)
	if err != nil {
		if errors.Is(err, ErrUnsupportedMedia) {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "MEDIA_INVALID", err.Error(), nil)
			return
		}
		h.logger.ErrorContext(r.Context(), "writing media upload failed",
			"request_id", httpx.RequestID(r.Context()), "file_name", header.Filename,
			"file_size", header.Size, "error", err)
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "OBJECT_STORAGE_UNAVAILABLE", "Media storage is temporarily unavailable", nil)
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
	media, object, err := h.service.Media(r.Context(), id, publishedOnly)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", media.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, media.OriginalName, media.CreatedAt, object)
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, target); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return false
	}
	return true
}

func (h *Handler) materialID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Material ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrMediaNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "WRITING_MATERIAL_NOT_FOUND", "Writing material was not found", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "WRITING_SLUG_EXISTS", "Writing material slug already exists", nil)
	case errors.Is(err, ErrRevisionConflict):
		httpx.WriteError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Writing material was changed by another editor", nil)
	default:
		h.logger.ErrorContext(r.Context(), "writing request failed", "request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
	}
}
