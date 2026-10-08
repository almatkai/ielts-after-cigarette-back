package fullmock

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrGuestUnavailable = errors.New("guest access expired or claimed")
var ErrRetakeNotReady = errors.New("finish the mock before retaking it")

type guestRetakeRepository interface {
	RetakeGuest(context.Context, uuid.UUID, uuid.UUID, func(context.Context) error) (Session, bool, error)
}

// RetakeGuest preserves the source report and starts fresh, fixed-material
// attempts. The source ID makes repeats idempotent; admission runs under the
// creation lock only when a new session is actually needed.
func (s *Service) RetakeGuest(ctx context.Context, userID, sourceID uuid.UUID, admit func(context.Context) error) (Session, bool, error) {
	source, err := s.GetSession(ctx, userID, sourceID)
	if err != nil {
		return Session{}, false, err
	}
	if source.Status != SessionSubmitted {
		return Session{}, false, ErrRetakeNotReady
	}
	repo, ok := s.repository.(guestRetakeRepository)
	if !ok {
		return Session{}, false, fmt.Errorf("guest retake is unavailable")
	}
	session, created, err := repo.RetakeGuest(ctx, userID, sourceID, admit)
	if err != nil {
		return Session{}, false, err
	}
	session, err = s.decorate(ctx, userID, session)
	return session, created, err
}
