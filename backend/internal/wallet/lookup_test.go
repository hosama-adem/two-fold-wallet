package wallet

import "testing"

func TestParseLookup(t *testing.T) {
	t.Parallel()

	phone, err := ParseLookup("0911223344")
	if err != nil || phone.Kind != QueryPhone || phone.Value != "+251911223344" {
		t.Fatalf("phone parse = %+v %v", phone, err)
	}

	spaced, err := ParseLookup("+251 911 223 344")
	if err != nil || spaced.Value != "+251911223344" {
		t.Fatalf("spaced parse = %+v %v", spaced, err)
	}

	handle, err := ParseLookup("@Selam_1")
	if err != nil || handle.Kind != QueryHandle || handle.Value != "selam_1" {
		t.Fatalf("handle parse = %+v %v", handle, err)
	}

	bare, err := ParseLookup("devwallet")
	if err != nil || bare.Kind != QueryHandle || bare.Value != "devwallet" {
		t.Fatalf("bare handle = %+v %v", bare, err)
	}

	if _, err := ParseLookup(""); err == nil {
		t.Fatal("empty query should fail")
	}
	if _, err := ParseLookup("@ab"); err == nil {
		t.Fatal("short handle should fail")
	}
}
