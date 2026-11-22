package funding

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// CurrentUser reads the authenticated user id set by access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Handler serves /v1/topups and /webhooks/providers/{provider}.
type Handler struct {
	svc         *Service
	require     gin.HandlerFunc
	idempotent  gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the funding HTTP adapter.
func NewHandler(
	svc *Service,
	require gin.HandlerFunc,
	idempotent gin.HandlerFunc,
	currentUser CurrentUser,
) *Handler {
	return &Handler{
		svc:         svc,
		require:     require,
		idempotent:  idempotent,
		currentUser: currentUser,
	}
}

type createBody struct {
	Provider    string `json:"provider"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// Mount registers customer top-up routes and the public provider webhook.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/topups", h.require, h.idempotent, h.Create)
	e.GET("/v1/topups/:id", h.require, h.Get)
	e.POST("/webhooks/providers/:provider", h.Webhook)
}

// Create handles POST /v1/topups.
func (h *Handler) Create(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	var body createBody
	if err := httpx.BindJSON(c, &body); err != nil {
		httpx.WriteError(c, err)
		return
	}
	body.Provider = strings.TrimSpace(body.Provider)
	body.Currency = strings.TrimSpace(body.Currency)
	if body.Currency == "" {
		body.Currency = wallet.CurrencyETB
	}
	idem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	out, err := h.svc.Create(c.Request.Context(), CreateInput{
		UserID:         userID,
		Provider:       body.Provider,
		AmountMinor:    body.AmountMinor,
		Currency:       body.Currency,
		IdempotencyKey: idem,
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// Get handles GET /v1/topups/{id}.
func (h *Handler) Get(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Top-up id must be a UUID."))
		return
	}
	out, err := h.svc.Get(c.Request.Context(), userID, id)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Webhook handles POST /webhooks/providers/{provider}.
func (h *Handler) Webhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		httpx.WriteError(c, httpx.BadRequest("Could not read webhook body."))
		return
	}
	providerName := strings.TrimSpace(c.Param("provider"))
	res, err := h.svc.HandleWebhook(c.Request.Context(), providerName, c.Request.Header, body)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"duplicate": res.Duplicate,
		"decision":  res.Decision,
		"status":    res.Status,
	})
}

func (h *Handler) caller(c *gin.Context) (uuid.UUID, bool) {
	if h.currentUser == nil {
		httpx.WriteError(c, httpx.Internal("Auth context is not configured."))
		return uuid.Nil, false
	}
	userID, ok := h.currentUser(c)
	if !ok || userID == uuid.Nil {
		httpx.WriteError(c, httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Authentication required."))
		return uuid.Nil, false
	}
	return userID, true
}
