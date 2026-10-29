package billing

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// BCC should implement this boundary once the bank supplies its signed API
// contract. ParseCallback must authenticate the signature and return the
// provider's amount and currency; never trust a browser payment result.
type Provider interface {
	Name() string
	CreatePayment(context.Context, Order, string) (string, string, error)
	ParseCallback(*http.Request) (Notification, error)
}
type fakeProvider struct {
	notify func(context.Context, string, Notification) error
}

func NewFakeProvider(environment string, notify func(context.Context, string, Notification) error) (Provider, error) {
	if strings.EqualFold(strings.TrimSpace(environment), "production") {
		return nil, ErrProviderUnavailable
	}
	return &fakeProvider{notify: notify}, nil
}
func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) CreatePayment(ctx context.Context, o Order, returnURL string) (string, string, error) {
	u, err := url.Parse(returnURL)
	if err != nil {
		return "", "", err
	}
	providerID := "fake-" + o.ID.String()
	if err = p.notify(ctx, p.Name(), Notification{OrderID: o.ID, ProviderOrderID: providerID, Status: "PAID", Amount: o.Amount, Currency: "KZT"}); err != nil {
		return "", "", err
	}
	q := u.Query()
	q.Set("orderId", o.ID.String())
	u.RawQuery = q.Encode()
	return u.String(), providerID, nil
}

// Fake payments settle internally. There is intentionally no unsigned public
// callback that could grant an entitlement.
func (p *fakeProvider) ParseCallback(*http.Request) (Notification, error) {
	return Notification{}, ErrInvalidCallback
}
