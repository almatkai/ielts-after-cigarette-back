package billing

import (
	"strings"
	"testing"
	"time"
)

func TestBCCStrictConfiguration(t *testing.T) {
	good := testBCCConfig()
	if err := good.Validate("development"); err != nil {
		t.Fatal(err)
	}
	production := good
	production.Gateway = BCCProductionGateway
	production.Terminal = "12345678"
	production.MACKey = strings.Repeat("cd", 16)
	if err := production.Validate("production"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BCCConfig){
		"gateway":               func(c *BCCConfig) { c.Gateway = "https://evil.example/webview" },
		"insecure gateway":      func(c *BCCConfig) { c.Gateway = "http://3dsecure.bcc.kz/webview" },
		"terminal":              func(c *BCCConfig) { c.Terminal = "" },
		"merchant":              func(c *BCCConfig) { c.Merchant = "merchantname" },
		"lowercase name":        func(c *BCCConfig) { c.MerchantName = "Merchant" },
		"cyrillic name":         func(c *BCCConfig) { c.MerchantName = "ТОРГОВЕЦ" },
		"long name":             func(c *BCCConfig) { c.MerchantName = strings.Repeat("A", 22) },
		"mac missing":           func(c *BCCConfig) { c.MACKey = "" },
		"mac hex":               func(c *BCCConfig) { c.MACKey = strings.Repeat("Z", 32) },
		"mac length":            func(c *BCCConfig) { c.MACKey = "ab" },
		"mac zero":              func(c *BCCConfig) { c.MACKey = strings.Repeat("00", 16) },
		"bridge missing":        func(c *BCCConfig) { c.BridgeKey = "" },
		"bridge zero":           func(c *BCCConfig) { c.BridgeKey = strings.Repeat("00", 32) },
		"bridge short":          func(c *BCCConfig) { c.BridgeKey = strings.Repeat("ab", 16) },
		"auth user":             func(c *BCCConfig) { c.NotifyUser = "" },
		"auth user colon":       func(c *BCCConfig) { c.NotifyUser = "a:b" },
		"auth password":         func(c *BCCConfig) { c.NotifyPassword = "" },
		"auth password newline": func(c *BCCConfig) { c.NotifyPassword = "long-long-password\n" },
		"notify port":           func(c *BCCConfig) { c.NotifyURL = "https://pay.example/api/v1/billing/callbacks/bcc" },
		"notify credentials":    func(c *BCCConfig) { c.NotifyURL = "https://u:p@pay.example:443/api/v1/billing/callbacks/bcc" },
		"notify wrong provider": func(c *BCCConfig) { c.NotifyURL = "https://pay.example:443/api/v1/billing/callbacks/fake" },
		"notify query":          func(c *BCCConfig) { c.NotifyURL += "?token=leak" },
		"base insecure":         func(c *BCCConfig) { c.PublicBaseURL = "http://pay.example" },
		"base path":             func(c *BCCConfig) { c.PublicBaseURL = "https://pay.example/other" },
		"base credentials":      func(c *BCCConfig) { c.PublicBaseURL = "https://u:p@pay.example" },
		"base fragment":         func(c *BCCConfig) { c.PublicBaseURL = "https://pay.example/#bad" },
		"ttl low":               func(c *BCCConfig) { c.BridgeTTL = time.Second },
		"ttl high":              func(c *BCCConfig) { c.BridgeTTL = time.Hour },
		"language":              func(c *BCCConfig) { c.Language = "" },
		"proxy":                 func(c *BCCConfig) { c.TrustedProxyCIDRs = []string{"private"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := good
			mutate(&c)
			if c.Validate("development") == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*BCCConfig){"test gateway": func(c *BCCConfig) { c.Gateway = BCCTestGateway }, "signed test TID": func(c *BCCConfig) { c.Terminal = "88888881" }, "unsigned test TID": func(c *BCCConfig) { c.Terminal = "88888888" }, "public MAC": func(c *BCCConfig) { c.MACKey = strings.ToLower(good.MACKey) }} {
		t.Run("production "+name, func(t *testing.T) {
			c := production
			mutate(&c)
			if c.Validate("production") == nil {
				t.Fatal("test configuration in production")
			}
		})
	}
}
