package aiproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

const MaxChain = 8
const ChainTimeout = 180 * time.Second

type Service struct {
	repo                    Repository
	cipher                  *Cipher
	env                     Provider
	logger                  *slog.Logger
	telemetryOptional       bool
	routing                 RoutingStore
	rotationMu              sync.Mutex
	rotations               map[string]uint64
	publicClient, envClient *http.Client
}

func NewService(repo Repository, cipher *Cipher, env Provider, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	env.FromEnv = true
	env.ID = EnvProviderID
	env.Name = "Провайдер из .env"
	env.Priority = 10000
	env.HasKey = env.APIKey != ""
	env.Enabled = env.APIKey != "" && env.Endpoint != "" && env.Model != ""
	env.Scopes = []string{"assistant", "writing", "speaking"}
	if env.TimeoutSeconds < 1 {
		env.TimeoutSeconds = 45
	}
	env.TimeoutSeconds = min(max(env.TimeoutSeconds, 5), 60)
	return &Service{repo: repo, cipher: cipher, env: env, logger: logger, publicClient: newClient(true), envClient: newClient(false)}
}
func (s *Service) Client(p Provider) *http.Client {
	if p.FromEnv {
		return s.envClient
	}
	return s.publicClient
}
func (s *Service) EncryptionConfigured() bool { return s.cipher != nil }
func (s *Service) TelemetryRequired() bool    { return !s.telemetryOptional }
func (s *Service) EnvConfigured() bool        { return s.env.Enabled }
func (s *Service) List(ctx context.Context) ([]Provider, error) {
	items, err := s.repo.List(ctx)
	items = s.providers(items)
	for i := range items {
		if items[i].FromEnv {
			items[i] = environmentDisplay(items[i])
		}
	}
	return items, err
}

func normalize(input Input) (Provider, error) {
	p := Provider{Name: strings.TrimSpace(input.Name), Endpoint: strings.TrimSpace(input.Endpoint), Model: strings.TrimSpace(input.Model), SpeakingModel: strings.TrimSpace(input.SpeakingModel), Scopes: input.Scopes, Enabled: input.Enabled, Priority: input.Priority, TimeoutSeconds: input.TimeoutSeconds, Revision: input.Revision}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 20
	}
	if p.Name == "" || len(p.Name) > 120 || p.Model == "" || len(p.Model) > 200 || len(p.SpeakingModel) > 200 || p.Priority < 0 || p.Priority > 10000 || p.TimeoutSeconds < 5 || p.TimeoutSeconds > 60 || len(p.Scopes) == 0 || len(p.Scopes) > 3 || len(input.APIKey) > 8192 {
		return Provider{}, ErrValidation
	}
	if err := ValidateEndpoint(p.Endpoint); err != nil {
		return Provider{}, err
	}
	seen := map[string]bool{}
	for _, scope := range p.Scopes {
		if (scope != "assistant" && scope != "writing" && scope != "speaking") || seen[scope] {
			return Provider{}, ErrValidation
		}
		seen[scope] = true
	}
	return p, nil
}
func (s *Service) Save(ctx context.Context, id, actor uuid.UUID, input Input) (Provider, error) {
	if id == EnvProviderID {
		return s.saveEnvironment(ctx, actor, input)
	}
	if s.cipher == nil {
		return Provider{}, ErrEncryption
	}
	p, err := normalize(input)
	if err != nil {
		return Provider{}, err
	}
	create := id == uuid.Nil
	key := strings.TrimSpace(input.APIKey)
	if create {
		items, err := s.repo.List(ctx)
		if err != nil {
			return Provider{}, err
		}
		count := 0
		for _, item := range items {
			if item.ID != EnvProviderID {
				count++
			}
		}
		if count >= MaxChain {
			return Provider{}, ErrValidation
		}
		p.ID = uuid.New()
	} else {
		old, err := s.repo.Get(ctx, id)
		if err != nil {
			return Provider{}, err
		}
		if input.Revision != old.Revision {
			return Provider{}, ErrConflict
		}
		p.ID = id
		if key == "" {
			key, err = s.cipher.Open(old.Ciphertext, old.aad())
			if err != nil {
				return Provider{}, err
			}
		}
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return Provider{}, ErrValidation
	}
	p.Ciphertext, err = s.cipher.Seal(key, p.aad())
	if err != nil {
		return Provider{}, err
	}
	return s.repo.Save(ctx, p, actor, create)
}
func (s *Service) Delete(ctx context.Context, id uuid.UUID, revision int64) error {
	if id == EnvProviderID || revision < 1 {
		return ErrValidation
	}
	return s.repo.Delete(ctx, id, revision)
}
func includes(scopes []string, purpose string) bool {
	for _, scope := range scopes {
		if scope == purpose {
			return true
		}
	}
	return false
}
func (s *Service) report(ctx context.Context, p Provider, purpose string, err error) *Failure {
	failure := &Failure{ProviderID: p.ID, Purpose: purpose, Code: "invalid_response"}
	var status *HTTPError
	var netErr net.Error
	switch {
	case errors.As(err, &status):
		failure.Code = "http_error"
		failure.Status = status.Status
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		failure.Code = "timeout"
	case errors.Is(err, ErrEncryption):
		failure.Code = "credential_decryption"
	case errors.Is(err, ErrValidation):
		failure.Code = "invalid_configuration"
	}
	s.logger.WarnContext(ctx, "AI provider failed; trying fallback", "provider_id", p.ID, "purpose", purpose, "code", failure.Code, "http_status", failure.Status, "request_id", httpx.RequestID(ctx))
	httpx.ReportBackground(ctx, "ai_provider", "error", failure, "request_id", httpx.RequestID(ctx), "provider_id", p.ID, "ai_purpose", purpose)
	return failure
}

