-- Model prices are a group's only (CONTRACTS §97): a user group (segment)
-- keeps its discount, its grants and its own rate in each group, but no
-- longer prices models. The model prices of segments are deleted and
-- price_rules loses segment_id, with the indexes over it: a rule is unique
-- by group and model.
--
-- One way: the deleted rules are gone, the way back is the pre-upgrade
-- backup. Usage records priced by one keep what they were priced with
-- (the expression in model_price_history, scale and base in
-- billing_detail.inputs.pricing). Safe to run again.

SET LOCAL lock_timeout = '10s';

DO $$ BEGIN
    DELETE FROM price_rules WHERE segment_id IS NOT NULL;
EXCEPTION WHEN undefined_column THEN NULL;
END $$;

DROP INDEX IF EXISTS price_rules_segment_uq;
DROP INDEX IF EXISTS price_rules_segment_idx;
DROP INDEX IF EXISTS price_rules_group_uq;
ALTER TABLE price_rules DROP COLUMN IF EXISTS segment_id;
CREATE UNIQUE INDEX IF NOT EXISTS price_rules_group_model_uq ON price_rules (group_id, model);
