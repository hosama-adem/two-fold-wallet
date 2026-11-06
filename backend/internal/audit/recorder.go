package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit/auditdb"
)

const (
	ActorUser      = "user"
	ActorStaff     = "staff"
	ActorSystem    = "system"
	ActorAnonymous = "anonymous"

	ActionLoginSuccess   = "login.success"
	ActionLoginFailure   = "login.failure"
	ActionSessionRevoke  = "session.revoke"
	ActionSessionLogout  = "session.logout"
)

// Entry is one append-only audit event.
type Entry struct {
	ActorUserID *uuid.UUID
	ActorKind   string
	Action      string
	TargetType  string
	TargetID    *uuid.UUID
	Summary     map[string]any
	IP          string
	UserAgent   string
	RequestID   string
}

// Recorder writes audit_logs rows.
type Recorder struct {
	pool *pgxpool.Pool
	q    *auditdb.Queries
}

// NewRecorder builds an audit recorder on a pgx pool.
func NewRecorder(pool *pgxpool.Pool) *Recorder {
	return &Recorder{pool: pool, q: auditdb.New(pool)}
}

// Record inserts an audit row. Failures are returned to the caller (never swallow silently at this layer).
func (r *Recorder) Record(ctx context.Context, e Entry) error {
	return r.record(ctx, r.q, e)
}

// RecordTx inserts inside an existing transaction.
func (r *Recorder) RecordTx(ctx context.Context, tx pgx.Tx, e Entry) error {
	return r.record(ctx, r.q.WithTx(tx), e)
}

func (r *Recorder) record(ctx context.Context, q *auditdb.Queries, e Entry) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("audit id: %w", err)
	}
	kind := e.ActorKind
	if kind == "" {
		if e.ActorUserID != nil {
			kind = ActorUser
		} else {
			kind = ActorAnonymous
		}
	}
	summary := e.Summary
	if summary == nil {
		summary = map[string]any{}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("audit summary: %w", err)
	}
	var targetType *string
	if e.TargetType != "" {
		targetType = &e.TargetType
	}
	_, err = q.InsertAuditLog(ctx, auditdb.InsertAuditLogParams{
		ID:          id,
		ActorUserID: e.ActorUserID,
		ActorKind:   kind,
		Action:      e.Action,
		TargetType:  targetType,
		TargetID:    e.TargetID,
		Summary:     raw,
		Ip:          optional(e.IP),
		UserAgent:   optional(e.UserAgent),
		RequestID:   optional(e.RequestID),
		CreatedAt:   time.Now().UTC(),
	})
	return err
}

// Pool exposes the pool for immutability tests.
func (r *Recorder) Pool() *pgxpool.Pool { return r.pool }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
