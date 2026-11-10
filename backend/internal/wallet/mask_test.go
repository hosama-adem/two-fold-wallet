package wallet

import (
	"strings"
	"testing"
)

func TestMaskPhoneHidesTheNationalNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		phone string
		want  string
	}{
		{phone: "+251912345678", want: "+251 9•• ••• 678"},
		{phone: "+251711111234", want: "+251 7•• ••• 234"},
	}
	for _, tc := range cases {
		got, err := MaskPhone(tc.phone)
		if err != nil {
			t.Fatalf("MaskPhone(%s): %v", tc.phone, err)
		}
		if got != tc.want {
			t.Fatalf("MaskPhone(%s) = %q, want %q", tc.phone, got, tc.want)
		}
		national := tc.phone[4:]
		if strings.Contains(got, tc.phone) || strings.Contains(got, national) {
			t.Fatalf("mask %q still contains %s", got, tc.phone)
		}
	}
}

func TestMaskPhoneRejectsAnythingElse(t *testing.T) {
	t.Parallel()
	for _, phone := range []string{"", "+2519123456789", "0912345678", "+251812345678", "+251 912345678"} {
		if _, err := MaskPhone(phone); err == nil {
			t.Fatalf("MaskPhone(%q) succeeded", phone)
		}
	}
}
