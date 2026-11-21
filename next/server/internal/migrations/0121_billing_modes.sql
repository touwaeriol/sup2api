-- Billing modes are per_request, per_token and expression only (CONTRACTS
-- §131, core 0.1.306). One way, in place:
--
--   0. custom price rules over a global price in expression mode are deleted:
--      they are inactive (an expression price takes a multiplier only), and
--      2 would make some of them apply again;
--   1. per_token configs of five flat prices (model_prices, and custom
--      price_rules) become one tier: {"tiers":[{"name":"base","max_len":null,
--      "unit":"token",<prices>}],"rules":[]};
--   2. expression prices edited visually (config.tiers) become per_token when
--      every tier has no flat fee and every rule is a header or param
--      contains/eq, or a time window, and the result is a config the
--      per_token generator accepts (names, prices, multipliers, hours, one
--      rest tier): tiers flattened ("prices" lifted onto the tier, unit
--      added; a lone tier has no max_len), each rule one condition. Their
--      expressions are what the per_token generator makes of the new config;
--   3. every other expression price (video templates, tiers with a flat fee,
--      param ranges, invalid configs, source mode) keeps its expression with
--      config {};
--   4. request rules are written in the language itself: an expression
--      "A ||| B ||| C" becomes "(A) * (B) * (C)" (a part with a line comment
--      anywhere gets its ")" on a new line), with its new expr_hash, in
--      model_prices, price_rules and model_price_history (a row for the new
--      hash; the row of the old hash keeps its hash, for the usage rows that
--      froze it, and gets the new text, which no longer has to be split), and
--      in the expressions the pricing inputs of usage rows that can still be
--      priced carry: pending and failed rows, reserved rows, and rows billed
--      at an estimate (abandoned or estimated settlements), which "reconcile
--      again" puts back to reserved.
--
-- Prices that are numeric strings ("0.15") become JSON numbers in 1 and 2.
-- 0 to 3 leave expression and expr_hash as they are. A second run changes
-- nothing: every per_token config has tiers, every expression config is {},
-- no expression has a separator and usage rows are written only when their
-- pricing inputs change. Usage rows are reached by id (their partial index,
-- or the ids settlements and tasks hold), never by a scan of the table.

SET LOCAL lock_timeout = '10s';

