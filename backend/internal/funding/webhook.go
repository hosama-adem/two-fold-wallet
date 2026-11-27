package funding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/funding/fundingdb"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/provider"
)

// WebhookResult is what the HTTP adapter returns to the provider.
type WebhookResult struct {
	Duplicate bool     `json:"duplicate"`
	Decision  Decision `json:"decision,omitempty"`
	Status    string   `json:"status,omitempty"`
}

type applyInput struct {
	ProviderPath string
	EventID      string
	ProviderRef  string
	Status       provider.Status
	AmountMinor  int64
	Currency     string
	Payload      []byte
	Actor        string
	ReasonPrefix string
}

// HandleWebhook verifies HMAC, dedupes, transitions, and posts the journal (PRD §11.3).
func (s *Service) HandleWebhook(ctx context.Context, providerPath string, headers http.Header, body []byte) (WebhookResult, error) {
	if s.pool == nil || s.rail == nil {
		return WebhookResult{}, fmt.Errorf("funding: webhook service not configured")
	}
	path, err := normalizeProvider(providerPath)
	if err != nil {
		return WebhookResult{}, errWebhookBadPayload()
	}
	if err := s.rail.VerifyWebhook(headers, body); err != nil {
		s.log.Warn("webhook signature rejected", "provider", path, "error", err)
		return WebhookResult{}, errWebhookUnauthorized()
	}

	var evt provider.WebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return WebhookResult{}, errWebhookBadPayload()
	}
	if evt.EventID == "" || evt.ProviderRef == "" {
		return WebhookResult{}, errWebhookBadPayload()
	}
	if evt.Status != provider.StatusSucceeded && evt.Status != provider.StatusFailed {
		// Pending webhooks are ignored after dedupe storage — return 200.
		hash := sha256Hex(body)
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return WebhookResult{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		n, err := fundingdb.New(tx).InsertWebhookEvent(ctx, fundingdb.InsertWebhookEventParams{
			Provider:      path,
			EventID:       evt.EventID,
			PayloadHash:   hash,
			TransactionID: nil,
		})
		if err != nil {
			return WebhookResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{Duplicate: n == 0, Decision: decisionIgnore}, nil
	}

	return s.applyStatus(ctx, applyInput{
		ProviderPath: path,
		EventID:      evt.EventID,
		ProviderRef:  evt.ProviderRef,
		Status:       evt.Status,
		AmountMinor:  evt.AmountMinor,
		Currency:     evt.Currency,
		Payload:      body,
		Actor:        actorProvider,
	})
}

func (s *Service) applyStatus(ctx context.Context, in applyInput) (WebhookResult, error) {
	hash := sha256Hex(in.Payload)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := fundingdb.New(tx)

	n, err := q.InsertWebhookEvent(ctx, fundingdb.InsertWebhookEventParams{
		Provider:      in.ProviderPath,
		EventID:       in.EventID,
		PayloadHash:   hash,
		TransactionID: nil,
	})
	if err != nil {
		return WebhookResult{}, err
	}
	if n == 0 {
		return WebhookResult{Duplicate: true, Decision: decisionIgnore}, nil
	}

	pay, err := q.LockProviderPaymentByRef(ctx, in.ProviderRef)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Unknown ref: keep the event (deduped) and acknowledge.
			if err := tx.Commit(ctx); err != nil {
				return WebhookResult{}, err
			}
			return WebhookResult{Decision: decisionIgnore}, nil
		}
		return WebhookResult{}, err
	}

	txn, err := q.GetTransactionByIDForUpdate(ctx, pay.TransactionID)
	if err != nil {
		return WebhookResult{}, err
	}

	var (
		plan transition
		ok   bool
	)
	switch txn.Kind {
	case kindTopUp:
		plan, ok = planTopUpTransition(txn.Status, in.Status)
	case kindCashOut:
		plan, ok = planWithdrawalTransition(txn.Status, in.Status)
	default:
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{Decision: decisionIgnore, Status: txn.Status}, nil
	}
	if !ok {
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{Decision: decisionIgnore, Status: txn.Status}, nil
	}
	if plan.decision == decisionIgnore {
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{Decision: decisionIgnore, Status: txn.Status}, nil
	}

	amount := txn.AmountMinor
	currency := txn.Currency
	if in.AmountMinor > 0 {
		amount = in.AmountMinor
	}
	if in.Currency != "" {
		currency = in.Currency
	}
	if amount != txn.AmountMinor || currency != txn.Currency {
		s.log.Warn("webhook amount/currency mismatch; using transaction values",
			"txn", txn.ID, "got_amount", in.AmountMinor, "got_currency", in.Currency)
		amount = txn.AmountMinor
		currency = txn.Currency
	}

	switch plan.decision {
	case decisionCreditWallet:
		if err := s.creditWallet(ctx, tx, q, txn, pay, amount, currency); err != nil {
			return WebhookResult{}, err
		}
	case decisionSuspenseLate:
		if err := s.creditSuspense(ctx, tx, q, txn, pay, amount, currency, plan.reason); err != nil {
			return WebhookResult{}, err
		}
	case decisionMarkFailed:
		// status-only; no journal
	case decisionSettlePayout:
		if err := s.settlePayout(ctx, tx, txn, pay, amount, currency); err != nil {
			return WebhookResult{}, err
		}
	case decisionRefundPayout:
		if err := s.refundPayout(ctx, tx, txn, pay); err != nil {
			return WebhookResult{}, err
		}
	default:
		return WebhookResult{}, fmt.Errorf("funding: unknown decision %s", plan.decision)
	}

	updated, err := q.TransitionTransaction(ctx, fundingdb.TransitionTransactionParams{
		ID:         txn.ID,
		FromStatus: txn.Status,
		ToStatus:   plan.to,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost the race — event is stored; do not post again.
			if err := tx.Commit(ctx); err != nil {
				return WebhookResult{}, err
			}
			return WebhookResult{Decision: decisionIgnore, Status: txn.Status}, nil
		}
		return WebhookResult{}, err
	}

	if _, err := q.UpdateProviderPaymentStatus(ctx, fundingdb.UpdateProviderPaymentStatusParams{
		ID:         pay.ID,
		FromStatus: pay.Status,
		Status:     plan.payStatus,
		Raw:        in.Payload,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{}, err
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return WebhookResult{}, err
	}
	reason := plan.reason
	if in.ReasonPrefix != "" {
		reason = in.ReasonPrefix + plan.reason
	}
	actor := in.Actor
	if actor == "" {
		actor = actorProvider
	}
	if _, err := q.InsertTransactionEvent(ctx, fundingdb.InsertTransactionEventParams{
		ID:            eventID,
		TransactionID: updated.ID,
		FromStatus:    txn.Status,
		ToStatus:      plan.to,
		Reason:        &reason,
		Actor:         actor,
	}); err != nil {
		return WebhookResult{}, err
	}

	// Link the webhook row to the transaction (best-effort update).
	_, _ = tx.Exec(ctx, `
		UPDATE webhook_events SET transaction_id = $1
		WHERE provider = $2 AND event_id = $3
	`, updated.ID, in.ProviderPath, in.EventID)

	if err := s.writeNotifyOutbox(ctx, tx, updated, plan); err != nil {
		return WebhookResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WebhookResult{}, err
	}
	return WebhookResult{Decision: plan.decision, Status: plan.to}, nil
}

