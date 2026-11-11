package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	otpTTL            = 5 * time.Minute
	otpResendCooldown = 60 * time.Second

	genericRegisterMsg = "If this phone can be registered, we sent a verification code."
	genericVerifyFail  = "Invalid or expired verification code."
	genericResendMsg   = "If verification is still pending, we sent a new code."
)

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
}

// WalletProvisioner opens the user's ETB liability account and wallet row
// inside the verification transaction. A verified user without a wallet is
// not a valid state, so verification refuses to commit when this is nil.
type WalletProvisioner interface {
	ProvisionETB(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Service implements register / verify-phone / resend-otp / login / refresh / logout / me / pin.
type Service struct {
	users      *user.Repository
	sessions   *session.Repository
	tokens     *TokenSigner
	audit      *audit.Recorder
	pins       *pin.Service
	pool       *pgxpool.Pool
	wallets    WalletProvisioner
	sms        SMSSender
	log        *slog.Logger
	clock      Clock
	refreshTTL time.Duration
}

// ServiceDeps wires auth use-cases.
type ServiceDeps struct {
	Users      *user.Repository
	Sessions   *session.Repository
	Tokens     *TokenSigner
	Audit      *audit.Recorder
	Pins       *pin.Service // optional; built automatically when nil
	Pool       *pgxpool.Pool
	Wallets    WalletProvisioner
	SMS        SMSSender
	Log        *slog.Logger
	RefreshTTL time.Duration
}

// NewService wires auth use-cases.
func NewService(deps ServiceDeps) *Service {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	sms := deps.SMS
	if sms == nil {
		sms = LogSMSSender{Log: log}
	}
	refreshTTL := deps.RefreshTTL
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	pins := deps.Pins
	if pins == nil {
		pins = pin.New(pin.Deps{
			Users: deps.Users,
			Audit: deps.Audit,
			Match: VerifyPIN,
			Log:   log,
		})
	}
	wallets := deps.Wallets
	if wallets == nil && deps.Pool != nil {
		wallets = wallet.NewService(deps.Pool)
	}
	return &Service{
		users:      deps.Users,
		sessions:   deps.Sessions,
		tokens:     deps.Tokens,
		audit:      deps.Audit,
		pins:       pins,
		pool:       deps.Pool,
		wallets:    wallets,
		sms:        sms,
		log:        log,
		clock:      systemClock{},
		refreshTTL: refreshTTL,
	}
}

// WalletProvisioner opens the ETB wallet in the verification transaction.
type WalletProvisioner interface {
	OpenETB(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (wallet.Opened, error)
}

// PIN returns the transaction-PIN verifier for money-moving handlers (FR-ID-7).
func (s *Service) PIN() *pin.Service { return s.pins }

func (s *Service) auditBestEffort(ctx context.Context, e audit.Entry) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, e); err != nil {
		s.log.Error("audit record failed", "action", e.Action, "error", err)
	}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(c Clock) *Service {
	s.clock = c
	if s.tokens != nil {
		s.tokens.WithClock(c)
	}
	if s.pins != nil {
		s.pins.WithClock(c)
	}
	return s
}

// RegisterInput is the register request body.
type RegisterInput struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// MessageResponse is the opaque success payload.
type MessageResponse struct {
	Message string `json:"message"`
}

// Register creates a pending user (or silently no-ops) and may send an OTP.
func (s *Service) Register(ctx context.Context, in RegisterInput) (MessageResponse, error) {
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return MessageResponse{}, err
	}
	displayName, err := normalizeDisplayName(in.DisplayName)
	if err != nil {
		return MessageResponse{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return MessageResponse{}, err
	}

	existing, err := s.users.GetByPhone(ctx, phone)
	switch {
	case errors.Is(err, user.ErrNotFound):
		hash, err := HashPassword(in.Password)
		if err != nil {
			return MessageResponse{}, httpx.Internal("Could not secure password.")
		}
		_, err = s.users.Create(ctx, user.CreateParams{
			PhoneE164:    phone,
			DisplayName:  displayName,
			PasswordHash: hash,
			Status:       "pending_verification",
		})
		if err != nil {
			if errors.Is(err, user.ErrPhoneTaken) {
				// Race: another request created the same phone — treat as existing.
				return s.registerExisting(ctx, phone)
			}
			if errors.Is(err, user.ErrInvalidPhone) {
				return MessageResponse{}, httpx.ValidationFailed("Phone must be an Ethiopian mobile number in E.164 (+2519… or +2517…).")
			}
			return MessageResponse{}, err
		}
		if err := s.issueOTP(ctx, phone, false); err != nil {
			return MessageResponse{}, err
		}
		return MessageResponse{Message: genericRegisterMsg}, nil

	case err != nil:
		return MessageResponse{}, err

	default:
		_ = existing
		return s.registerExisting(ctx, phone)
	}
}

func (s *Service) registerExisting(ctx context.Context, phone string) (MessageResponse, error) {
	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		// Still return generic success to avoid enumeration.
		return MessageResponse{Message: genericRegisterMsg}, nil
	}
	if u.Status == "pending_verification" {
		// Respect cooldown; ignore ErrCooldown as success shape.
		if err := s.issueOTP(ctx, phone, true); err != nil && !errors.Is(err, errCooldown) {
			return MessageResponse{}, err
		}
	}
	// Already verified (or other status): no OTP.
	return MessageResponse{Message: genericRegisterMsg}, nil
}

// VerifyPhoneInput is the verify-phone body.
type VerifyPhoneInput struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// VerifyPhone consumes a valid OTP and activates the user.
func (s *Service) VerifyPhone(ctx context.Context, in VerifyPhoneInput) (MessageResponse, error) {
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return MessageResponse{}, err
	}
	code := strings.TrimSpace(in.Code)
	if len(code) != OTPLength {
		return MessageResponse{}, httpx.ValidationFailed("Code must be a 6-digit number.")
	}

	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		return MessageResponse{}, httpx.ValidationFailed(genericVerifyFail)
	}
	if u.Status != "pending_verification" {
		// Already verified — same client-facing failure (no enumeration of status).
		return MessageResponse{}, httpx.ValidationFailed(genericVerifyFail)
	}

	otp, err := s.users.LatestActiveOTP(ctx, phone, user.PurposeVerifyPhone)
	if err != nil {
		return MessageResponse{}, httpx.ValidationFailed(genericVerifyFail)
	}

	now := s.clock.Now()
	if now.After(otp.ExpiresAt) || otp.Attempts >= OTPMaxAttempts {
		return MessageResponse{}, httpx.ValidationFailed(genericVerifyFail)
	}

	if !OTPMatches(code, otp.CodeHash) {
		_, _ = s.users.IncrementOTPAttempts(ctx, otp.ID)
		return MessageResponse{}, httpx.ValidationFailed(genericVerifyFail)
	}
	if s.pool == nil || s.wallets == nil {
		return MessageResponse{}, httpx.Internal("Phone verification is not configured.")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MessageResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.activateWithWallet(ctx, phone, otp.ID); err != nil {
		return MessageResponse{}, err
	}
	return MessageResponse{Message: "Phone verified."}, nil
}