-- A number given as a numeric string, as a JSON number; anything else as it is.
CREATE FUNCTION billing_modes_num(v jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\r\n') ~ '^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]{1,3})?$'
        THEN to_jsonb(btrim(v #>> '{}', E' \t\r\n')::numeric) ELSE v END
$$;

-- Whether v is a non-negative JSON number.
CREATE FUNCTION billing_modes_amount(v jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_typeof(v) = 'number' AND (v #>> '{}')::numeric >= 0, false)
$$;

-- Whether v is a whole JSON number from lo to hi (the generator's int hours).
CREATE FUNCTION billing_modes_hour(v jsonb, lo int, hi int) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_typeof(v) = 'number' AND (v #>> '{}') ~ '^[0-9]{1,2}$' AND (v #>> '{}')::int BETWEEN lo AND hi, false)
$$;

-- An array, or [] for anything else (SQL does not promise to evaluate the
-- conditions of an AND in order, so no array function may see a non-array).
CREATE FUNCTION billing_modes_array(j jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(j) = 'array' THEN j ELSE '[]' END
$$;

-- The prices of a tier: the token prices that are set, nothing else.
CREATE FUNCTION billing_modes_prices(j jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_object_agg(key, billing_modes_num(value)), '{}')
    FROM jsonb_each(CASE WHEN jsonb_typeof(j) = 'object' THEN j ELSE '{}' END)
    WHERE key IN ('p', 'c', 'cr', 'cc', 'cc1h') AND value <> 'null'
$$;

-- An old visual rule as one condition the generator accepts, NULL when it
-- has none.
CREATE FUNCTION billing_modes_cond(r jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN src = 'header' AND op IN ('contains', 'eq') AND jsonb_typeof(v) = 'string' AND v <> '""'
            AND lower(btrim(r->>'name')) ~ '^[a-z0-9!#$%&''*+.^_|~-]{1,100}$' THEN
            jsonb_build_object('source', 'header', 'key', r->>'name', 'op', op, 'value', v)
        WHEN src = 'param' AND k <> '' AND octet_length(k) <= 200
            AND (op = 'contains' AND jsonb_typeof(v) = 'string' AND v <> '""'
                OR op = 'eq' AND jsonb_typeof(v) IN ('string', 'number', 'boolean')) THEN
            jsonb_build_object('source', 'param', 'key', k, 'op', op, 'value', v)
        WHEN src = 'time' AND billing_modes_hour(r->'from_hour', 0, 23) AND billing_modes_hour(r->'to_hour', 0, 24)
            AND r->'from_hour' <> r->'to_hour' AND COALESCE(jsonb_typeof(r->'tz'), 'null') IN ('null', 'string')
            AND COALESCE(NULLIF(btrim(r->>'tz'), ''), 'UTC') IN (SELECT name FROM pg_timezone_names) THEN
            jsonb_strip_nulls(jsonb_build_object('source', 'time', 'tz', r->'tz', 'from_hour', r->'from_hour', 'to_hour', r->'to_hour'))
    END
    FROM (SELECT src, COALESCE(NULLIF(r->>'op', ''), CASE src WHEN 'header' THEN 'contains' ELSE 'eq' END) AS op,
            COALESCE(NULLIF(btrim(r->>'path'), ''), btrim(r->>'name'), '') AS k, r->'value' AS v
        FROM (SELECT COALESCE(NULLIF(r->>'source', ''), r->>'kind') AS src) s) c
$$;

-- The per_token config of an old visual config.
CREATE FUNCTION billing_modes_tokens(cfg jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
    SELECT jsonb_build_object(
        'tiers', (SELECT jsonb_agg(jsonb_build_object('name', t->'name',
                'max_len', CASE WHEN jsonb_array_length(billing_modes_array(cfg->'tiers')) = 1 THEN 'null' ELSE billing_modes_num(COALESCE(t->'max_len', 'null')) END,
                'unit', 'token')
            || billing_modes_prices(CASE WHEN jsonb_typeof(t->'prices') = 'object' THEN t->'prices' ELSE t END) ORDER BY n)
            FROM jsonb_array_elements(billing_modes_array(cfg->'tiers')) WITH ORDINALITY a(t, n)),
        'rules', (SELECT COALESCE(jsonb_agg(jsonb_build_object('multiplier', billing_modes_num(r->'multiplier'), 'conds', jsonb_build_array(billing_modes_cond(r))) ORDER BY n), '[]')
            FROM jsonb_array_elements(billing_modes_array(cfg->'rules')) WITH ORDINALITY a(r, n)))
$$;

-- Whether a converted config is one the per_token generator accepts: tier
-- names valid and distinct, prices and max_len non-negative numbers, one
-- rest tier and distinct max_len, every rule a non-negative multiplier over
-- a condition.
CREATE FUNCTION billing_modes_valid(cfg jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
    SELECT NOT EXISTS (SELECT 1 FROM jsonb_array_elements(billing_modes_array(cfg->'tiers')) t
            WHERE jsonb_typeof(t->'name') IS DISTINCT FROM 'string' OR t->>'name' !~ '^[A-Za-z0-9_.-]{1,64}$'
                OR t->'max_len' <> 'null' AND NOT billing_modes_amount(t->'max_len')
                OR EXISTS (SELECT 1 FROM jsonb_each(t) WHERE key IN ('p', 'c', 'cr', 'cc', 'cc1h') AND NOT billing_modes_amount(value)))
        AND (SELECT count(DISTINCT t->>'name') = count(*) AND count(*) FILTER (WHERE t->'max_len' = 'null') = 1
                AND count(DISTINCT t->'max_len') FILTER (WHERE t->'max_len' <> 'null') = count(*) FILTER (WHERE t->'max_len' <> 'null')
            FROM jsonb_array_elements(billing_modes_array(cfg->'tiers')) t)
        AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(billing_modes_array(cfg->'rules')) r
            WHERE NOT billing_modes_amount(r->'multiplier') OR r #> '{conds,0}' = 'null')
$$;

-- Whether an old visual config becomes per_token.
CREATE FUNCTION billing_modes_convertible(cfg jsonb) RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT jsonb_array_length(billing_modes_array(cfg->'tiers')) > 0
        AND COALESCE(jsonb_typeof(cfg->'rules'), 'null') IN ('array', 'null')
        AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(billing_modes_array(cfg->'tiers')) t
            WHERE jsonb_typeof(t) <> 'object' OR billing_modes_num(COALESCE(t->'flat', 'null')) NOT IN ('null', '0'))
        AND billing_modes_valid(billing_modes_tokens(cfg))
$$;

-- An old flat per_token config as one tier.
CREATE FUNCTION billing_modes_one_tier(cfg jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_build_object('tiers', jsonb_build_array(
        jsonb_build_object('name', 'base', 'max_len', 'null'::jsonb, 'unit', 'token') || billing_modes_prices(cfg)), 'rules', '[]'::jsonb)
$$;

-- "A ||| B" as "(A) * (B)", without the version prefix; NULL when src has
-- no separator outside string literals (the old engine's split). The text
-- is walked as an array of characters, since substr counts from the start
-- on every call.
CREATE FUNCTION billing_modes_product(src text) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE
    chars text[];
    n int;
    q text := '';
    commented boolean := false;
    start int := 1;
    i int := 1;
    ch text;
    parts text[] := '{}';
    closers text[] := '{}';
    product text := '';
BEGIN
    IF strpos(src, '|||') = 0 THEN
        RETURN NULL;
    END IF;
    chars := regexp_split_to_array(regexp_replace(btrim(src, E' \t\r\n'), '^v1\s*:\s*', ''), '');
    n := cardinality(chars);
    WHILE i <= n LOOP
        ch := chars[i];
        IF q <> '' THEN
            IF ch = '\' AND q <> '`' THEN
                i := i + 1;
            ELSIF ch = q THEN
                q := '';
            END IF;
        ELSIF ch IN ('"', '''', '`') THEN
            q := ch;
        ELSIF ch = '|' AND chars[i + 1] = '|' AND chars[i + 2] = '|' THEN
            parts := parts || btrim(array_to_string(chars[start:i - 1], ''), E' \t\r\n');
            closers := closers || CASE WHEN commented THEN E'\n)' ELSE ')' END;
            commented := false;
            i := i + 2;
            start := i + 1;
        ELSIF ch = '/' AND chars[i + 1] = '/' THEN
            commented := true;
        END IF;
        i := i + 1;
    END LOOP;
    IF cardinality(parts) = 0 THEN
        RETURN NULL;
    END IF;
    parts := parts || btrim(array_to_string(chars[start:n], ''), E' \t\r\n');
    closers := closers || CASE WHEN commented THEN E'\n)' ELSE ')' END;
    FOR i IN 1..cardinality(parts) LOOP
        product := product || CASE WHEN i > 1 THEN ' * (' ELSE '(' END || parts[i] || closers[i];
    END LOOP;
    RETURN product;
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

-- The priced components (core.PricedUsage) of usage pricing inputs; anything
-- but an array as it is.
CREATE FUNCTION billing_modes_items(items jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(items) IS DISTINCT FROM 'array' THEN items ELSE (
        SELECT COALESCE(jsonb_agg(CASE WHEN jsonb_typeof(i->'Price') IS DISTINCT FROM 'object' THEN i
            ELSE jsonb_set(i, '{Price}', (SELECT CASE WHEN jsonb_typeof(pr->'Base') = 'object'
                THEN jsonb_set(pr, '{Base}', billing_modes_rehash(pr->'Base', 'expression', 'expr_hash')) ELSE pr END
                FROM (SELECT billing_modes_rehash(i->'Price', 'Expression', 'ExprHash') AS pr) s)) END ORDER BY n), '[]')
        FROM jsonb_array_elements(billing_modes_array(items)) WITH ORDINALITY a(i, n)) END
$$;

-- A usage row's billing_detail with the expressions of its pricing inputs
-- rewritten: the base price, and the additional and replacement components.
CREATE FUNCTION billing_modes_detail(d jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(d->'inputs') IS DISTINCT FROM 'object' THEN d ELSE jsonb_set(d, '{inputs}', in_
        || CASE WHEN jsonb_typeof(in_ #> '{pricing,base}') = 'object' THEN jsonb_build_object('pricing',
            jsonb_set(in_->'pricing', '{base}', billing_modes_rehash(in_ #> '{pricing,base}', 'expression', 'expr_hash'))) ELSE '{}' END
        || CASE WHEN in_ ? 'additional' THEN jsonb_build_object('additional', billing_modes_items(in_->'additional')) ELSE '{}' END
        || CASE WHEN in_ ? 'replacement' THEN jsonb_build_object('replacement', billing_modes_items(in_->'replacement')) ELSE '{}' END) END
    FROM (SELECT d->'inputs' AS in_) s
$$;

-- The usage rows among ids in one of statuses whose pricing inputs have a
-- separator, rewritten. ids is an array, so the rows are found by primary
-- key and never by a scan; a row is written only when it changes.
CREATE FUNCTION billing_modes_usage(ids bigint[], statuses text[]) RETURNS void LANGUAGE sql AS $$
    UPDATE usage_logs u SET billing_detail = s.d
    FROM (SELECT id, billing_modes_detail(billing_detail) AS d FROM usage_logs
        WHERE id = ANY (ids) AND billing_status = ANY (statuses) AND billing_detail::text LIKE '%|||%') s
    WHERE u.id = s.id AND s.d IS DISTINCT FROM u.billing_detail
$$;

-- 0.
DELETE FROM price_rules r WHERE r.kind = 'custom'
    AND EXISTS (SELECT 1 FROM model_prices m WHERE m.model = r.model AND m.mode = 'expression');

-- 1.
UPDATE model_prices SET config = billing_modes_one_tier(config) WHERE mode = 'per_token' AND NOT config ? 'tiers';
UPDATE price_rules SET config = billing_modes_one_tier(config) WHERE kind = 'custom' AND mode = 'per_token' AND NOT config ? 'tiers';

-- 2 and 3.
UPDATE model_prices SET mode = 'per_token', config = billing_modes_tokens(config)
WHERE mode = 'expression' AND billing_modes_convertible(config);
UPDATE model_prices SET config = '{}' WHERE mode = 'expression' AND config <> '{}';

-- 4. Each product is computed once, and only for text with a separator.
INSERT INTO model_price_history (expr_hash, expression, expr_version)
SELECT billing_modes_hash(p), 'v1:' || p, 1
FROM (SELECT billing_modes_product(expression) AS p FROM model_price_history WHERE expression LIKE '%|||%'
    UNION ALL SELECT billing_modes_product(expression) FROM model_prices WHERE expression LIKE '%|||%'
    UNION ALL SELECT billing_modes_product(expression) FROM price_rules WHERE expression LIKE '%|||%') s
WHERE p IS NOT NULL
ON CONFLICT (expr_hash) DO NOTHING;
UPDATE model_price_history h SET expression = 'v1:' || s.p
FROM (SELECT expr_hash, billing_modes_product(expression) AS p FROM model_price_history WHERE expression LIKE '%|||%') s
WHERE h.expr_hash = s.expr_hash AND s.p IS NOT NULL;
UPDATE model_prices m SET expression = s.p, expr_hash = billing_modes_hash(s.p)
FROM (SELECT id, billing_modes_product(expression) AS p FROM model_prices WHERE expression LIKE '%|||%') s
WHERE m.id = s.id AND s.p IS NOT NULL;
UPDATE price_rules r SET expression = s.p, expr_hash = billing_modes_hash(s.p)
FROM (SELECT id, billing_modes_product(expression) AS p FROM price_rules WHERE expression LIKE '%|||%') s
WHERE r.id = s.id AND s.p IS NOT NULL;
-- Pending and failed rows by their partial index; reserved rows by the
-- settlements and task observations that point at them; rows billed at an
-- estimate by their settlements.
SELECT billing_modes_usage(ARRAY(SELECT id FROM usage_logs WHERE billing_status IN ('pending', 'failed')), '{pending,failed}');
SELECT billing_modes_usage(ARRAY(SELECT usage_log_id FROM pending_settlements
    UNION SELECT usage_log_id FROM async_tasks WHERE observation_status = 'pending'), '{reserved}');
SELECT billing_modes_usage(ARRAY(SELECT usage_log_id FROM pending_settlements WHERE state IN ('abandoned', 'estimated')), '{billed}');

DROP FUNCTION billing_modes_usage(bigint[], text[]);
DROP FUNCTION billing_modes_detail(jsonb);
DROP FUNCTION billing_modes_items(jsonb);
DROP FUNCTION billing_modes_rehash(jsonb, text, text);
DROP FUNCTION billing_modes_hash(text);
DROP FUNCTION billing_modes_product(text);
DROP FUNCTION billing_modes_one_tier(jsonb);
DROP FUNCTION billing_modes_convertible(jsonb);
DROP FUNCTION billing_modes_valid(jsonb);
DROP FUNCTION billing_modes_tokens(jsonb);
DROP FUNCTION billing_modes_cond(jsonb);
DROP FUNCTION billing_modes_prices(jsonb);
DROP FUNCTION billing_modes_array(jsonb);
DROP FUNCTION billing_modes_hour(jsonb, int, int);
DROP FUNCTION billing_modes_amount(jsonb);
DROP FUNCTION billing_modes_num(jsonb);
