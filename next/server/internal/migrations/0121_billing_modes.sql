-- Billing modes are per_request, per_token and expression only (CONTRACTS
-- §131, core 0.1.306). One way, in place:
--
--   1. per_token configs of five flat prices (model_prices, and custom
--      price_rules) become one tier: {"tiers":[{"name":"base","max_len":null,
--      "unit":"token",<prices>}],"rules":[]};
--   2. expression prices edited visually (config.tiers) become per_token when
--      every tier has no flat fee and every rule is a header or param
--      contains/eq, or a time window: tiers flattened ("prices" lifted onto
--      the tier, unit added; a lone tier has no max_len), each rule one
--      condition. Their expressions are what the per_token generator makes
--      of the new config;
--   3. every other expression price (video templates, tiers with a flat fee,
--      param ranges, source mode) keeps its expression with config {};
--   4. request rules are written in the language itself: an expression
--      "A ||| B ||| C" becomes "(A) * (B) * (C)" (a part that ends in a line
--      comment gets its ")" on a new line), with its new expr_hash, in
--      model_prices, price_rules and model_price_history (a row for the new
--      hash; the row of the old hash keeps its hash, for the usage rows that
--      froze it, and gets the new text, which no longer has to be split), and
--      in the expressions the pricing inputs of unsettled usage rows carry.
--
-- 1 to 3 leave expression and expr_hash as they are. A second run finds
-- nothing to change: every per_token config has tiers, every expression
-- config is {} and no expression has a separator.

-- The prices of a tier: the token prices that are set, nothing else.
CREATE FUNCTION billing_modes_prices(j jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_object_agg(key, value), '{}')
    FROM jsonb_each(CASE WHEN jsonb_typeof(j) = 'object' THEN j ELSE '{}' END)
    WHERE key IN ('p', 'c', 'cr', 'cc', 'cc1h') AND value <> 'null'
$$;

-- An old visual rule as one condition, NULL when it has none.
CREATE FUNCTION billing_modes_cond(r jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN src = 'header' AND op IN ('contains', 'eq') THEN
            jsonb_build_object('source', 'header', 'key', r->>'name', 'op', op, 'value', r->'value')
        WHEN src = 'param' AND op IN ('contains', 'eq') THEN
            jsonb_build_object('source', 'param', 'key', COALESCE(NULLIF(btrim(r->>'path'), ''), btrim(r->>'name')), 'op', op, 'value', r->'value')
        WHEN src = 'time' THEN
            jsonb_strip_nulls(jsonb_build_object('source', 'time', 'tz', r->'tz', 'from_hour', r->'from_hour', 'to_hour', r->'to_hour'))
    END
    FROM (SELECT COALESCE(NULLIF(r->>'source', ''), r->>'kind') AS src,
        COALESCE(NULLIF(r->>'op', ''), CASE COALESCE(NULLIF(r->>'source', ''), r->>'kind') WHEN 'header' THEN 'contains' ELSE 'eq' END) AS op) s
$$;

-- Whether an old visual config becomes per_token.
CREATE FUNCTION billing_modes_convertible(cfg jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_typeof(cfg->'tiers') = 'array' AND jsonb_array_length(cfg->'tiers') > 0
        AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(cfg->'tiers') t
            WHERE COALESCE(t->'flat', 'null') NOT IN ('null', '0'))
        AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(cfg->'rules') = 'array' THEN cfg->'rules' ELSE '[]' END) r
            WHERE billing_modes_cond(r) IS NULL)
$$;

