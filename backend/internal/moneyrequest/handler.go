package moneyrequest

import (
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

// Handler serves /v1/requests (FR-REQ).
type Handler struct {
	svc         *Service
	pins        *pin.Service
	require     gin.HandlerFunc
	idempotent  gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the money-request HTTP adapter.
// Pay mounts Idempotency-Key middleware after auth, same as transfers.
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

type createBody struct {
	Payer       string `json:"payer"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Note        string `json:"note"`
}

type payBody struct {
	PIN              string `json:"pin"`
	ExpectedFeeMinor int64  `json:"expected_fee_minor"`
}

// Mount registers money-request routes behind auth.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/requests", h.require, h.Create)
	e.GET("/v1/requests", h.require, h.List)
	e.POST("/v1/requests/:id/pay", h.require, h.idempotent, h.Pay)
	e.POST("/v1/requests/:id/decline", h.require, h.Decline)
	e.POST("/v1/requests/:id/cancel", h.require, h.Cancel)
}

// Create handles POST /v1/requests.
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
	body.Payer = strings.TrimSpace(body.Payer)
	body.Currency = strings.TrimSpace(body.Currency)
	if body.Currency == "" {
		body.Currency = wallet.CurrencyETB
	}
	out, err := h.svc.Create(c.Request.Context(), CreateInput{
		RequesterID: userID,
		PayerQuery:  body.Payer,
		AmountMinor: body.AmountMinor,
		Currency:    body.Currency,
		Note:        body.Note,
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// List handles GET /v1/requests?box=incoming|outgoing.
func (h *Handler) List(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	out, err := h.svc.List(c.Request.Context(), userID, c.Query("box"))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Pay handles POST /v1/requests/:id/pay.
func (h *Handler) Pay(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, errNotFound())
		return
	}
	var body payBody
	if err := httpx.BindJSON(c, &body); err != nil {
		httpx.WriteError(c, err)
		return
	}
	if body.PIN == "" {
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

	idemKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	out, err := h.svc.Pay(c.Request.Context(), PayInput{
		PayerUserID:      userID,
		RequestID:        id,
		ExpectedFeeMinor: body.ExpectedFeeMinor,
		IdempotencyKey:   strings.ToLower(idemKey),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Decline handles POST /v1/requests/:id/decline.
func (h *Handler) Decline(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, errNotFound())
		return
	}
	out, err := h.svc.Decline(c.Request.Context(), userID, id)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Cancel handles POST /v1/requests/:id/cancel.
func (h *Handler) Cancel(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, errNotFound())
		return
	}
	out, err := h.svc.Cancel(c.Request.Context(), userID, id)
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
