package wallet

import (
	"errors"
	"testing"
)

func TestAllowsFreezeModes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   Status
		mode     FreezeMode
		movement Movement
		want     error
	}{
		{"active send", StatusActive, FreezeNone, MovementSend, nil},
		{"active receive", StatusActive, FreezeNone, MovementReceive, nil},
		{"debit freeze still receives", StatusFrozen, FreezeDebit, MovementReceive, nil},
		{"debit freeze blocks send", StatusFrozen, FreezeDebit, MovementSend, ErrFrozenDebit},
		{"full freeze blocks send", StatusFrozen, FreezeFull, MovementSend, ErrFrozenFull},
		{"full freeze blocks receive", StatusFrozen, FreezeFull, MovementReceive, ErrFrozenFull},
		{"closed blocks send", StatusClosed, FreezeNone, MovementSend, ErrClosed},
		{"closed blocks receive", StatusClosed, FreezeNone, MovementReceive, ErrClosed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Allows(tc.status, tc.mode, tc.movement)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Allows() = %v, want %v", err, tc.want)
			}
		})
	}
}
