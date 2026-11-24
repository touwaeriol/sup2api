-- Plugin stores without keys (CONTRACTS §136, §137.6).
--
-- Market sources carry no key: index.json is no longer signed, its source
-- is trusted for its HTTPS origin, and the packages it lists are pinned by
-- their sha256 in it. The public key that verified index.json.sig goes.
--
-- A version installed from the store runs with the trust level its index
-- gave its publisher, recorded on the version (trust_level; NULL: the
-- publisher's, as before).
--
-- One way: the dropped keys and sources are not needed by any core from
-- this release on; the way back is the pre-upgrade backup. Safe to run
-- again.

SET LOCAL lock_timeout = '10s';

ALTER TABLE market_sources DROP COLUMN IF EXISTS public_key;
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS trust_level varchar(20);

-- Plugin stores (CONTRACTS §137.6): a store is a GitHub repository of a
-- plugin registry, its channels are branches, whose index CI publishes to
-- the branch index-<branch>. The official store is seeded; others are
-- added by an administrator. A publisher belongs to the store that first
-- registered it on this core (publishers.store_id; NULL: registered by hand,
-- or before this release).
CREATE TABLE IF NOT EXISTS market_stores (
    id          bigserial    PRIMARY KEY,
    name        varchar(100) NOT NULL,
    repo        varchar(200) NOT NULL UNIQUE,  -- owner/name on GitHub
    official    boolean      NOT NULL DEFAULT false,
    created_at  timestamptz  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS market_stores_one_official ON market_stores (official) WHERE official;
INSERT INTO market_stores (name, repo, official) VALUES ('官方商店', 'touwaeriol/sup2api-plugin', true)
    ON CONFLICT DO NOTHING;
ALTER TABLE publishers ADD COLUMN IF NOT EXISTS store_id bigint REFERENCES market_stores (id) ON DELETE SET NULL;

-- The market sources are the channels of the stores. Each store has one
-- default channel, which a listing, review or install without a channel
-- reads (the official store's for no store either). The official row
-- becomes the official store's default channel "正式" on main (keeping its
-- id), the channel "测试" on develop is added, and any other row (the
-- store took only its official row) goes. url is derived from the store's
-- base and the branch; every start of the core rewrites it (the official
-- store's base is SUB2API_MARKET_BASE_URL when set).
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS branch varchar(64);
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS is_default boolean NOT NULL DEFAULT false;
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS store_id bigint REFERENCES market_stores (id) ON DELETE CASCADE;
UPDATE market_sources SET name = '正式', branch = 'main', is_default = true, enabled = true,
        url = 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-main/index.json'
    WHERE name = 'official' AND branch IS NULL;
DELETE FROM market_sources WHERE branch IS NULL;
UPDATE market_sources SET store_id = (SELECT id FROM market_stores WHERE official) WHERE store_id IS NULL;
ALTER TABLE market_sources ALTER COLUMN branch SET NOT NULL;
ALTER TABLE market_sources ALTER COLUMN store_id SET NOT NULL;
ALTER TABLE market_sources DROP CONSTRAINT IF EXISTS market_sources_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_store_name ON market_sources (store_id, name);
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_store_branch ON market_sources (store_id, branch);
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_one_default ON market_sources (store_id) WHERE is_default;
INSERT INTO market_sources (store_id, name, branch, url, is_default)
    SELECT id, '正式', 'main', 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-main/index.json', true
        FROM market_stores WHERE official
    UNION ALL
    SELECT id, '测试', 'develop', 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-develop/index.json', false
        FROM market_stores WHERE official
    ON CONFLICT DO NOTHING;
