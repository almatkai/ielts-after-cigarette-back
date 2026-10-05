package analytics

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

// overviewTTL bounds how often the dashboard hits PostgreSQL: any number of
// admins refreshing at once share one batch of aggregate queries.
const overviewTTL = 30 * time.Second

type cachedOverview struct {
	value   Overview
	expires time.Time
}

type Handler struct {
	repository *Repository
	tracker    *Tracker
	logger     *slog.Logger
	maxBytes   int64

	mu    sync.Mutex
	cache map[int]cachedOverview
}

func NewHandler(repository *Repository, tracker *Tracker, logger *slog.Logger, maxBytes int64) *Handler {
	return &Handler{repository: repository, tracker: tracker, logger: logger, maxBytes: maxBytes, cache: map[int]cachedOverview{}}
}

var allowedDays = map[int]bool{7: true, 14: true, 30: true, 90: true, 180: true}

func parseDays(r *http.Request, fallback int) (int, bool) {
	raw := r.URL.Query().Get("days")
	if raw == "" {
		return fallback, true
	}
	days, err := strconv.Atoi(raw)
	return days, err == nil && allowedDays[days]
}

// Overview runs behind auth middleware that already enforced the ADMIN role.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	days, ok := parseDays(r, 30)
	if !ok {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"days": "must be one of 7, 14, 30, 90, 180"})
		return
	}
	now := time.Now()
	h.mu.Lock()
	cached, hit := h.cache[days]
	h.mu.Unlock()
	if hit && now.Before(cached.expires) {
		httpx.WriteJSON(w, http.StatusOK, cached.value)
		return
	}

	overview, err := h.repository.Overview(r.Context(), now, days)
	if err != nil {
		httpx.InternalError(w, r, h.logger.With("operation", "analytics overview"), err)
		return
	}
	dates := make([]time.Time, len(overview.Daily))
	for i, point := range overview.Daily {
		dates[i], _ = time.ParseInLocation(time.DateOnly, point.Day, location)
	}
	visitors, views := h.tracker.DailyVisitors(r.Context(), dates)
	for i := range overview.Daily {
		overview.Daily[i].Visitors = visitors[i]
		overview.Daily[i].PageViews = views[i]
	}

	h.mu.Lock()
	h.cache[days] = cachedOverview{value: overview, expires: now.Add(overviewTTL)}
	h.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, overview)
}

func (h *Handler) Realtime(w http.ResponseWriter, r *http.Request) {
	realtime, err := h.tracker.Realtime(r.Context())
	if err != nil {
		// Redis is optional for analytics: report "unavailable", not a 500.
		h.logger.WarnContext(r.Context(), "analytics realtime unavailable", "error", err)
		realtime.Available = false
	}
	httpx.WriteJSON(w, http.StatusOK, realtime)
}

type pingRequest struct {
	VisitorID string `json:"visitorId"`
	Path      string `json:"path"`
	View      bool   `json:"view"`
}

// Ping is the public browser heartbeat. It answers 204 even when Redis is
// down: a broken analytics store must never surface to visitors.
func (h *Handler) Ping(w http.ResponseWriter, r *http.Request) {
	var request pingRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	visitorID, err := uuid.Parse(request.VisitorID)
	if err != nil || visitorID == uuid.Nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"visitorId": "must be a UUID"})
		return
	}
	if userID, ok := auth.UserID(r.Context()); ok {
		h.tracker.TouchUser(userID)
	}
	if err := h.tracker.Ping(r.Context(), visitorID, request.Path, request.View); err != nil {
		h.logger.WarnContext(r.Context(), "analytics ping dropped", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Export streams a pseudonymous CSV dataset for offline analysis
// (pandas, DuckDB, ClickHouse, a warehouse loader…).
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	dataset := r.PathValue("dataset")
	if !ValidDataset(dataset) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Unknown dataset", nil)
		return
	}
	since := time.Unix(0, 0)
	if r.URL.Query().Get("days") != "" {
		days, ok := parseDays(r, 0)
		if !ok {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"days": "must be one of 7, 14, 30, 90, 180"})
			return
		}
		since = time.Now().AddDate(0, 0, -days)
	}
	filename := "iac-" + dataset + "-" + time.Now().In(location).Format("20060102-1504") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	out := &countingWriter{ResponseWriter: w}
	err := h.repository.Export(r.Context(), out, dataset, since)
	switch {
	case err == nil || r.Context().Err() == context.Canceled:
	case out.written == 0:
		w.Header().Del("Content-Disposition")
		httpx.InternalError(w, r, h.logger.With("operation", "analytics export"), err)
	default:
		// Headers are already sent; the truncated file is the signal.
		h.logger.ErrorContext(r.Context(), "analytics export failed mid-stream", "dataset", dataset, "error", err)
	}
}

type countingWriter struct {
	http.ResponseWriter
	written int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}
