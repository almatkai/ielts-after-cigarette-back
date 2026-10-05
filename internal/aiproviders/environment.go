package aiproviders

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Never expose credentials embedded in a trusted environment URL.
func environmentDisplay(p Provider) Provider {
	if u, err := url.Parse(p.Endpoint); err == nil {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		p.Endpoint = u.String()
	} else {
		p.Endpoint = ""
	}
	p.APIKey = ""
	p.Ciphertext = nil
	return p
}

func (s *Service) environment(settings *Provider) Provider {
	p := s.env
	if settings != nil {
		p.Name = settings.Name
		p.Priority = settings.Priority
		p.Scopes = settings.Scopes
		p.Enabled = settings.Enabled
		p.TimeoutSeconds = settings.TimeoutSeconds
		p.Revision = settings.Revision
		p.UpdatedAt = settings.UpdatedAt
	}
	return p
}

// Both admin display and execution use the same order. The credential and
// endpoint of the env row always come from THIS process, not from PostgreSQL.
func (s *Service) providers(items []Provider) []Provider {
	result := make([]Provider, 0, len(items)+1)
	var settings *Provider
	for _, p := range items {
		if p.ID == EnvProviderID {
			copy := p
			settings = &copy
			continue
		}
		result = append(result, p)
	}
	if s.EnvConfigured() {
		result = append(result, s.environment(settings))
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		if result[i].FromEnv != result[j].FromEnv {
			return !result[i].FromEnv
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	return result
}

func (s *Service) loadEnvironment(ctx context.Context) (Provider, error) {
	if !s.EnvConfigured() {
		return Provider{}, ErrNotFound
	}
	settings, err := s.repo.Get(ctx, EnvProviderID)
	if errors.Is(err, ErrNotFound) {
		return s.environment(nil), nil
	}
	if err != nil {
		return Provider{}, err
	}
	return s.environment(&settings), nil
}

func (s *Service) editEnvironment(p Provider, in Input) (Provider, error) {
	display := environmentDisplay(p)
	// No admin request can redirect the trusted credential or replace it.
	if in.Endpoint != display.Endpoint || in.Model != p.Model || in.SpeakingModel != p.SpeakingModel || in.APIKey != "" {
		return Provider{}, ErrValidation
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || in.Priority < 0 || in.Priority > 10000 || in.TimeoutSeconds < 5 || in.TimeoutSeconds > 60 || len(in.Scopes) == 0 || len(in.Scopes) > 3 {
		return Provider{}, ErrValidation
	}
	seen := map[string]bool{}
	for _, scope := range in.Scopes {
		if (scope != "assistant" && scope != "writing" && scope != "speaking") || seen[scope] {
			return Provider{}, ErrValidation
		}
		seen[scope] = true
	}
	p.Name = strings.TrimSpace(in.Name)
	p.Priority = in.Priority
	p.Scopes = append([]string(nil), in.Scopes...)
	p.Enabled = in.Enabled
	p.TimeoutSeconds = in.TimeoutSeconds
	return p, nil
}

func (s *Service) saveEnvironment(ctx context.Context, actor uuid.UUID, in Input) (Provider, error) {
	if s.cipher == nil {
		return Provider{}, ErrEncryption
	}
	p, err := s.loadEnvironment(ctx)
	if err != nil {
		return Provider{}, err
	}
	if p.Revision != in.Revision {
		return Provider{}, ErrConflict
	}
	p, err = s.editEnvironment(p, in)
	if err != nil {
		return Provider{}, err
	}
	// Existing table/constraints can store this reference without a new migration.
	// The marker is not an API key; the real secret remains exclusively in .env.
	record := environmentDisplay(p)
	record.Ciphertext, err = s.cipher.Seal("environment-credential-reference", record.aad())
	if err != nil {
		return Provider{}, err
	}
	saved, err := s.repo.Save(ctx, record, actor, p.Revision == 0)
	if err != nil {
		return Provider{}, err
	}
	return environmentDisplay(s.environment(&saved)), nil
}
