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
-- One way: the dropped keys are not needed by any core from this release
-- on; the way back is the pre-upgrade backup. Safe to run again.

SET LOCAL lock_timeout = '10s';

ALTER TABLE market_sources DROP COLUMN IF EXISTS public_key;
ALTER TABLE plugin_versions ADD COLUMN IF NOT EXISTS trust_level varchar(20);
