package httpx

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// RevalidateMedia is called only after current access to published media was
// checked. A UUID names immutable bytes. Revalidation on every use preserves
// archive/logout access checks, unlike a day-long fresh private cache.
func RevalidateMedia(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	etag := `"` + id.String() + `"`
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}
