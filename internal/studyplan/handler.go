package studyplan

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
	maxBody int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBody int64) *Handler {
	return &Handler{service: service, logger: logger, maxBody: maxBody}
}

// Routes is mounted inside the application's authenticated route group. The
// handler independently checks context identity to prevent accidental exposure.
func (h *Handler) Routes(r chi.Router) {
	h.learningRoutes(r)
	r.Get("/study-plan", h.Get)
	r.Post("/study-plan", h.Create)
	r.Put("/study-plan/settings", h.Settings)
	r.Post("/study-plan/replan", h.Replan)
	r.Post("/study-plan/assignments/{id}/start", h.Start)
	r.Post("/study-plan/assignments/{id}/skip", h.Skip)
	r.Post("/study-plan/assignments/{id}/review", h.Review)
	r.Post("/study-plan/weeks/{week}/mock", h.Mock)
}

type mutationInput struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Reason           string `json:"reason,omitempty"`
}

func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok || id == uuid.Nil {
		httpx.WriteError(w, r, 401, "UNAUTHENTICATED", "Для доступа к плану необходимо войти в аккаунт", nil)
		return uuid.Nil, false
	}
	return id, true
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, input any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, input); err != nil {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Некорректные параметры плана обучения", nil)
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request, value any, err error) {
	if err == nil {
		httpx.WriteJSON(w, 200, value)
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, 404, "NOT_FOUND", ErrNotFound.Error(), nil)
	case errors.Is(err, ErrConflict), errors.Is(err, attempts.ErrExamDeadlineExceeded), errors.Is(err, attempts.ErrSectionLocked):
		httpx.WriteError(w, r, 409, "CONFLICT", ErrConflict.Error(), nil)
	case errors.Is(err, ErrValidation):
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Выберите 30, 45, 60 или 90 минут и неповторяющиеся дни недели (1–7). Проверьте версию плана, действие, дату и окно Full Mock", nil)
	case errors.Is(err, attempts.ErrMaterialNotFound):
		httpx.WriteError(w, r, 409, "CONFLICT", "Материал больше недоступен; обновите страницу и перепланируйте будущие задания", nil)
	case errors.Is(err, attempts.ErrDailyLimitExceeded):
		httpx.WriteError(w, r, 429, "DAILY_LIMIT_EXCEEDED", "Достигнут дневной лимит практики с ИИ", nil)
	default:
		h.logger.Error("study plan request failed", "error", err)
		httpx.WriteError(w, r, 500, "INTERNAL_ERROR", "Не удалось загрузить или обновить план обучения", nil)
	}
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, e := h.service.Get(r.Context(), user)
	h.write(w, r, out, e)
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input Settings
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Create(r.Context(), user, input)
	h.write(w, r, out, e)
}
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		Settings
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Settings(r.Context(), user, input.Settings, input.ExpectedRevision)
	h.write(w, r, out, e)
}
func (h *Handler) Replan(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input mutationInput
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Replan(r.Context(), user, input.ExpectedRevision)
	h.write(w, r, out, e)
}
func (h *Handler) assignment(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, e := uuid.Parse(chi.URLParam(r, "id"))
	if e != nil {
		h.write(w, r, nil, ErrNotFound)
		return "", false
	}
	return id.String(), true
}
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.assignment(w, r)
	if !ok {
		return
	}
	var input mutationInput
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Start(r.Context(), user, id, input.ExpectedRevision)
	h.write(w, r, out, e)
}
func (h *Handler) Skip(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.assignment(w, r)
	if !ok {
		return
	}
	var input mutationInput
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Skip(r.Context(), user, id, input.ExpectedRevision, input.Reason)
	h.write(w, r, out, e)
}
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.assignment(w, r)
	if !ok {
		return
	}
	var input mutationInput
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Review(r.Context(), user, id, input.ExpectedRevision)
	h.write(w, r, out, e)
}
func (h *Handler) Mock(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	week, e := strconv.Atoi(chi.URLParam(r, "week"))
	if e != nil {
		h.write(w, r, nil, ErrValidation)
		return
	}
	var input struct {
		ExpectedRevision int64  `json:"expectedRevision"`
		Action           string `json:"action"`
		ScheduledDate    string `json:"scheduledDate,omitempty"`
		WindowMinutes    int    `json:"windowMinutes,omitempty"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	out, e := h.service.Mock(r.Context(), user, week, input.ExpectedRevision, input.Action, input.ScheduledDate, input.WindowMinutes)
	h.write(w, r, out, e)
}
