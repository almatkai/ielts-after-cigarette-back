package aiproviders

import (
	"context"
	"errors"
	"strconv"
	"time"
)

type rotationStore interface {
	NextRotation(context.Context, int, string) (int64, error)
}

func (r *postgresRoutingStore) NextRotation(ctx context.Context, priority int, purpose string) (int64, error) {
	var cursor int64
	err := r.pool.QueryRow(ctx, `INSERT INTO ai_provider_rotation(priority,purpose,cursor) VALUES($1,$2,0) ON CONFLICT(priority,purpose) DO UPDATE SET cursor=ai_provider_rotation.cursor+1 RETURNING cursor`, priority, purpose).Scan(&cursor)
	return cursor, storageError(err)
}

func (s *Service) rotationOffset(ctx context.Context, purpose string, priority, count int) int {
	if count < 2 {
		return 0
	}
	if store, ok := s.routing.(rotationStore); ok {
		loadCtx, cancel := context.WithTimeout(ctx, time.Second)
		cursor, err := store.NextRotation(loadCtx, priority, purpose)
		cancel()
		if err == nil {
			return int(cursor % int64(count))
		}
		if !errors.Is(err, ErrMigrationRequired) && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "Shared AI rotation unavailable; using process-local rotation")
		}
	}
	// Before migration / during an outage: retain working load balancing without
	// returning errors to students. No secrets or student content enter this key.
	key := strconv.Itoa(priority) + ":" + purpose
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	if s.rotations == nil {
		s.rotations = map[string]uint64{}
	}
	next := s.rotations[key]
	s.rotations[key] = next + 1
	return int(next % uint64(count))
}
