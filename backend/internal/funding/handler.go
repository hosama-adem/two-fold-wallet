package funding

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// CurrentUser reads the authenticated user id set by access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Handler serves top-ups, withdrawals, destinations, and provider webhooks.
type Handler struct {
	svc         *Service
	pins        *pin.Service
	require     gin.HandlerFunc
	idempotent  gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the funding HTTP adapter.
func NewHandler(
	svc *Service,
	pins *pin.Service,
	require gin.HandlerFunc,
	idempotent gin.HandlerFunc,
	currentUser CurrentUser,
) *Handler {
	return &Handler{
		svc:         svc,
		pins:        pins,
		require:     require,
		idempotent:  idempotent,
		currentUser: currentUser,
	}
}

type createTopUpBody struct {
	Provider    string `json:"provider"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type createDestinationBody struct {
	Provider   string `json:"provider"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Identifier string `json:"identifier"`
}

type createWithdrawalBody struct {
	DestinationID    string `json:"destination_id"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	PIN              string `json:"pin"`
	ExpectedFeeMinor int64  `json:"expected_fee_minor"`
}

// Mount registers funding routes.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/topups", h.require, h.idempotent, h.CreateTopUp)
	e.GET("/v1/topups/:id", h.require, h.GetTopUp)

	e.POST("/v1/payout-destinations", h.require, h.CreateDestination)
	e.GET("/v1/payout-destinations", h.require, h.ListDestinations)
	e.DELETE("/v1/payout-destinations/:id", h.require, h.DeleteDestination)

	e.POST("/v1/withdrawals", h.require, h.idempotent, h.CreateWithdrawal)
	e.GET("/v1/withdrawals/:id", h.require, h.GetWithdrawal)

	e.POST("/webhooks/providers/:provider", h.Webhook)
}

// CreateTopUp handles POST /v1/topups.
func (h *Handler) CreateTopUp(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	var body createTopUpBody
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

// GetTopUp handles GET /v1/topups/{id}.
func (h *Handler) GetTopUp(c *gin.Context) {
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

// CreateDestination handles POST /v1/payout-destinations.
func (h *Handler) CreateDestination(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	var body createDestinationBody
	if err := httpx.BindJSON(c, &body); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.CreateDestination(c.Request.Context(), DestinationInput{
		UserID:     userID,
		Provider:   strings.TrimSpace(body.Provider),
		Kind:       strings.TrimSpace(body.Kind),
		Label:      strings.TrimSpace(body.Label),
		Identifier: strings.TrimSpace(body.Identifier),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// ListDestinations handles GET /v1/payout-destinations.
func (h *Handler) ListDestinations(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	items, err := h.svc.ListDestinations(c.Request.Context(), userID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// DeleteDestination handles DELETE /v1/payout-destinations/{id}.
func (h *Handler) DeleteDestination(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Destination id must be a UUID."))
		return
	}
	if err := h.svc.DeleteDestination(c.Request.Context(), userID, id); err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// CreateWithdrawal handles POST /v1/withdrawals.
func (h *Handler) CreateWithdrawal(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	var body createWithdrawalBody
	if err := httpx.BindJSON(c, &body); err != nil {
		httpx.WriteError(c, err)
		return
	}
	if strings.TrimSpace(body.PIN) == "" {
		httpx.WriteError(c, httpx.ValidationFailed("Transaction PIN is required."))
		return
	}
	if h.pins == nil {
		httpx.WriteError(c, httpx.Internal("PIN verifier is not configured."))
		return
	}
	if err := h.pins.Verify(c.Request.Context(), userID, body.PIN, pin.Meta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
		RequestID: httpx.RequestIDFromContext(c),
	}); err != nil {
		httpx.WriteError(c, err)
		return
	}
	destID, err := uuid.Parse(strings.TrimSpace(body.DestinationID))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Destination id must be a UUID."))
		return
	}
	currency := strings.TrimSpace(body.Currency)
	if currency == "" {
		currency = wallet.CurrencyETB
	}
	out, err := h.svc.Withdraw(c.Request.Context(), WithdrawInput{
		UserID:           userID,
		DestinationID:    destID,
		AmountMinor:      body.AmountMinor,
		Currency:         currency,
		ExpectedFeeMinor: body.ExpectedFeeMinor,
		IdempotencyKey:   strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// GetWithdrawal handles GET /v1/withdrawals/{id}.
func (h *Handler) GetWithdrawal(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Withdrawal id must be a UUID."))
		return
	}
	out, err := h.svc.GetWithdrawal(c.Request.Context(), userID, id)
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
