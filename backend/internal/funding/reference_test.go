package funding

import "testing"

func TestNewReferenceIsValid(t *testing.T) {
	t.Parallel()
	for i := 0; i < 32; i++ {
		ref, err := NewReference()
		if err != nil {
			t.Fatal(err)
		}
		if !validReference(ref) {
			t.Fatalf("generated reference rejected: %q", ref)
		}
	}
}

func TestValidReferenceRejectsBadShape(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"", "TF-7K2M-9QXA", "TU-7K2M9QXA", "TU-7K2M-9QX!", "tu-7k2m-9qxa"} {
		if validReference(ref) {
			t.Fatalf("accepted %q", ref)
		}
	}
}
