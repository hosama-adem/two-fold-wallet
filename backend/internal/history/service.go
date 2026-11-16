package history

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/history/historydb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	exportBatch  = 500

	dirIn      = "in"
	dirOut     = "out"
	dirNeutral = "neutral"

	notFoundDetail = "Transaction not found."
)

// Known filter values mirror the transactions table CHECK constraints. An
// unknown value is a client mistake, not an empty page — the service answers 422.
var (
	validKinds = map[string]struct{}{
		"p2p": {}, "top_up": {}, "cash_out": {},
		"merchant_payment": {}, "money_request": {}, "adjustment": {},
	}
	validStatuses = map[string]struct{}{
		"pending": {}, "processing": {}, "needs_review": {}, "completed": {},
		"failed": {}, "reversed": {}, "expired": {}, "cancelled": {},
	}
)

// Service reads transaction history for a caller (FR-HIS). All reads are scoped
// to the caller's participant rows inside SQL; the service never widens that.
type Service struct {
	pool *pgxpool.Pool
	q    *historydb.Queries
}

// NewService builds a history service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: historydb.New(pool)}
}

// Counterparty is the other side of a transaction, relative to the caller.
type Counterparty struct {
	DisplayName string  `json:"display_name"`
	Handle      *string `json:"handle,omitempty"`
	PhoneMasked string  `json:"phone_masked"`
}

// HistoryItem is one row in the list (FR-HIS-1). JSON stays snake_case,
// consistent with transfer.Transaction.
type HistoryItem struct {
	ID                uuid.UUID     `json:"id"`
	Reference         string        `json:"reference"`
	Kind              string        `json:"kind"`
	Status            string        `json:"status"`
	AmountMinor       int64         `json:"amount_minor"`
	FeeMinor          int64         `json:"fee_minor"`
	Currency          string        `json:"currency"`
	Note              *string       `json:"note,omitempty"`
	FromWalletID      *uuid.UUID    `json:"from_wallet_id,omitempty"`
	ToWalletID        *uuid.UUID    `json:"to_wallet_id,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	CompletedAt       *time.Time    `json:"completed_at,omitempty"`
	Direction         string        `json:"direction"`
	Counterparty      *Counterparty `json:"counterparty,omitempty"`
	SignedAmountMinor int64         `json:"signed_amount_minor"`
}

// ListResult is the paginated list envelope. next_cursor is omitted when the
// page is the last one.
type ListResult struct {
	Items      []HistoryItem `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}

// ListInput carries the raw query parameters. The service validates them and
// maps invalid values to 422.
type ListInput struct {
	UserID uuid.UUID
	Cursor string
	Kind   string
	Status string
	From   string
	To     string
	Limit  string
}

// Receipt is a single transaction's detail view (FR-HIS-2).
type Receipt struct {
	Reference         string        `json:"reference"`
	CreatedAt         time.Time     `json:"created_at"`
	CreatedAtAddis    string        `json:"created_at_addis"`
	AmountMinor       int64         `json:"amount_minor"`
	FeeMinor          int64         `json:"fee_minor"`
	Currency          string        `json:"currency"`
	Status            string        `json:"status"`
	Kind              string        `json:"kind"`
	Note              *string       `json:"note,omitempty"`
	Direction         string        `json:"direction"`
	Counterparty      *Counterparty `json:"counterparty,omitempty"`
	BalanceAfterMinor *int64        `json:"balance_after_minor"`
}

// ExportInput carries the CSV export window (FR-HIS-3).
type ExportInput struct {
	UserID uuid.UUID
	From   string
	To     string
}

// txView is the shared shape of a transaction row across the list and receipt
// queries, so mapping lives in one place.
type txView struct {
	ID              uuid.UUID
	Reference       string
	Kind            string
	Status          string
	AmountMinor     int64
	FeeMinor        int64
	Currency        string
	Note            *string
	FromWalletID    *uuid.UUID
	ToWalletID      *uuid.UUID
	CreatedAt       time.Time
	CompletedAt     *time.Time
	FromUserID      *uuid.UUID
	ToUserID        *uuid.UUID
	FromDisplayName *string
	FromHandle      *string
	FromPhone       *string
	ToDisplayName   *string
	ToHandle        *string
	ToPhone         *string
}

