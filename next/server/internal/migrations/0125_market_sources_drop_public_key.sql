-- Plugin store without keys (CONTRACTS §136).
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

-- The market sources are the channels of the plugin store (CONTRACTS
-- §137.6): the store is one GitHub repository, a channel one of its
-- branches, whose index CI publishes to the branch index-<branch>. One
-- channel is the default, which a listing, review or install without a
-- channel reads. The official row becomes the default channel "正式" on
-- main (keeping its id), the channel "测试" on develop is added, and any
-- other row (the store took only its official row) goes. url is derived
-- from the store's base and the branch; every start of the core rewrites
-- it from its base (SUB2API_MARKET_BASE_URL).
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS branch varchar(64);
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS is_default boolean NOT NULL DEFAULT false;
UPDATE market_sources SET name = '正式', branch = 'main', is_default = true, enabled = true
    WHERE name = 'official' AND branch IS NULL;
DELETE FROM market_sources WHERE branch IS NULL;
ALTER TABLE market_sources ALTER COLUMN branch SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_branch_key ON market_sources (branch);
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_one_default ON market_sources (is_default) WHERE is_default;
INSERT INTO market_sources (name, branch, url, is_default) VALUES
    ('正式', 'main', 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-main/index.json', true),
    ('测试', 'develop', 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-develop/index.json', false)
    ON CONFLICT DO NOTHING;
