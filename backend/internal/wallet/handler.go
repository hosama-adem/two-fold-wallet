package wallet

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Handler exposes the customer wallet routes.
type Handler struct {
	svc  *Service
	auth gin.HandlerFunc
}

// NewHandler builds the wallet HTTP adapter.
// auth is the access-token middleware (auth.Handler.RequireAccess).
func NewHandler(svc *Service, authenticate gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, auth: authenticate}
}

// Mount registers wallet routes. Both require the authenticated user.
func (h *Handler) Mount(e *gin.Engine) {
	if h == nil || h.auth == nil {
		return
	}
	e.GET("/v1/wallets", h.auth, h.List)
	e.GET("/v1/recipients/lookup", h.auth, h.Lookup)
}

type walletResponse struct {
	ID                     uuid.UUID `json:"id"`
	Currency               string    `json:"currency"`
	Status                 string    `json:"status"`
	FreezeMode             string    `json:"freeze_mode"`
	LedgerBalanceMinor     int64     `json:"ledger_balance_minor"`
	PendingWithdrawalMinor int64     `json:"pending_withdrawal_minor"`
	AvailableMinor         int64     `json:"available_minor"`
}

type listResponse struct {
	Wallets []walletResponse `json:"wallets"`
}

type recipientResponse struct {
	DisplayName string  `json:"display_name"`
	Handle      *string `json:"handle,omitempty"`
	PhoneMasked string  `json:"phone_masked"`
}

// List handles GET /v1/wallets.
func (h *Handler) List(c *gin.Context) {
	userID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, unauthorized())
		return
	}
	rows, err := h.svc.List(c.Request.Context(), userID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	out := listResponse{Wallets: make([]walletResponse, 0, len(rows))}
	for _, row := range rows {
		out.Wallets = append(out.Wallets, walletResponse{
			ID:                     row.ID,
			Currency:               row.Currency,
			Status:                 string(row.Status),
			FreezeMode:             string(row.FreezeMode),
			LedgerBalanceMinor:     row.LedgerBalanceMinor,
			PendingWithdrawalMinor: row.PendingWithdrawalMinor,
			AvailableMinor:         row.AvailableMinor,
		})
	}
	c.JSON(http.StatusOK, out)
}

// Lookup handles GET /v1/recipients/lookup?q=
func (h *Handler) Lookup(c *gin.Context) {
	if _, ok := auth.UserIDFromContext(c); !ok {
		httpx.WriteError(c, unauthorized())
		return
	}
	rec, err := h.svc.Lookup(c.Request.Context(), c.Query("q"))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, recipientResponse{
		DisplayName: rec.DisplayName,
		Handle:      rec.Handle,
		PhoneMasked: rec.PhoneMasked,
	})
}

func unauthorized() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Missing or invalid Authorization header.")
}
