package funding_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/provider"
)

// fakeRail is an in-memory Provider for top-up tests (no HTTP simulator).
type fakeRail struct {
	mu       sync.Mutex
	secret   string
	name     string
	byRef    map[string]provider.PaymentStatus
	now      func() time.Time
}

func newFakeRail(secret string) *fakeRail {
	return &fakeRail{
		secret: secret,
		name:   "telebirr",
		byRef:  make(map[string]provider.PaymentStatus),
		now:    time.Now,
	}
}

func (f *fakeRail) Name() string { return f.name }

func (f *fakeRail) InitiateCollection(_ context.Context, req provider.CollectionRequest) (provider.CollectionRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := "fake_" + uuid.NewString()
	st := provider.PaymentStatus{
		ProviderRef: ref,
		Kind:        "collection",
		Status:      provider.StatusPending,
		AmountMinor: req.AmountMinor,
		Currency:    req.Currency,
		Reference:   req.Reference,
		UpdatedAt:   f.now().UTC(),
	}
	f.byRef[ref] = st
	return provider.CollectionRef{
		ProviderRef: ref,
		Status:      provider.StatusPending,
		CreatedAt:   f.now().UTC(),
	}, nil
}

func (f *fakeRail) InitiatePayout(_ context.Context, req provider.PayoutRequest) (provider.PayoutRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := "fake_pay_" + uuid.NewString()
	st := provider.PaymentStatus{
		ProviderRef: ref,
		Kind:        "payout",
		Status:      provider.StatusPending,
		AmountMinor: req.AmountMinor,
		Currency:    req.Currency,
		Reference:   req.Reference,
		UpdatedAt:   f.now().UTC(),
	}
	f.byRef[ref] = st
	return provider.PayoutRef{
		ProviderRef: ref,
		Status:      provider.StatusPending,
		CreatedAt:   f.now().UTC(),
	}, nil
}

func (f *fakeRail) GetStatus(_ context.Context, ref string) (provider.PaymentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.byRef[ref]
	if !ok {
		return provider.PaymentStatus{}, fmt.Errorf("fake rail: unknown ref %s", ref)
	}
	return st, nil
}

func (f *fakeRail) setStatus(ref string, status provider.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.byRef[ref]
	st.Status = status
	st.UpdatedAt = f.now().UTC()
	f.byRef[ref] = st
}

func (f *fakeRail) VerifyWebhook(headers http.Header, body []byte) error {
	return provider.VerifyWebhookSignature(f.secret, headers, body, f.now())
}

var _ provider.Provider = (*fakeRail)(nil)
