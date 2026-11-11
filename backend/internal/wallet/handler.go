package wallet

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// CurrentUser reads the authenticated user id set by the access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Handler serves wallet reads and recipient lookup.
type Handler struct {
	svc         *Service
	require     gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the wallet HTTP adapter.
// require is the access-token middleware. currentUser reads the id it stored.
func NewHandler(svc *Service, require gin.HandlerFunc, currentUser CurrentUser) *Handler {
	return &Handler{svc: svc, require: require, currentUser: currentUser}
}

// Mount registers GET /v1/wallets and GET /v1/recipients/lookup.
func (h *Handler) Mount(e *gin.Engine) {
	e.GET("/v1/wallets", h.require, h.List)
	e.GET("/v1/recipients/lookup", h.require, h.Lookup)
}

// List handles GET /v1/wallets.
func (h *Handler) List(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.List(c.Request.Context(), userID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Lookup handles GET /v1/recipients/lookup?q=.
func (h *Handler) Lookup(c *gin.Context) {
	if _, ok := h.caller(c); !ok {
		return
	}
	out, err := h.svc.Lookup(c.Request.Context(), c.Query("q"))
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

func unauthorized() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Missing or invalid Authorization header.")
}