// List returns a newest-first keyset page of the caller's transactions.
func (s *Service) List(ctx context.Context, in ListInput) (ListResult, error) {
	if s.pool == nil {
		return ListResult{}, httpx.Internal("History store is not configured.")
	}
	if in.UserID == uuid.Nil {
		return ListResult{}, httpx.ValidationFailed("User id is required.")
	}

	limit, err := parseLimit(in.Limit)
	if err != nil {
		return ListResult{}, err
	}

	walletIDs, err := s.walletIDs(ctx, in.UserID)
	if err != nil {
		return ListResult{}, err
	}

	userID := in.UserID
	params := historydb.ListTransactionsForUserParams{
		UserID:    &userID,
		WalletIds: walletIDs,
		RowLimit:  int32(limit + 1), // one extra row proves whether another page exists
	}
	if in.Kind != "" {
		if _, ok := validKinds[in.Kind]; !ok {
			return ListResult{}, httpx.ValidationFailed("Unknown kind filter.")
		}
		k := in.Kind
		params.Kind = &k
	}
	if in.Status != "" {
		if _, ok := validStatuses[in.Status]; !ok {
			return ListResult{}, httpx.ValidationFailed("Unknown status filter.")
		}
		st := in.Status
		params.Status = &st
	}
	if in.From != "" {
		t, err := parseTime(in.From, "from")
		if err != nil {
			return ListResult{}, err
		}
		params.FromTime = &t
	}
	if in.To != "" {
		t, err := parseTime(in.To, "to")
		if err != nil {
			return ListResult{}, err
		}
		params.ToTime = &t
	}
	if in.Cursor != "" {
		cur, err := decodeCursor(in.Cursor)
		if err != nil {
			return ListResult{}, err
		}
		params.CursorCreatedAt = &cur.CreatedAt
		params.CursorID = &cur.ID
	}

	rows, err := s.q.ListTransactionsForUser(ctx, params)
	if err != nil {
		return ListResult{}, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	result := ListResult{Items: make([]HistoryItem, 0, len(rows))}
	for _, r := range rows {
		result.Items = append(result.Items, buildItem(in.UserID, listView(r)))
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeCursor(cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		result.NextCursor = &c
	}
	return result, nil
}

// Receipt returns one transaction by reference for a participant (FR-HIS-2).
// Non-participants and unknown references both get 404 — existence never leaks.
func (s *Service) Receipt(ctx context.Context, userID uuid.UUID, reference string) (Receipt, error) {
	if s.pool == nil {
		return Receipt{}, httpx.Internal("History store is not configured.")
	}
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return Receipt{}, httpx.NotFound(notFoundDetail)
	}

	uid := userID
	row, err := s.q.GetTransactionForUser(ctx, historydb.GetTransactionForUserParams{
		Reference: reference,
		UserID:    &uid,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, httpx.NotFound(notFoundDetail)
		}
		return Receipt{}, err
	}

	v := receiptView(row)
	dir := direction(userID, v)
	rec := Receipt{
		Reference:      v.Reference,
		CreatedAt:      v.CreatedAt.UTC(),
		CreatedAtAddis: v.CreatedAt.In(addisLocation).Format(time.RFC3339),
		AmountMinor:    v.AmountMinor,
		FeeMinor:       v.FeeMinor,
		Currency:       v.Currency,
		Status:         v.Status,
		Kind:           v.Kind,
		Note:           v.Note,
		Direction:      dir,
		Counterparty:   counterpartyFor(dir, v),
	}

	bal, err := s.balanceAfter(ctx, v.ID, userID)
	if err != nil {
		return Receipt{}, err
	}
	rec.BalanceAfterMinor = bal
	return rec, nil
}

// csvHeader is the fixed column order of the export (FR-HIS-3).
var csvHeader = []string{
	"reference", "created_at_utc", "created_at_addis", "kind", "status", "direction",
	"amount_minor", "fee_minor", "currency", "counterparty", "note", "balance_after_minor",
}

// Export streams the caller's transactions as CSV into w (FR-HIS-3). It walks
// keyset batches newest-first, loads that batch's balances in one query, and
// flushes after each batch, so memory stays bounded no matter how large the
// window is. Every cell is escaped against CSV injection.
func (s *Service) Export(ctx context.Context, in ExportInput, w io.Writer) error {
	if s.pool == nil {
		return httpx.Internal("History store is not configured.")
	}
	if in.UserID == uuid.Nil {
		return httpx.ValidationFailed("User id is required.")
	}
	from, to, err := resolveExportWindow(in.From, in.To)
	if err != nil {
		return err
	}

	walletIDs, err := s.walletIDs(ctx, in.UserID)
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}

	userID := in.UserID
	var cur *cursor
	for {
		params := historydb.ListTransactionsForUserParams{
			UserID:    &userID,
			WalletIds: walletIDs,
			FromTime:  &from,
			ToTime:    &to,
			RowLimit:  exportBatch,
		}
		if cur != nil {
			params.CursorCreatedAt = &cur.CreatedAt
			params.CursorID = &cur.ID
		}
		rows, err := s.q.ListTransactionsForUser(ctx, params)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		// One balance lookup for the batch. A failure here happens before this
		// batch is flushed, so the handler can still answer problem+json when
		// nothing has been written yet.
		balances, err := s.balancesAfter(ctx, userID, ids)
		if err != nil {
			return err
		}
		for _, r := range rows {
			v := listView(r)
			dir := direction(userID, v)
			var bal *int64
			if b, ok := balances[v.ID]; ok {
				copied := b
				bal = &copied
			}
			if err := cw.Write(escapeCSVRow(csvRow(v, dir, bal))); err != nil {
				return err
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
		if len(rows) < exportBatch {
			break
		}
		last := rows[len(rows)-1]
		cur = &cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}

	cw.Flush()
	return cw.Error()
}

// walletIDs lists the caller's wallet ids for the participant predicate. The
// list is tiny (one ETB wallet today) and feeds the uuid[] the index-friendly
// query expects.
func (s *Service) walletIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListWalletIDsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// balancesAfter returns the caller's balance after each transaction in ids.
// Missing keys are pending rows with no journal yet.
func (s *Service) balancesAfter(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListBalancesAfterForUser(ctx, historydb.ListBalancesAfterForUserParams{
		TransactionIds: ids,
		UserID:         userID,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		id := r.TransactionID
		if id == nil || *id == uuid.Nil {
			continue
		}
		out[*id] = r.BalanceAfterMinor
	}
	return out, nil
}

func (s *Service) balanceAfter(ctx context.Context, txID, userID uuid.UUID) (*int64, error) {
	id := txID
	bal, err := s.q.GetReceiptBalanceAfterForUser(ctx, historydb.GetReceiptBalanceAfterForUserParams{
		TransactionID: &id,
		UserID:        userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // pending: no posting yet
		}
		return nil, err
	}
	return &bal, nil
}

func csvRow(v txView, dir string, bal *int64) []string {
	note := ""
	if v.Note != nil {
		note = *v.Note
	}
	balStr := ""
	if bal != nil {
		balStr = strconv.FormatInt(*bal, 10)
	}
	return []string{
		v.Reference,
		v.CreatedAt.UTC().Format(time.RFC3339Nano),
		v.CreatedAt.In(addisLocation).Format(time.RFC3339),
		v.Kind,
		v.Status,
		dir,
		strconv.FormatInt(v.AmountMinor, 10),
		strconv.FormatInt(v.FeeMinor, 10),
		v.Currency,
		counterpartyLabel(dir, v),
		note,
		balStr,
	}
}

func buildItem(userID uuid.UUID, v txView) HistoryItem {
	dir := direction(userID, v)
	return HistoryItem{
		ID:                v.ID,
		Reference:         v.Reference,
		Kind:              v.Kind,
		Status:            v.Status,
		AmountMinor:       v.AmountMinor,
		FeeMinor:          v.FeeMinor,
		Currency:          v.Currency,
		Note:              v.Note,
		FromWalletID:      v.FromWalletID,
		ToWalletID:        v.ToWalletID,
		CreatedAt:         v.CreatedAt.UTC(),
		CompletedAt:       v.CompletedAt,
		Direction:         dir,
		Counterparty:      counterpartyFor(dir, v),
		SignedAmountMinor: signedAmount(dir, v),
	}
}

// direction is "out" when the caller owns the source wallet, "in" when they own
// the destination, and "neutral" otherwise (e.g. they only initiated it).
func direction(userID uuid.UUID, v txView) string {
	ownsFrom := v.FromUserID != nil && *v.FromUserID == userID
	ownsTo := v.ToUserID != nil && *v.ToUserID == userID
	switch {
	case ownsFrom && !ownsTo:
		return dirOut
	case ownsTo && !ownsFrom:
		return dirIn
	default:
		return dirNeutral
	}
}

// signedAmount is negative for money leaving the caller (amount + fee) and
// positive for money arriving (amount). Neutral rows carry no sign.
func signedAmount(dir string, v txView) int64 {
	switch dir {
	case dirOut:
		return -(v.AmountMinor + v.FeeMinor)
	case dirIn:
		return v.AmountMinor
	default:
		return 0
	}
}

func counterpartyFor(dir string, v txView) *Counterparty {
	switch dir {
	case dirOut:
		return buildCounterparty(v.ToDisplayName, v.ToHandle, v.ToPhone)
	case dirIn:
		return buildCounterparty(v.FromDisplayName, v.FromHandle, v.FromPhone)
	default:
		return nil
	}
}

func buildCounterparty(name, handle, phone *string) *Counterparty {
	if name == nil && handle == nil && phone == nil {
		return nil
	}
	cp := &Counterparty{Handle: handle}
	if name != nil {
		cp.DisplayName = *name
	}
	if phone != nil {
		if masked, err := wallet.MaskPhone(*phone); err == nil {
			cp.PhoneMasked = masked
		}
	}
	return cp
}

func counterpartyLabel(dir string, v txView) string {
	cp := counterpartyFor(dir, v)
	if cp == nil {
		return ""
	}
	if cp.DisplayName != "" {
		return cp.DisplayName
	}
	if cp.Handle != nil {
		return *cp.Handle
	}
	return cp.PhoneMasked
}

func parseLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, httpx.ValidationFailed("limit must be a positive integer.")
	}
	if n > maxLimit {
		n = maxLimit
	}
	return n, nil
}

func parseTime(raw, field string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, httpx.ValidationFailed(fmt.Sprintf("%s must be an RFC3339 timestamp.", field))
	}
	return t.UTC(), nil
}

