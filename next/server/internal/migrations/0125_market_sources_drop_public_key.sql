-- Market sources carry no key (CONTRACTS §136): index.json is no longer
-- signed, its source is trusted for its HTTPS origin, and the packages it
-- lists are pinned by their sha256 in it. The public key that verified
-- index.json.sig goes.
--
-- One way: the dropped keys are not needed by any core from this release
-- on; the way back is the pre-upgrade backup. Safe to run again.

SET LOCAL lock_timeout = '10s';

ALTER TABLE market_sources DROP COLUMN IF EXISTS public_key;
