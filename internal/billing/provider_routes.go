package billing

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Public payment-provider routes, relative to the API mount (e.g. /api/v1).
// They are registered and recognised from these constants only, so a rename
// cannot silently drop the CORS bypass for one of them.
const (
	callbackPrefix  = "/billing/callbacks/"
	bccBridgePath   = "/billing/bcc/bridge"
	bccReturnPrefix = "/billing/bcc/return/"
)

// RegisterProviderRoutes mounts every route that a payment provider or the
// bank's browser redirect reaches without a frontend Origin.
func (h *Handler) RegisterProviderRoutes(r chi.Router) {
	r.Post(callbackPrefix+"{provider}", h.Callback)
	h.RegisterBCCRoutes(r)
}

// RegisterBCCRoutes keeps the public bridge/return contract shared by app and
// httptest. Authentication for the bridge is the short-lived order capability.
func (h *Handler) RegisterBCCRoutes(r chi.Router) {
	r.Get(bccBridgePath, h.BCCBridge)
	r.Post(bccBridgePath, h.BCCBridge)
	r.Get(bccReturnPrefix+"{orderID}", h.BCCBackref)
	r.Post(bccReturnPrefix+"{orderID}", h.BCCBackref)
}

// SelfAuthenticatingRoute reports whether r targets a provider route mounted
// under mount that authenticates itself and must therefore skip the
// frontend-origin CORS allowlist: provider callbacks (Basic Auth/provider
// checks), the BCC bridge POST (capability + same-origin check) and BCC
// BACKREF (navigation only, never settles an order).
func SelfAuthenticatingRoute(r *http.Request, mount string) bool {
	path, ok := strings.CutPrefix(r.URL.Path, mount)
	if !ok {
		return false
	}
	switch {
	case r.Method == http.MethodPost && path == bccBridgePath:
		return true
	case r.Method == http.MethodPost && singleSegment(path, callbackPrefix):
		return true
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && singleSegment(path, bccReturnPrefix):
		return true
	}
	return false
}

func singleSegment(path, prefix string) bool {
	rest, ok := strings.CutPrefix(path, prefix)
	return ok && rest != "" && !strings.Contains(rest, "/")
}

// returnTarget is the frontend return contract shared by every provider:
// the configured return URL, its existing query preserved, plus orderId.
func returnTarget(returnURL string, orderID uuid.UUID) (string, error) {
	u, err := url.Parse(returnURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("orderId", orderID.String())
	u.RawQuery = q.Encode()
	return u.String(), nil
}
