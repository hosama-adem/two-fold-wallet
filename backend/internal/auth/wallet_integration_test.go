package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
)

func TestVerifyPhoneCreatesOneWalletUnderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv, pool := newAuthStack(t, sms, nil, ratelimit.NewMemory(ratelimit.Config{
		Burst:  1000,
		Window: time.Minute,
	}))

	phone := uniquePhone()
	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        phone,
		"password":     "correct-horse-1",
		"display_name": "Selam",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, 2)
	bodies := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got := postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{
				"phone": phone,
				"code":  sms.LastCode,
			})
			codes[i] = got.Code
			bodies[i] = got.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	okCount := 0
	for i, code := range codes {
		if code == http.StatusOK {
			okCount++
		}
		if code != http.StatusOK && code != http.StatusUnprocessableEntity {
			t.Fatalf("verify[%d] status=%d body=%s", i, code, bodies[i])
		}
	}
	if okCount != 1 {
		t.Fatalf("successful verifies = %d, codes=%v bodies=%v", okCount, codes, bodies)
	}

	ctx := context.Background()
	var wallets int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM wallets w
		JOIN users u ON u.id = w.user_id
		WHERE u.phone_e164 = $1
	`, phone).Scan(&wallets); err != nil {
		t.Fatal(err)
	}
	if wallets != 1 {
		t.Fatalf("wallets = %d, want 1", wallets)
	}

	var accounts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM accounts a
		JOIN users u ON u.id = a.owner_user_id
		WHERE u.phone_e164 = $1
		  AND a.code = 'wallet:' || u.id::text
		  AND a.type = 'liability'
		  AND a.allow_negative = false
	`, phone).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 {
		t.Fatalf("liability accounts = %d, want 1", accounts)
	}
}

func TestWalletListAndLookupNeverRevealThePhone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv, pool := newAuthStack(t, sms, nil, ratelimit.NewMemory(ratelimit.Config{
		Burst:  1000,
		Window: time.Minute,
	}))

	ownerPhone := uniquePhone()
	ownerToken := registerVerifyLogin(t, srv, sms, ownerPhone, "Owner")

	sms.Calls = 0
	sms.LastCode = ""
	otherPhone := uniquePhone()
	otherToken := registerVerifyLogin(t, srv, sms, otherPhone, "Abebe Bekele")
	handle := "abebe" + randomHandleSuffix(t)
	if _, err := pool.Exec(context.Background(), `
		UPDATE users SET handle = $2 WHERE phone_e164 = $1
	`, otherPhone, handle); err != nil {
		t.Fatal(err)
	}

	missing := getJSON(t, srv, "/v1/wallets", "")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status=%d", missing.Code)
	}
	empty := getJSON(t, srv, "/v1/recipients/lookup?q=", ownerToken)
	if empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty lookup status=%d body=%s", empty.Code, empty.Body.String())
	}

	list := getJSON(t, srv, "/v1/wallets", ownerToken)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Wallets []struct {
			ID                     string `json:"id"`
			Currency               string `json:"currency"`
			Status                 string `json:"status"`
			FreezeMode             string `json:"freeze_mode"`
			LedgerBalanceMinor     int64  `json:"ledger_balance_minor"`
			PendingWithdrawalMinor int64  `json:"pending_withdrawal_minor"`
			AvailableMinor         int64  `json:"available_minor"`
		} `json:"wallets"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Wallets) != 1 {
		t.Fatalf("wallets = %d, body=%s", len(listed.Wallets), list.Body.String())
	}
	got := listed.Wallets[0]
	if got.Currency != "ETB" || got.Status != "active" || got.FreezeMode != "none" {
		t.Fatalf("wallet = %+v", got)
	}
	if got.LedgerBalanceMinor != 0 || got.PendingWithdrawalMinor != 0 || got.AvailableMinor != 0 {
		t.Fatalf("balances = %+v", got)
	}
	if bytes.Contains(list.Body.Bytes(), []byte("frozen_reason")) {
		t.Fatalf("frozen reason leaked: %s", list.Body.String())
	}

	otherList := getJSON(t, srv, "/v1/wallets", otherToken)
	if otherList.Code != http.StatusOK {
		t.Fatalf("other list status=%d body=%s", otherList.Code, otherList.Body.String())
	}
	if bytes.Contains(otherList.Body.Bytes(), []byte(got.ID)) {
		t.Fatalf("other user saw wallet %s", got.ID)
	}

	found := getJSON(t, srv, "/v1/recipients/lookup?q="+url.QueryEscape(otherPhone), ownerToken)
	if found.Code != http.StatusOK {
		t.Fatalf("lookup status=%d body=%s", found.Code, found.Body.String())
	}
	assertPhoneHidden(t, found.Body.Bytes(), otherPhone)
	var recipient struct {
		DisplayName string  `json:"display_name"`
		Handle      *string `json:"handle"`
		PhoneMasked string  `json:"phone_masked"`
	}
	if err := json.Unmarshal(found.Body.Bytes(), &recipient); err != nil {
		t.Fatal(err)
	}
	if recipient.DisplayName != "Abebe Bekele" {
		t.Fatalf("display name = %q", recipient.DisplayName)
	}
	if recipient.Handle == nil || *recipient.Handle != handle {
		t.Fatalf("handle = %v, want %s", recipient.Handle, handle)
	}
	if recipient.PhoneMasked == "" || recipient.PhoneMasked == otherPhone {
		t.Fatalf("phone_masked = %q", recipient.PhoneMasked)
	}

	byHandle := getJSON(t, srv, "/v1/recipients/lookup?q="+url.QueryEscape("@"+strings.ToUpper(handle)), ownerToken)
	if byHandle.Code != http.StatusOK {
		t.Fatalf("handle lookup status=%d body=%s", byHandle.Code, byHandle.Body.String())
	}
	assertPhoneHidden(t, byHandle.Body.Bytes(), otherPhone)

	unknown := getJSON(t, srv, "/v1/recipients/lookup?q="+url.QueryEscape("@nobodyhere"), ownerToken)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	if bytes.Contains(unknown.Body.Bytes(), []byte(otherPhone)) || bytes.Contains(unknown.Body.Bytes(), []byte("nobodyhere")) {
		t.Fatalf("lookup error echoed the query: %s", unknown.Body.String())
	}
}

func registerVerifyLogin(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) string {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        phone,
		"password":     "correct-horse-1",
		"display_name": name,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{
		"phone": phone,
		"code":  sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone":    phone,
		"password": "correct-horse-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
	return decodeTokens(t, rec).AccessToken
}

func randomHandleSuffix(t *testing.T) string {
	t.Helper()
	const letters = "abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	for i, b := range buf {
		buf[i] = letters[int(b)%len(letters)]
	}
	return string(buf)
}

func assertPhoneHidden(t *testing.T, body []byte, phone string) {
	t.Helper()
	if bytes.Contains(body, []byte(phone)) {
		t.Fatalf("response contains the full phone %s: %s", phone, body)
	}
	national := phone
	if len(phone) > 4 {
		national = phone[4:]
	}
	if bytes.Contains(body, []byte(national)) {
		t.Fatalf("response contains the national number %s: %s", national, body)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"phone", "phone_e164", "msisdn"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("response has key %s: %s", key, body)
		}
	}
}
