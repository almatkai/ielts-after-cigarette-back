package blog

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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

func (h *Handler) ListPublished(w http.ResponseWriter, r *http.Request) {
	if hidePublicBlog(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := h.service.ListPublished(r.Context(), limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "postID")
	if !ok {
		return
	}
	post, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	if hidePublicBlog(w, r) {
		return
	}
	slug := chi.URLParam(r, "slug")
	post, err := h.service.GetPublic(r.Context(), slug)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.UserID(r.Context())
	items, err := h.service.ListMine(r.Context(), actor)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	post, details, err := h.service.Create(r.Context(), actor, input)
	if h.writeValidation(w, r, details) {
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, post)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "postID")
	if !ok {
		return
	}
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	role := auth.Role(r.Context())
	actorUUID := actor
	post, details, err := h.service.Update(r.Context(), id, actorUUID, uuid.Nil, role, input)
	if h.writeValidation(w, r, details) {
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "postID")
	if !ok {
		return
	}
	var input PublishInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	role := auth.Role(r.Context())
	actorUUID := actor
	post, details, err := h.service.Publish(r.Context(), id, actorUUID, role, input)
	if h.writeValidation(w, r, details) {
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "postID")
	if !ok {
		return
	}
	actor, _ := auth.UserID(r.Context())
	role := auth.Role(r.Context())
	actorUUID := actor
	post, err := h.service.Archive(r.Context(), id, actorUUID, role)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxMedia+(1<<20))
	if err := r.ParseMultipartForm(h.maxMedia); err != nil {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Blog media is too large", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "FILE_REQUIRED", "Multipart field file is required", nil)
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > h.maxMedia {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Blog media is too large", nil)
		return
	}
	uploaderID, _ := auth.UserID(r.Context())
	media, err := h.service.StoreMedia(r.Context(), uploaderID, header, file)
	if err != nil {
		if errors.Is(err, ErrUnsupportedMedia) {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "MEDIA_INVALID", err.Error(), nil)
			return
		}
		h.logger.ErrorContext(r.Context(), "blog media upload failed",
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
	media, object, err := h.service.Media(r.Context(), id, role != auth.RoleEditor && role != auth.RoleAdmin && role != auth.RoleWriter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", media.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, media.OriginalName, media.CreatedAt, object)
}

func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) {
	var input ApplyInput
	if !h.decode(w, r, &input) {
		return
	}
	userID, _ := auth.UserID(r.Context())
	application, details, err := h.service.Apply(r.Context(), userID, input, input.CertificateMediaID)
	if h.writeValidation(w, r, details) {
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, application)
}

func (h *Handler) MyApplication(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	application, found, err := h.service.MyApplication(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": application, "exists": found})
}

func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListApplications(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) GetApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "applicationID")
	if !ok {
		return
	}
	application, err := h.service.GetApplication(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, application)
}

func (h *Handler) ApproveApplication(w http.ResponseWriter, r *http.Request) {
	h.review(w, r, true)
}

func (h *Handler) RejectApplication(w http.ResponseWriter, r *http.Request) {
	h.review(w, r, false)
}

func (h *Handler) review(w http.ResponseWriter, r *http.Request, approve bool) {
	id, ok := h.parseID(w, r, "applicationID")
	if !ok {
		return
	}
	var input ReviewInput
	if !h.decode(w, r, &input) {
		return
	}
	reviewerID, _ := auth.UserID(r.Context())
	application, details, err := h.service.ReviewApplication(r.Context(), id, reviewerID, approve, input.Notes)
	if h.writeValidation(w, r, details) {
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, application)
}

func (h *Handler) Certificate(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r, "applicationID")
	if !ok {
		return
	}
	requesterID, _ := auth.UserID(r.Context())
	role := auth.Role(r.Context())
	isAdmin := role == auth.RoleAdmin || role == auth.RoleEditor
	certificate, err := h.service.Certificate(r.Context(), id, requesterID, isAdmin)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	certificateMedia := Media{
		ID: certificate.MediaID, OriginalName: "certificate",
		MimeType: certificate.MimeType, StorageKey: certificate.StorageKey,
	}
	object, err := h.service.OpenMedia(r.Context(), certificateMedia)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", certificate.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, "certificate", time.Time{}, object)
}

func (h *Handler) parseID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, target); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return false
	}
	return true
}

func (h *Handler) writeValidation(w http.ResponseWriter, r *http.Request, details map[string]string) bool {
	if len(details) == 0 {
		return false
	}
	httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
	return true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrApplicationNotFound), errors.Is(err, ErrMediaNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "BLOG_NOT_FOUND", "Resource was not found", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "BLOG_SLUG_EXISTS", "Blog post slug already exists", nil)
	case errors.Is(err, ErrNotAuthor):
		httpx.WriteError(w, r, http.StatusForbidden, "NOT_AUTHOR", "You are not the author of this post", nil)
	case errors.Is(err, ErrAlreadyPublished):
		httpx.WriteError(w, r, http.StatusConflict, "ALREADY_PUBLISHED", "Published posts cannot be edited; archive instead", nil)
	case errors.Is(err, ErrApplicationExists):
		httpx.WriteError(w, r, http.StatusConflict, "APPLICATION_EXISTS", "A writer application is already pending for this account", nil)
	case errors.Is(err, ErrWriterRoleHeld):
		httpx.WriteError(w, r, http.StatusConflict, "WRITER_ROLE_HELD", "This account already has the writer role", nil)
	case errors.Is(err, ErrBandTooLow):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "BAND_TOO_LOW", "Overall band must be at least 7.5", nil)
	default:
		if httpx.ClientGone(w, r, err) {
			return
		}
		h.logger.ErrorContext(r.Context(), "blog request failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong", nil)
	}
}

// PublicBlogEnabled controls the editorial launch; preparation stays available to authors.
const PublicBlogEnabled = false

func hidePublicBlog(w http.ResponseWriter, r *http.Request) bool {
	if PublicBlogEnabled || auth.Role(r.Context()) == auth.RoleWriter || auth.Role(r.Context()) == auth.RoleEditor || auth.Role(r.Context()) == auth.RoleAdmin {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Страница не найдена", nil)
	return true
}