// Run preserves the sequential interface for callbacks with side effects.
// Production callers use Execute and return isolated values for racing.
func (s *Service) Run(ctx context.Context, purpose string, call func(context.Context, Provider) error) error {
	_, err := execute(ctx, s, purpose, func(ctx context.Context, p Provider) (struct{}, error) { return struct{}{}, call(ctx, p) }, true)
	return err
}

type TestResult struct {
	OK         bool   `json:"ok"`
	Model      string `json:"model"`
	LatencyMS  int64  `json:"latencyMs"`
	Code       string `json:"code,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
}

func (s *Service) Test(ctx context.Context, id uuid.UUID, input *Input) (TestResult, error) {
	if id == EnvProviderID {
		p, err := s.loadEnvironment(ctx)
		if err != nil {
			return TestResult{}, err
		}
		if input != nil {
			p, err = s.editEnvironment(p, *input)
			if err != nil {
				return TestResult{}, err
			}
		}
		return s.testProvider(ctx, p)
	}
	var p Provider
	var err error
	if input != nil {
		p, err = normalize(*input)
		if err != nil {
			return TestResult{}, err
		}
		p.ID = id
		if id == uuid.Nil {
			p.ID = uuid.New()
		}
		p.APIKey = strings.TrimSpace(input.APIKey)
		if p.APIKey == "" && id != uuid.Nil {
			old, getErr := s.repo.Get(ctx, id)
			if getErr != nil {
				return TestResult{}, getErr
			}
			p.APIKey, err = s.cipher.Open(old.Ciphertext, old.aad())
			if err != nil {
				s.report(ctx, p, "test", err)
				return TestResult{}, ErrEncryption
			}
		}
		if p.APIKey == "" {
			return TestResult{}, ErrValidation
		}
	} else {
		p, err = s.repo.Get(ctx, id)
		if err != nil {
			return TestResult{}, err
		}
		if err := ValidateEndpoint(p.Endpoint); err != nil {
			s.report(ctx, p, "test", err)
			return TestResult{}, err
		}
		p.APIKey, err = s.cipher.Open(p.Ciphertext, p.aad())
		if err != nil {
			s.report(ctx, p, "test", err)
			return TestResult{}, ErrEncryption
		}
	}
	return s.testProvider(ctx, p)
}

func (s *Service) probeProvider(ctx context.Context, p Provider) (TestResult, error) {
	testCtx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now()
	body, _ := json.Marshal(map[string]any{"model": p.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 16})
	req, err := http.NewRequestWithContext(testCtx, "POST", p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return TestResult{}, ErrValidation
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "Daiyndyq IELTS")
	response, err := s.Client(p).Do(req)
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			err = &HTTPError{response.StatusCode}
		} else {
			var completion struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&completion)
			if err == nil && (len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "") {
				err = ErrAllFailed
			}
		}
	}
	result := TestResult{OK: err == nil, Model: p.Model, LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		if ctx.Err() != nil {
			return TestResult{}, ctx.Err()
		}
		failure := s.report(ctx, p, "test", err)
		result.Code = failure.Code
		result.HTTPStatus = failure.Status
	}
	return result, nil
}
