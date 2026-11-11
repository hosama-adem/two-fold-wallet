package transfer

import (
	"strings"
	"testing"
)

func TestNewReferenceShape(t *testing.T) {
	seen := make(map[string]struct{}, 200)
	for i := 0; i < 200; i++ {
		ref, err := NewReference()
		if err != nil {
			t.Fatal(err)
		}
		if !validReference(ref) {
			t.Fatalf("invalid reference %q", ref)
		}
		if !strings.HasPrefix(ref, "TF-") {
			t.Fatalf("prefix: %q", ref)
		}
		if _, dup := seen[ref]; dup {
			t.Fatalf("duplicate reference in 200 draws: %q", ref)
		}
		seen[ref] = struct{}{}
	}
}
