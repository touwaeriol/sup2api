-- Online top-up, promo codes and redeem codes in the core (CONTRACTS §140):
-- the payment plugin's tables move here. Payment methods, orders, gateway
-- events, promo codes with their usages, and redeem codes (hash and last
-- four characters only, never the code).
--
-- The payment plugin only ever had a mock channel, so it has no real
-- orders. What it holds that people may still use, its promo codes and
-- unused redeem codes, is copied once when its tables exist; the plugin is
-- uninstalled after the release, which drops its tables. Safe to run again.

SET LOCAL lock_timeout = '10s';

CREATE TABLE IF NOT EXISTS payment_methods (
    id          bigserial     PRIMARY KEY,
    type        varchar(50)   NOT NULL UNIQUE,  -- the gateway's type key (epay "alipay", ...)
    name        varchar(100)  NOT NULL,
    gateway     varchar(100)  NOT NULL,         -- epay | stripe | <plugin id>.<gateway id>
    icon        varchar(50)   NOT NULL DEFAULT '',
    color       varchar(20)   NOT NULL DEFAULT '',
    logo        text          NOT NULL DEFAULT '', -- data: URL or https URL
    min_topup   numeric(20,8) NOT NULL DEFAULT 0,
    enabled     boolean       NOT NULL DEFAULT true,
    sort        int           NOT NULL DEFAULT 0,
    created_at  timestamptz   NOT NULL DEFAULT now(),
    updated_at  timestamptz   NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS promo_codes (
    id            bigserial     PRIMARY KEY,
    code          varchar(64)   NOT NULL UNIQUE,  -- upper case
    bonus_amount  numeric(20,8) NOT NULL,
    min_amount    numeric(20,8) NOT NULL DEFAULT 0,
    max_uses      int           NOT NULL DEFAULT 0, -- 0 = unlimited
    used_count    int           NOT NULL DEFAULT 0,
    status        varchar(20)   NOT NULL DEFAULT 'active', -- active | disabled
    expires_at    timestamptz,
    notes         text          NOT NULL DEFAULT '',
    created_by    bigint        REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    updated_at    timestamptz   NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payment_orders (
    id                  bigserial     PRIMARY KEY,
    out_trade_no        varchar(64)   NOT NULL UNIQUE,  -- s2a_ + 128 random bits
    user_id             bigint        NOT NULL REFERENCES users(id),
    amount              numeric(20,8) NOT NULL,         -- credited balance (USD)
    bonus               numeric(20,8) NOT NULL DEFAULT 0,
    pay_amount          numeric(20,8) NOT NULL,         -- paid in currency, minor-unit exact
    currency            varchar(10)   NOT NULL,
    price               numeric(20,8) NOT NULL,         -- currency per USD at ordering
    discount_rate       numeric(10,4) NOT NULL DEFAULT 1,
    method              varchar(50)   NOT NULL,
    method_name         varchar(100)  NOT NULL DEFAULT '',
    gateway             varchar(100)  NOT NULL,
    merchant_ref        varchar(200)  NOT NULL DEFAULT '',
    promo_code_id       bigint        REFERENCES promo_codes(id) ON DELETE SET NULL,
    promo_code          varchar(64)   NOT NULL DEFAULT '',
    trade_no            varchar(200),                   -- the gateway's transaction number
    gateway_session_id  varchar(200)  NOT NULL DEFAULT '',
    pay_url             text          NOT NULL DEFAULT '',
    qr_code             text          NOT NULL DEFAULT '',
    -- pending | paid | completed | failed | expired | cancelled
    status              varchar(20)   NOT NULL DEFAULT 'pending',
    fail_reason         text          NOT NULL DEFAULT '',
    expires_at          timestamptz   NOT NULL,
    paid_at             timestamptz,
    completed_at        timestamptz,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    -- one gateway transaction pays one order; numbers of different gateways
    -- may coincide
    UNIQUE (gateway, trade_no)
);
CREATE INDEX IF NOT EXISTS payment_orders_user_idx ON payment_orders (user_id, id DESC);
CREATE INDEX IF NOT EXISTS payment_orders_status_idx ON payment_orders (status, expires_at);
CREATE INDEX IF NOT EXISTS payment_orders_created_idx ON payment_orders (created_at DESC);
-- the orders of a promo code: deleting the code locks them first and then
-- sets their promo_code_id to NULL (ON DELETE SET NULL), both by this column
CREATE INDEX IF NOT EXISTS payment_orders_promo_idx ON payment_orders (promo_code_id) WHERE promo_code_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS payment_events (
    id          bigserial     PRIMARY KEY,
    order_id    bigint        REFERENCES payment_orders(id) ON DELETE CASCADE,
    gateway     varchar(100)  NOT NULL,
    -- the gateway's event id; a notification already recorded is a duplicate
    event_id    varchar(200),
    -- NOTIFY_OK | SIGNATURE_INVALID | ORDER_NOT_FOUND | PAYMENT_AMOUNT_MISMATCH | STATUS_CHANGED | ...
    kind        varchar(50)   NOT NULL,
    verified    boolean       NOT NULL DEFAULT false,
    detail      jsonb         NOT NULL DEFAULT '{}', -- whitelisted, non-secret fields only
    created_at  timestamptz   NOT NULL DEFAULT now(),
    UNIQUE (gateway, event_id)
);
CREATE INDEX IF NOT EXISTS payment_events_order_idx ON payment_events (order_id, id);

CREATE TABLE IF NOT EXISTS promo_code_usages (
    id             bigserial     PRIMARY KEY,
    promo_code_id  bigint        NOT NULL REFERENCES promo_codes(id) ON DELETE CASCADE,
    user_id        bigint        NOT NULL REFERENCES users(id),
    order_id       bigint        REFERENCES payment_orders(id) ON DELETE SET NULL,
    bonus_amount   numeric(20,8) NOT NULL,
    status         varchar(20)   NOT NULL DEFAULT 'reserved', -- reserved | used
    created_at     timestamptz   NOT NULL DEFAULT now(),
    used_at        timestamptz,
    UNIQUE (promo_code_id, user_id)
);
CREATE INDEX IF NOT EXISTS promo_code_usages_order_idx ON promo_code_usages (order_id);

CREATE TABLE IF NOT EXISTS redeem_codes (
    id          bigserial     PRIMARY KEY,
    code_hash   varchar(64)   NOT NULL UNIQUE,  -- sha256 hex of the normalized code
    code_tail   varchar(8)    NOT NULL,
    value       numeric(20,8) NOT NULL,
    status      varchar(20)   NOT NULL DEFAULT 'unused', -- unused | used | voided
    used_by     bigint        REFERENCES users(id),
    used_at     timestamptz,
    expires_at  timestamptz,
    notes       text          NOT NULL DEFAULT '',
    created_by  bigint        REFERENCES users(id) ON DELETE SET NULL,
    voided_by   bigint        REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz   NOT NULL DEFAULT now(),
    updated_at  timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS redeem_codes_status_idx ON redeem_codes (status, id DESC);

-- What the payment plugin holds: promo codes (their usages pointed at the
-- plugin's orders, so only the counts come along; a code longer than the
-- core's 64 characters stays behind, deploy/rollback counts them) and the
-- redeem codes not used yet, by hash (the plugin hashed the upper-case code
-- the same way).
DO $$
BEGIN
    IF to_regclass('plg_payment__promo_codes') IS NOT NULL THEN
        INSERT INTO promo_codes (code, bonus_amount, max_uses, used_count, status, expires_at, notes, created_at, updated_at)
        SELECT upper(code), bonus_amount, max_uses, used_count,
               CASE WHEN status = 'active' THEN 'active' ELSE 'disabled' END,
               expires_at, coalesce(notes, ''), created_at, updated_at
          FROM plg_payment__promo_codes
         WHERE length(code) <= 64
        ON CONFLICT (code) DO NOTHING;
    END IF;
    IF to_regclass('plg_payment__redeem_codes') IS NOT NULL THEN
        INSERT INTO redeem_codes (code_hash, code_tail, value, expires_at, notes, created_at, updated_at)
        SELECT code_hash, right(upper(code), 4), value, expires_at, coalesce(notes, ''), created_at, updated_at
          FROM plg_payment__redeem_codes
         WHERE status = 'unused' AND type = 'balance'
        ON CONFLICT (code_hash) DO NOTHING;
    END IF;
EXCEPTION WHEN insufficient_privilege THEN
    -- The core's role cannot read the plugin's tables: the release's
    -- read-only check (deploy/rollback) tells whether anything was left.
    RAISE WARNING 'payment plugin tables not readable, promo and redeem codes not copied: %', SQLERRM;
END $$;
