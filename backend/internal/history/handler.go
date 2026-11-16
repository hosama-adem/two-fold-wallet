package history

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// CurrentUser reads the authenticated user id set by the access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Handler serves the transaction history reads (FR-HIS).
type Handler struct {
	svc         *Service
	require     gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the history HTTP adapter.
// require is the access-token middleware. currentUser reads the id it stored.
func NewHandler(svc *Service, require gin.HandlerFunc, currentUser CurrentUser) *Handler {
	return &Handler{svc: svc, require: require, currentUser: currentUser}
}

// Mount registers history routes behind auth.
//
// Route order matters: /v1/transactions/export.csv is a literal segment and
// must be registered before the /v1/transactions/:reference wildcard, or Gin
// would route "export.csv" into the receipt handler.
func (h *Handler) Mount(e *gin.Engine) {
	e.GET("/v1/transactions", h.require, h.List)
	e.GET("/v1/transactions/export.csv", h.require, h.Export)
	e.GET("/v1/transactions/:reference", h.require, h.Receipt)
}

// List handles GET /v1/transactions.
func (h *Handler) List(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.List(c.Request.Context(), ListInput{
		UserID: userID,
		Cursor: c.Query("cursor"),
		Kind:   c.Query("kind"),
		Status: c.Query("status"),
		From:   c.Query("from"),
		To:     c.Query("to"),
		Limit:  c.Query("limit"),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Receipt handles GET /v1/transactions/:reference.
func (h *Handler) Receipt(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.Receipt(c.Request.Context(), userID, c.Param("reference"))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Export handles GET /v1/transactions/export.csv.
// Export validates the window before writing any bytes, so an invalid request
// still returns a problem+json rather than a half-written CSV.
func (h *Handler) Export(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="transactions.csv"`)
	if err := h.svc.Export(c.Request.Context(), ExportInput{
		UserID: userID,
		From:   c.Query("from"),
		To:     c.Query("to"),
	}, c.Writer); err != nil {
		httpx.WriteError(c, err)
		return
	}
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
