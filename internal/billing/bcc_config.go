package billing

import (
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const BCCTestGateway = "https://test3ds.bcc.kz:5445/cgi-bin/cgi_link"
const BCCProductionGateway = "https://3dsecure.bcc.kz/webview"

// BCCConfig contains credentials only on the server. No defaults enable acquiring.
type BCCConfig struct {
	Gateway, Terminal, Merchant, MerchantName     string
	MACKey, NotifyURL, NotifyUser, NotifyPassword string
	PublicBaseURL, BridgeKey, Language            string
	BridgeTTL                                     time.Duration
	TrustedProxyCIDRs                             []string
}

var bccDigits = regexp.MustCompile(`^[0-9]+$`)
var bccBankOrder = regexp.MustCompile(`^[1-9][0-9]{15}$`)
var bccMerchantName = regexp.MustCompile(`^[A-Z][A-Z0-9 .&'-]{0,20}$`)

func (c BCCConfig) Validate(environment string) error {
	var problems []string
	prod := strings.EqualFold(strings.TrimSpace(environment), "production")
	if c.Gateway != BCCTestGateway && c.Gateway != BCCProductionGateway {
		problems = append(problems, "BCC_GATEWAY must be a documented BCC endpoint")
	}
	if !bccDigits.MatchString(c.Terminal) || len(c.Terminal) > 32 {
		problems = append(problems, "BCC_TERMINAL must be numeric (max 32)")
	}
	if !bccDigits.MatchString(c.Merchant) || len(c.Merchant) > 32 {
		problems = append(problems, "BCC_MERCHANT must be numeric (max 32)")
	}
	if !bccMerchantName.MatchString(c.MerchantName) {
		problems = append(problems, "BCC_MERCHANT_NAME must be uppercase Latin (max 21)")
	}
	key, err := hex.DecodeString(c.MACKey)
	if err != nil || len(key) != 16 {
		problems = append(problems, "BCC_MAC_KEY must be a 16-byte hex key")
	}
	bridge, err := hex.DecodeString(c.BridgeKey)
	if err != nil || len(bridge) != 32 {
		problems = append(problems, "BCC_BRIDGE_KEY must be an independent 32-byte hex key")
	}
	if len(bridge) > 0 && strings.Trim(string(bridge), "\x00") == "" {
		problems = append(problems, "BCC_BRIDGE_KEY must not be all zero")
	}
	if len(key) > 0 && strings.Trim(string(key), "\x00") == "" {
		problems = append(problems, "BCC_MAC_KEY must not be all zero")
	}
	if c.NotifyUser == "" || strings.ContainsAny(c.NotifyUser, ":\r\n") || len(c.NotifyPassword) < 16 || strings.ContainsAny(c.NotifyPassword, "\r\n") {
		problems = append(problems, "BCC notify Basic Auth requires a username and password (at least 16 characters)")
	}
	base, err := url.Parse(c.PublicBaseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		problems = append(problems, "BCC_PUBLIC_BASE_URL must be an HTTPS origin")
	}
	notify, err := url.Parse(c.NotifyURL)
	if err != nil || notify.Scheme != "https" || notify.Hostname() == "" || notify.Port() == "" || notify.User != nil || notify.RawQuery != "" || notify.Fragment != "" || notify.Path != "/api/v1/billing/callbacks/bcc" {
		problems = append(problems, "BCC_NOTIFY_URL must be HTTPS with an explicit port and /api/v1/billing/callbacks/bcc path")
	}
	if c.Language != "ru" && c.Language != "en" && c.Language != "kz" {
		problems = append(problems, "BCC_LANGUAGE must be ru, en or kz")
	}
	if c.BridgeTTL < time.Minute || c.BridgeTTL > 30*time.Minute {
		problems = append(problems, "BCC_BRIDGE_TTL must be between 1m and 30m")
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			problems = append(problems, "BCC_TRUSTED_PROXY_CIDRS contains an invalid CIDR")
		}
	}
	if prod && (c.Gateway != BCCProductionGateway || c.Terminal == "88888881" || c.Terminal == "88888888" || strings.EqualFold(c.MACKey, "6BB0AC02E47BDF73D98FEB777F3B5294")) {
		problems = append(problems, "production BCC forbids the test gateway, test TIDs and public test MAC key")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
