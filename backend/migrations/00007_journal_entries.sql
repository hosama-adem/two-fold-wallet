-- +goose Up
-- One business event on the books. Balanced debits and credits are NOT
-- checked here. The posting engine adds that deferred constraint.
-- A reversal is a new journal that points at the original. The unique
-- column means the original can be reversed only once. Immutability
-- triggers (reject UPDATE/DELETE) arrive with that reversal work.

CREATE TABLE journal_entries (
    id                   UUID PRIMARY KEY,
    transaction_id       UUID REFERENCES transactions (id),
    kind                 TEXT NOT NULL,
    reverses_journal_id  UUID REFERENCES journal_entries (id),
    description          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT journal_entries_reverses_unique UNIQUE (reverses_journal_id),
    CONSTRAINT journal_entries_kind_known CHECK (
        kind IN (
            'p2p',
            'top_up',
            'cash_out',
            'merchant_payment',
            'money_request',
            'adjustment',
            'reversal'
        )
    ),
    -- Reversal journals point back. Every other kind leaves the link empty.
    -- The self-exclusion matters on UPDATE: an immediate FK already rejects
    -- an INSERT that references its own id, because that row is not visible yet.
    CONSTRAINT journal_entries_reversal_link CHECK (
        (
            kind = 'reversal'
            AND reverses_journal_id IS NOT NULL
            AND reverses_journal_id <> id
        )
        OR (
            kind <> 'reversal'
            AND reverses_journal_id IS NULL
        )
    )
);

CREATE INDEX journal_entries_transaction_id_idx ON journal_entries (transaction_id);
CREATE INDEX journal_entries_created_at_idx ON journal_entries (created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS journal_entries_created_at_idx;
DROP INDEX IF EXISTS journal_entries_transaction_id_idx;
DROP TABLE IF EXISTS journal_entries;
