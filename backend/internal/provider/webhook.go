package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// HeaderSignature carries t=<unix>,v1=<hex> (Stripe-style).
	HeaderSignature = "X-Twofold-Signature"
	// MaxWebhookSkew is how old/new a webhook timestamp may be.
	MaxWebhookSkew = 5 * time.Minute
)

// SignWebhook builds the X-Twofold-Signature value for body at time t.
// Payload is "<unix>.<body>" — the timestamp is bound into the MAC so a
// captured body cannot be replayed under a fresh t.
func SignWebhook(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.UTC().Unix(), 10)
	mac := hmacSHA256(secret, ts+"."+string(body))
	return "t=" + ts + ",v1=" + mac
}

// VerifyWebhookSignature checks HeaderSignature against body.
// valid / tampered / expired are covered by unit tests (Day 22 done-when).
func VerifyWebhookSignature(secret string, headers http.Header, body []byte, now time.Time) error {
	if secret == "" {
		return fmt.Errorf("provider: webhook secret is empty")
	}
	raw := strings.TrimSpace(headers.Get(HeaderSignature))
	if raw == "" {
		return fmt.Errorf("provider: missing %s", HeaderSignature)
	}
	ts, sig, err := parseSignatureHeader(raw)
	if err != nil {
		return err
	}
	skew := now.UTC().Sub(time.Unix(ts, 0).UTC())
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxWebhookSkew {
		return fmt.Errorf("provider: webhook timestamp outside %s window", MaxWebhookSkew)
	}
	want := hmacSHA256(secret, strconv.FormatInt(ts, 10)+"."+string(body))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return fmt.Errorf("provider: webhook signature mismatch")
	}
	return nil
}

func parseSignatureHeader(raw string) (ts int64, v1 string, err error) {
	var gotT, gotV bool
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			ts, err = strconv.ParseInt(val, 10, 64)
			if err != nil {
				return 0, "", fmt.Errorf("provider: invalid webhook timestamp")
			}
			gotT = true
		case "v1":
			v1 = val
			gotV = true
		}
	}
	if !gotT || !gotV || v1 == "" {
		return 0, "", fmt.Errorf("provider: malformed %s", HeaderSignature)
	}
	return ts, v1, nil
}

func hmacSHA256(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
