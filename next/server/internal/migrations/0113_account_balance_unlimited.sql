-- Account balances read through the plugin (CONTRACTS §52, core 0.1.304):
--
--   updated_at  NULL until the first successful read: a first read that
--               fails is stored as an error snapshot without an amount
--               (it used to violate NOT NULL and was lost);
--   unlimited   the upstream reports no limit for the account (an amount
--               and a currency are then meaningless and left 0 and '');
--   source      dropped: it only ever held "active" and nothing read it.
--
-- All three change the catalog only (no table rewrite); the table is empty
-- anyway: its only reader went with claude_oauth (CONTRACTS §116).
ALTER TABLE account_balances ALTER COLUMN updated_at DROP NOT NULL;
ALTER TABLE account_balances ADD COLUMN IF NOT EXISTS unlimited boolean NOT NULL DEFAULT false;
ALTER TABLE account_balances DROP COLUMN IF EXISTS source;
