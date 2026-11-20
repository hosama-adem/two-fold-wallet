package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestVerifyWebhookSignatureValid(t *testing.T) {
	secret := "dev-simulator-secret"
	body := []byte(`{"event_id":"evt_1","status":"succeeded"}`)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sig := SignWebhook(secret, now, body)
	h := http.Header{}
	h.Set(HeaderSignature, sig)
	if err := VerifyWebhookSignature(secret, h, body, now); err != nil {
		t.Fatalf("valid webhook: %v", err)
	}
}

func TestVerifyWebhookSignatureTampered(t *testing.T) {
	secret := "dev-simulator-secret"
	body := []byte(`{"event_id":"evt_1","status":"succeeded"}`)
	now := time.Now().UTC()
	sig := SignWebhook(secret, now, body)
	h := http.Header{}
	h.Set(HeaderSignature, sig)
	tampered := []byte(`{"event_id":"evt_1","status":"failed"}`)
	if err := VerifyWebhookSignature(secret, h, tampered, now); err == nil {
		t.Fatal("expected signature mismatch for tampered body")
	}
}

func TestVerifyWebhookSignatureExpired(t *testing.T) {
	secret := "dev-simulator-secret"
	body := []byte(`{"event_id":"evt_1"}`)
	signedAt := time.Now().UTC().Add(-6 * time.Minute)
	sig := SignWebhook(secret, signedAt, body)
	h := http.Header{}
	h.Set(HeaderSignature, sig)
	if err := VerifyWebhookSignature(secret, h, body, time.Now().UTC()); err == nil {
		t.Fatal("expected rejection for timestamp outside 5-minute window")
	}
}

func TestVerifyWebhookSignatureMissingHeader(t *testing.T) {
	err := VerifyWebhookSignature("secret", http.Header{}, []byte(`{}`), time.Now().UTC())
	if err == nil {
		t.Fatal("expected missing header error")
	}
}

func TestVerifyWebhookSignatureWrongSecret(t *testing.T) {
	body := []byte(`{"ok":true}`)
	now := time.Now().UTC()
	sig := SignWebhook("right-secret", now, body)
	h := http.Header{}
	h.Set(HeaderSignature, sig)
	if err := VerifyWebhookSignature("wrong-secret", h, body, now); err == nil {
		t.Fatal("expected mismatch for wrong secret")
	}
}
