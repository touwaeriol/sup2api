-- The protocol the last console test of an account used (its plugin's
-- usage_protocol, CONTRACTS §122), shown with last_test. Nullable and
-- additive: an older core running on the new table ignores it.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS last_test_protocol text;
