# Archive — RealLedger

Twofold started as **RealLedger**, an earlier exploration of the same idea: a digital wallet whose balances come from a double-entry ledger, not a mutable `balance` column.

## Why the pivot

| RealLedger | Twofold |
|---|---|
| Working title / repo name | Product name that states the guarantee |
| Docs and experiments scattered | One PRD, one 49-day plan, one design system |
| Identity not fully specified | Phone OTP, rotating refresh, staff MFA audiences |
| Money flows partly sketched | Every MVP flow has ledger postings written down first |

Renaming was not cosmetic. **Twofold** means every movement is written twice — debit and credit — and the books must always sum to zero.

## What belongs here

Put superseded RealLedger READMEs, ADRs, sketches, and screenshots in this folder so the live tree stays Twofold-only.

```
docs/archive/realledger/
  README.md          ← you are here
  (legacy files…)
```

Nothing under this archive is normative for implementation. Canonical requirements live in [`docs/PRD.md`](../../PRD.md) and the day plan in [`docs/implementation-plan.md`](../../implementation-plan.md).

## Credit

Prior art and early drafts by Hosama Adem. Twofold continues that work with a clearer product boundary: sandbox-only, simulated providers, portfolio-grade correctness.
