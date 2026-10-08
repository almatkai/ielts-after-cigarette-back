package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request-id"

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err == nil {
				requestID = hex.EncodeToString(bytes[:])
			} else {
				requestID = time.Now().UTC().Format("20060102150405.000000000")
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)
			if recorder.status == 0 {
				recorder.status = http.StatusOK
			}
			logger.InfoContext(r.Context(), "http request",
				"request_id", RequestID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		})
	}
}

func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					stack := string(debug.Stack())
					logger.ErrorContext(r.Context(), "panic recovered",
						"request_id", RequestID(r.Context()),
						"panic", recovered,
						"stack", stack,
					)
					if error, ok := recovered.(error); ok {
						reportError(r.Context(), ErrorEvent{Error: error, RequestID: RequestID(r.Context()), Method: r.Method, Path: r.URL.Path, Panic: recovered, Origin: "http"})
					} else {
						reportError(r.Context(), ErrorEvent{RequestID: RequestID(r.Context()), Method: r.Method, Path: r.URL.Path, Panic: recovered, Origin: "http"})
					}
					WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// FormPostOrigin permits an identity provider's navigation POST only at its
// credential endpoint. That endpoint must validate its own CSRF token.
// Safari sends "Origin: null" when the POST follows a cross-site redirect
// chain, as Google's redirect sign-in does on iPhone, so null is accepted too.
type FormPostOrigin struct {
	Path   string
	Origin string
}

func CORS(origins []string, formPosts ...FormPostOrigin) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			for _, formPost := range formPosts {
				if r.Method == http.MethodPost && r.URL.Path == formPost.Path && (origin == formPost.Origin || origin == "null") && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
					next.ServeHTTP(w, r)
					return
				}
			}
			if origin != "" {
				if _, ok := allowed[origin]; !ok {
					WriteError(w, r, http.StatusForbidden, "CORS_ORIGIN_DENIED", "Origin is not allowed", nil)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, ETag")
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