// resolveExportWindow defaults to the last 30 days when a bound is missing and
// rejects windows wider than 12 months (FR-HIS-3).
func resolveExportWindow(fromRaw, toRaw string) (time.Time, time.Time, error) {
	var to time.Time
	if strings.TrimSpace(toRaw) == "" {
		to = time.Now().UTC()
	} else {
		t, err := parseTime(toRaw, "to")
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}

	var from time.Time
	if strings.TrimSpace(fromRaw) == "" {
		from = to.AddDate(0, 0, -30)
	} else {
		t, err := parseTime(fromRaw, "from")
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}

	if !to.After(from) {
		return time.Time{}, time.Time{}, httpx.ValidationFailed("to must be after from.")
	}
	if from.AddDate(1, 0, 0).Before(to) {
		return time.Time{}, time.Time{}, httpx.ValidationFailed("Export span must be at most 12 months.")
	}
	return from, to, nil
}

func listView(r historydb.ListTransactionsForUserRow) txView {
	return txView{
		ID:              r.ID,
		Reference:       r.Reference,
		Kind:            r.Kind,
		Status:          r.Status,
		AmountMinor:     r.AmountMinor,
		FeeMinor:        r.FeeMinor,
		Currency:        r.Currency,
		Note:            r.Note,
		FromWalletID:    r.FromWalletID,
		ToWalletID:      r.ToWalletID,
		CreatedAt:       r.CreatedAt,
		CompletedAt:     r.CompletedAt,
		FromUserID:      r.FromUserID,
		ToUserID:        r.ToUserID,
		FromDisplayName: r.FromDisplayName,
		FromHandle:      r.FromHandle,
		FromPhone:       r.FromPhone,
		ToDisplayName:   r.ToDisplayName,
		ToHandle:        r.ToHandle,
		ToPhone:         r.ToPhone,
	}
}

func receiptView(r historydb.GetTransactionForUserRow) txView {
	return txView{
		ID:              r.ID,
		Reference:       r.Reference,
		Kind:            r.Kind,
		Status:          r.Status,
		AmountMinor:     r.AmountMinor,
		FeeMinor:        r.FeeMinor,
		Currency:        r.Currency,
		Note:            r.Note,
		FromWalletID:    r.FromWalletID,
		ToWalletID:      r.ToWalletID,
		CreatedAt:       r.CreatedAt,
		CompletedAt:     r.CompletedAt,
		FromUserID:      r.FromUserID,
		ToUserID:        r.ToUserID,
		FromDisplayName: r.FromDisplayName,
		FromHandle:      r.FromHandle,
		FromPhone:       r.FromPhone,
		ToDisplayName:   r.ToDisplayName,
		ToHandle:        r.ToHandle,
		ToPhone:         r.ToPhone,
	}
}
