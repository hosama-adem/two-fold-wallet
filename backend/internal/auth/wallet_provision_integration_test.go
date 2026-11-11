package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestVerifyPhoneCreatesOneWalletUnderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}

	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()

	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        phone,
		"password":     "correct-horse-1",
		"display_name": "Selam",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	code := sms.LastCode

	const n = 8
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			payload, err := json.Marshal(map[string]string{"phone": phone, "code": code})
			if err != nil {
				bodies[i] = err.Error()
				return
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/verify-phone", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			got := httptest.NewRecorder()
			srv.Engine().ServeHTTP(got, req)
			codes[i] = got.Code
			bodies[i] = got.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for i, status := range codes {
		switch status {
		case http.StatusOK:
			ok++
		case http.StatusUnprocessableEntity:
		default:
			t.Fatalf("verify[%d] status=%d body=%s", i, status, bodies[i])
		}
	}
	if ok != 1 {
		t.Fatalf("successes=%d codes=%v bodies=%v", ok, codes, bodies)
	}

	pool := testutil.Postgres(t)
	var wallets, accounts int
	err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM wallets w
			   JOIN users u ON u.id = w.user_id
			  WHERE u.phone_e164 = $1),
			(SELECT count(*) FROM accounts a
			   JOIN users u ON u.id = a.owner_user_id
			  WHERE u.phone_e164 = $1
			    AND a.type = 'liability'
			    AND a.normal_balance = 'credit'
			    AND a.currency = 'ETB'
			    AND a.allow_negative = false)
	`, phone).Scan(&wallets, &accounts)
	if err != nil {
		t.Fatal(err)
	}
	if wallets != 1 || accounts != 1 {
		t.Fatalf("wallets=%d liability accounts=%d, want 1 and 1", wallets, accounts)
	}
}