// activateWithWallet consumes the OTP, activates the user, and opens the ETB
// wallet in one transaction. A second concurrent verify blocks on the user
// row, then finds the phone already verified and opens nothing.
func (s *Service) activateWithWallet(ctx context.Context, phone string, otpID uuid.UUID) error {
	if s.pool == nil || s.wallets == nil {
		return httpx.Internal("Wallet provisioning is not configured.")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	locked, err := s.users.GetByPhoneForUpdate(ctx, tx, phone)
	if err != nil || locked.Status != "pending_verification" {
		return httpx.ValidationFailed(genericVerifyFail)
	}
	if _, err := s.users.ConsumeOTPTx(ctx, tx, otpID); err != nil {
		return httpx.ValidationFailed(genericVerifyFail)
	}
	if _, err := s.users.MarkPhoneVerifiedTx(ctx, tx, locked.ID); err != nil {
		return err
	}
	if _, err := s.wallets.OpenETB(ctx, tx, locked.ID); err != nil {
		if isUniqueViolation(err) {
			return httpx.ValidationFailed(genericVerifyFail)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ResendOTPInput is the resend-otp body.
type ResendOTPInput struct {
	Phone string `json:"phone"`
}

// ResendOTP sends a new code when the account is still pending verification.
func (s *Service) ResendOTP(ctx context.Context, in ResendOTPInput) (MessageResponse, error) {
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return MessageResponse{}, err
	}

	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		return MessageResponse{Message: genericResendMsg}, nil
	}
	if u.Status != "pending_verification" {
		return MessageResponse{Message: genericResendMsg}, nil
	}

	if err := s.issueOTP(ctx, phone, true); err != nil {
		if errors.Is(err, errCooldown) {
			return MessageResponse{}, httpx.New(
				http.StatusTooManyRequests,
				"otp_cooldown",
				"Too Many Requests",
				"Please wait before requesting another code.",
			)
		}
		return MessageResponse{}, err
	}
	return MessageResponse{Message: genericResendMsg}, nil
}

var errCooldown = errors.New("otp resend cooldown")

func (s *Service) issueOTP(ctx context.Context, phone string, enforceCooldown bool) error {
	if enforceCooldown {
		latest, err := s.users.LatestOTP(ctx, phone, user.PurposeVerifyPhone)
		if err == nil {
			elapsed := s.clock.Now().Sub(latest.CreatedAt)
			if elapsed < otpResendCooldown {
				return errCooldown
			}
		} else if !errors.Is(err, user.ErrNotFound) {
			return err
		}
	}

	code, err := GenerateOTP()
	if err != nil {
		return httpx.Internal("Could not generate verification code.")
	}
	now := s.clock.Now()
	_, err = s.users.CreateOTP(ctx, user.CreateOTPParams{
		PhoneE164: phone,
		Purpose:   user.PurposeVerifyPhone,
		CodeHash:  HashOTP(code),
		ExpiresAt: now.Add(otpTTL),
		CreatedAt: now,
	})
	if err != nil {
		return err
	}
	if err := s.sms.SendOTP(ctx, phone, code); err != nil {
		s.log.Error("sms send failed", "phone", phone, "error", err)
		return httpx.Internal("Could not send verification code.")
	}
	return nil
}

func normalizeDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", httpx.ValidationFailed("Display name is required.")
	}
	if utf8.RuneCountInString(name) > 80 {
		return "", httpx.ValidationFailed("Display name must be at most 80 characters.")
	}
	return name, nil
}
