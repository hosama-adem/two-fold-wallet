package idempotency_test

import (
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/idempotency"
)

func TestCanonicalJSONSortsKeys(t *testing.T) {
	a, err := idempotency.CanonicalJSON([]byte(`{"b":1,"a":{"d":2,"c":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := idempotency.CanonicalJSON([]byte(`{"a":{"c":3,"d":2},"b":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical mismatch:\n%s\n%s", a, b)
	}
	if string(a) != `{"a":{"c":3,"d":2},"b":1}` {
		t.Fatalf("got %s", a)
	}
}

func TestRequestHashIgnoresKeyOrder(t *testing.T) {
	h1, err := idempotency.RequestHash("POST", "/v1/transfers", []byte(`{"amount_minor":100,"note":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := idempotency.RequestHash("POST", "/v1/transfers", []byte(`{"note":"hi","amount_minor":100}`))
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("want 64 hex chars, got %d", len(h1))
	}
}

func TestRequestHashChangesWithPathOrMethod(t *testing.T) {
	body := []byte(`{"x":1}`)
	base, err := idempotency.RequestHash("POST", "/v1/a", body)
	if err != nil {
		t.Fatal(err)
	}
	otherPath, err := idempotency.RequestHash("POST", "/v1/b", body)
	if err != nil {
		t.Fatal(err)
	}
	otherMethod, err := idempotency.RequestHash("PUT", "/v1/a", body)
	if err != nil {
		t.Fatal(err)
	}
	if base == otherPath || base == otherMethod {
		t.Fatal("hash should include method and path")
	}
}

func TestCanonicalJSONRejectsTrailingJunk(t *testing.T) {
	_, err := idempotency.CanonicalJSON([]byte(`{"a":1}{"b":2}`))
	if err == nil {
		t.Fatal("expected error")
	}
}
