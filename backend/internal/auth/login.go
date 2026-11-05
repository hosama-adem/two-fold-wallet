package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

const (
	genericInvalidCredentials = "Invalid phone or password."
	genericInvalidRefresh     = "Invalid or expired refresh token."
)

// dummyPasswordHash is used when no user exists so argon2id still runs (timing).
var dummyPasswordHash string

func init() {
	h, err := HashPassword("timing-dummy-password-xx")
	if err != nil {
		panic("auth: dummy password hash: " + err.Error())
	}
	dummyPasswordHash = h
}

// LoginInput is the login request body.
type LoginInput struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// TokenResponse is the OAuth-style token payload.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// LoginMeta is server-captured client metadata for the session row.
type LoginMeta struct {
	UserAgent string
	IP        string
}

// Login authenticates an active verified user and opens a session.
func (s *Service) Login(ctx context.Context, in LoginInput, meta LoginMeta) (TokenResponse, error) {
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		// Validation failed shapes stay 422; do not treat as credential miss.
		return TokenResponse{}, err
	}
	password := in.Password
	if password == "" {
		return TokenResponse{}, httpx.ValidationFailed("Password is required.")
	}

	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		_, _ = VerifyPassword(dummyPasswordHash, password)
		if errors.Is(err, user.ErrNotFound) {
			return TokenResponse{}, invalidCredentials()
		}
		return TokenResponse{}, err
	}

	ok, verr := VerifyPassword(u.PasswordHash, password)
	if verr != nil || !ok {
		return TokenResponse{}, invalidCredentials()
	}
	if u.Status != "active" || u.PhoneVerifiedAt == nil {
		return TokenResponse{}, invalidCredentials()
	}

	return s.issueSession(ctx, u.ID, uuid.Nil, meta)
}

// RefreshInput is the refresh request body.
type RefreshInput struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh rotates a valid refresh token and returns a new token pair.
func (s *Service) Refresh(ctx context.Context, in RefreshInput, meta LoginMeta) (TokenResponse, error) {
	plain := strings.TrimSpace(in.RefreshToken)
	if plain == "" {
		return TokenResponse{}, httpx.ValidationFailed("Refresh token is required.")
	}
	hash := HashRefreshToken(plain)

	refreshPlain, refreshHash, err := NewRefreshToken()
	if err != nil {
		return TokenResponse{}, httpx.Internal("Could not issue refresh token.")
	}

	now := s.clock.Now()
	result, err := s.sessions.Rotate(ctx, hash, session.RotateParams{
		RefreshHash: refreshHash,
		UserAgent:   optionalString(meta.UserAgent),
		IP:          optionalString(meta.IP),
		ExpiresAt:   now.Add(s.refreshTTL),
		CreatedAt:   now,
		Now:         now,
	})
	if err != nil {
		if errors.Is(err, session.ErrReuseDetected) ||
			errors.Is(err, session.ErrNotFound) ||
			errors.Is(err, session.ErrExpired) {
			return TokenResponse{}, invalidRefresh()
		}
		return TokenResponse{}, err
	}

	access, _, err := s.tokens.IssueAccessToken(result.New.UserID, result.New.ID)
	if err != nil {
		return TokenResponse{}, httpx.Internal("Could not issue access token.")
	}
	return TokenResponse{
		AccessToken:  access,
		RefreshToken: refreshPlain,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.tokens.AccessTTL() / time.Second),
	}, nil
}

// LogoutInput is the logout request body.
type LogoutInput struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout revokes the session bound to the refresh token.
func (s *Service) Logout(ctx context.Context, in LogoutInput) error {
	plain := strings.TrimSpace(in.RefreshToken)
	if plain == "" {
		return httpx.ValidationFailed("Refresh token is required.")
	}
	_, err := s.sessions.RevokeByRefreshHash(ctx, HashRefreshToken(plain))
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return invalidRefresh()
		}
		return err
	}
	return nil
}

// MeProfile is the authenticated profile payload.
type MeProfile struct {
	ID              uuid.UUID  `json:"id"`
	Phone           string     `json:"phone"`
	Handle          *string    `json:"handle"`
	DisplayName     string     `json:"display_name"`
	KYCTier         int16      `json:"kyc_tier"`
	Status          string     `json:"status"`
	PhoneVerifiedAt *time.Time `json:"phone_verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Me returns the profile for the authenticated user.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (MeProfile, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return MeProfile{}, unauthorized("Invalid or expired access token.")
		}
		return MeProfile{}, err
	}
	return MeProfile{
		ID:              u.ID,
		Phone:           u.PhoneE164,
		Handle:          u.Handle,
		DisplayName:     u.DisplayName,
		KYCTier:         u.KycTier,
		Status:          u.Status,
		PhoneVerifiedAt: u.PhoneVerifiedAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}, nil
}

func (s *Service) issueSession(ctx context.Context, userID, familyID uuid.UUID, meta LoginMeta) (TokenResponse, error) {
	refreshPlain, refreshHash, err := NewRefreshToken()
	if err != nil {
		return TokenResponse{}, httpx.Internal("Could not issue refresh token.")
	}
	now := s.clock.Now()
	sess, err := s.sessions.Create(ctx, session.CreateParams{
		UserID:      userID,
		FamilyID:    familyID,
		RefreshHash: refreshHash,
		UserAgent:   optionalString(meta.UserAgent),
		IP:          optionalString(meta.IP),
		ExpiresAt:   now.Add(s.refreshTTL),
		CreatedAt:   now,
	})
	if err != nil {
		return TokenResponse{}, err
	}

	access, _, err := s.tokens.IssueAccessToken(userID, sess.ID)
	if err != nil {
		return TokenResponse{}, httpx.Internal("Could not issue access token.")
	}
	return TokenResponse{
		AccessToken:  access,
		RefreshToken: refreshPlain,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.tokens.AccessTTL() / time.Second),
	}, nil
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func invalidCredentials() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "invalid_credentials", "Unauthorized", genericInvalidCredentials)
}

func invalidRefresh() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "invalid_refresh_token", "Unauthorized", genericInvalidRefresh)
}
