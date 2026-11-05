package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Handler exposes auth HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler builds the auth HTTP adapter.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Mount registers /v1/auth routes on the engine or group.
func (h *Handler) Mount(r gin.IRoutes) {
	r.POST("/v1/auth/register", h.Register)
	r.POST("/v1/auth/verify-phone", h.VerifyPhone)
	r.POST("/v1/auth/resend-otp", h.ResendOTP)
}

// Register handles POST /v1/auth/register.
func (h *Handler) Register(c *gin.Context) {
	var in RegisterInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.Register(c.Request.Context(), in)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// VerifyPhone handles POST /v1/auth/verify-phone.
func (h *Handler) VerifyPhone(c *gin.Context) {
	var in VerifyPhoneInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.VerifyPhone(c.Request.Context(), in)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ResendOTP handles POST /v1/auth/resend-otp.
func (h *Handler) ResendOTP(c *gin.Context) {
	var in ResendOTPInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.ResendOTP(c.Request.Context(), in)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
