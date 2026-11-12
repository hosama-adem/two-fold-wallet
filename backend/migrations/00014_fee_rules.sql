-- +goose Up
-- Configurable fee schedules (FR-FEE). Rules are data: kind + tier + effective_from.
-- Fee = clamp(flat + round_half_up(amount * rate_bps / 10000), min, max) in minor units.
-- waive_below_minor encodes FR-FEE-3 "P2P free under 100 ETB" without hard-coding it.

CREATE TABLE fee_rules (
    id                 UUID PRIMARY KEY,
    kind               TEXT NOT NULL,
    tier               SMALLINT NOT NULL,
    flat_minor         BIGINT NOT NULL,
    rate_bps           INTEGER NOT NULL,
    min_minor          BIGINT NOT NULL,
    max_minor          BIGINT NOT NULL,
    waive_below_minor  BIGINT NOT NULL DEFAULT 0,
    effective_from     TIMESTAMPTZ NOT NULL,

    CONSTRAINT fee_rules_kind_known CHECK (
        kind IN (
            'p2p',
            'top_up',
            'cash_out',
            'merchant_payment',
            'money_request',
            'adjustment'
        )
    ),
    CONSTRAINT fee_rules_tier_range CHECK (tier BETWEEN 0 AND 2),
    CONSTRAINT fee_rules_flat_nonneg CHECK (flat_minor >= 0 AND flat_minor <= 1000000000000000),
    CONSTRAINT fee_rules_rate_bps_range CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    CONSTRAINT fee_rules_min_nonneg CHECK (min_minor >= 0 AND min_minor <= 1000000000000000),
    CONSTRAINT fee_rules_max_bounds CHECK (
        max_minor >= min_minor AND max_minor <= 1000000000000000
    ),
    CONSTRAINT fee_rules_waive_nonneg CHECK (
        waive_below_minor >= 0 AND waive_below_minor <= 1000000000000000
    )
);

CREATE UNIQUE INDEX fee_rules_kind_tier_effective_uidx
    ON fee_rules (kind, tier, effective_from);

CREATE INDEX fee_rules_lookup_idx
    ON fee_rules (kind, tier, effective_from DESC);

-- Illustrative defaults (FR-FEE-3). Fixed ids so every environment shares the chart.
INSERT INTO fee_rules (
    id, kind, tier, flat_minor, rate_bps, min_minor, max_minor, waive_below_minor, effective_from
) VALUES
    (
        'f0000000-0000-4000-8000-000000000001',
        'p2p',
        0,
        0,
        50,
        50,
        1500,
        10000,
        TIMESTAMPTZ '2020-01-01 00:00:00+00'
    ),
    (
        'f0000000-0000-4000-8000-000000000002',
        'top_up',
        0,
        0,
        0,
        0,
        0,
        0,
        TIMESTAMPTZ '2020-01-01 00:00:00+00'
    ),
    (
        'f0000000-0000-4000-8000-000000000003',
        'cash_out',
        0,
        500,
        0,
        500,
        500,
        0,
        TIMESTAMPTZ '2020-01-01 00:00:00+00'
    );

-- +goose Down
DROP INDEX IF EXISTS fee_rules_lookup_idx;
DROP INDEX IF EXISTS fee_rules_kind_tier_effective_uidx;
DROP TABLE IF EXISTS fee_rules;
