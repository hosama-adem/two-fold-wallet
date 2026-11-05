package user

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/user/userdb"
)

const PurposeVerifyPhone = "verify_phone"

// CreateOTPParams is input for inserting an OTP row.
type CreateOTPParams struct {
	PhoneE164 string
	Purpose   string
	CodeHash  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateOTP inserts a new OTP code.
func (r *Repository) CreateOTP(ctx context.Context, p CreateOTPParams) (userdb.OtpCode, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return userdb.OtpCode{}, fmt.Errorf("uuid v7: %w", err)
	}
	purpose := p.Purpose
	if purpose == "" {
		purpose = PurposeVerifyPhone
	}
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return r.q.CreateOTP(ctx, userdb.CreateOTPParams{
		ID:        id,
		PhoneE164: p.PhoneE164,
		Purpose:   purpose,
		CodeHash:  p.CodeHash,
		ExpiresAt: p.ExpiresAt,
		CreatedAt: createdAt,
	})
}

// LatestOTP returns the most recent OTP for phone+purpose (any state).
func (r *Repository) LatestOTP(ctx context.Context, phone, purpose string) (userdb.OtpCode, error) {
	o, err := r.q.GetLatestOTPByPhonePurpose(ctx, userdb.GetLatestOTPByPhonePurposeParams{
		PhoneE164: phone,
		Purpose:   purpose,
	})
	if err != nil {
		return userdb.OtpCode{}, mapReadError(err)
	}
	return o, nil
}

// LatestActiveOTP returns the latest unconsumed OTP for phone+purpose.
func (r *Repository) LatestActiveOTP(ctx context.Context, phone, purpose string) (userdb.OtpCode, error) {
	o, err := r.q.GetLatestActiveOTPByPhonePurpose(ctx, userdb.GetLatestActiveOTPByPhonePurposeParams{
		PhoneE164: phone,
		Purpose:   purpose,
	})
	if err != nil {
		return userdb.OtpCode{}, mapReadError(err)
	}
	return o, nil
}

// IncrementOTPAttempts bumps the attempt counter.
func (r *Repository) IncrementOTPAttempts(ctx context.Context, id uuid.UUID) (userdb.OtpCode, error) {
	o, err := r.q.IncrementOTPAttempts(ctx, id)
	if err != nil {
		return userdb.OtpCode{}, mapReadError(err)
	}
	return o, nil
}

// ConsumeOTP marks an OTP as used.
func (r *Repository) ConsumeOTP(ctx context.Context, id uuid.UUID) (userdb.OtpCode, error) {
	o, err := r.q.ConsumeOTP(ctx, id)
	if err != nil {
		return userdb.OtpCode{}, mapReadError(err)
	}
	return o, nil
}
