package attempts

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

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

func NewHandler(service *Service, logger *slog.Logger, maxBody int64) *Handler {
	return &Handler{service: service, logger: logger, maxBody: maxBody}
}

func (h *Handler) WithSpeakingMedia(maxMedia int64) *Handler {
	h.maxMedia = maxMedia
	return h
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MaterialListening, "testID", "Test ID", "test")
}

func (h *Handler) StartReading(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MaterialReading, "materialID", "Material ID", "material")
}

func (h *Handler) StartWriting(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MaterialWriting, "materialID", "Material ID", "material")
}

func (h *Handler) StartSpeaking(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MaterialSpeaking, "materialID", "Material ID", "material")
}

func (h *Handler) UploadSpeakingRecording(w http.ResponseWriter, r *http.Request) {
	maxMedia := h.maxMedia
	if maxMedia < 1 {
		maxMedia = defaultMaxSpeakingAssessmentAudioBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMedia+(1<<20))
	if err := r.ParseMultipartForm(maxMedia); err != nil {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Recording upload is too large", nil)
		return
	}
	attemptID, ok := h.id(w, r)
	if !ok {
		return
	}
	partID, err := uuid.Parse(strings.TrimSpace(r.FormValue("partId")))
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"partId": "must be a valid UUID"})
		return
	}
	file, header, err := r.FormFile("recording")
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"recording": "audio file is required"})
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > maxMedia {
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "Recording upload is too large", nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	recording, err := h.service.StoreSpeakingRecording(r.Context(), actor, attemptID, partID, header, file)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, recording)
}

func (h *Handler) SpeakingRecording(w http.ResponseWriter, r *http.Request) {
	attemptID, ok := h.id(w, r)
	if !ok {
		return
	}
	partID, err := uuid.Parse(chi.URLParam(r, "partID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Part ID must be UUID", nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	recording, path, err := h.service.SpeakingRecording(r.Context(), actor, attemptID, partID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "open speaking recording", "request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "Recording is unavailable", nil)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", recording.MimeType)
	w.Header().Set("Content-Disposition", "inline; filename=\"recording\"")
	http.ServeContent(w, r, recording.OriginalName, recording.UpdatedAt, file)
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request, materialType, param, paramName, responseKey string) {
	materialID, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", paramName+" must be UUID", nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	attempt, material, created, err := h.service.Start(r.Context(), actor, materialType, materialID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"attempt": attempt, responseKey: material})
}

func (h *Handler) SaveAnswers(w http.ResponseWriter, r *http.Request) {
	attemptID, ok := h.id(w, r)
	if !ok {
		return
	}
	input, ok := h.decodeAnswers(w, r)
	if !ok {
		return
	}
	actor, _ := auth.UserID(r.Context())
	if err := h.service.SaveAnswers(r.Context(), actor, attemptID, input); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"saved": len(input.Answers)})
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	attemptID, ok := h.id(w, r)
	if !ok {
		return
	}
	input, ok := h.decodeAnswers(w, r)
	if !ok {
		return
	}
	actor, _ := auth.UserID(r.Context())
	attempt, err := h.service.Submit(r.Context(), actor, attemptID, input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, attempt)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	materialType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("materialType")))
	if materialType != "" && materialType != MaterialListening && materialType != MaterialReading && materialType != MaterialWriting && materialType != MaterialSpeaking {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed",
			map[string]string{"materialType": "must be listening, reading, writing, or speaking"})
		return
	}
	actor, _ := auth.UserID(r.Context())
	items, err := h.service.List(r.Context(), actor, materialType)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	attemptID, ok := h.id(w, r)
	if !ok {
		return
	}
	actor, _ := auth.UserID(r.Context())
	detail, err := h.service.Get(r.Context(), actor, attemptID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handler) decodeAnswers(w http.ResponseWriter, r *http.Request) (SaveAnswersInput, bool) {
	var input SaveAnswersInput
	if err := httpx.DecodeJSON(w, r, h.maxBody, &input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return SaveAnswersInput{}, false
	}
	if input.Answers == nil {
		input.Answers = []AnswerInput{}
	}
	details := map[string]string{}
	for i, item := range input.Answers {
		if item.QuestionID == uuid.Nil {
			details["answers["+strconv.Itoa(i)+"].questionId"] = "must be a valid UUID"
		}
		if item.Answer == nil {
			input.Answers[i].Answer = map[string]any{}
		}
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return SaveAnswersInput{}, false
	}
	return input, true
}

func (h *Handler) id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "attemptID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Attempt ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Attempt was not found", nil)
	case errors.Is(err, ErrMaterialNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Material was not found or is not published", nil)
	case errors.Is(err, ErrUnsupportedMaterial):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed",
			map[string]string{"materialType": "must be listening, reading, writing, or speaking"})
	case errors.Is(err, ErrWritingIncomplete):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "WRITING_INCOMPLETE", "Both Writing tasks need an answer before submission", nil)
	case errors.Is(err, ErrSpeakingIncomplete):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "SPEAKING_INCOMPLETE", "Every Speaking part needs a recording or transcript before submission", nil)
	case errors.Is(err, ErrRecordingNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "SPEAKING_RECORDING_NOT_FOUND", "Speaking recording was not found", nil)
	case errors.Is(err, ErrRecordingTooLarge):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "SPEAKING_RECORDING_TOO_LARGE", "Speaking recording is too large for AI assessment", nil)
	case errors.Is(err, ErrAIUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "AI_NOT_CONFIGURED", "AI assessment is not configured", nil)
	case errors.Is(err, ErrAIEvaluationFailed):
		httpx.WriteError(w, r, http.StatusBadGateway, "AI_EVALUATION_FAILED", "AI assessment could not be completed", nil)
	case errors.Is(err, ErrAlreadySubmitted):
		httpx.WriteError(w, r, http.StatusConflict, "ATTEMPT_ALREADY_SUBMITTED", "Attempt was already submitted", nil)
	default:
		h.logger.ErrorContext(r.Context(), "attempts request failed",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
	}
}
