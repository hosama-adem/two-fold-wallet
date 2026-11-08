package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

const pinLength = 4

// SetPINInput is the PUT /v1/me/pin body.
// Set (no PIN yet): password + pin. Change (PIN exists): old_pin + pin.
type SetPINInput struct {
	Password string `json:"password"`
	OldPIN   string `json:"old_pin"`
	PIN      string `json:"pin"`
}

// PINMeta is server-captured client metadata for the audit row.
type PINMeta struct {
	UserAgent string
	IP        string
	RequestID string
}

// SetOrChangePIN sets a first PIN (password step-up) or changes an existing one (old PIN step-up).
func (s *Service) SetOrChangePIN(ctx context.Context, userID uuid.UUID, in SetPINInput, meta PINMeta) (MessageResponse, error) {
	// Validate the new PIN before any credential work so weak-PIN rejection
	// stays a cheap, uniform path (no argon2 timing differences).
	if err := ValidatePIN(in.PIN); err != nil {
		return MessageResponse{}, err
	}

	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return MessageResponse{}, unauthorized("Invalid or expired access token.")
		}
		return MessageResponse{}, err
	}

	hasPIN := u.PinHash != nil && *u.PinHash != ""
	var action string

	if !hasPIN {
		if in.OldPIN != "" {
			return MessageResponse{}, httpx.ValidationFailed("Password is required to set a PIN. Do not send old_pin.")
		}
		if in.Password == "" {
			return MessageResponse{}, httpx.ValidationFailed("Password is required to set a PIN.")
		}
		ok, verr := VerifyPassword(u.PasswordHash, in.Password)
		if verr != nil || !ok {
			return MessageResponse{}, invalidPassword()
		}
		action = audit.ActionPINSet
	} else {
		if in.Password != "" {
			return MessageResponse{}, httpx.ValidationFailed("Current PIN is required to change PIN. Do not send password.")
		}
		if in.OldPIN == "" {
			return MessageResponse{}, httpx.ValidationFailed("Current PIN is required to change PIN.")
		}
		// Format-check old_pin before argon2 so garbage input stays a cheap
		// validation_failed and does not burn lockout tries.
		if err := validatePINFormat(in.OldPIN); err != nil {
			return MessageResponse{}, httpx.ValidationFailed("Current PIN must be exactly 4 digits.")
		}
		if in.OldPIN == in.PIN {
			return MessageResponse{}, httpx.ValidationFailed("New PIN must be different from the current PIN.")
		}
		// Same lockout path money movement will use — wrong old_pin burns tries.
		if err := s.pins.Verify(ctx, userID, in.OldPIN, pin.Meta{
			UserAgent: meta.UserAgent,
			IP:        meta.IP,
			RequestID: meta.RequestID,
		}); err != nil {
			return MessageResponse{}, err
		}
		action = audit.ActionPINChange
	}

	hash, err := HashPIN(in.PIN)
	if err != nil {
		return MessageResponse{}, httpx.Internal("Could not secure PIN.")
	}

	if err := s.persistPIN(ctx, userID, hash, action, meta); err != nil {
		return MessageResponse{}, err
	}

	if action == audit.ActionPINSet {
		return MessageResponse{Message: "PIN set."}, nil
	}
	return MessageResponse{Message: "PIN changed."}, nil
}

func (s *Service) persistPIN(ctx context.Context, userID uuid.UUID, hash, action string, meta PINMeta) error {
	if s.pool == nil || s.audit == nil {
		// Tests / wiring without audit: still persist the hash.
		if _, err := s.users.UpdatePinHash(ctx, userID, hash); err != nil {
			return err
		}
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := s.users.UpdatePinHashTx(ctx, tx, userID, hash); err != nil {
		return err
	}

	actorID := userID
	targetID := userID
	hadPIN := action == audit.ActionPINChange
	if err := s.audit.RecordTx(ctx, tx, audit.Entry{
		ActorUserID: &actorID,
		ActorKind:   audit.ActorUser,
		Action:      action,
		TargetType:  "user",
		TargetID:    &targetID,
		Summary: map[string]any{
			"pin_set_before": hadPIN,
			"pin_set_after":  true,
		},
		IP:        meta.IP,
		UserAgent: meta.UserAgent,
		RequestID: meta.RequestID,
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ValidatePIN enforces exactly 4 digits and rejects weak patterns.
func ValidatePIN(pin string) error {
	if err := validatePINFormat(pin); err != nil {
		return err
	}
	if isWeakPIN(pin) {
		return httpx.ValidationFailed("PIN is too weak. Choose a less predictable 4-digit PIN.")
	}
	return nil
}

func validatePINFormat(pin string) error {
	// Exactly four ASCII bytes '0'..'9'. Reject Unicode digits and multi-byte
	// runes so length and character-class checks stay aligned.
	if len(pin) != pinLength {
		return httpx.ValidationFailed("PIN must be exactly 4 digits.")
	}
	for i := 0; i < pinLength; i++ {
		c := pin[i]
		if c < '0' || c > '9' {
			return httpx.ValidationFailed("PIN must be exactly 4 digits.")
		}
	}
	return nil
}

// isWeakPIN rejects repeated digits and strict ±1 sequences (0000, 1111, 1234, 2345, 5432, …).
func isWeakPIN(pin string) bool {
	if len(pin) != pinLength {
		return true
	}
	repeated := true
	ascending := true
	descending := true
	for i := 1; i < pinLength; i++ {
		if pin[i] != pin[0] {
			repeated = false
		}
		if pin[i] != pin[i-1]+1 {
			ascending = false
		}
		if pin[i] != pin[i-1]-1 {
			descending = false
		}
	}
	return repeated || ascending || descending
}

// HashPIN returns a PHC-style argon2id hash for a transaction PIN.
func HashPIN(pin string) (string, error) {
	return HashPassword(pin)
}

// VerifyPIN checks a PIN against an encoded argon2id hash.
func VerifyPIN(encoded, pin string) (bool, error) {
	return VerifyPassword(encoded, pin)
}

func invalidPassword() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "invalid_password", "Unauthorized", "Invalid password.")
}
