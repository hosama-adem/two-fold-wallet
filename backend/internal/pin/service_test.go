package pin

import (
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func TestInvalidPINErrTries(t *testing.T) {
	t.Parallel()

	err := invalidPINErr(3)
	he, ok := httpx.AsError(err)
	if !ok {
		t.Fatal("expected httpx.Error")
	}
	if he.Code != "invalid_pin" || he.Status != 401 {
		t.Fatalf("code=%q status=%d", he.Code, he.Status)
	}
	if he.TriesRemaining == nil || *he.TriesRemaining != 3 {
		t.Fatalf("tries=%v", he.TriesRemaining)
	}
	if he.Detail != "Invalid PIN. 3 tries remaining." {
		t.Fatalf("detail=%q", he.Detail)
	}

	one := invalidPINErr(1)
	if one.Detail != "Invalid PIN. 1 try remaining." {
		t.Fatalf("detail=%q", one.Detail)
	}
}

func TestLockedErr(t *testing.T) {
	t.Parallel()

	until := time.Date(2026, 11, 7, 22, 30, 0, 0, time.UTC)
	err := lockedErr(until)
	if err.Code != "pin_locked" || err.Status != 403 {
		t.Fatalf("code=%q status=%d", err.Code, err.Status)
	}
	if err.LockedUntil == nil || !err.LockedUntil.Equal(until) {
		t.Fatalf("locked_until=%v", err.LockedUntil)
	}
	if err.TriesRemaining != nil {
		t.Fatalf("locked response must not burn/advertise tries: %v", err.TriesRemaining)
	}
}

func TestLockPolicyConstants(t *testing.T) {
	t.Parallel()
	if MaxFailures != 5 {
		t.Fatalf("MaxFailures=%d", MaxFailures)
	}
	if LockDuration != 30*time.Minute {
		t.Fatalf("LockDuration=%s", LockDuration)
	}
}
