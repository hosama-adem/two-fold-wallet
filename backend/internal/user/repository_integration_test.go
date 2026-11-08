package user_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

func TestCreateRejectsDuplicatePhone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	repo := user.NewRepository(pool)
	ctx := context.Background()

	phone := uniquePhone()
	first, err := repo.Create(ctx, user.CreateParams{
		PhoneE164:    phone,
		DisplayName:  "Selam",
		PasswordHash: "argon2id-placeholder",
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.PhoneE164 != phone {
		t.Fatalf("phone = %q, want %q", first.PhoneE164, phone)
	}

	_, err = repo.Create(ctx, user.CreateParams{
		PhoneE164:    phone,
		DisplayName:  "Other",
		PasswordHash: "argon2id-placeholder",
	})
	if !errors.Is(err, user.ErrPhoneTaken) {
		t.Fatalf("second create error = %v, want %v", err, user.ErrPhoneTaken)
	}
}

func TestCreateRejectsInvalidPhone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	repo := user.NewRepository(pool)

	_, err := repo.Create(context.Background(), user.CreateParams{
		PhoneE164:    "+1234567890",
		DisplayName:  "Bad",
		PasswordHash: "argon2id-placeholder",
	})
	if !errors.Is(err, user.ErrInvalidPhone) {
		t.Fatalf("error = %v, want %v", err, user.ErrInvalidPhone)
	}
}

func TestGetByPhoneAndMarkVerified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	repo := user.NewRepository(pool)
	ctx := context.Background()

	phone := uniquePhone()
	created, err := repo.Create(ctx, user.CreateParams{
		PhoneE164:    phone,
		DisplayName:  "Dawit",
		PasswordHash: "argon2id-placeholder",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByPhone(ctx, phone)
	if err != nil {
		t.Fatalf("get by phone: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id mismatch")
	}
	if got.Status != "pending_verification" {
		t.Fatalf("status = %q", got.Status)
	}

	verified, err := repo.MarkPhoneVerified(ctx, created.ID)
	if err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if verified.PhoneVerifiedAt == nil {
		t.Fatal("expected phone_verified_at to be set")
	}
	if verified.Status != "active" {
		t.Fatalf("status = %q, want active", verified.Status)
	}
}

// uniquePhone avoids TRUNCATE. audit_logs references users and refuses
// UPDATE, DELETE, and TRUNCATE, so CASCADE cleanup cannot wipe the table.
func uniquePhone() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("+2519%08d", n.Int64())
}
