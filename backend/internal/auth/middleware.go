package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// RequireAccess validates a Bearer access JWT and loads an active session.
func (h *Handler) RequireAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			httpx.WriteError(c, unauthorized("Missing or invalid Authorization header."))
			return
		}

		claims, err := h.svc.tokens.ParseAccessToken(raw)
		if err != nil {
			httpx.WriteError(c, unauthorized("Invalid or expired access token."))
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			httpx.WriteError(c, unauthorized("Invalid or expired access token."))
			return
		}

		sess, err := h.svc.sessions.GetByID(c.Request.Context(), claims.SessionID)
		if err != nil {
			httpx.WriteError(c, unauthorized("Invalid or expired access token."))
			return
		}
		if sess.UserID != userID || sess.RevokedAt != nil {
			httpx.WriteError(c, unauthorized("Invalid or expired access token."))
			return
		}
		if h.svc.clock.Now().After(sess.ExpiresAt) {
			httpx.WriteError(c, unauthorized("Invalid or expired access token."))
			return
		}

		setUserID(c, userID)
		setSessionID(c, claims.SessionID)
		c.Next()
	}
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func unauthorized(detail string) *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
}
