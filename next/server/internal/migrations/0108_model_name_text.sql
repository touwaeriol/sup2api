-- A model name has no length limit (the gateway's 256-byte check, the
-- 200-byte truncation of usage records and the 200-character price model id
-- are gone): every column that stores one becomes text.
--
--   usage_logs.model, usage_logs.upstream_model
--   usage_hourly.model, usage_hourly.upstream_model
--   model_prices.model (its check keeps the character set, drops the length)
--   price_rules.model
--
-- varchar(n) to text is binary coercible: no table rewrite and no index
-- rebuild, only a short ACCESS EXCLUSIVE lock per table. The trigger
-- usage_hourly_mark_update names model and upstream_model in its UPDATE OF
-- list, which PostgreSQL does not let a column type change pass, so it is
-- dropped and recreated as 0096 defined it, in this transaction.
--
-- The unique btree indexes model_prices_model_uq and price_rules_group_uq /
-- price_rules_segment_uq still cover the model of a price: an entry larger
-- than a btree page allows (about 2.7 KB) fails its insert with an error,
-- it is not cut. usage_logs and usage_hourly have no index on these columns.

-- Fail fast when a lock is not to be had, rather than queue for it: whoever
-- reads usage_logs would queue behind this migration in turn.
SET LOCAL lock_timeout = '10s';

DROP TRIGGER IF EXISTS usage_hourly_mark_update ON usage_logs;

ALTER TABLE usage_logs
    ALTER COLUMN model TYPE text,
    ALTER COLUMN upstream_model TYPE text;
ALTER TABLE usage_hourly
    ALTER COLUMN model TYPE text,
    ALTER COLUMN upstream_model TYPE text;
ALTER TABLE price_rules ALTER COLUMN model TYPE text;
ALTER TABLE model_prices DROP CONSTRAINT model_prices_model_id_chk;
ALTER TABLE model_prices ALTER COLUMN model TYPE text;
ALTER TABLE model_prices ADD CONSTRAINT model_prices_model_id_chk
    CHECK (model ~ '^[A-Za-z0-9._:/@+-]+$');

CREATE OR REPLACE TRIGGER usage_hourly_mark_update AFTER UPDATE OF
        created_at, user_id, api_key_id, group_id, account_id, model, upstream_model, protocol, platform,
        success, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cache_creation_1h_tokens,
        total_cost, base_cost, rate_multiplier, billing_detail, anomalies, latency_ms, first_token_ms
    ON usage_logs FOR EACH ROW
    WHEN (date_trunc('hour', LEAST(OLD.created_at, NEW.created_at), 'UTC') < clock_timestamp() - interval '75 minutes')
    EXECUTE FUNCTION usage_hourly_mark();
