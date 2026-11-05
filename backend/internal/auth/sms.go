package auth

import (
	"context"
	"log/slog"
)

// SMSSender delivers OTP messages. Production adapters plug in later.
type SMSSender interface {
	SendOTP(ctx context.Context, phoneE164, code string) error
}

// LogSMSSender logs OTPs to stdout for local development — never returned in API bodies.
type LogSMSSender struct {
	Log *slog.Logger
}

// SendOTP logs the code for developers.
func (s LogSMSSender) SendOTP(ctx context.Context, phoneE164, code string) error {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("dev OTP (not for production)",
		"phone", phoneE164,
		"code", code,
		"channel", "sms_stub",
	)
	return nil
}

// RecordingSMSSender is a test double that records the last code sent.
type RecordingSMSSender struct {
	LastPhone string
	LastCode  string
	Calls     int
}

// SendOTP records the delivery.
func (s *RecordingSMSSender) SendOTP(ctx context.Context, phoneE164, code string) error {
	s.LastPhone = phoneE164
	s.LastCode = code
	s.Calls++
	return nil
}
