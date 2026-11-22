-- Rates replace each other, one applies (CONTRACTS §97): the most specific
-- of user group × account group × model, user group × account group,
-- account group × model, account group base rate. A model price of an
-- account group is a price of its own only.
--
--   1. group_model_rates: an account group's rate for one model.
--   2. segment_model_rates: a user group's rate for one model in one
--      account group.
--   3. The discount of a user group goes: where it was not 1, every account
--      group the user group has no rate in gets the rate it paid there
--      (base rate × discount).
--   4. A multiplier rule (global price × N) becomes a model rate that keeps
--      the price: the account group's for the model (N × base rate), and
--      for each user group with a rate in that account group, its own for
--      the model (N × that rate). Then price_rules keeps custom prices only
--      and loses kind and multiplier.
--
-- One way: the discount and the multiplier rules are gone, the way back is
-- the pre-upgrade backup. Usage records keep what they were priced with
-- (billing_detail.inputs.pricing). Safe to run again.

SET LOCAL lock_timeout = '10s';

CREATE TABLE IF NOT EXISTS group_model_rates (
    group_id   bigint        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model      text          NOT NULL,
    rate       numeric(10,4) NOT NULL CHECK (rate >= 0),
    updated_by bigint,
    updated_at timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, model)
);

CREATE TABLE IF NOT EXISTS segment_model_rates (
    segment_id bigint        NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    group_id   bigint        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model      text          NOT NULL,
    rate       numeric(10,4) NOT NULL CHECK (rate >= 0),
    updated_at timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (segment_id, group_id, model)
);
CREATE INDEX IF NOT EXISTS segment_model_rates_group_idx ON segment_model_rates (group_id);

-- 3. Before 4, which reads the rates 3 writes.
DO $$ BEGIN
    INSERT INTO segment_groups (segment_id, group_id, rate_override)
    SELECT s.id, g.id, round(COALESCE(g.rate_multiplier, 1) * s.discount, 4)
    FROM segments s CROSS JOIN groups g
    WHERE s.discount <> 1
    ON CONFLICT (segment_id, group_id) DO UPDATE SET rate_override = EXCLUDED.rate_override, updated_at = now()
    WHERE segment_groups.rate_override IS NULL;
EXCEPTION WHEN undefined_column THEN NULL;
END $$;
ALTER TABLE segments DROP COLUMN IF EXISTS discount;

-- 4.
DO $$ BEGIN
    INSERT INTO group_model_rates (group_id, model, rate, updated_by)
    SELECT r.group_id, r.model, round(r.multiplier * COALESCE(g.rate_multiplier, 1), 4), r.updated_by
    FROM price_rules r JOIN groups g ON g.id = r.group_id
    WHERE r.kind = 'multiplier'
    ON CONFLICT (group_id, model) DO NOTHING;

    INSERT INTO segment_model_rates (segment_id, group_id, model, rate)
    SELECT sg.segment_id, r.group_id, r.model, round(r.multiplier * sg.rate_override, 4)
    FROM price_rules r JOIN segment_groups sg ON sg.group_id = r.group_id AND sg.rate_override IS NOT NULL
    WHERE r.kind = 'multiplier'
    ON CONFLICT (segment_id, group_id, model) DO NOTHING;

    DELETE FROM price_rules WHERE kind = 'multiplier';
EXCEPTION WHEN undefined_column THEN NULL;
END $$;

ALTER TABLE price_rules DROP CONSTRAINT IF EXISTS price_rules_kind;
ALTER TABLE price_rules DROP COLUMN IF EXISTS kind;
ALTER TABLE price_rules DROP COLUMN IF EXISTS multiplier;
ALTER TABLE price_rules ALTER COLUMN mode SET NOT NULL;
ALTER TABLE price_rules ALTER COLUMN config SET NOT NULL;
ALTER TABLE price_rules DROP CONSTRAINT IF EXISTS price_rules_expression;
ALTER TABLE price_rules ADD CONSTRAINT price_rules_expression CHECK (expression <> '');