func (s *Service) writeNotifyOutbox(ctx context.Context, tx pgx.Tx, txn fundingdb.Transaction, plan transition) error {
	if s.outbox == nil {
		return nil
	}
	topic, ok := notifyTopic(txn.Kind, plan)
	if !ok {
		return nil
	}
	payload := map[string]any{
		"transaction_id": txn.ID,
		"reference":      txn.Reference,
		"kind":           txn.Kind,
		"status":         plan.to,
		"amount_minor":   txn.AmountMinor,
		"fee_minor":      txn.FeeMinor,
		"currency":       txn.Currency,
		"reason":         plan.reason,
	}
	if txn.FromWalletID != nil {
		payload["from_wallet_id"] = *txn.FromWalletID
	}
	if txn.ToWalletID != nil {
		payload["to_wallet_id"] = *txn.ToWalletID
	}
	if txn.InitiatorUserID != nil {
		payload["initiator_user_id"] = *txn.InitiatorUserID
	}
	_, err := s.outbox.Write(ctx, tx, topic, payload)
	return err
}

func notifyTopic(kind string, plan transition) (string, bool) {
	switch kind {
	case kindTopUp:
		switch plan.to {
		case statusCompleted:
			return outbox.TopicTopUpCompleted, true
		case statusFailed:
			return outbox.TopicTopUpFailed, true
		}
	case kindCashOut:
		switch plan.to {
		case statusCompleted:
			return outbox.TopicWithdrawalCompleted, true
		case statusFailed:
			return outbox.TopicWithdrawalFailedRefunded, true
		}
	}
	return "", false
}

