-- Billing modes are visual and expression only, and the tiers of a visual
-- price have conditions (CONTRACTS §131, core 0.1.307). One way, configs
-- only, in model_prices and in the custom prices of price_rules:
--
--   1. per_token prices become visual: their tiers ordered by max_len, those
--      without one last (the order the old generator built them in), each
--      max_len turned into "when": {"var": "len", "op": "<=", "value":
--      max_len} and the max_len key removed (a lone tier only loses it);
--   2. per_request prices {price, rules?} become visual with one request
--      tier: {"tiers": [{"name": "base", "unit": "request", "price": price}],
--      "rules": rules, or []};
--   3. price_rules.mode may be visual only.
--
-- expression and expr_hash stay: the new configs generate the same text
-- (expr.Generate, tested). A second run writes nothing: no price is
-- per_token or per_request any more.

SET LOCAL lock_timeout = '10s';

-- A max_len as a number to order by; NULL for none (null, absent).
CREATE FUNCTION tier_conditions_len(v jsonb) RETURNS numeric LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(v) IN ('number', 'string')
            AND btrim(v #>> '{}', E' \t\r\n') ~ '^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]{1,3})?$'
        THEN btrim(v #>> '{}', E' \t\r\n')::numeric END
$$;

-- Tiers by max_len as tiers with conditions, in the old generator's order.
CREATE FUNCTION tier_conditions_tiers(tiers jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_agg((t - 'max_len') || CASE WHEN tier_conditions_len(t->'max_len') IS NULL THEN '{}'::jsonb
                ELSE jsonb_build_object('when', jsonb_build_object('var', 'len', 'op', '<=', 'value', t->'max_len')) END
            ORDER BY tier_conditions_len(t->'max_len') NULLS LAST, n), '[]')
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(tiers) = 'array' THEN tiers ELSE '[]' END) WITH ORDINALITY a(t, n)
$$;

-- The visual config of a per_token or per_request config.
CREATE FUNCTION tier_conditions_visual(mode text, cfg jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE mode
        WHEN 'per_token' THEN jsonb_set(cfg, '{tiers}', tier_conditions_tiers(cfg->'tiers'))
        ELSE jsonb_build_object('tiers', jsonb_build_array(jsonb_build_object('name', 'base', 'unit', 'request', 'price', cfg->'price')),
            'rules', COALESCE(cfg->'rules', '[]'))
    END
$$;

-- 1 and 2.
UPDATE model_prices SET mode = 'visual', config = tier_conditions_visual(mode, config) WHERE mode IN ('per_token', 'per_request');

-- 3, with 1 and 2 for custom prices.
ALTER TABLE price_rules DROP CONSTRAINT IF EXISTS price_rules_mode_check;
UPDATE price_rules SET mode = 'visual', config = tier_conditions_visual(mode, config) WHERE mode IN ('per_token', 'per_request');
ALTER TABLE price_rules ADD CONSTRAINT price_rules_mode_check CHECK (mode IN ('visual'));

DROP FUNCTION tier_conditions_visual(text, jsonb);
DROP FUNCTION tier_conditions_tiers(jsonb);
DROP FUNCTION tier_conditions_len(jsonb);
