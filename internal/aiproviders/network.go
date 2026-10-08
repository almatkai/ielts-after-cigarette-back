package aiproviders

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(raw) > 1000 {
		return ErrValidation
	}
	if u.Port() != "" && u.Port() != "443" {
		return ErrValidation
	}
	if u.Path == "" || u.Path == "/" {
		return ErrValidation
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return ErrValidation
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return ErrValidation
	}
	return nil
}

var reservedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range reservedNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, errors.New("invalid provider address")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, errors.New("provider DNS resolution failed")
	}
	if len(addresses) == 0 {
		return nil, errors.New("provider DNS resolution failed")
	}
	// Validate every address, then dial the checked IP, never resolve again.
	for _, a := range addresses {
		if !publicIP(a.IP) {
			return nil, errors.New("provider destination is not public")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for _, a := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("provider connection failed")
}
func newClient(protected bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Do not bypass checked DNS through an ambient proxy.
	transport.TLSHandshakeTimeout = 5 * time.Second
	if protected {
		transport.DialContext = safeDial
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
