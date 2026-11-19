-- The request body limit is the gateway setting max_request_body_mb
-- (settings row "gateway", CONTRACTS §111): the one limit on a gateway
-- request, set in the console under system settings, gateway. It used to be
-- the environment variable of the same name (CONTRACTS §74.3) with a global
-- layer (settings row "environment") and a group layer (groups.environment).
-- The variable is no longer declared. Reading ignores a key that is not
-- declared, but saving refuses it, and the console sends a layer back whole:
-- the environment page and every group holding the key could not be saved.
--
--   1. A value on the global layer becomes the gateway setting, so a limit
--      an administrator chose survives the upgrade. Only a whole number from
--      1 to 1024 (what the variable accepted) is carried over; the other
--      fields of the gateway row stay, and a missing row is created with
--      this key alone (absent fields read as their defaults).
--   2. The key leaves the global layer.
--   3. The key leaves every group. A group's value is dropped: the limit has
--      no group layer any more.
--   4. The account layer never accepted the variable; a value written into
--      it some other way goes too, for the same reason as 3.
--
-- A limit set only through SUB2API_MAX_REQUEST_BODY_MB is not in the
-- database and is not carried over. Data only. One way. Safe to run again.

SET LOCAL lock_timeout = '10s';

WITH old AS (
    SELECT CASE WHEN jsonb_typeof(value -> 'max_request_body_mb') = 'number'
                THEN (value ->> 'max_request_body_mb')::numeric END AS mb
      FROM settings
     WHERE key = 'environment'
)
INSERT INTO settings (key, value)
SELECT 'gateway', jsonb_build_object('max_request_body_mb', mb::int)
  FROM old
 WHERE mb BETWEEN 1 AND 1024 AND mb = trunc(mb)
ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value;

UPDATE settings SET value = value - 'max_request_body_mb'
 WHERE key = 'environment' AND value ? 'max_request_body_mb';
UPDATE groups SET environment = environment - 'max_request_body_mb'
 WHERE environment ? 'max_request_body_mb';
UPDATE accounts SET environment = environment - 'max_request_body_mb'
 WHERE environment ? 'max_request_body_mb';
