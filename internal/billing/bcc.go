package billing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // BCC's documented TRTYPE=1 MAC algorithm, not a password hash.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// bccClockSkew tolerates a small database/app clock difference. Order.CreatedAt
// is written by the database clock, so a just-created row can appear "future".
const bccClockSkew = 2 * time.Minute

type bccRepository interface {
	Order(context.Context, uuid.UUID) (Order, error)
	OrderByProvider(context.Context, string, string) (Order, error)
	ReserveBCCOrder(context.Context, uuid.UUID) (string, error)
}

type BCCProvider struct {
	cfg               BCCConfig
	repo              bccRepository
	macKey, bridgeKey []byte
	returnURL         string
	now               func() time.Time
	proxies           []*net.IPNet
}

func NewBCCProvider(c BCCConfig, repo bccRepository, returnURL, environment string) (*BCCProvider, error) {
	if err := c.Validate(environment); err != nil {
		return nil, err
	}
	u, err := url.Parse(returnURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || repo == nil {
		return nil, ErrValidation
	}
	// Browser Origin serialization lowercases hostnames and omits default :443.
	// Canonicalize the configured origin before emitting URLs/checking Origin.
	base, _ := url.Parse(c.PublicBaseURL)
	base.Host = strings.ToLower(base.Host)
	if base.Port() == "443" {
		base.Host = base.Hostname()
		if strings.Contains(base.Host, ":") {
			base.Host = "[" + base.Host + "]"
		}
	}
	base.Path = ""
	c.PublicBaseURL = base.String()
	mac, _ := hex.DecodeString(c.MACKey)
	bridge, _ := hex.DecodeString(c.BridgeKey)
	p := &BCCProvider{cfg: c, repo: repo, macKey: mac, bridgeKey: bridge, returnURL: returnURL, now: time.Now}
	for _, cidr := range c.TrustedProxyCIDRs {
		_, network, _ := net.ParseCIDR(cidr)
		p.proxies = append(p.proxies, network)
	}
	return p, nil
}
func (p *BCCProvider) Name() string { return "bcc" }

// Whole KZT are the existing billing storage unit, NOT minor units. Never float.
func bccAmount(amount int64) (string, error) {
	if amount <= 0 {
		return "", ErrValidation
	}
	return strconv.FormatInt(amount, 10) + ".00", nil
}
func bccParseAmount(s string) (int64, error) {
	parts := strings.Split(s, ".")
	if len(parts) > 2 || !bccDigits.MatchString(parts[0]) || len(parts[0]) > 19 {
		return 0, ErrInvalidCallback
	}
	if len(parts) == 2 && (len(parts[1]) < 1 || len(parts[1]) > 2 || strings.Trim(parts[1], "0") != "") {
		return 0, ErrInvalidCallback
	}
	n, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrInvalidCallback
	}
	return n, nil
}
func bccMAC(key []byte, fields ...string) string {
	mac := hmac.New(sha1.New, key)
	for _, s := range fields {
		io.WriteString(mac, strconv.Itoa(len(s)))
		io.WriteString(mac, s)
	}
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}
func bccNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(b)), nil
}
func (p *BCCProvider) capability(o Order, bankID string) string {
	expiry := strconv.FormatInt(o.CreatedAt.Add(p.cfg.BridgeTTL).Unix(), 10)
	body := o.ID.String() + "." + expiry
	mac := hmac.New(sha256.New, p.bridgeKey)
	io.WriteString(mac, "bcc-bridge-v1|"+body+"|"+o.UserID.String()+"|"+bankID+"|"+strconv.FormatInt(o.Amount, 10))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (p *BCCProvider) CreatePayment(ctx context.Context, o Order, _ string) (string, string, error) {
	if _, err := bccAmount(o.Amount); err != nil {
		return "", "", err
	}
	// BCC deduplicates the last six digits, not just the full ORDER. Reserve
	// that suffix and the complete mapping together before exposing the bridge.
	bankID, err := p.repo.ReserveBCCOrder(ctx, o.ID)
	if err != nil {
		return "", "", err
	}
	token := p.capability(o, bankID)
	return strings.TrimRight(p.cfg.PublicBaseURL, "/") + "/api/v1/billing/bcc/bridge?token=" + url.QueryEscape(token), bankID, nil
}
func (p *BCCProvider) bridgeOrder(ctx context.Context, token string) (Order, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 200 {
		return Order{}, ErrValidation
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return Order{}, ErrValidation
	}
	o, err := p.repo.Order(ctx, id)
	if err != nil {
		return Order{}, err
	}
	now := p.now()
	if o.Provider != "bcc" || o.Status != "PENDING" || o.ProviderOrderID == nil || !bccBankOrder.MatchString(*o.ProviderOrderID) || !now.Before(o.CreatedAt.Add(p.cfg.BridgeTTL)) || o.CreatedAt.After(now.Add(bccClockSkew)) {
		return Order{}, ErrValidation
	}
	if !hmac.Equal([]byte(token), []byte(p.capability(o, *o.ProviderOrderID))) {
		return Order{}, ErrValidation
	}
	return o, nil
}

// readBCCForm does not merge query parameters into the authenticated body.
// Limit even when used outside Handler.Callback; reject ambiguity in every field.
func readBCCForm(r *http.Request) (url.Values, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || r.Method != http.MethodPost || r.URL.RawQuery != "" {
		return nil, ErrInvalidCallback
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 32769))
	if err != nil || len(b) > 32768 {
		return nil, ErrInvalidCallback
	}
	v, err := url.ParseQuery(string(b))
	if err != nil {
		return nil, ErrInvalidCallback
	}
	for _, values := range v {
		if len(values) != 1 {
			return nil, ErrInvalidCallback
		}
	}
	return v, nil
}
func constantCredential(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

// bccCallbackStatus maps the authenticated result fields to a local order
// status. Only the documented full approval (ACTION=0, RC=00) is PAID; any
// other definitive bank result is a non-success. Missing or malformed result
// fields are not a valid notification, so they never settle an order.
func bccCallbackStatus(action, rc string) (string, error) {
	if len(action) < 1 || len(action) > 2 || !bccDigits.MatchString(action) || len(rc) != 2 {
		return "", ErrInvalidCallback
	}
	for _, c := range rc {
		if !('0' <= c && c <= '9') && !('A' <= c && c <= 'Z') && !('a' <= c && c <= 'z') {
			return "", ErrInvalidCallback
		}
	}
	if action == "0" && rc == "00" {
		return "PAID", nil
	}
	return "FAILED", nil
}

// bccNotifyCredentials reads the notification credentials. The standard form is
// "Basic <base64(user:password)>", but BCC's notification documentation shows
// the header as the bare base64 value without the scheme, so both are accepted.
// Either way the same configured user and password must match.
func bccNotifyCredentials(r *http.Request) (string, string, bool) {
	if user, pass, ok := r.BasicAuth(); ok {
		return user, pass, true
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" || strings.ContainsAny(raw, " \t") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(decoded), ":")
}
func (p *BCCProvider) ParseCallback(r *http.Request) (Notification, error) {
	user, pass, ok := bccNotifyCredentials(r)
	userOK := constantCredential(user, p.cfg.NotifyUser)
	passOK := constantCredential(pass, p.cfg.NotifyPassword)
	if !ok || p.cfg.NotifyUser == "" || p.cfg.NotifyPassword == "" || !userOK || !passOK || len(r.Header.Values("Authorization")) != 1 {
		return Notification{}, ErrInvalidCallback
	}
	v, err := readBCCForm(r)
	if err != nil {
		return Notification{}, err
	}
	// The request P_SIGN does NOT authenticate ACTION/RC. Only bank-configured
	// HTTPS Basic Auth authenticates this result. Never treat browser data as it.
	if v.Get("MERCHANT") != p.cfg.Merchant || v.Get("TERMINAL") != p.cfg.Terminal || v.Get("CURRENCY") != "398" || v.Get("TRTYPE") != "1" || !bccBankOrder.MatchString(v.Get("ORDER")) || v.Get("MERCH_RN_ID") != bccMerchRNID(p.cfg.Terminal, v.Get("ORDER")) {
		return Notification{}, ErrInvalidCallback
	}
	action, rc := v.Get("ACTION"), v.Get("RC")
	status, err := bccCallbackStatus(action, rc)
	if err != nil {
		return Notification{}, err
	}
	amount, err := bccParseAmount(v.Get("AMOUNT"))
	if err != nil {
		return Notification{}, err
	}
	o, err := p.repo.OrderByProvider(r.Context(), "bcc", v.Get("ORDER"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Notification{}, ErrInvalidCallback
		}
		return Notification{}, fmt.Errorf("lookup BCC order: %w", err)
	}
	// OrderByProvider already filters by provider and provider ORDER; the
	// remaining integrity check is the immutable amount/currency.
	if o.Amount != amount || o.Currency != "KZT" {
		return Notification{}, ErrInvalidCallback
	}
	// Whitelist persisted metadata: no card/token/phone/address/full callback body.
	return Notification{OrderID: o.ID, ProviderOrderID: v.Get("ORDER"), Amount: amount, Currency: "KZT", Status: status, Payload: map[string]any{"ACTION": action, "RC": rc, "TRTYPE": "1"}}, nil
}

type bccInfo struct {
	Height string `json:"browserScreenHeight"`
	Width  string `json:"browserScreenWidth"`
	Phone  struct {
		CC         string `json:"cc"`
		Subscriber string `json:"subscriber"`
	} `json:"mobilePhone"`
	Address string `json:"billAddrLine1"`
}

func (info bccInfo) validate() error {
	for _, s := range []string{info.Height, info.Width} {
		if !bccDigits.MatchString(s) || len(s) > 6 || strings.Trim(s, "0") == "" {
			return ErrValidation
		}
	}
	if !bccDigits.MatchString(info.Phone.CC) || len(info.Phone.CC) > 3 || info.Phone.CC[0] == '0' || !bccDigits.MatchString(info.Phone.Subscriber) || len(info.Phone.Subscriber) > 15 || len(info.Phone.CC)+len(info.Phone.Subscriber) > 15 {
		return ErrValidation
	}
	if !utf8.ValidString(info.Address) || strings.TrimSpace(info.Address) == "" || utf8.RuneCountInString(info.Address) > 50 {
		return ErrValidation
	}
	for _, c := range info.Address {
		if unicode.IsControl(c) {
			return ErrValidation
		}
	}
	return nil
}

// bccMerchRNID is BCC field 129: a merchant-generated, unique, 16-character
// alphanumeric reference, distinct from ORDER (the bank's sample uses random
// uppercase hex). Deriving it from TERMINAL+ORDER keeps it unique per order
// without a schema change and lets the notification be checked against it.
func bccMerchRNID(terminal, order string) string {
	sum := sha256.Sum256([]byte("bcc-merch-rn-id\x00" + terminal + "\x00" + order))
	return strings.ToUpper(hex.EncodeToString(sum[:8]))
}
func (p *BCCProvider) fields(o Order, info bccInfo, ip string) (map[string]string, error) {
	if err := info.validate(); err != nil {
		return nil, err
	}
	client := net.ParseIP(ip)
	if client == nil || client.IsUnspecified() || client.IsMulticast() {
		return nil, ErrValidation
	}
	if o.ProviderOrderID == nil || !bccBankOrder.MatchString(*o.ProviderOrderID) {
		return nil, ErrValidation
	}
	amount, err := bccAmount(o.Amount)
	if err != nil {
		return nil, err
	}
	nonce, err := bccNonce()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	f := map[string]string{"AMOUNT": amount, "CURRENCY": "398", "ORDER": *o.ProviderOrderID, "MERCH_RN_ID": bccMerchRNID(p.cfg.Terminal, *o.ProviderOrderID), "DESC": "IELTS study access", "MERCHANT": p.cfg.Merchant, "MERCH_NAME": p.cfg.MerchantName, "TERMINAL": p.cfg.Terminal, "TIMESTAMP": p.now().UTC().Format("20060102150405"), "MERCH_GMT": "0", "TRTYPE": "1", "LANG": p.cfg.Language, "NONCE": nonce, "NOTIFY_URL": p.cfg.NotifyURL, "CLIENT_IP": client.String(), "M_INFO": base64.StdEncoding.EncodeToString(data), "BACKREF": strings.TrimRight(p.cfg.PublicBaseURL, "/") + "/api/v1/billing/bcc/return/" + o.ID.String()}
	f["P_SIGN"] = bccMAC(p.macKey, f["AMOUNT"], f["CURRENCY"], f["ORDER"], f["MERCHANT"], f["TERMINAL"], f["MERCH_GMT"], f["TIMESTAMP"], f["TRTYPE"], f["NONCE"])
	return f, nil
}

// Explicit proxy allowlist, traversed right-to-left. Unlike the rate-limit
// legacy resolver, never trust all private peers or the leftmost arbitrary XFF.
func (p *BCCProvider) clientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	trusted := func(s string) bool {
		ip := net.ParseIP(strings.TrimSpace(s))
		for _, n := range p.proxies {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer
	}
	// Header.Get reads only the first line. A trusted edge proxy that appends
	// the real client as a separate header line must not lose to a forged first
	// line, so join every line in wire order before walking right-to-left.
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if strings.TrimSpace(xff) == "" {
		return peer
	}
	hops := strings.Split(xff, ",")
	if len(hops) > 20 {
		return ""
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !trusted(peer) {
			break
		}
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			// Empty list entries (e.g. a trailing comma) are not addresses.
			continue
		}
		peer = hop
		if net.ParseIP(peer) == nil {
			return ""
		}
	}
	return peer
}
