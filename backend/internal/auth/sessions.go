package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/session"
)

// SessionView is a safe session projection for clients.
type SessionView struct {
	ID        uuid.UUID `json:"id"`
	UserAgent *string   `json:"user_agent"`
	IP        *string   `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

// SessionsResponse lists active sessions.
type SessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// ListSessions returns active sessions for the user, marking the current sid.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID uuid.UUID) (SessionsResponse, error) {
	rows, err := s.sessions.ListActive(ctx, userID, s.clock.Now())
	if err != nil {
		return SessionsResponse{}, err
	}
	out := make([]SessionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, SessionView{
			ID:        row.ID,
			UserAgent: row.UserAgent,
			IP:        row.Ip,
			CreatedAt: row.CreatedAt,
			ExpiresAt: row.ExpiresAt,
			Current:   row.ID == currentSessionID,
		})
	}
	return SessionsResponse{Sessions: out}, nil
}

// RevokeSessionMeta carries request metadata for audit.
type RevokeSessionMeta struct {
	UserAgent string
	IP        string
	RequestID string
}

// RevokeSession revokes a session owned by userID.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, meta RevokeSessionMeta) error {
	sess, err := s.sessions.RevokeForUser(ctx, sessionID, userID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return httpx.NotFound("Session not found.")
		}
		return err
	}
	s.auditBestEffort(ctx, audit.Entry{
		ActorUserID: &userID,
		ActorKind:   audit.ActorUser,
		Action:      audit.ActionSessionRevoke,
		TargetType:  "session",
		TargetID:    &sess.ID,
		Summary:     map[string]any{"via": "api"},
		IP:          meta.IP,
		UserAgent:   meta.UserAgent,
		RequestID:   meta.RequestID,
	})
	return nil
}
