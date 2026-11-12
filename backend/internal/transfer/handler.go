package transfer

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

// Handler serves POST /v1/transfers and POST /v1/transfers/preview.
type Handler struct {
	svc         *Service
	pins        *pin.Service
	require     gin.HandlerFunc
	idempotent  gin.HandlerFunc
	currentUser CurrentUser
}

// NewHandler builds the transfer HTTP adapter.
// require is access-token middleware. idempotent is Idempotency-Key middleware
// (mounted after auth on the money path only). currentUser reads the id auth stored.
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

// createBody is the POST /v1/transfers JSON body.
type createBody struct {
	Recipient        string `json:"recipient"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	PIN              string `json:"pin"`
	Note             string `json:"note"`
	ExpectedFeeMinor int64  `json:"expected_fee_minor"`
}

// previewBody is the POST /v1/transfers/preview JSON body.
type previewBody struct {
	Recipient   string `json:"recipient"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// Mount registers transfer routes behind auth. Preview has no PIN and no
// Idempotency-Key. Confirm keeps both.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/transfers/preview", h.require, h.Preview)
	e.POST("/v1/transfers", h.require, h.idempotent, h.Create)
}

// Preview handles POST /v1/transfers/preview.
func (h *Handler) Preview(c *gin.Context) {
	userID, ok := h.caller(c)
	if !ok {
		return
	}

	var body previewBody
	if err := httpx.BindJSON(c, &body); err != nil {
		httpx.WriteError(c, err)
		return
	}
	body.Recipient = strings.TrimSpace(body.Recipient)
	body.Currency = strings.TrimSpace(body.Currency)
	if body.Currency == "" {
		body.Currency = wallet.CurrencyETB
	}

	out, err := h.svc.Preview(c.Request.Context(), PreviewRequest{
		SenderUserID:   userID,
		RecipientQuery: body.Recipient,
		AmountMinor:    body.AmountMinor,
		Currency:       body.Currency,
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Create handles POST /v1/transfers.
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
	body.Recipient = strings.TrimSpace(body.Recipient)
	body.Currency = strings.TrimSpace(body.Currency)
	if body.Currency == "" {
		body.Currency = wallet.CurrencyETB
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
	out, err := h.svc.Transfer(c.Request.Context(), Request{
		SenderUserID:     userID,
		RecipientQuery:   body.Recipient,
		AmountMinor:      body.AmountMinor,
		Currency:         body.Currency,
		Note:             body.Note,
		ExpectedFeeMinor: body.ExpectedFeeMinor,
		IdempotencyKey:   strings.ToLower(idemKey),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
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
