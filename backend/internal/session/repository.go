package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/session/sessiondb"
)

// Sentinel errors for callers above the repository.
var (
	ErrNotFound      = errors.New("session not found")
	ErrReuseDetected = errors.New("refresh token reuse detected")
	ErrExpired       = errors.New("session expired")
)

// Repository persists sessions through sqlc-generated queries.
type Repository struct {
	pool *pgxpool.Pool
	q    *sessiondb.Queries
}

// NewRepository builds a session repository on a pgx pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sessiondb.New(pool)}
}

// CreateParams is the input for inserting a session row.
type CreateParams struct {
	UserID      uuid.UUID
	FamilyID    uuid.UUID
	RefreshHash string
	UserAgent   *string
	IP          *string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// Create inserts a session with a fresh UUIDv7.
func (r *Repository) Create(ctx context.Context, p CreateParams) (sessiondb.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return sessiondb.Session{}, fmt.Errorf("uuid v7: %w", err)
	}
	familyID := p.FamilyID
	if familyID == uuid.Nil {
		familyID = id
	}
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return r.q.CreateSession(ctx, sessiondb.CreateSessionParams{
		ID:          id,
		UserID:      p.UserID,
		FamilyID:    familyID,
		RefreshHash: p.RefreshHash,
		UserAgent:   p.UserAgent,
		Ip:          p.IP,
		ExpiresAt:   p.ExpiresAt,
		CreatedAt:   createdAt,
	})
}

// GetByID loads a session by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (sessiondb.Session, error) {
	s, err := r.q.GetSessionByID(ctx, id)
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	return s, nil
}

// GetByRefreshHash loads a session by stored refresh hash.
func (r *Repository) GetByRefreshHash(ctx context.Context, hash string) (sessiondb.Session, error) {
	s, err := r.q.GetSessionByRefreshHash(ctx, hash)
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	return s, nil
}

// RotateParams describes the new session created during refresh rotation.
type RotateParams struct {
	RefreshHash string
	UserAgent   *string
	IP          *string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	Now         time.Time
}

// RotateResult is the outcome of a successful rotation.
type RotateResult struct {
	Old sessiondb.Session
	New sessiondb.Session
}

// Rotate locks the current session by refresh hash, detects reuse, and inserts a replacement.
func (r *Repository) Rotate(ctx context.Context, refreshHash string, p RotateParams) (RotateResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RotateResult{}, err
	}
	defer tx.Rollback(ctx)

	q := r.q.WithTx(tx)
	cur, err := q.GetSessionByRefreshHashForUpdate(ctx, refreshHash)
	if err != nil {
		return RotateResult{}, mapReadError(err)
	}

	if cur.ReplacedBy != nil || cur.RevokedAt != nil {
		if _, revErr := q.RevokeSessionFamily(ctx, cur.FamilyID); revErr != nil {
			return RotateResult{}, revErr
		}
		if err := tx.Commit(ctx); err != nil {
			return RotateResult{}, err
		}
		return RotateResult{}, ErrReuseDetected
	}

	now := p.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.After(cur.ExpiresAt) {
		if err := tx.Commit(ctx); err != nil {
			return RotateResult{}, err
		}
		return RotateResult{}, ErrExpired
	}

	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return RotateResult{}, fmt.Errorf("uuid v7: %w", err)
	}

	next, err := q.CreateSession(ctx, sessiondb.CreateSessionParams{
		ID:          newID,
		UserID:      cur.UserID,
		FamilyID:    cur.FamilyID,
		RefreshHash: p.RefreshHash,
		UserAgent:   firstNonNil(p.UserAgent, cur.UserAgent),
		Ip:          firstNonNil(p.IP, cur.Ip),
		ExpiresAt:   p.ExpiresAt,
		CreatedAt:   createdAt,
	})
	if err != nil {
		return RotateResult{}, err
	}

	old, err := q.MarkSessionReplaced(ctx, sessiondb.MarkSessionReplacedParams{
		ID:         cur.ID,
		ReplacedBy: &newID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, revErr := q.RevokeSessionFamily(ctx, cur.FamilyID); revErr != nil {
				return RotateResult{}, revErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return RotateResult{}, commitErr
			}
			return RotateResult{}, ErrReuseDetected
		}
		return RotateResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RotateResult{}, err
	}
	return RotateResult{Old: old, New: next}, nil
}

// Revoke marks a single session revoked if still active.
func (r *Repository) Revoke(ctx context.Context, id uuid.UUID) (sessiondb.Session, error) {
	s, err := r.q.RevokeSession(ctx, id)
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	return s, nil
}

// RevokeByRefreshHash revokes the session that owns the given refresh hash.
func (r *Repository) RevokeByRefreshHash(ctx context.Context, refreshHash string) (sessiondb.Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return sessiondb.Session{}, err
	}
	defer tx.Rollback(ctx)

	q := r.q.WithTx(tx)
	cur, err := q.GetSessionByRefreshHashForUpdate(ctx, refreshHash)
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	if cur.RevokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return sessiondb.Session{}, err
		}
		return cur, nil
	}
	s, err := q.RevokeSession(ctx, cur.ID)
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sessiondb.Session{}, err
	}
	return s, nil
}

// RevokeFamily revokes every active session in a token family.
func (r *Repository) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	_, err := r.q.RevokeSessionFamily(ctx, familyID)
	return err
}

// ListActive returns non-revoked, non-expired sessions for a user.
func (r *Repository) ListActive(ctx context.Context, userID uuid.UUID, now time.Time) ([]sessiondb.Session, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := r.q.ListActiveSessionsByUser(ctx, sessiondb.ListActiveSessionsByUserParams{
		UserID:    userID,
		ExpiresAt: now,
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []sessiondb.Session{}, nil
	}
	return rows, nil
}

// RevokeForUser revokes a session owned by userID.
func (r *Repository) RevokeForUser(ctx context.Context, sessionID, userID uuid.UUID) (sessiondb.Session, error) {
	s, err := r.q.RevokeSessionForUser(ctx, sessiondb.RevokeSessionForUserParams{
		ID:     sessionID,
		UserID: userID,
	})
	if err != nil {
		return sessiondb.Session{}, mapReadError(err)
	}
	return s, nil
}

func mapReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func firstNonNil(primary, fallback *string) *string {
	if primary != nil {
		return primary
	}
	return fallback
}
