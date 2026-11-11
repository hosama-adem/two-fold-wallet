package wallet

import "testing"

func TestGateAllows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		gate Gate
		move Movement
		want bool
	}{
		{name: "active send", gate: Gate{Status: StatusActive, FreezeMode: FreezeNone}, move: MovementSend, want: true},
		{name: "active receive", gate: Gate{Status: StatusActive, FreezeMode: FreezeNone}, move: MovementReceive, want: true},
		{name: "active cash out", gate: Gate{Status: StatusActive, FreezeMode: FreezeNone}, move: MovementCashOut, want: true},
		{name: "debit can receive", gate: Gate{Status: StatusFrozen, FreezeMode: FreezeDebit}, move: MovementReceive, want: true},
		{name: "debit cannot send", gate: Gate{Status: StatusFrozen, FreezeMode: FreezeDebit}, move: MovementSend, want: false},
		{name: "debit cannot cash out", gate: Gate{Status: StatusFrozen, FreezeMode: FreezeDebit}, move: MovementCashOut, want: false},
		{name: "full cannot receive", gate: Gate{Status: StatusFrozen, FreezeMode: FreezeFull}, move: MovementReceive, want: false},
		{name: "closed cannot send", gate: Gate{Status: StatusClosed, FreezeMode: FreezeNone}, move: MovementSend, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.gate.Allows(tc.move); got != tc.want {
				t.Fatalf("Allows(%s) = %v, want %v", tc.move, got, tc.want)
			}
		})
	}
}
