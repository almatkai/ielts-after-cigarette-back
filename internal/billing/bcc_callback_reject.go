package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// callbackRejection explains why a provider notification was refused. It still
// matches ErrInvalidCallback (the HTTP response is unchanged); the extra detail
// only goes to the server log so a refused bank callback can be diagnosed.
//
// Attrs must never contain secrets: no credentials, MAC, card, phone or address
// values. Field values are logged only for the non-sensitive whitelist below.
type callbackRejection struct {
	reason string
	attrs  []slog.Attr
}

func (e *callbackRejection) Error() string { return "invalid payment callback: " + e.reason }
func (e *callbackRejection) Unwrap() error { return ErrInvalidCallback }

// bccLoggableFields are routing/result fields that are safe to log verbatim.
var bccLoggableFields = []string{"ORDER", "AMOUNT", "CURRENCY", "TRTYPE", "MERCHANT", "TERMINAL", "ACTION", "RC", "MERCH_RN_ID"}

func bccAuthScheme(r *http.Request) string {
	values := r.Header.Values("Authorization")
	switch {
	case len(values) == 0:
		return "missing"
	case len(values) > 1:
		return "multiple"
	}
	raw := strings.TrimSpace(values[0])
	switch {
	case raw == "":
		return "empty"
	case strings.HasPrefix(strings.ToLower(raw), "basic "):
		return "basic"
	case !strings.ContainsAny(raw, " \t"):
		return "bare"
	default:
		return "other"
	}
}

func bccRequestAttrs(r *http.Request) []slog.Attr {
	return []slog.Attr{
		slog.String("content_type", r.Header.Get("Content-Type")),
		slog.String("auth_scheme", bccAuthScheme(r)),
		slog.Bool("has_query", r.URL.RawQuery != ""),
		slog.Int64("content_length", r.ContentLength),
	}
}

func bccRejectRequest(r *http.Request, reason string, extra ...slog.Attr) error {
	return &callbackRejection{reason: reason, attrs: append(bccRequestAttrs(r), extra...)}
}

// bccFormAttrs lists every received field name and the whitelisted values.
func bccFormAttrs(v map[string][]string) []slog.Attr {
	names := make([]string, 0, len(v))
	for k := range v {
		names = append(names, k)
	}
	sort.Strings(names)
	attrs := []slog.Attr{slog.String("fields", strings.Join(names, ","))}
	for _, k := range bccLoggableFields {
		if values, ok := v[k]; ok && len(values) > 0 {
			attrs = append(attrs, slog.String("f_"+strings.ToLower(k), values[0]))
		}
	}
	return attrs
}

func (h *Handler) logCallbackRejection(r *http.Request, provider string, err error) {
	var rej *callbackRejection
	if !errors.As(err, &rej) {
		h.logger.WarnContext(r.Context(), "payment callback rejected", "provider", provider, "error", err.Error())
		return
	}
	args := []any{slog.String("provider", provider), slog.String("reason", rej.reason)}
	for _, a := range rej.attrs {
		args = append(args, a)
	}
	h.logger.WarnContext(r.Context(), "payment callback rejected", args...)
}
