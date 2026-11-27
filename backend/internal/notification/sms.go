package notification

import (
	"context"
	"log/slog"
)

// Sender delivers SMS for critical money events (FR-NOT-2).
// Production plugs in a real provider; development logs to stdout.
type Sender interface {
	SendSMS(ctx context.Context, phoneE164, body string) error
}

// LogSender mirrors auth.LogSMSSender: console only, never in API bodies.
type LogSender struct {
	Log *slog.Logger
}

// SendSMS logs the message for developers.
func (s LogSender) SendSMS(_ context.Context, phoneE164, body string) error {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("dev SMS (not for production)",
		"phone", phoneE164,
		"body", body,
		"channel", "sms_stub",
	)
	return nil
}

// RecordingSender is a test double.
type RecordingSender struct {
	LastPhone string
	LastBody  string
	Calls     int
}

// SendSMS records the delivery.
func (s *RecordingSender) SendSMS(_ context.Context, phoneE164, body string) error {
	s.LastPhone = phoneE164
	s.LastBody = body
	s.Calls++
	return nil
}
