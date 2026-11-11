package wallet

import "testing"

func TestParseLookup(t *testing.T) {
	t.Parallel()

	phone, err := parseLookup("0911223344")
	if err != nil || phone.kind != queryPhone || phone.value != "+251911223344" {
		t.Fatalf("phone parse = %+v %v", phone, err)
	}

	spaced, err := parseLookup("+251 911 223 344")
	if err != nil || spaced.value != "+251911223344" {
		t.Fatalf("spaced parse = %+v %v", spaced, err)
	}

	handle, err := parseLookup("@Selam_1")
	if err != nil || handle.kind != queryHandle || handle.value != "selam_1" {
		t.Fatalf("handle parse = %+v %v", handle, err)
	}

	bare, err := parseLookup("devwallet")
	if err != nil || bare.kind != queryHandle || bare.value != "devwallet" {
		t.Fatalf("bare handle = %+v %v", bare, err)
	}

	if _, err := parseLookup(""); err == nil {
		t.Fatal("empty query should fail")
	}
	if _, err := parseLookup("@ab"); err == nil {
		t.Fatal("short handle should fail")
	}
}
