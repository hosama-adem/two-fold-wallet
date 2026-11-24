package funding

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSecretBoxRoundTrip(t *testing.T) {
	t.Parallel()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	box, err := NewSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("+251911223344")
	ct, nonce, err := box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce len=%d", len(nonce))
	}
	got, err := box.Open(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestMaskIdentifierMobileMoney(t *testing.T) {
	t.Parallel()
	got, err := MaskIdentifier(destinationMobileMoney, "+251911223344")
	if err != nil {
		t.Fatal(err)
	}
	if got != "+251 9•• ••• 344" {
		t.Fatalf("masked=%q", got)
	}
}
