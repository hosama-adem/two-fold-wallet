package outbox_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/outbox/outboxdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

// memoryPublisher records PublishOutbox calls without River.
type memoryPublisher struct {
	mu     sync.Mutex
	events []published
}

type published struct {
	EventID uuid.UUID
	Topic   string
	Payload json.RawMessage
}

func (m *memoryPublisher) PublishOutbox(_ context.Context, _ pgx.Tx, eventID uuid.UUID, topic string, payload json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, published{EventID: eventID, Topic: topic, Payload: payload})
	return nil
}

func (m *memoryPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func TestCrashBetweenCommitAndDispatchStillDelivers(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := outbox.NewWriter().Write(ctx, tx, outbox.TopicTransferCompleted, map[string]any{
		"transaction_id": uuid.Must(uuid.NewV7()),
		"reference":      "TF-CRASH1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Process "crashed" after commit: row is durable, not yet on River.
	row, err := outboxdb.New(pool).GetOutboxEvent(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if row.DispatchedAt != nil {
		t.Fatal("dispatched_at set before dispatcher ran")
	}

	pub := &memoryPublisher{}
	d := outbox.NewDispatcher(pool, pub, nil)
	for {
		n, err := d.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}

	found := false
	for _, e := range pub.events {
		if e.EventID == eventID && e.Topic == outbox.TopicTransferCompleted {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("event %s was not published after recovery dispatch", eventID)
	}

	row, err = outboxdb.New(pool).GetOutboxEvent(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if row.DispatchedAt == nil {
		t.Fatal("dispatched_at still null after dispatch")
	}
}

func TestOutboxRollbackDropsEvent(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()

	before, err := outboxdb.New(pool).CountUndispatched(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.NewWriter().Write(ctx, tx, outbox.TopicTopUpFailed, map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	after, err := outboxdb.New(pool).CountUndispatched(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("undispatched before=%d after rollback=%d", before, after)
	}
}

func TestDispatcherSkipLockedConcurrent(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()

	const total = 20
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := outbox.NewWriter()
	for i := 0; i < total; i++ {
		if _, err := w.Write(ctx, tx, outbox.TopicTransferCompleted, map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	pub := &memoryPublisher{}
	d := outbox.NewDispatcher(pool, pub, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := d.RunOnce(ctx)
				if err != nil {
					errs <- err
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for concurrent dispatch")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if pub.count() < total {
		t.Fatalf("published=%d want at least %d", pub.count(), total)
	}
}
