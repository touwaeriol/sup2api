-- Plugin store without keys (CONTRACTS §136).
--
-- Market sources carry no key: index.json is no longer signed, its source
-- is trusted for its HTTPS origin, and the packages it lists are pinned by
-- their sha256 in it. The public key that verified index.json.sig goes.
--
-- A version installed from a market source runs with the trust level its
-- index gave its publisher, recorded on the version (trust_level; NULL: the
-- publisher's, as before), and remembers where it came from (source:
-- upload, builtin or market:<source id>; NULL for versions recorded
-- before), so that a market upgrade comes from the same source.
--
-- One way: the dropped keys are not needed by any core from this release
-- on; the way back is the pre-upgrade backup. Safe to run again.

SET LOCAL lock_timeout = '10s';

ALTER TABLE market_sources DROP COLUMN IF EXISTS public_key;
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS trust_level varchar(20);
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS source varchar(30);
