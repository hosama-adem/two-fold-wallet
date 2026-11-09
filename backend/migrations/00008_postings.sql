-- +goose Up
-- One line of a journal. amount_minor is always positive.
-- direction says which side of the T-account the line sits on.
-- The café can be credited 300 and debited 3 in the same journal. Both
-- lines stay readable. See docs/adr/0001-double-entry-ledger.md.
-- Currency is stored again so it must match the account.
-- balance_after_minor and account_version are required so a line cannot
-- omit the running balance. This migration does not compute them.

CREATE TABLE postings (
    id                   UUID PRIMARY KEY,
    journal_id           UUID NOT NULL REFERENCES journal_entries (id),
    account_id           UUID NOT NULL,
    direction            TEXT NOT NULL,
    amount_minor         BIGINT NOT NULL,
    currency             TEXT NOT NULL,
    balance_after_minor  BIGINT NOT NULL,
    account_version      BIGINT NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT postings_direction_known CHECK (direction IN ('debit', 'credit')),
    CONSTRAINT postings_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT postings_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT postings_account_version_nonneg CHECK (account_version >= 0),
    CONSTRAINT postings_account_currency_fk
        FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency)
);

CREATE INDEX postings_journal_id_idx ON postings (journal_id);
CREATE INDEX postings_account_created_at_idx ON postings (account_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS postings_account_created_at_idx;
DROP INDEX IF EXISTS postings_journal_id_idx;
DROP TABLE IF EXISTS postings;
