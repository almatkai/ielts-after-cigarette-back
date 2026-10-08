package attempts

import (
	"context"

	"github.com/google/uuid"
)

// ReviewAttempt is response metadata only. Persisted grades remain unchanged
// for overall scoring and are withheld until the parent mock is submitted.
func (s *Service) ReviewAttempt(ctx context.Context, userID uuid.UUID, item Attempt) (Attempt, error) {
	if s.examGuard != nil {
		sessionID, locked, err := s.examGuard.ReviewAccess(ctx, userID, item.ID)
		if err != nil {
			return Attempt{}, err
		}
		item.FullMockSessionID, item.ReviewLocked = sessionID, locked
	}
	if item.ReviewLocked {
		item.Band, item.Score, item.MaxScore = nil, nil, nil
	}
	return item, nil
}
