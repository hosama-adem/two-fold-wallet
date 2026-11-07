package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
)

// Handler exposes auth HTTP endpoints.
type Handler struct {
	svc     *Service
	limiter ratelimit.Limiter
}

// NewHandler builds the auth HTTP adapter.
func NewHandler(svc *Service, limiter ratelimit.Limiter) *Handler {
	if limiter == nil {
		limiter = ratelimit.NewMemory(ratelimit.Config{})
	}
	return &Handler{svc: svc, limiter: limiter}
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
	e.PUT("/v1/me/pin", h.RequireAccess(), h.SetPIN)
	e.GET("/v1/me/sessions", h.RequireAccess(), h.ListSessions)
	e.DELETE("/v1/me/sessions/:id", h.RequireAccess(), h.RevokeSession)
}

func (h *Handler) enforceAuthLimit(c *gin.Context, route, phone string) bool {
	keys := []string{"ip:" + c.ClientIP() + ":" + route}
	if phone != "" {
		keys = append(keys, "phone:"+phone+":"+route)
	}
	res, err := ratelimit.AllowAll(c.Request.Context(), h.limiter, keys...)
	if err != nil {
		httpx.WriteError(c, httpx.Internal("Rate limiter unavailable."))
		return false
	}
	if !res.Allowed {
		httpx.WriteError(c, httpx.RateLimited(res.RetryAfter))
		return false
	}
	return true
}

func phoneKey(raw string) string {
	phone, err := NormalizePhone(raw)
	if err != nil {
		return ""
	}
	return phone
}

// Register handles POST /v1/auth/register.
func (h *Handler) Register(c *gin.Context) {
	var in RegisterInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	if !h.enforceAuthLimit(c, "register", phoneKey(in.Phone)) {
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
	if !h.enforceAuthLimit(c, "verify-phone", phoneKey(in.Phone)) {
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
	if !h.enforceAuthLimit(c, "resend-otp", phoneKey(in.Phone)) {
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
	if !h.enforceAuthLimit(c, "login", phoneKey(in.Phone)) {
		return
	}
	out, err := h.svc.Login(c.Request.Context(), in, LoginMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
		RequestID: httpx.RequestIDFromContext(c),
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
		RequestID: httpx.RequestIDFromContext(c),
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
	if err := h.svc.Logout(c.Request.Context(), in, LoginMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
		RequestID: httpx.RequestIDFromContext(c),
	}); err != nil {
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

// SetPIN handles PUT /v1/me/pin (set or change).
func (h *Handler) SetPIN(c *gin.Context) {
	userID, ok := UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, unauthorized("Missing or invalid Authorization header."))
		return
	}
	var in SetPINInput
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.WriteError(c, err)
		return
	}
	out, err := h.svc.SetOrChangePIN(c.Request.Context(), userID, in, PINMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
		RequestID: httpx.RequestIDFromContext(c),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ListSessions handles GET /v1/me/sessions.
func (h *Handler) ListSessions(c *gin.Context) {
	userID, ok := UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, unauthorized("Missing or invalid Authorization header."))
		return
	}
	sid, _ := SessionIDFromContext(c)
	out, err := h.svc.ListSessions(c.Request.Context(), userID, sid)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// RevokeSession handles DELETE /v1/me/sessions/:id.
func (h *Handler) RevokeSession(c *gin.Context) {
	userID, ok := UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, unauthorized("Missing or invalid Authorization header."))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, httpx.ValidationFailed("Session id must be a UUID."))
		return
	}
	if err := h.svc.RevokeSession(c.Request.Context(), userID, id, RevokeSessionMeta{
		UserAgent: c.Request.UserAgent(),
		IP:        c.ClientIP(),
		RequestID: httpx.RequestIDFromContext(c),
	}); err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
