package auth

import "testing"

func TestValidatePIN(t *testing.T) {
	t.Parallel()

	ok := []string{"4829", "7391", "2580", "1357"}
	for _, pin := range ok {
		if err := ValidatePIN(pin); err != nil {
			t.Fatalf("ValidatePIN(%q)=%v", pin, err)
		}
	}

	weak := []string{
		"0000", "1111", "9999",
		"1234", "2345", "6789",
		"5432", "9876", "3210",
	}
	for _, pin := range weak {
		if err := ValidatePIN(pin); err == nil {
			t.Fatalf("ValidatePIN(%q) expected weak rejection", pin)
		}
	}

	badFormat := []string{
		"", "12", "12345", "12a4", "12 4", "abcd",
		"١٢٣٤", // Arabic-Indic digits (multi-byte) — must not pass as "4 digits"
		"1²34",
	}
	for _, pin := range badFormat {
		if err := ValidatePIN(pin); err == nil {
			t.Fatalf("ValidatePIN(%q) expected format rejection", pin)
		}
	}
}

func TestHashAndVerifyPIN(t *testing.T) {
	t.Parallel()

	hash, err := HashPIN("4829")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || hash == "4829" {
		t.Fatalf("unexpected hash %q", hash)
	}
	ok, err := VerifyPIN(hash, "4829")
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPIN(hash, "7391")
	if err != nil || ok {
		t.Fatalf("verify bad: ok=%v err=%v", ok, err)
	}
}
