package fullmock

import (
	"context"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
)

func SessionForViewer(ctx context.Context, session Session) Session {
	if auth.Role(ctx) != "GUEST" && session.Status == SessionSubmitted {
		return session
	}
	session.ResultsLocked = auth.Role(ctx) == "GUEST"
	if session.Status != SessionSubmitted {
		session.OverallBand = nil
	}
	session.Sections = append([]SessionSection(nil), session.Sections...)
	for index := range session.Sections {
		item := session.Sections[index].Attempt
		item.ReviewLocked = session.Status != SessionSubmitted
		item.FullMockSessionID = &session.ID
		session.Sections[index].Attempt = attempts.AttemptForViewer(ctx, item)
	}
	return session
}
