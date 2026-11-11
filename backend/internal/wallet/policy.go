package wallet

import (
	"errors"
	"fmt"
)

// Status is the customer-facing life of a wallet.
type Status string

const (
	StatusActive Status = "active"
	StatusFrozen Status = "frozen"
	StatusClosed Status = "closed"
)

// FreezeMode selects which direction a frozen wallet still accepts.
// none is only valid while the wallet is active (or closed, with no movement).
type FreezeMode string

const (
	// FreezeNone allows both directions.
	FreezeNone FreezeMode = "none"
	// FreezeDebit blocks sends and cash-out. Incoming credits still post.
	FreezeDebit FreezeMode = "debit"
	// FreezeFull blocks every movement, including receives.
	FreezeFull FreezeMode = "full"
)

// Movement is one direction a future money API will ask permission for.
type Movement string

const (
	// MovementSend is a peer transfer or a cash-out. Both debit the wallet.
	MovementSend Movement = "send"
	// MovementReceive is an incoming credit.
	MovementReceive Movement = "receive"
)

var (
	// ErrClosed means the wallet is shut and the balance had to be zero to get there.
	ErrClosed = errors.New("wallet: closed")
	// ErrFrozenDebit means the customer can still receive, but cannot spend.
	ErrFrozenDebit = errors.New("wallet: frozen for spending")
	// ErrFrozenFull means nothing moves in or out.
	ErrFrozenFull = errors.New("wallet: frozen for all movement")
)

// Allows reports whether a movement may be posted.
//
// Call it from the handler that moves money (transfer, cash-out), inside the
// same database transaction that locks the wallet, before ledger.Post.
// GET /v1/wallets does not call it. A frozen customer can still see the
// balance they are not allowed to spend.
func Allows(status Status, mode FreezeMode, movement Movement) error {
	if movement != MovementSend && movement != MovementReceive {
		return fmt.Errorf("wallet: unknown movement %q", movement)
	}
	switch status {
	case StatusClosed:
		return ErrClosed
	case StatusActive, StatusFrozen:
	default:
		return fmt.Errorf("wallet: unknown status %q", status)
	}

	switch mode {
	case FreezeNone:
		if status == StatusFrozen {
			return ErrFrozenDebit
		}
		return nil
	case FreezeDebit:
		if movement == MovementReceive {
			return nil
		}
		return ErrFrozenDebit
	case FreezeFull:
		return ErrFrozenFull
	default:
		return fmt.Errorf("wallet: unknown freeze mode %q", mode)
	}
}
