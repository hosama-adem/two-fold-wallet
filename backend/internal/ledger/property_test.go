package ledger

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// TestPropertyValidJournalDebitsEqualCredits is the core double-entry law:
// every journal the constructor accepts has sum(debits) == sum(credits).
func TestPropertyValidJournalDebitsEqualCredits(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		j := genValidJournal(rt)
		debits, credits := sumSides(j.Lines())
		if debits != credits {
			rt.Fatalf("debits %d != credits %d for kind %s (%d lines)", debits, credits, j.Kind(), len(j.Lines()))
		}
		if debits <= 0 {
			rt.Fatalf("balanced journal moved zero money")
		}
	})
}

// TestPropertyInvalidJournalsRejected proves NewJournal is the first gate:
// unbalanced, single-line, and currency-mismatch shapes never become a Journal.
func TestPropertyInvalidJournalsRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		kind := genKind(rt)
		var (
			lines []Line
			want  error
			label string
		)
		switch rapid.IntRange(0, 2).Draw(rt, "invalid_shape") {
		case 0:
			lines, want, label = genUnbalancedLines(rt), ErrUnbalanced, "unbalanced"
		case 1:
			lines, want, label = genSingleLine(rt), ErrEmptyJournal, "single_line"
		default:
			lines, want, label = genCurrencyMismatchLines(rt), ErrCurrencyMismatch, "currency_mismatch"
		}
		_, err := NewJournal(kind, lines)
		if !errors.Is(err, want) {
			rt.Fatalf("%s: got %v, want %v", label, err, want)
		}
	})
}

// TestPropertyFlipLinesStayBalanced is the shape Reverse relies on:
// flipping every direction keeps the journal balanced and inverts each side.
func TestPropertyFlipLinesStayBalanced(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		j := genValidJournal(rt)
		flipped := flipLines(rt, j.Lines())
		debits, credits := sumSides(flipped)
		if debits != credits {
			rt.Fatalf("flipped debits %d != credits %d", debits, credits)
		}
		orig := j.Lines()
		if len(flipped) != len(orig) {
			rt.Fatalf("flip changed line count %d → %d", len(orig), len(flipped))
		}
		for i := range orig {
			if flipped[i].AccountID() != orig[i].AccountID() {
				rt.Fatalf("line %d account changed", i)
			}
			if flipped[i].Amount().AmountMinor() != orig[i].Amount().AmountMinor() {
				rt.Fatalf("line %d amount changed", i)
			}
			if flipped[i].Direction() == orig[i].Direction() {
				rt.Fatalf("line %d direction was not flipped", i)
			}
		}
		// The flipped set is itself a valid journal (same gate Reverse uses).
		if _, err := NewJournal(KindAdjustment, flipped); err != nil {
			rt.Fatalf("flipped lines rejected by NewJournal: %v", err)
		}
	})
}

// TestPropertyBalanceDeltaReverses: posting a line then its opposite is a no-op
// on the cached balance, for every normal-balance side the ledger uses.
func TestPropertyBalanceDeltaReverses(t *testing.T) {
	normals := []Side{Debit, Credit}
	rapid.Check(t, func(rt *rapid.T) {
		normal := rapid.SampledFrom(normals).Draw(rt, "normal")
		direction := rapid.SampledFrom([]Side{Debit, Credit}).Draw(rt, "direction")
		amount := genAmountMinor(rt)
		opp, err := opposite(direction)
		if err != nil {
			rt.Fatalf("opposite: %v", err)
		}
		net := BalanceDelta(normal, direction, amount) + BalanceDelta(normal, opp, amount)
		if net != 0 {
			rt.Fatalf("BalanceDelta(%s,%s,%d) + opposite = %d, want 0", normal, direction, amount, net)
		}
	})
}

