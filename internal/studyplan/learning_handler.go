package studyplan

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h *Handler) learningRoutes(r chi.Router) {
	r.Get("/study-plan/learning", h.learningEndpoint("get"))
	r.Get("/study-plan/learning/inventory", h.learningEndpoint("inventory"))
	r.Get("/study-plan/learning/cycles/{id}", h.learningEndpoint("cycle"))
	r.Post("/study-plan/learning/cycles/{id}/view", h.learningEndpoint("view"))
	r.Post("/study-plan/learning/cycles/{id}/correction", h.learningEndpoint("correction"))
	r.Post("/study-plan/learning/cycles/{id}/skip", h.learningEndpoint("skip"))
	r.Post("/study-plan/learning/drills/{id}/start", h.learningEndpoint("start"))
	r.Get("/study-plan/learning/sessions/{id}", h.learningEndpoint("session"))
	r.Post("/study-plan/learning/sessions/{id}/submit", h.learningEndpoint("submit"))
}
func (h *Handler) learningEndpoint(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := h.actor(w, r)
		if !ok {
			return
		}
		lc := &learningCommand{Action: action, ID: chi.URLParam(r, "id")}
		if r.Method == http.MethodPost && !h.decode(w, r, &lc.Input) {
			return
		}
		out, err := h.service.Learning(r.Context(), user, lc)
		h.write(w, r, out, err)
	}
}
