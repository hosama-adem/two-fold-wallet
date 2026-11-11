package idempotency_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func TestIdempotencyReplaySameBody(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	store := idempotency.NewStore(pool)
	engine := newTestEngine(store, userID, nil)

	key := uuid.NewString()
	body := []byte(`{"amount_minor":500,"note":"tea"}`)

	first := doPOST(t, engine, "/v1/demo/charge", key, body, userID)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.StatusCode, first.Body)
	}
	if first.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("first response must not be marked replayed")
	}

	second := doPOST(t, engine, "/v1/demo/charge", key, body, userID)
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("replay status=%d body=%s", second.StatusCode, second.Body)
	}
	if second.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("expected Idempotent-Replayed: true")
	}
	if !bytes.Equal(first.Body, second.Body) {
		t.Fatalf("replay body mismatch:\n%s\n%s", first.Body, second.Body)
	}
}

func TestIdempotencyReplayIgnoresJSONKeyOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	engine := newTestEngine(idempotency.NewStore(pool), userID, nil)

	key := uuid.NewString()
	first := doPOST(t, engine, "/v1/demo/charge", key, []byte(`{"amount_minor":7,"note":"a"}`), userID)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.StatusCode, first.Body)
	}
	second := doPOST(t, engine, "/v1/demo/charge", key, []byte(`{"note":"a","amount_minor":7}`), userID)
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", second.StatusCode, second.Body)
	}
	if second.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("expected replay for canonical-equivalent body")
	}
}

func TestIdempotencyRejectsBodyMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	store := idempotency.NewStore(pool)
	engine := newTestEngine(store, userID, nil)

	key := uuid.NewString()
	first := doPOST(t, engine, "/v1/demo/charge", key, []byte(`{"amount_minor":100}`), userID)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.StatusCode, first.Body)
	}

	second := doPOST(t, engine, "/v1/demo/charge", key, []byte(`{"amount_minor":200}`), userID)
	if second.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", second.StatusCode, second.Body)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(second.Body, &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != idempotency.CodeKeyReused {
		t.Fatalf("code=%s", problem.Code)
	}
}

func TestIdempotencyConcurrentSameKeyOneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	store := idempotency.NewStore(pool)

	started := make(chan struct{})
	release := make(chan struct{})
	var handlerCalls atomic.Int32

	engine := newTestEngine(store, userID, func(c *gin.Context) {
		handlerCalls.Add(1)
		closeOnce(started)
		<-release
		c.JSON(http.StatusCreated, gin.H{"ok": true, "n": handlerCalls.Load()})
	})

	key := uuid.NewString()
	body := []byte(`{"amount_minor":42}`)

	var wg sync.WaitGroup
	results := make(chan httpResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- doPOST(t, engine, "/v1/demo/charge", key, body, userID)
		}()
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	// Give the second request time to observe in_progress.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	var sawOK, sawConflict bool
	for r := range results {
		switch r.StatusCode {
		case http.StatusCreated:
			sawOK = true
		case http.StatusConflict:
			sawConflict = true
			if r.Header.Get("Retry-After") != "1" {
				t.Fatalf("Retry-After=%q", r.Header.Get("Retry-After"))
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(r.Body, &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != idempotency.CodeInProgress {
				t.Fatalf("code=%s", problem.Code)
			}
		default:
			t.Fatalf("unexpected status %d body=%s", r.StatusCode, r.Body)
		}
	}
	if !sawOK || !sawConflict {
		t.Fatalf("want one 201 and one 409; calls=%d", handlerCalls.Load())
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler ran %d times", handlerCalls.Load())
	}
}

func TestIdempotencyRequiresHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	engine := newTestEngine(idempotency.NewStore(pool), userID, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/demo/charge", bytes.NewReader([]byte(`{"amount_minor":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", userID.String())
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteExpiredRemovesOldKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	userID := insertUser(t, pool)
	store := idempotency.NewStore(pool)
	ctx := context.Background()

	key := uuid.NewString()
	hash, err := idempotency.RequestHash(http.MethodPost, "/v1/demo/charge", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, userID, key, hash)
	if err != nil || !claim.Won {
		t.Fatalf("claim: won=%v err=%v", claim.Won, err)
	}
	if err := store.Complete(ctx, userID, key, idempotency.StoredResponse{
		StatusCode:  http.StatusOK,
		Body:        []byte(`{"ok":true}`),
		ContentType: "application/json",
	}); err != nil {
		t.Fatal(err)
	}

	// Force expiry in the past.
	_, err = pool.Exec(ctx, `
		UPDATE idempotency_keys
		SET created_at = now() - interval '48 hours',
		    expires_at = now() - interval '24 hours'
		WHERE user_id = $1 AND key = $2
	`, userID, key)
	if err != nil {
		t.Fatal(err)
	}

	n, err := store.DeleteExpired(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("deleted=%d", n)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM idempotency_keys WHERE user_id = $1 AND key = $2
	`, userID, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("row still present")
	}
}

type httpResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func newTestEngine(store *idempotency.Store, userID uuid.UUID, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	for _, mw := range httpx.Standard() {
		r.Use(mw)
	}
	current := func(c *gin.Context) (uuid.UUID, bool) {
		raw := c.GetHeader("X-Test-User")
		id, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, false
		}
		return id, id == userID
	}
	if handler == nil {
		handler = func(c *gin.Context) {
			var in map[string]any
			if err := httpx.BindJSON(c, &in); err != nil {
				httpx.WriteError(c, err)
				return
			}
			c.JSON(http.StatusCreated, gin.H{
				"ok":           true,
				"amount_minor": in["amount_minor"],
			})
		}
	}
	r.POST("/v1/demo/charge",
		func(c *gin.Context) {
			// Simulate auth middleware having already run.
			c.Next()
		},
		idempotency.Middleware(store, current),
		handler,
	)
	return r
}

func doPOST(t *testing.T, engine http.Handler, path, key string, body []byte, userID uuid.UUID) httpResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Test-User", userID.String())
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return httpResult{StatusCode: rec.Code, Header: rec.Header().Clone(), Body: out}
}

func insertUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+2519%08d", n.Int64())
	_, err = pool.Exec(context.Background(), `
		INSERT INTO users (id, phone_e164, display_name, password_hash)
		VALUES ($1, $2, 'Idempotency', 'test-hash')
	`, id, phone)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}