func (s *Service) creditWallet(
	ctx context.Context,
	tx pgx.Tx,
	q *fundingdb.Queries,
	txn fundingdb.Transaction,
	pay fundingdb.ProviderPayment,
	amount int64,
	currency string,
) error {
	if txn.ToWalletID == nil {
		return fmt.Errorf("funding: top-up missing wallet")
	}
	locked, err := q.LockWalletBalance(ctx, *txn.ToWalletID)
	if err != nil {
		return err
	}
	if locked.Status == "closed" || locked.FreezeMode == "full" {
		return errWalletUnavailable()
	}
	if err := limit.CheckReceive(ctx, tx, locked.UserID, locked.LedgerBalanceMinor, amount); err != nil {
		return err
	}

	code, err := clearingCode(pay.Provider)
	if err != nil {
		return err
	}
	clearingID, err := s.systemAccountID(ctx, tx, code)
	if err != nil {
		return err
	}
	journal, err := buildTopUpJournal(clearingID, locked.AccountID, amount, currency)
	if err != nil {
		return err
	}
	journal = journal.WithTransactionID(txn.ID).WithDescription("Top-up " + txn.Reference)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		return err
	}
	return nil
}

func (s *Service) creditSuspense(
	ctx context.Context,
	tx pgx.Tx,
	q *fundingdb.Queries,
	txn fundingdb.Transaction,
	pay fundingdb.ProviderPayment,
	amount int64,
	currency string,
	reason string,
) error {
	code, err := clearingCode(pay.Provider)
	if err != nil {
		return err
	}
	clearingID, err := s.systemAccountID(ctx, tx, code)
	if err != nil {
		return err
	}
	suspenseID, err := s.systemAccountID(ctx, tx, ledger.CodeSuspense)
	if err != nil {
		return err
	}
	journal, err := buildSuspenseJournal(clearingID, suspenseID, amount, currency)
	if err != nil {
		return err
	}
	journal = journal.WithTransactionID(txn.ID).WithDescription("Late top-up " + txn.Reference)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		return err
	}

	if s.audit != nil {
		tid := txn.ID
		if err := s.audit.RecordTx(ctx, tx, audit.Entry{
			ActorKind:  audit.ActorSystem,
			Action:     auditLateSuccess,
			TargetType: "transaction",
			TargetID:   &tid,
			Summary: map[string]any{
				"reference":    txn.Reference,
				"provider":     pay.Provider,
				"provider_ref": pay.ProviderRef,
				"amount_minor": amount,
				"currency":     currency,
				"reason":       reason,
				"from_status":  txn.Status,
			},
		}); err != nil {
			return err
		}
	}
	s.log.Error("topup late success parked in suspense",
		"transaction_id", txn.ID,
		"reference", txn.Reference,
		"provider", pay.Provider,
		"amount_minor", amount,
		"reason", reason,
	)
	_ = q // payment row updated by caller
	return nil
}

func (s *Service) settlePayout(
	ctx context.Context,
	tx pgx.Tx,
	txn fundingdb.Transaction,
	pay fundingdb.ProviderPayment,
	amount int64,
	currency string,
) error {
	pendingID, err := s.systemAccountID(ctx, tx, ledger.CodePayoutsPending)
	if err != nil {
		return err
	}
	code, err := clearingCode(pay.Provider)
	if err != nil {
		return err
	}
	clearingID, err := s.systemAccountID(ctx, tx, code)
	if err != nil {
		return err
	}
	journal, err := buildCashOutSettleJournal(pendingID, clearingID, amount, currency)
	if err != nil {
		return err
	}
	journal = journal.WithTransactionID(txn.ID).WithDescription("Cash-out settle " + txn.Reference)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		return err
	}
	return nil
}

func (s *Service) refundPayout(
	ctx context.Context,
	tx pgx.Tx,
	txn fundingdb.Transaction,
	pay fundingdb.ProviderPayment,
) error {
	if pay.ReserveJournalID == nil || *pay.ReserveJournalID == uuid.Nil {
		return fmt.Errorf("funding: missing reserve journal for payout refund")
	}
	if _, err := ledger.Reverse(ctx, tx, *pay.ReserveJournalID, "payout_failed_refund "+txn.Reference); err != nil {
		if errors.Is(err, ledger.ErrAlreadyReversed) {
			return nil
		}
		return err
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