// TestPropertyApplyLinesReverseRestores is reverse(post(j)) at the pure-function
// layer Post/Reverse both call: apply the lines, apply the flipped lines, every
// balance is unchanged. Runs at CI scale without a database round-trip.
func TestPropertyApplyLinesReverseRestores(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		j := genValidJournal(rt)
		states := statesForJournal(rt, j, 1_000_000, false)
		before := copyBalances(states)

		if _, err := applyLines(states, j.Lines()); err != nil {
			rt.Fatalf("apply: %v", err)
		}
		if _, err := applyLines(states, flipLines(rt, j.Lines())); err != nil {
			rt.Fatalf("apply flipped: %v", err)
		}
		for id, bal := range before {
			if states[id].balanceMinor != bal {
				rt.Fatalf("account %s: before %d after reverse %d", id, bal, states[id].balanceMinor)
			}
		}
	})
}

// TestPropertyApplyLinesRejectsNegative: a non-negative account that would end
// below zero makes applyLines return ErrNegativeBalance and is the gate Post uses.
func TestPropertyApplyLinesRejectsNegative(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		amount := genAmountMinor(rt)
		from := uuid.Must(uuid.NewV7())
		to := uuid.Must(uuid.NewV7())
		money := mustMoneyRapid(rt, amount)
		lines := []Line{
			mustLineRapid(rt, from, Debit, money),
			mustLineRapid(rt, to, Credit, money),
		}
		// Liability wallets: debit lowers the cache. Zero opening → any debit fails.
		states := map[uuid.UUID]*accountState{
			from: {currency: CurrencyETB, normal: Credit, balanceMinor: 0, version: 0, loadedVersion: 0, allowNegative: false},
			to:   {currency: CurrencyETB, normal: Credit, balanceMinor: 0, version: 0, loadedVersion: 0, allowNegative: false},
		}
		_, err := applyLines(states, lines)
		if !errors.Is(err, ErrNegativeBalance) {
			rt.Fatalf("got %v, want ErrNegativeBalance", err)
		}
	})
}

// TestPropertyApplyLinesCacheMatchesDeltas: after a successful apply, each
// account's cached balance equals opening + Σ BalanceDelta — the in-memory
// form of "cache equals recompute".
func TestPropertyApplyLinesCacheMatchesDeltas(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		j := genValidJournal(rt)
		opening := int64(1_000_000)
		states := statesForJournal(rt, j, opening, false)
		if _, err := applyLines(states, j.Lines()); err != nil {
			rt.Fatalf("apply: %v", err)
		}
		for id, state := range states {
			want := opening
			for _, line := range j.Lines() {
				if line.AccountID() != id {
					continue
				}
				want += BalanceDelta(state.normal, line.Direction(), line.Amount().AmountMinor())
			}
			if state.balanceMinor != want {
				rt.Fatalf("account %s cache %d, Σ deltas %d", id, state.balanceMinor, want)
			}
			if state.balanceMinor < 0 {
				rt.Fatalf("account %s went negative: %d", id, state.balanceMinor)
			}
		}
	})
}

func statesForJournal(rt *rapid.T, j Journal, opening int64, allowNegative bool) map[uuid.UUID]*accountState {
	rt.Helper()
	states := make(map[uuid.UUID]*accountState)
	for _, line := range j.Lines() {
		id := line.AccountID()
		if _, ok := states[id]; ok {
			continue
		}
		// Alternate normal sides so both debit-normal and credit-normal paths move.
		normal := Credit
		if len(states)%2 == 0 {
			normal = Debit
		}
		states[id] = &accountState{
			currency:      line.Amount().Currency(),
			normal:        normal,
			balanceMinor:  opening,
			version:       0,
			loadedVersion: 0,
			allowNegative: allowNegative,
		}
	}
	return states
}

func copyBalances(states map[uuid.UUID]*accountState) map[uuid.UUID]int64 {
	out := make(map[uuid.UUID]int64, len(states))
	for id, state := range states {
		out[id] = state.balanceMinor
	}
	return out
}
