package wallet

import (
	"strings"
	"testing"
)

func TestMaskPhoneHidesTheNumber(t *testing.T) {
	t.Parallel()

	const full = "+251911223344"
	got, err := MaskPhone(full)
	if err != nil {
		t.Fatal(err)
	}
	const want = "+251 9•• ••• 344"
	if got != want {
		t.Fatalf("mask = %q, want %q", got, want)
	}
	if strings.Contains(got, full) || strings.Contains(got, "911223344") {
		t.Fatalf("mask still contains the phone: %q", got)
	}
}

func TestMaskPhoneRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := MaskPhone("+251811223344"); err == nil {
		t.Fatal("expected invalid phone")
	}
}
