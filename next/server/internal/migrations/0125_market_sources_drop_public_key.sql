-- Plugin id = publisher + key, and the official plugin store without keys
-- (CONTRACTS §136, §137).
--
-- One way: the dropped keys, sources and urls are not needed by any core
-- from this release on; the way back is the pre-upgrade backup. Safe to run
-- again.

SET LOCAL lock_timeout = '10s';

-- From this release on the core keys a plugin by its id: the bare key for
-- the first-party publisher sub2api, _<publisher>__<key> for any other
-- (plugins.key and its foreign keys, the plg_<id>__ tables, data
-- directories, permission keys, key retirements). A plugin of another
-- publisher installed by an earlier core is stored under its bare key, which
-- now names the sub2api plugin of that key, and so is the retirement of its
-- key after an uninstall. No rewrite moves them: tables, roles, data and
-- grants would all have to follow. The upgrade stops first, before any
-- change, naming them; the operator uninstalls such plugins on the release
-- before and releases their keys (which purges what they left), then
-- installs them again after the upgrade. A retirement without a publisher
-- (an unsigned plugin's) stays as it is: it keeps every signed publisher
-- off the key, as before.
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(k, ', ' ORDER BY k) INTO bad FROM (
        SELECT DISTINCT plugin_key || ' (publisher ' || coalesce(manifest->>'publisher', 'none') || ')' AS k
            FROM plugin_versions
            WHERE manifest->>'publisher' IS DISTINCT FROM 'sub2api'
        UNION
        SELECT r.plugin_key || ' (retired key of publisher ' || p.name || ')'
            FROM plugin_key_retirements r JOIN publishers p ON p.id = r.publisher_id
            WHERE p.name <> 'sub2api') s;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'plugins of publishers other than sub2api are installed or keep their keys: %', bad
            USING HINT = 'uninstall them on the previous release first and release their keys, then upgrade and install them again';
    END IF;
END $$;

-- Market sources carry no key: index.json is no longer signed, its source
-- is trusted for its HTTPS origin, and the packages it lists are pinned by
-- their sha256 in it. The public key that verified index.json.sig goes.
--
-- A version installed from the store runs with the trust level its index
-- gave its publisher, recorded on the version (trust_level; NULL: the
-- publisher's, as before).
ALTER TABLE market_sources DROP COLUMN IF EXISTS public_key;
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS trust_level varchar(20);

-- The plugin store (CONTRACTS §137.6) is the GitHub repository of the
-- official plugin registry; its channels are branches, whose index CI
-- publishes to the branch index-<branch>. Only the official store exists;
-- the table leaves room for more.
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

-- The market sources are the channels of the store. Each store has one
-- default channel, which a listing, review or install without a channel
-- reads. A channel's index URL is not stored: the core derives it from the
-- store's base (SUB2API_MARKET_BASE_URL when set) and the branch.
--
-- The first run (no source has a branch yet) adds the channel "测试" on
-- develop, turns the source named official into the default channel "正式"
-- on main (keeping its id) and drops any other source (the store took only
-- its official row); without a default channel it adds 正式 on main. A
-- rerun changes nothing, so a channel an administrator deleted stays
-- deleted.
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS branch varchar(64);
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS is_default boolean NOT NULL DEFAULT false;
ALTER TABLE market_sources ADD COLUMN IF NOT EXISTS store_id bigint REFERENCES market_stores (id) ON DELETE CASCADE;
ALTER TABLE market_sources DROP COLUMN IF EXISTS url;
INSERT INTO market_sources (store_id, name, branch, is_default)
    SELECT id, '测试', 'develop', false FROM market_stores
        WHERE official AND NOT EXISTS (SELECT 1 FROM market_sources WHERE branch IS NOT NULL)
    ON CONFLICT DO NOTHING;
UPDATE market_sources SET name = '正式', branch = 'main', is_default = true, enabled = true
    WHERE name = 'official' AND branch IS NULL;
DELETE FROM market_sources WHERE branch IS NULL;
UPDATE market_sources SET store_id = (SELECT id FROM market_stores WHERE official) WHERE store_id IS NULL;
ALTER TABLE market_sources ALTER COLUMN branch SET NOT NULL;
ALTER TABLE market_sources ALTER COLUMN store_id SET NOT NULL;
ALTER TABLE market_sources DROP CONSTRAINT IF EXISTS market_sources_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_store_name ON market_sources (store_id, name);
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_store_branch ON market_sources (store_id, branch);
CREATE UNIQUE INDEX IF NOT EXISTS market_sources_one_default ON market_sources (store_id) WHERE is_default;
INSERT INTO market_sources (store_id, name, branch, is_default)
    SELECT st.id, '正式', 'main', true FROM market_stores st
        WHERE st.official AND NOT EXISTS (SELECT 1 FROM market_sources c WHERE c.store_id = st.id AND c.is_default)
    ON CONFLICT DO NOTHING;
