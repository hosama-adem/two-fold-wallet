package auth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	jwtIssuer   = "twofold"
	jwtAudience = "twofold-app"
)

// AccessClaims are the JWT claims embedded in short-lived access tokens.
type AccessClaims struct {
	SessionID uuid.UUID `json:"sid"`
	jwt.RegisteredClaims
}

// TokenSigner issues and validates Ed25519 (EdDSA) access JWTs.
type TokenSigner struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	kid     string
	ttl     time.Duration
	clock   Clock
}

// NewTokenSigner parses a PKCS8 PEM Ed25519 private key.
func NewTokenSigner(pemBytes []byte, kid string, ttl time.Duration, clock Clock) (*TokenSigner, error) {
	if kid == "" {
		kid = "dev-1"
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if clock == nil {
		clock = systemClock{}
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("jwt: no PEM block in signing key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("jwt: parse pkcs8: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("jwt: signing key must be Ed25519")
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("jwt: missing Ed25519 public key")
	}
	return &TokenSigner{private: priv, public: pub, kid: kid, ttl: ttl, clock: clock}, nil
}

// IssueAccessToken builds a signed access JWT for the user/session pair.
func (s *TokenSigner) IssueAccessToken(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := s.clock.Now()
	exp := now.Add(s.ttl)
	claims := AccessClaims{
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    jwtIssuer,
			Audience:  []string{jwtAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        sessionID.String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.kid
	signed, err := token.SignedString(s.private)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// ParseAccessToken validates signature, expiry, issuer, audience, and required claims.
func (s *TokenSigner) ParseAccessToken(raw string) (AccessClaims, error) {
	var claims AccessClaims
	token, err := jwt.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodEdDSA {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.public, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return s.clock.Now() }),
	)
	if err != nil || !token.Valid {
		return AccessClaims{}, err
	}
	if claims.Subject == "" {
		return AccessClaims{}, fmt.Errorf("missing sub")
	}
	if claims.SessionID == uuid.Nil {
		return AccessClaims{}, fmt.Errorf("missing sid")
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return AccessClaims{}, fmt.Errorf("invalid sub")
	}
	return claims, nil
}

// AccessTTL returns the configured access-token lifetime.
func (s *TokenSigner) AccessTTL() time.Duration { return s.ttl }

// WithClock overrides the clock used for iat/exp (tests).
func (s *TokenSigner) WithClock(c Clock) *TokenSigner {
	s.clock = c
	return s
}
