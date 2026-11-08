package pin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

const (
	// MaxFailures is how many wrong PINs trigger a lockout.
	MaxFailures = 5
	// LockDuration is how long money movement stays blocked after MaxFailures.
	LockDuration = 30 * time.Minute
)

// MatchFunc checks a raw PIN against a stored argon2id hash.
// Injected so this package does not import auth (avoids an import cycle).
type MatchFunc func(encoded, pin string) (bool, error)

// Clock abstracts time for lockout expiry tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Meta is server-captured client metadata for audit rows (never includes the PIN).
type Meta struct {
	UserAgent string
	IP        string
	RequestID string
}

// Service verifies transaction PINs with concurrent-safe failure counters.
type Service struct {
	users *user.Repository
	audit *audit.Recorder
	match MatchFunc
	log   *slog.Logger
	clock Clock
}

// Deps wires the PIN verifier.
type Deps struct {
	Users *user.Repository
	Audit *audit.Recorder
	Match MatchFunc
	Log   *slog.Logger
	Clock Clock
}

// New builds a PIN verifier. Match must be non-nil (typically auth.VerifyPIN).
func New(deps Deps) *Service {
	if deps.Match == nil {
		panic("pin: Match is required")
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	clk := deps.Clock
	if clk == nil {
		clk = systemClock{}
	}
	return &Service{
		users: deps.Users,
		audit: deps.Audit,
		match: deps.Match,
		log:   log,
		clock: clk,
	}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(c Clock) *Service {
	s.clock = c
	return s
}

// Verify checks a transaction PIN for the user (FR-ID-7).
//
// Rules:
//   - Already locked (pin_locked_until > now): 403 pin_locked, no argon2, no counter bump.
//   - Expired lock: counters cleared, then verify proceeds.
//   - Wrong PIN: atomic increment; at MaxFailures sets a LockDuration lockout.
//   - Success: resets pin_failed_count and clears pin_locked_until.
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, rawPIN string, meta Meta) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Invalid or expired access token.")
		}
		return err
	}

	now := s.clock.Now()

	if u.PinLockedUntil != nil && u.PinLockedUntil.After(now) {
		return lockedErr(*u.PinLockedUntil)
	}

	// Lazy unlock after expiry so the next window starts at zero failures.
	if u.PinLockedUntil != nil {
		cleared, cerr := s.users.ResetPinLockout(ctx, userID)
		if cerr != nil {
			return cerr
		}
		u = cleared
	}

	if u.PinHash == nil || *u.PinHash == "" {
		return httpx.New(http.StatusForbidden, "pin_not_set", "Forbidden", "Transaction PIN has not been set.")
	}

	ok, merr := s.match(*u.PinHash, rawPIN)
	if merr != nil {
		// Corrupt / unsupported hash — treat as failure without leaking internals.
		s.log.Error("pin hash verify failed", "user_id", userID, "error", merr)
		ok = false
	}
	if ok {
		if _, err := s.users.ResetPinLockout(ctx, userID); err != nil {
			return err
		}
		return nil
	}

	return s.recordFailure(ctx, userID, now, meta)
}

func (s *Service) recordFailure(ctx context.Context, userID uuid.UUID, now time.Time, meta Meta) error {
	lockedUntil := now.Add(LockDuration)
	u, err := s.users.RecordPinFailure(ctx, userID, MaxFailures, lockedUntil)
	if err != nil {
		return err
	}

	triesLeft := int(MaxFailures - u.PinFailedCount)
	if triesLeft < 0 {
		triesLeft = 0
	}

	s.auditBestEffort(ctx, audit.Entry{
		ActorUserID: &userID,
		ActorKind:   audit.ActorUser,
		Action:      audit.ActionPINFailure,
		TargetType:  "user",
		TargetID:    &userID,
		Summary: map[string]any{
			"failed_count":    u.PinFailedCount,
			"tries_remaining": triesLeft,
			"locked":          u.PinLockedUntil != nil && u.PinLockedUntil.After(now),
		},
		IP:        meta.IP,
		UserAgent: meta.UserAgent,
		RequestID: meta.RequestID,
	})

	if u.PinLockedUntil != nil && u.PinLockedUntil.After(now) {
		s.auditBestEffort(ctx, audit.Entry{
			ActorUserID: &userID,
			ActorKind:   audit.ActorUser,
			Action:      audit.ActionPINLockout,
			TargetType:  "user",
			TargetID:    &userID,
			Summary: map[string]any{
				"failed_count": u.PinFailedCount,
				"locked_until": u.PinLockedUntil.UTC().Format(time.RFC3339),
				"duration_m":   int(LockDuration / time.Minute),
			},
			IP:        meta.IP,
			UserAgent: meta.UserAgent,
			RequestID: meta.RequestID,
		})
		return lockedErr(*u.PinLockedUntil)
	}

	return invalidPINErr(triesLeft)
}

func (s *Service) auditBestEffort(ctx context.Context, e audit.Entry) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, e); err != nil {
		s.log.Error("audit record failed", "action", e.Action, "error", err)
	}
}

func lockedErr(until time.Time) *httpx.Error {
	until = until.UTC()
	return httpx.New(
		http.StatusForbidden,
		"pin_locked",
		"Forbidden",
		fmt.Sprintf("Transaction PIN is locked until %s.", until.Format(time.RFC3339)),
	).WithLockedUntil(until)
}

func invalidPINErr(triesLeft int) *httpx.Error {
	detail := fmt.Sprintf("Invalid PIN. %d tries remaining.", triesLeft)
	if triesLeft == 1 {
		detail = "Invalid PIN. 1 try remaining."
	}
	return httpx.New(
		http.StatusUnauthorized,
		"invalid_pin",
		"Unauthorized",
		detail,
	).WithTriesRemaining(triesLeft)
}
