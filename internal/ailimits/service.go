package ailimits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/redis/go-redis/v9"
)

const redisSettingsKey = "iac:daily-limits:settings"

var ErrInvalidLimits = errors.New("limits must be non-negative")

type Service struct {
	repo     Repository
	limiter  *cache.DailyLimiter
	redis    *redis.Client
	defaults Limits

	mu       sync.RWMutex
	cached   Limits
	cachedAt time.Time
}

func NewService(repo Repository, limiter *cache.DailyLimiter, redisClient *redis.Client, defaults Limits) *Service {
	return &Service{
		repo:     repo,
		limiter:  limiter,
		redis:    redisClient,
		defaults: defaults,
		cached:   defaults,
		cachedAt: time.Time{},
	}
}

func (s *Service) Get(ctx context.Context) (Limits, error) {
	s.mu.RLock()
	if time.Since(s.cachedAt) < 3*time.Second {
		cached := s.cached
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double check
	if time.Since(s.cachedAt) < 3*time.Second {
		return s.cached, nil
	}

	// 1. Try Redis
	if s.redis != nil {
		data, err := s.redis.Get(ctx, redisSettingsKey).Bytes()
		if err == nil {
			var limits Limits
			if err := json.Unmarshal(data, &limits); err == nil {
				s.cached = limits
				s.cachedAt = time.Now()
				return limits, nil
			}
		}
	}

	// 2. Try DB
	if s.repo != nil {
		limits, found, err := s.repo.Get(ctx)
		if err == nil && found {
			s.cached = limits
			s.cachedAt = time.Now()
			s.syncRedis(ctx, limits)
			return limits, nil
		}
	}

	// 3. Fallback to defaults
	s.cached = s.defaults
	s.cachedAt = time.Now()
	return s.defaults, nil
}

func (s *Service) Update(ctx context.Context, input UpdateLimitsInput) (Limits, error) {
	current, err := s.Get(ctx)
	if err != nil {
		current = s.defaults
	}

	if input.AssistantLimit != nil {
		if *input.AssistantLimit < 0 {
			return Limits{}, fmt.Errorf("%w: assistant limit", ErrInvalidLimits)
		}
		current.AssistantLimit = *input.AssistantLimit
	}
	if input.GuestAssistantLimit != nil {
		if *input.GuestAssistantLimit < 0 {
			return Limits{}, fmt.Errorf("%w: guest assistant limit", ErrInvalidLimits)
		}
		current.GuestAssistantLimit = *input.GuestAssistantLimit
	}
	if input.WritingLimit != nil {
		if *input.WritingLimit < 0 {
			return Limits{}, fmt.Errorf("%w: writing limit", ErrInvalidLimits)
		}
		current.WritingLimit = *input.WritingLimit
	}
	if input.SpeakingLimit != nil {
		if *input.SpeakingLimit < 0 {
			return Limits{}, fmt.Errorf("%w: speaking limit", ErrInvalidLimits)
		}
		current.SpeakingLimit = *input.SpeakingLimit
	}

	// Save to DB
	var saved Limits
	if s.repo != nil {
		saved, err = s.repo.Upsert(ctx, current)
		if err != nil {
			return Limits{}, fmt.Errorf("save daily limits: %w", err)
		}
	} else {
		current.UpdatedAt = time.Now().UTC()
		saved = current
	}

	// Update Redis & in-memory cache
	s.mu.Lock()
	s.cached = saved
	s.cachedAt = time.Now()
	s.mu.Unlock()

	s.syncRedis(ctx, saved)
	return saved, nil
}

func (s *Service) syncRedis(ctx context.Context, limits Limits) {
	if s.redis == nil {
		return
	}
	data, err := json.Marshal(limits)
	if err == nil {
		_ = s.redis.Set(ctx, redisSettingsKey, data, 7*24*time.Hour).Err()
	}
}

func (s *Service) Allow(ctx context.Context, scope, id string) (Result, error) {
	limits, err := s.Get(ctx)
	if err != nil {
		limits = s.defaults
	}
	limit := s.resolveLimit(scope, limits)

	res, err := s.limiter.Allow(ctx, scope, id, limit)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Allowed:   res.Allowed,
		Current:   res.Current,
		Limit:     res.Limit,
		Remaining: res.Remaining,
		ResetAt:   res.ResetAt,
	}, nil
}

func (s *Service) Check(ctx context.Context, scope, id string) (Result, error) {
	limits, err := s.Get(ctx)
	if err != nil {
		limits = s.defaults
	}
	limit := s.resolveLimit(scope, limits)

	res, err := s.limiter.Check(ctx, scope, id, limit)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Allowed:   res.Allowed,
		Current:   res.Current,
		Limit:     res.Limit,
		Remaining: res.Remaining,
		ResetAt:   res.ResetAt,
	}, nil
}

func (s *Service) resolveLimit(scope string, limits Limits) int64 {
	switch scope {
	case "assistant:user":
		return limits.AssistantLimit
	case "assistant:guest":
		return limits.GuestAssistantLimit
	case "writing":
		return limits.WritingLimit
	case "speaking":
		return limits.SpeakingLimit
	default:
		return 100
	}
}
