package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ctxKeyUserID    = "auth_user_id"
	ctxKeySessionID = "auth_session_id"
)

// UserIDFromContext returns the authenticated user id, if present.
func UserIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxKeyUserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

// SessionIDFromContext returns the authenticated session id, if present.
func SessionIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxKeySessionID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func setUserID(c *gin.Context, id uuid.UUID) {
	c.Set(ctxKeyUserID, id)
}

func setSessionID(c *gin.Context, id uuid.UUID) {
	c.Set(ctxKeySessionID, id)
}
