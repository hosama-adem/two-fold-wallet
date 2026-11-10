package wallet

import "github.com/google/uuid"

// Status is wallets.status.
type Status string

const (
	StatusActive Status = "active"
	StatusFrozen Status = "frozen"
	StatusClosed Status = "closed"
)

// FreezeMode is how a frozen wallet still moves, if it moves at all.
// Status active and closed always use FreezeNone. The CHECK
// wallets_freeze_consistent is the same rule in Postgres.
type FreezeMode string

const (
	FreezeNone     FreezeMode = "none"
	FreezeOutbound FreezeMode = "outbound"
	FreezeFull     FreezeMode = "full"
)

// Movement is a money action a later endpoint will ask the wallet to allow.
// Sending and cashing out leave the wallet. Receiving enters it.
type Movement string

const (
	MovementSend    Movement = "send"
	MovementReceive Movement = "receive"
	MovementCashOut Movement = "cash_out"
)

// Gate is the freeze rule without a database round-trip.
// Call it inside the transfer or cash-out transaction, after the wallet row
// is locked. The read API returns the mode and does not apply the gate.
type Gate struct {
	Status     Status
	FreezeMode FreezeMode
}

// Allows reports whether this wallet may perform m.
func (g Gate) Allows(m Movement) bool {
	switch g.Status {
	case StatusActive:
		return m == MovementSend || m == MovementReceive || m == MovementCashOut
	case StatusClosed:
		return false
	case StatusFrozen:
		if g.FreezeMode == FreezeOutbound && m == MovementReceive {
			return true
		}
		return false
	default:
		return false
	}
}

// AccountCode is the ledger code for a user's ETB liability account.
// It fits accounts_code_format: wallet: plus a lowercase UUIDv7.
func AccountCode(userID uuid.UUID) string {
	return "wallet:" + userID.String()
}

// Balance is one wallet as the customer API shows it.
// FrozenReason is intentionally absent.
type Balance struct {
	ID                     uuid.UUID
	Currency               string
	Status                 Status
	FreezeMode             FreezeMode
	LedgerBalanceMinor     int64
	PendingWithdrawalMinor int64
	AvailableMinor         int64
}

// Opened is the wallet row ProvisionETB / OpenETB just found or inserted.
type Opened struct {
	WalletID  uuid.UUID
	AccountID uuid.UUID
	Created   bool
}

// Recipient is a lookup hit. Phone is already masked.
type Recipient struct {
	DisplayName string
	Handle      *string
	PhoneMasked string
}
