# ADR 0003: Sorted wallet locks, hot system accounts stay unlocked

**Status:** Accepted
**Date:** 2026-11-12

## Context

Two transfers at the same moment can each read a balance, each decide the
send is allowed, and each write their own number back. The second write
destroys the first. That is a lost update, and it is how a wallet goes
negative or pays the same birr twice.

The mirror image is a deadlock. A→B locks the sender, then waits for the
recipient. B→A has already locked that recipient and is waiting for the
sender. Postgres reports `40P01`. Both transactions die. Retrying hides the
bug and adds a latency spike on the exact path that is already hot.

PRD §8.1 fixes both with one rule, inside a single `READ COMMITTED`
transaction:

1. Collect the user wallet accounts the movement touches.
2. `SELECT … FOR UPDATE` them in sorted id order.
3. Read balance and status from the locked rows.
4. Insert the journal and postings.
5. Update the cached balance with `WHERE id = $1 AND version = $2`.
6. Commit.

A second fact sits next to that rule. `fee_revenue` and
`provider_clearing:*` are on almost every money movement once fees and
provider flows exist. They are hot rows. Locking them would put the whole
platform in a queue behind one tuple.

## Decision

### User wallets lock in sorted id order

`POST /v1/transfers` locks both wallets and their liability accounts
together, with `FOR UPDATE OF w, a`, in ascending wallet-id byte order.
The posting engine then locks the accounts that are actually in the journal,
again in sorted account-id order. On a peer transfer those are the same two
accounts, already held by this transaction, so the second lock cannot invert
order with another transfer.

Opposing sends take the same order:

```
wallet id A < wallet id B

A → B:  lock A, lock B, check, post, commit
B → A:  lock A, lock B, check, post, commit
```

One of them waits at the first lock. It does not hold the row the other
needs. There is no cycle, so there is no deadlock. The waiter then reads the
balance the winner committed, which is why a drain cannot overshoot zero.

The same sort applies to any set of wallets, not only a pair. Locks are a
total order. A cycle needs two transactions to disagree about which row
comes first.

### The version predicate stays

The balance update is `WHERE version = <the version we locked>`. A path that
forgets the lock does not overwrite a newer cache. It fails the predicate
(`ledger.ErrVersionConflict`) and the transaction rolls back. The lock is
what makes the common path succeed. The version is what makes a forgotten
lock loud.

### Hot system accounts stay unlocked

`fee_revenue`, `provider_clearing:telebirr`, `provider_clearing:chapa`,
`payouts_pending`, and `suspense` are not in the transfer lock set.

A fee-0 peer journal is two lines: debit the sender, credit the recipient.
The hot rows are absent, so neither the wallet lock nor `ledger.Post` touches
them. Day 17 may add a fee posting. That line is still not a locked row. The
concurrency suite holds `FOR UPDATE` on every system account and still
expects `POST /v1/transfers` to commit, which is the regression test for
this decision.

Why they stay unlocked:

| If we lock the hot row | What happens |
|---|---|
| Every transfer takes `fee_revenue` | Throughput collapses to one transfer at a time |
| A→B and a top-up both need clearing | Unrelated flows deadlock or queue on a row the user never sees |
| The cache is updated in line | Correct, and irrelevant — the platform is already serial |

Postings are the books. The cached balance on a hot account is allowed to
lag. `allow_negative` is true on those five rows so a stale cache is not a
hard stop. The drift job (PRD §7.5, Day 27) rebuilds the cache from
postings. User wallets are the opposite: `allow_negative` is false, and the
cache is updated under the row lock before commit, because that number is
what the next transfer is allowed to spend.

## Consequences

- A→B racing B→A cannot deadlock on the pair. The suite runs 500 of each,
  together, and treats `40P01` or `40001` as a failed build.
- Ten concurrent debits against one balance succeed exactly as many times as
  the balance can pay. The committed balance is never negative, and neither
  is any `balance_after_minor` on those postings.
- Fifty requests with one `Idempotency-Key` insert one `transactions` row.
  The key is claimed before the handler; the unique
  `(initiator_user_id, idempotency_key)` index is the second net.
- A future fee line that starts locking `fee_revenue` will fail
  `TestConcurrencySystemAccountsStayUnlocked` before it serializes production.
- Lock waits on a popular wallet are real. They are a queue, not a retry
  loop. Deadlock is not an expected outcome and is not caught and retried.

## Alternatives considered

- **Lock sender, then recipient, in request order.** Rejected. A→B and B→A
  deadlock. That is the bug this day exists to prove we do not have.
- **`FOR UPDATE NOWAIT` plus application retry.** Rejected as the primary
  strategy. A retry loop hides a lock-order bug and turns contention into
  extra latency. `NOWAIT` remains a fair tool for a caller that would rather
  fail than wait. It is not how transfers avoid deadlocks.
- **Serializable isolation and retry on `40001`.** Rejected for the hot
  path. Under opposing transfers the database would abort one side and force
  a retry that sorted `READ COMMITTED` locks do not need. The suite still
  fails the build if a `40001` appears, so a later change to the isolation
  level cannot pass quietly.
- **One advisory lock per wallet pair.** Rejected. The rows we are about to
  change are the right thing to lock. An advisory lock can be forgotten by
  the next writer; a row lock cannot be skipped by a second `UPDATE`.
- **Lock `fee_revenue` so its cache is always fresh.** Rejected. Every
  transfer in the system would take that lock. The cache would be exact and
  the product would be single-threaded. Postings already record the fee.
  Freshness of the hot cache is a batch job, not a row lock on the send path.
