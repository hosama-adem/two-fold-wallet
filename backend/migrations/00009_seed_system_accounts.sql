-- +goose Up
-- Platform buckets every environment must have before the first top-up.
-- Ids are fixed so a row means the same account in every database.
-- allow_negative is true: clearing can lead or lag a settlement, a fee
-- reversal can debit revenue through zero, and suspense can sit on either side.
-- User wallets are not seeded here. They are liabilities created with the user,
-- and they keep the default allow_negative = false.

INSERT INTO accounts (
    id, code, type, normal_balance, currency, allow_negative, balance_minor, version, owner_user_id
) VALUES
    (
        'a0000000-0000-4000-8000-000000000001',
        'provider_clearing:telebirr',
        'asset',
        'debit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000002',
        'provider_clearing:chapa',
        'asset',
        'debit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000003',
        'payouts_pending',
        'liability',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000004',
        'fee_revenue',
        'revenue',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000005',
        'suspense',
        'equity',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    );

-- +goose Down
-- Fails if a posting already references one of these accounts. That is
-- deliberate: seeded history is not something a down should orphan.
DELETE FROM accounts
WHERE code IN (
    'provider_clearing:telebirr',
    'provider_clearing:chapa',
    'payouts_pending',
    'fee_revenue',
    'suspense'
);
