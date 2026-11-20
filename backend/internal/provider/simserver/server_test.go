package simserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/provider/simserver"
)

func TestCollectionSucceedsAndSignsWebhook(t *testing.T) {
	var (
		mu        sync.Mutex
		gotBody   []byte
		gotHeader http.Header
	)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = append([]byte(nil), b...)
		gotHeader = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	secret := "test-secret"
	sim := simserver.New(simserver.Config{
		HMACSecret:   secret,
		CallbackURL:  callback.URL,
		DefaultDelay: time.Millisecond,
	})
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()

	body := []byte(`{"idempotency_key":"k1","amount_minor":10000,"currency":"ETB","phone_e164":"+251911000001","reference":"TOP-1"}`)
	res, err := http.Post(srv.URL+"/v1/collections", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var ref provider.CollectionRef
	if err := json.NewDecoder(res.Body).Decode(&ref); err != nil {
		t.Fatal(err)
	}
	if ref.ProviderRef == "" || ref.Status != provider.StatusPending {
		t.Fatalf("ref=%+v", ref)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := len(gotBody) > 0
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotBody) == 0 {
		t.Fatal("expected webhook")
	}
	if err := provider.VerifyWebhookSignature(secret, gotHeader, gotBody, time.Now().UTC()); err != nil {
		t.Fatalf("webhook verify: %v", err)
	}
	var evt provider.WebhookEvent
	if err := json.Unmarshal(gotBody, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Status != provider.StatusSucceeded || evt.ProviderRef != ref.ProviderRef {
		t.Fatalf("event=%+v", evt)
	}
}

func TestDeclineScenario(t *testing.T) {
	sim := simserver.New(simserver.Config{HMACSecret: "s"})
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()

	body := []byte(`{"amount_minor":1013,"currency":"ETB","phone_e164":"+251911000001","reference":"X"}`)
	res, err := http.Post(srv.URL+"/v1/collections", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var ref provider.CollectionRef
	_ = json.NewDecoder(res.Body).Decode(&ref)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := http.Get(srv.URL + "/v1/payments/" + ref.ProviderRef)
		if err != nil {
			t.Fatal(err)
		}
		var ps provider.PaymentStatus
		_ = json.NewDecoder(st.Body).Decode(&ps)
		st.Body.Close()
		if ps.Status == provider.StatusFailed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected declined payment")
}
