package ids_test

import (
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/ids"
)

func TestNewUnique(t *testing.T) {
	t.Parallel()

	a, err := ids.New()
	if err != nil {
		t.Fatal(err)
	}
	b, err := ids.New()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected unique ids, both %q", a)
	}
	if len(a) != 32 {
		t.Fatalf("len = %d, want 32 hex chars", len(a))
	}
}
