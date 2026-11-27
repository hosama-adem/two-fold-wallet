package notification

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// CurrentUser reads the authenticated user id set by the access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Handler serves notification reads and mark-read.
type Handler struct {
	svc         *Service
	require     gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the notification HTTP adapter.
func NewHandler(svc *Service, require gin.HandlerFunc, currentUser CurrentUser) *Handler {
	return &Handler{svc: svc, require: require, currentUser: currentUser}
}

// Mount registers GET /v1/notifications and POST /v1/notifications/:id/read.
func (h *Handler) Mount(e *gin.Engine) {
	e.GET("/v1/notifications", h.require, h.List)
	e.POST("/v1/notifications/:id/read", h.require, h.MarkRead)
}

// List handles GET /v1/notifications.
func (h *Handler) List(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.List(c.Request.Context(), userID, c.Query("limit"))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// MarkRead handles POST /v1/notifications/:id/read.
func (h *Handler) MarkRead(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Notification id must be a UUID."))
		return
	}
	out, err := h.svc.MarkRead(c.Request.Context(), userID, id)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) caller(c *gin.Context) (uuid.UUID, bool) {
	if h.currentUser == nil {
		httpx.WriteError(c, unauthorized())
		return uuid.Nil, false
	}
	userID, ok := h.currentUser(c)
	if !ok || userID == uuid.Nil {
		httpx.WriteError(c, unauthorized())
		return uuid.Nil, false
	}
	return userID, true
}
