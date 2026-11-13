package transfer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func previewFee(t *testing.T, srv *httpserver.Server, token, recipient string, amount int64) int64 {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"recipient": recipient, "amount_minor": amount, "currency": "ETB",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/preview", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		FeeMinor int64 `json:"fee_minor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.FeeMinor
}

func TestTransferPerTxnBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	// Fresh sender per case so a prior near-cap debit cannot trip the daily window.
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")

	const cap int64 = 100_000
	for _, tc := range []struct {
		name   string
		amount int64
		ok     bool
	}{
		{"limit-1", cap - 1, true},
		{"limit", cap, true},
		{"limit+1", cap + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			senderPhone := uniquePhone()
			registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
			senderToken := login(t, srv, senderPhone)
			senderAccount, _ := walletIDs(t, ctx, pool, senderPhone)
			fund(t, ctx, pool, senderAccount, 250_000)

			fee := previewFee(t, srv, senderToken, recipientPhone, tc.amount)
			rec := postTransfer(t, srv, senderToken, map[string]any{
				"recipient":          recipientPhone,
				"amount_minor":       tc.amount,
				"currency":           "ETB",
				"pin":                testPIN,
				"expected_fee_minor": fee,
			}, uuid.NewString())
			if tc.ok {
				if rec.Code != http.StatusCreated {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				return
			}
			assertProblem(t, rec, http.StatusUnprocessableEntity, limit.CodeLimitExceeded)
			var p httpx.Problem
			_ = json.Unmarshal(rec.Body.Bytes(), &p)
			if p.Limit == nil || *p.Limit != limit.LimitPerTxn {
				t.Fatalf("limit=%v body=%s", p.Limit, rec.Body.String())
			}
			if p.RemainingMinor == nil {
				t.Fatal("remaining_minor missing")
			}
		})
	}
}

func TestTransferRecipientBalanceCap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")
	senderToken := login(t, srv, senderPhone)

	senderAccount, _ := walletIDs(t, ctx, pool, senderPhone)
	recipientAccount, _ := walletIDs(t, ctx, pool, recipientPhone)
	fund(t, ctx, pool, senderAccount, 50_000)
	fund(t, ctx, pool, recipientAccount, 499_000) // room for 1_000 under tier-0 max 500_000

	ok := postTransfer(t, srv, senderToken, map[string]any{
		"recipient": recipientPhone, "amount_minor": 1_000, "currency": "ETB",
		"pin": testPIN, "expected_fee_minor": 0,
	}, uuid.NewString())
	if ok.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", ok.Code, ok.Body.String())
	}

	// Recipient is now at max. Another 1 minor must fail before journaling.
	fund(t, ctx, pool, senderAccount, 10_000)
	bad := postTransfer(t, srv, senderToken, map[string]any{
		"recipient": recipientPhone, "amount_minor": 1, "currency": "ETB",
		"pin": testPIN, "expected_fee_minor": 0,
	}, uuid.NewString())
	assertProblem(t, bad, http.StatusUnprocessableEntity, limit.CodeLimitExceeded)
	var p httpx.Problem
	_ = json.Unmarshal(bad.Body.Bytes(), &p)
	if p.Limit == nil || *p.Limit != limit.LimitMaxBalance {
		t.Fatalf("limit=%v body=%s", p.Limit, bad.Body.String())
	}
}

func TestTransferConcurrentDailyCap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")
	senderToken := login(t, srv, senderPhone)
	senderAccount, _ := walletIDs(t, ctx, pool, senderPhone)
	fund(t, ctx, pool, senderAccount, 400_000)

	const (
		workers = 16
		amount  = int64(50_000) // daily 200_000 → a handful succeed, the rest fail
	)
	fee := previewFee(t, srv, senderToken, recipientPhone, amount)

	var (
		ok   atomic.Int64
		fail atomic.Int64
		wg   sync.WaitGroup
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rec := postTransfer(t, srv, senderToken, map[string]any{
				"recipient":          recipientPhone,
				"amount_minor":       amount,
				"currency":           "ETB",
				"pin":                testPIN,
				"expected_fee_minor": fee,
			}, uuid.NewString())
			switch rec.Code {
			case http.StatusCreated:
				ok.Add(1)
			case http.StatusUnprocessableEntity:
				var p httpx.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
					t.Errorf("decode: %v body=%s", err, rec.Body.String())
					return
				}
				if p.Code != limit.CodeLimitExceeded && p.Code != "insufficient_funds" {
					t.Errorf("code=%s body=%s", p.Code, rec.Body.String())
					return
				}
				fail.Add(1)
			default:
				t.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("expected some successes")
	}
	if fail.Load() == 0 {
		t.Fatal("expected some failures once the daily cap filled")
	}
	// amount-only check vs fee-inflated debit usage → at most 3 of 50_000 fit in 200_000.
	if ok.Load() > 4 {
		t.Fatalf("successes=%d want <= 4", ok.Load())
	}

	var debitSum int64
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.amount_minor), 0)
		FROM postings p
		JOIN accounts a ON a.id = p.account_id
		JOIN users u ON u.id = a.owner_user_id
		WHERE u.phone_e164 = $1 AND p.direction = 'debit'
	`, senderPhone).Scan(&debitSum)
	if err != nil {
		t.Fatal(err)
	}
	// Last accepted transfer may post amount+fee slightly over the amount-based
	// daily check; still must stay within one fee of the daily cap.
	if debitSum > 200_000+fee {
		t.Fatalf("debit sum=%d exceeds daily+fee", debitSum)
	}
}
