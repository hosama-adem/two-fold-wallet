package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/idempotency/idempotencydb"
)

const (
	stateInProgress = "in_progress"
	stateCompleted  = "completed"
)

// Clock supplies the current time (overridable in tests).
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Store persists idempotency claims and completed responses.
type Store struct {
	pool  *pgxpool.Pool
	q     *idempotencydb.Queries
	clock Clock
	ttl   time.Duration
}

// NewStore builds a store on a pgx pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:  pool,
		q:     idempotencydb.New(pool),
		clock: systemClock{},
		ttl:   TTL,
	}
}

// WithClock overrides the wall clock (tests).
func (s *Store) WithClock(c Clock) *Store {
	if c != nil {
		s.clock = c
	}
	return s
}

// ClaimResult is the outcome of trying to own a key for this request.
type ClaimResult struct {
	// Won is true when this caller inserted (or reclaimed) the in_progress row.
	Won bool
	// Existing is set when Won is false (live row already present).
	Existing *Record
}

// Record is a stored idempotency row.
type Record struct {
	UserID              uuid.UUID
	Key                 string
	RequestHash         string
	State               string
	ResponseCode        int
	ResponseBody        []byte
	ResponseContentType string
	CreatedAt           time.Time
	ExpiresAt           time.Time
}

// Claim inserts an in_progress row for (userID, key), or returns the live row.
// Expired rows are reclaimed atomically (see ClaimKey SQL).
func (s *Store) Claim(ctx context.Context, userID uuid.UUID, key, requestHash string) (ClaimResult, error) {
	return s.claim(ctx, s.q, userID, key, requestHash)
}

// ClaimTx is Claim inside an existing transaction (for Day 15 money writes).
func (s *Store) ClaimTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key, requestHash string) (ClaimResult, error) {
	return s.claim(ctx, s.q.WithTx(tx), userID, key, requestHash)
}

func (s *Store) claim(ctx context.Context, q *idempotencydb.Queries, userID uuid.UUID, key, requestHash string) (ClaimResult, error) {
	now := s.clock.Now().UTC()
	row, err := q.ClaimKey(ctx, idempotencydb.ClaimKeyParams{
		UserID:      userID,
		Key:         key,
		RequestHash: requestHash,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.ttl),
	})
	if err == nil {
		return ClaimResult{Won: true, Existing: recordFrom(row)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ClaimResult{}, fmt.Errorf("idempotency claim: %w", err)
	}

	existing, err := q.GetKey(ctx, idempotencydb.GetKeyParams{
		UserID: userID,
		Key:    key,
	})
	if err != nil {
		return ClaimResult{}, fmt.Errorf("idempotency get after conflict: %w", err)
	}
	return ClaimResult{Won: false, Existing: recordFrom(existing)}, nil
}

// StoredResponse is what Complete persists for replay.
type StoredResponse struct {
	StatusCode  int
	Body        []byte
	ContentType string
}

// Complete marks an in_progress key as completed with the captured response.
func (s *Store) Complete(ctx context.Context, userID uuid.UUID, key string, resp StoredResponse) error {
	return s.complete(ctx, s.q, userID, key, resp)
}

// CompleteTx is Complete inside an existing transaction.
func (s *Store) CompleteTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string, resp StoredResponse) error {
	return s.complete(ctx, s.q.WithTx(tx), userID, key, resp)
}

func (s *Store) complete(ctx context.Context, q *idempotencydb.Queries, userID uuid.UUID, key string, resp StoredResponse) error {
	if resp.StatusCode < 100 || resp.StatusCode > 599 {
		return fmt.Errorf("idempotency complete: invalid status %d", resp.StatusCode)
	}
	ct := resp.ContentType
	if ct == "" {
		ct = "application/json"
	}
	code := int32(resp.StatusCode)
	body := resp.Body
	if body == nil {
		body = []byte{}
	}
	_, err := q.CompleteKey(ctx, idempotencydb.CompleteKeyParams{
		UserID:              userID,
		Key:                 key,
		ResponseCode:        &code,
		ResponseBody:        body,
		ResponseContentType: &ct,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotClaimed
	}
	if err != nil {
		return fmt.Errorf("idempotency complete: %w", err)
	}
	return nil
}

// Release deletes an in_progress row so a retry may claim again (used on 5xx).
func (s *Store) Release(ctx context.Context, userID uuid.UUID, key string) error {
	return s.release(ctx, s.q, userID, key)
}

// ReleaseTx is Release inside an existing transaction.
func (s *Store) ReleaseTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string) error {
	return s.release(ctx, s.q.WithTx(tx), userID, key)
}

func (s *Store) release(ctx context.Context, q *idempotencydb.Queries, userID uuid.UUID, key string) error {
	if err := q.ReleaseKey(ctx, idempotencydb.ReleaseKeyParams{
		UserID: userID,
		Key:    key,
	}); err != nil {
		return fmt.Errorf("idempotency release: %w", err)
	}
	return nil
}

// DeleteExpired removes rows whose expires_at is at or before asOf.
// The Day 25 job calls this; tests use it directly.
func (s *Store) DeleteExpired(ctx context.Context, asOf time.Time) (int64, error) {
	n, err := s.q.DeleteExpired(ctx, asOf.UTC())
	if err != nil {
		return 0, fmt.Errorf("idempotency delete expired: %w", err)
	}
	return n, nil
}

func recordFrom(row idempotencydb.IdempotencyKey) *Record {
	r := &Record{
		UserID:      row.UserID,
		Key:         row.Key,
		RequestHash: row.RequestHash,
		State:       row.State,
		CreatedAt:   row.CreatedAt,
		ExpiresAt:   row.ExpiresAt,
	}
	if row.ResponseCode != nil {
		r.ResponseCode = int(*row.ResponseCode)
	}
	if row.ResponseBody != nil {
		r.ResponseBody = append([]byte(nil), row.ResponseBody...)
	}
	if row.ResponseContentType != nil {
		r.ResponseContentType = *row.ResponseContentType
	}
	return r
}
