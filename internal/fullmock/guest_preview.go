package fullmock

import (
	"context"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
)

func SessionForViewer(ctx context.Context, session Session) Session {
	if auth.Role(ctx) != "GUEST" {
		return session
	}
	session.ResultsLocked = true
	session.Sections = append([]SessionSection(nil), session.Sections...)
	for index := range session.Sections {
		session.Sections[index].Attempt = attempts.AttemptForViewer(ctx, session.Sections[index].Attempt)
	}
	return session
}
