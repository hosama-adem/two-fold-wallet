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

// Mount registers auth and /v1/me routes on the engine.
func (h *Handler) Mount(e *gin.Engine) {
	e.POST("/v1/auth/register", h.Register)
	e.POST("/v1/auth/verify-phone", h.VerifyPhone)
	e.POST("/v1/auth/resend-otp", h.ResendOTP)
	e.POST("/v1/auth/login", h.Login)
	e.POST("/v1/auth/refresh", h.Refresh)
	e.POST("/v1/auth/logout", h.Logout)
	e.GET("/v1/me", h.RequireAccess(), h.Me)
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

// Login handles POST /v1/auth/login.
func (h *Handler) Login(c *gin.Context) {
	var in LoginInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.Login(c.Request.Context(), in, LoginMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Refresh handles POST /v1/auth/refresh.
func (h *Handler) Refresh(c *gin.Context) {
	var in RefreshInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.Refresh(c.Request.Context(), in, LoginMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Logout handles POST /v1/auth/logout.
func (h *Handler) Logout(c *gin.Context) {
	var in LogoutInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	if err := h.svc.Logout(c.Request.Context(), in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me handles GET /v1/me.
func (h *Handler) Me(c *gin.Context) {
	userID, ok := UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, unauthorized("Missing or invalid Authorization header."))
		return
	}
	out, err := h.svc.Me(c.Request.Context(), userID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
