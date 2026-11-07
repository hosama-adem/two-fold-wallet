package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/user/userdb"
)

// Sentinel errors for callers above the repository.
var (
	ErrNotFound     = errors.New("user not found")
	ErrPhoneTaken   = errors.New("phone number already registered")
	ErrHandleTaken  = errors.New("handle already taken")
	ErrInvalidPhone = errors.New("phone number failed validation")
)

// Repository persists users through sqlc-generated queries.
type Repository struct {
	pool *pgxpool.Pool
	q    *userdb.Queries
}

// NewRepository builds a user repository on a pgx pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: userdb.New(pool)}
}

// CreateParams is the input for inserting a user row.
type CreateParams struct {
	PhoneE164    string
	Handle       *string
	DisplayName  string
	PasswordHash string
	KYCTier      int16
	Status       string
}

// Create inserts a user with a fresh UUIDv7.
func (r *Repository) Create(ctx context.Context, p CreateParams) (userdb.User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return userdb.User{}, fmt.Errorf("uuid v7: %w", err)
	}

	status := p.Status
	if status == "" {
		status = "pending_verification"
	}

	u, err := r.q.CreateUser(ctx, userdb.CreateUserParams{
		ID:           id,
		PhoneE164:    p.PhoneE164,
		Handle:       p.Handle,
		DisplayName:  p.DisplayName,
		PasswordHash: p.PasswordHash,
		KycTier:      p.KYCTier,
		Status:       status,
	})
	if err != nil {
		return userdb.User{}, mapWriteError(err)
	}
	return u, nil
}

// GetByID loads a user by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (userdb.User, error) {
	u, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		return userdb.User{}, mapReadError(err)
	}
	return u, nil
}

// GetByPhone loads a user by E.164 phone.
func (r *Repository) GetByPhone(ctx context.Context, phone string) (userdb.User, error) {
	u, err := r.q.GetUserByPhone(ctx, phone)
	if err != nil {
		return userdb.User{}, mapReadError(err)
	}
	return u, nil
}

// MarkPhoneVerified stamps verification and activates pending users.
func (r *Repository) MarkPhoneVerified(ctx context.Context, id uuid.UUID) (userdb.User, error) {
	u, err := r.q.MarkPhoneVerified(ctx, id)
	if err != nil {
		return userdb.User{}, mapReadError(err)
	}
	return u, nil
}

// UpdatePinHash stores an argon2id PIN hash and clears lockout counters.
func (r *Repository) UpdatePinHash(ctx context.Context, id uuid.UUID, pinHash string) (userdb.User, error) {
	return r.updatePinHash(ctx, r.q, id, pinHash)
}

// UpdatePinHashTx is UpdatePinHash inside an existing transaction.
func (r *Repository) UpdatePinHashTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, pinHash string) (userdb.User, error) {
	return r.updatePinHash(ctx, r.q.WithTx(tx), id, pinHash)
}

func (r *Repository) updatePinHash(ctx context.Context, q *userdb.Queries, id uuid.UUID, pinHash string) (userdb.User, error) {
	u, err := q.UpdateUserPinHash(ctx, userdb.UpdateUserPinHashParams{
		ID:      id,
		PinHash: &pinHash,
	})
	if err != nil {
		return userdb.User{}, mapReadError(err)
	}
	return u, nil
}

func mapReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "users_phone_e164_unique":
			return ErrPhoneTaken
		case "users_handle_unique":
			return ErrHandleTaken
		case "users_phone_e164_format":
			return ErrInvalidPhone
		}
		// Unique_violation without named constraint match
		if pgErr.Code == "23505" {
			return ErrPhoneTaken
		}
		if pgErr.Code == "23514" { // check_violation
			return ErrInvalidPhone
		}
	}
	return err
}