-- The per_token config of an old visual config.
CREATE FUNCTION billing_modes_tokens(cfg jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object(
        'tiers', (SELECT jsonb_agg(jsonb_build_object('name', t->'name',
                'max_len', CASE WHEN jsonb_array_length(cfg->'tiers') = 1 THEN 'null' ELSE COALESCE(t->'max_len', 'null') END,
                'unit', 'token')
            || billing_modes_prices(CASE WHEN jsonb_typeof(t->'prices') = 'object' THEN t->'prices' ELSE t END) ORDER BY n)
            FROM jsonb_array_elements(cfg->'tiers') WITH ORDINALITY a(t, n)),
        'rules', (SELECT COALESCE(jsonb_agg(jsonb_build_object('multiplier', r->'multiplier', 'conds', jsonb_build_array(billing_modes_cond(r))) ORDER BY n), '[]')
            FROM jsonb_array_elements(CASE WHEN jsonb_typeof(cfg->'rules') = 'array' THEN cfg->'rules' ELSE '[]' END) WITH ORDINALITY a(r, n)))
$$;

-- An old flat per_token config as one tier.
CREATE FUNCTION billing_modes_one_tier(cfg jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object('tiers', jsonb_build_array(
        jsonb_build_object('name', 'base', 'max_len', 'null'::jsonb, 'unit', 'token') || billing_modes_prices(cfg)), 'rules', '[]'::jsonb)
$$;

-- "A ||| B" as "(A) * (B)", without the version prefix; NULL when src has
-- no separator outside string literals (the old engine's split).
CREATE FUNCTION billing_modes_product(src text) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE
    body text := regexp_replace(btrim(src, E' \t\r\n'), '^v1\s*:\s*', '');
    q text := '';
    commented boolean := false;
    start int := 1;
    i int := 1;
    ch text;
    parts text[] := '{}';
    closers text[] := '{}';
BEGIN
    WHILE i <= length(body) LOOP
        ch := substr(body, i, 1);
        IF q <> '' THEN
            IF ch = '\' AND q <> '`' THEN
                i := i + 1;
            ELSIF ch = q THEN
                q := '';
            END IF;
        ELSIF ch IN ('"', '''', '`') THEN
            q := ch;
        ELSIF substr(body, i, 3) = '|||' THEN
            parts := parts || btrim(substr(body, start, i - start), E' \t\r\n');
            closers := closers || CASE WHEN commented THEN E'\n)' ELSE ')' END;
            commented := false;
            i := i + 2;
            start := i + 1;
        ELSIF substr(body, i, 2) = '//' THEN
            commented := true;
        END IF;
        i := i + 1;
    END LOOP;
    IF cardinality(parts) = 0 THEN
        RETURN NULL;
    END IF;
    parts := parts || btrim(substr(body, start), E' \t\r\n');
    closers := closers || CASE WHEN commented THEN E'\n)' ELSE ')' END;
    body := '';
    FOR i IN 1..cardinality(parts) LOOP
        body := body || CASE WHEN i > 1 THEN ' * (' ELSE '(' END || parts[i] || closers[i];
    END LOOP;
    RETURN body;
END
$$;

-- expr.Hash of an expression body: sha256 of "v1:<body>".
CREATE FUNCTION billing_modes_hash(body text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT encode(sha256(convert_to('v1:' || body, 'UTF8')), 'hex')
$$;

-- j with the expression under e rewritten and its hash under h.
CREATE FUNCTION billing_modes_rehash(j jsonb, e text, h text) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN p IS NULL THEN j ELSE j || jsonb_build_object(e, p, h, billing_modes_hash(p)) END
    FROM (SELECT billing_modes_product(j->>e) AS p) s
$$;

-- The priced components (core.PricedUsage) of usage pricing inputs.
CREATE FUNCTION billing_modes_items(items jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_agg(CASE WHEN jsonb_typeof(i->'Price') IS DISTINCT FROM 'object' THEN i
        ELSE jsonb_set(i, '{Price}', (SELECT CASE WHEN jsonb_typeof(pr->'Base') = 'object'
            THEN jsonb_set(pr, '{Base}', billing_modes_rehash(pr->'Base', 'expression', 'expr_hash')) ELSE pr END
            FROM (SELECT billing_modes_rehash(i->'Price', 'Expression', 'ExprHash') AS pr) s)) END ORDER BY n), '[]')
    FROM jsonb_array_elements(items) WITH ORDINALITY a(i, n)
$$;

-- 1.
UPDATE model_prices SET config = billing_modes_one_tier(config) WHERE mode = 'per_token' AND NOT config ? 'tiers';
UPDATE price_rules SET config = billing_modes_one_tier(config)
WHERE kind = 'custom' AND mode = 'per_token' AND config IS NOT NULL AND NOT config ? 'tiers';

-- 2 and 3.
UPDATE model_prices SET mode = 'per_token', config = billing_modes_tokens(config)
WHERE mode = 'expression' AND billing_modes_convertible(config);
UPDATE model_prices SET config = '{}' WHERE mode = 'expression' AND config <> '{}';

-- 4.
INSERT INTO model_price_history (expr_hash, expression, expr_version)
SELECT billing_modes_hash(p), 'v1:' || p, 1
FROM (SELECT billing_modes_product(expression) AS p FROM model_price_history
    UNION ALL SELECT billing_modes_product(expression) FROM model_prices
    UNION ALL SELECT billing_modes_product(expression) FROM price_rules) s
WHERE p IS NOT NULL
ON CONFLICT (expr_hash) DO NOTHING;
UPDATE model_price_history SET expression = 'v1:' || billing_modes_product(expression)
WHERE billing_modes_product(expression) IS NOT NULL;
UPDATE model_prices SET expression = billing_modes_product(expression), expr_hash = billing_modes_hash(billing_modes_product(expression))
WHERE billing_modes_product(expression) IS NOT NULL;
UPDATE price_rules SET expression = billing_modes_product(expression), expr_hash = billing_modes_hash(billing_modes_product(expression))
WHERE billing_modes_product(expression) IS NOT NULL;
UPDATE usage_logs SET billing_detail = jsonb_set(jsonb_set(
        CASE WHEN jsonb_typeof(billing_detail #> '{inputs,pricing,base}') = 'object'
            THEN jsonb_set(billing_detail, '{inputs,pricing,base}', billing_modes_rehash(billing_detail #> '{inputs,pricing,base}', 'expression', 'expr_hash'))
            ELSE billing_detail END,
        '{inputs,additional}', billing_modes_items(COALESCE(billing_detail #> '{inputs,additional}', '[]')), false),
    '{inputs,replacement}', billing_modes_items(COALESCE(billing_detail #> '{inputs,replacement}', '[]')), false)
WHERE billing_status IN ('pending', 'failed', 'reserved') AND billing_detail::text LIKE '%|||%';

DROP FUNCTION billing_modes_items(jsonb);
DROP FUNCTION billing_modes_rehash(jsonb, text, text);
DROP FUNCTION billing_modes_hash(text);
DROP FUNCTION billing_modes_product(text);
DROP FUNCTION billing_modes_one_tier(jsonb);
DROP FUNCTION billing_modes_tokens(jsonb);
DROP FUNCTION billing_modes_convertible(jsonb);
DROP FUNCTION billing_modes_cond(jsonb);
DROP FUNCTION billing_modes_prices(jsonb);
