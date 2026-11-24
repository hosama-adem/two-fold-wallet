package funding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/funding/fundingdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	destinationMobileMoney = "mobile_money"
	destinationBankAccount = "bank_account"
	maxDestinationLabel    = 80
)

// DestinationInput is POST /v1/payout-destinations.
type DestinationInput struct {
	UserID     uuid.UUID
	Provider   string
	Kind       string
	Label      string
	Identifier string
}

// Destination is the customer-facing saved payout target (never plaintext).
type Destination struct {
	ID                uuid.UUID `json:"id"`
	Provider          string    `json:"provider"`
	Kind              string    `json:"kind"`
	Label             string    `json:"label"`
	IdentifierMasked  string    `json:"identifier_masked"`
	CreatedAt         time.Time `json:"created_at"`
}

// CreateDestination seals the identifier and stores a masked copy (FR-WDR-1).
func (s *Service) CreateDestination(ctx context.Context, in DestinationInput) (Destination, error) {
	if s.pool == nil || s.box == nil {
		return Destination{}, httpx.Internal("Payout destinations are not configured.")
	}
	providerName, err := normalizeProvider(in.Provider)
	if err != nil {
		return Destination{}, err
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != destinationMobileMoney && kind != destinationBankAccount {
		return Destination{}, httpx.ValidationFailed("Destination kind must be mobile_money or bank_account.")
	}
	label := strings.TrimSpace(in.Label)
	if label == "" || utf8.RuneCountInString(label) > maxDestinationLabel {
		return Destination{}, httpx.ValidationFailed("Label must be 1–80 characters.")
	}
	identifier := strings.TrimSpace(in.Identifier)
	if identifier == "" || utf8.RuneCountInString(identifier) > 64 {
		return Destination{}, errDestinationIdentifier()
	}
	masked, err := MaskIdentifier(kind, identifier)
	if err != nil {
		return Destination{}, err
	}
	ct, nonce, err := s.box.Seal([]byte(identifier))
	if err != nil {
		return Destination{}, httpx.Internal("Could not encrypt destination.")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Destination{}, fmt.Errorf("funding: destination id: %w", err)
	}
	row, err := fundingdb.New(s.pool).InsertPayoutDestination(ctx, fundingdb.InsertPayoutDestinationParams{
		ID:                   id,
		UserID:               in.UserID,
		Provider:             providerName,
		Kind:                 kind,
		Label:                label,
		IdentifierCiphertext: ct,
		IdentifierNonce:      nonce,
		IdentifierMasked:     masked,
	})
	if err != nil {
		return Destination{}, err
	}
	return mapDestination(row), nil
}

// ListDestinations returns the caller's active destinations.
func (s *Service) ListDestinations(ctx context.Context, userID uuid.UUID) ([]Destination, error) {
	if s.pool == nil {
		return nil, httpx.Internal("Payout destinations are not configured.")
	}
	rows, err := fundingdb.New(s.pool).ListPayoutDestinations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Destination, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapDestination(row))
	}
	return out, nil
}

// DeleteDestination soft-deletes a destination owned by userID.
func (s *Service) DeleteDestination(ctx context.Context, userID, id uuid.UUID) error {
	if s.pool == nil {
		return httpx.Internal("Payout destinations are not configured.")
	}
	_, err := fundingdb.New(s.pool).SoftDeletePayoutDestination(ctx, fundingdb.SoftDeletePayoutDestinationParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errDestinationNotFound()
		}
		return err
	}
	return nil
}

func (s *Service) loadDestinationPlain(ctx context.Context, userID, id uuid.UUID) (fundingdb.PayoutDestination, string, error) {
	row, err := fundingdb.New(s.pool).GetPayoutDestination(ctx, fundingdb.GetPayoutDestinationParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fundingdb.PayoutDestination{}, "", errDestinationNotFound()
		}
		return fundingdb.PayoutDestination{}, "", err
	}
	if s.box == nil {
		return fundingdb.PayoutDestination{}, "", httpx.Internal("Payout destinations are not configured.")
	}
	plain, err := s.box.Open(row.IdentifierCiphertext, row.IdentifierNonce)
	if err != nil {
		return fundingdb.PayoutDestination{}, "", httpx.Internal("Could not decrypt destination.")
	}
	return row, string(plain), nil
}

func mapDestination(row fundingdb.PayoutDestination) Destination {
	return Destination{
		ID:               row.ID,
		Provider:         row.Provider,
		Kind:             row.Kind,
		Label:            row.Label,
		IdentifierMasked: row.IdentifierMasked,
		CreatedAt:        row.CreatedAt,
	}
}
