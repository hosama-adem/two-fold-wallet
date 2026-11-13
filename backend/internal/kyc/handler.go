package kyc

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// UserIDFunc extracts the authenticated user id from the request context.
type UserIDFunc func(*gin.Context) (uuid.UUID, bool)

// Handler exposes KYC HTTP endpoints.
type Handler struct {
	svc    *Service
	auth   gin.HandlerFunc
	userID UserIDFunc
}

// NewHandler builds the KYC HTTP adapter.
func NewHandler(svc *Service, auth gin.HandlerFunc, userID UserIDFunc) *Handler {
	return &Handler{svc: svc, auth: auth, userID: userID}
}

// Mount registers KYC routes.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/kyc/submissions", h.auth, h.Submit)
}

// Submit handles POST /v1/kyc/submissions.
func (h *Handler) Submit(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		httpx.WriteError(c, httpx.New(
			http.StatusUnauthorized,
			"unauthorized",
			"Unauthorized",
			"Missing or invalid Authorization header.",
		))
		return
	}
	var in SubmitInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.Submit(c.Request.Context(), userID, in)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}
