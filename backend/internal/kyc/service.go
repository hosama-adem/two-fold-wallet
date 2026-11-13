package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/kyc/kycdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	maxDocumentKeys = 8
	maxKeyRunes     = 512
	maxDataBytes    = 16 << 10
)

// Service accepts KYC submissions.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a KYC service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// SubmitInput is the POST /v1/kyc/submissions body.
type SubmitInput struct {
	TargetTier   int16           `json:"target_tier"`
	Data         json.RawMessage `json:"data"`
	DocumentKeys []string        `json:"document_keys"`
}

// Submission is the customer-facing submission record.
type Submission struct {
	ID           uuid.UUID       `json:"id"`
	TargetTier   int16           `json:"target_tier"`
	Data         json.RawMessage `json:"data"`
	DocumentKeys []string        `json:"document_keys"`
	Status       string          `json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Submit records a new KYC submission in status submitted (FR-KYC-1).
func (s *Service) Submit(ctx context.Context, userID uuid.UUID, in SubmitInput) (Submission, error) {
	if s == nil || s.pool == nil {
		return Submission{}, httpx.Internal("KYC store is not configured.")
	}
	if userID == uuid.Nil {
		return Submission{}, httpx.ValidationFailed("User id is required.")
	}
	if err := validateSubmit(in); err != nil {
		return Submission{}, err
	}

	q := kycdb.New(s.pool)
	current, err := q.GetUserKYCTier(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Submission{}, httpx.ValidationFailed("User not found.")
		}
		return Submission{}, err
	}
	if in.TargetTier <= current {
		return Submission{}, httpx.ValidationFailed(
			fmt.Sprintf("Target tier must be higher than your current tier (%d).", current),
		)
	}

	data := in.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	keys := in.DocumentKeys
	if keys == nil {
		keys = []string{}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Submission{}, fmt.Errorf("kyc: id: %w", err)
	}
	row, err := q.InsertSubmission(ctx, kycdb.InsertSubmissionParams{
		ID:           id,
		UserID:       userID,
		TargetTier:   in.TargetTier,
		Data:         data,
		DocumentKeys: keys,
	})
	if err != nil {
		return Submission{}, mapInsertError(err)
	}
	return Submission{
		ID:           row.ID,
		TargetTier:   row.TargetTier,
		Data:         row.Data,
		DocumentKeys: row.DocumentKeys,
		Status:       row.Status,
		CreatedAt:    row.CreatedAt,
	}, nil
}

func validateSubmit(in SubmitInput) error {
	if in.TargetTier != 1 && in.TargetTier != 2 {
		return httpx.ValidationFailed("target_tier must be 1 or 2.")
	}
	if len(in.Data) > maxDataBytes {
		return httpx.ValidationFailed("data is too large.")
	}
	if len(in.Data) > 0 && !json.Valid(in.Data) {
		return httpx.ValidationFailed("data must be a JSON object.")
	}
	if len(in.Data) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(in.Data, &obj); err != nil {
			return httpx.ValidationFailed("data must be a JSON object.")
		}
		if err := validateTierData(in.TargetTier, obj); err != nil {
			return err
		}
	} else {
		return httpx.ValidationFailed("data is required.")
	}
	if len(in.DocumentKeys) > maxDocumentKeys {
		return httpx.ValidationFailed("At most 8 document_keys are allowed.")
	}
	for _, key := range in.DocumentKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return httpx.ValidationFailed("document_keys must not contain empty values.")
		}
		if utf8.RuneCountInString(key) > maxKeyRunes {
			return httpx.ValidationFailed("document_keys entries must be at most 512 characters.")
		}
	}
	return nil
}

func validateTierData(tier int16, obj map[string]any) error {
	required := []string{"full_name", "date_of_birth", "national_id"}
	if tier == 2 {
		required = append(required, "business_name", "tin", "trade_license_number")
	}
	for _, key := range required {
		v, ok := obj[key]
		if !ok {
			return httpx.ValidationFailed(fmt.Sprintf("data.%s is required.", key))
		}
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return httpx.ValidationFailed(fmt.Sprintf("data.%s must be a non-empty string.", key))
		}
	}
	return nil
}

func mapInsertError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "kyc_submissions_one_open_uidx" {
			return httpx.Conflict("You already have a KYC submission awaiting review.")
		}
	}
	return fmt.Errorf("kyc: insert: %w", err)
}
