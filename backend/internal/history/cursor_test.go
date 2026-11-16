package history

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	want := cursor{
		CreatedAt: time.Date(2026, 11, 13, 9, 15, 30, 123456789, time.UTC),
		ID:        uuid.MustParse("018f7b2c-1a2b-7c3d-8e4f-5a6b7c8d9e0f"),
	}
	got, err := decodeCursor(encodeCursor(want))
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created_at = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if got.ID != want.ID {
		t.Fatalf("id = %v, want %v", got.ID, want.ID)
	}
}

func TestCursorNanosecondPrecision(t *testing.T) {
	// Keyset correctness depends on the cursor preserving the full created_at,
	// down to the nanosecond, so two rows in the same microsecond never collide.
	want := cursor{
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 999999999, time.UTC),
		ID:        uuid.New(),
	}
	got, err := decodeCursor(encodeCursor(want))
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created_at = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, s := range []string{"not-base64!!", "", "Zm9vYmFy", "aGVsbG8"} {
		if _, err := decodeCursor(s); err == nil {
			t.Fatalf("decodeCursor(%q) expected error", s)
		}
	}
}
