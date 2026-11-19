-- account_balances.currency holds the unit an upstream gives with its
-- balance, as it gives it, or '' for none (CONTRACTS §52, 2026-11-18: the
-- balance is the upstream's number and unit, never converted). A unit is
-- not necessarily a three-letter currency code, so the column is text
-- instead of VARCHAR(3); no value is cut to fit.
--
-- Widening VARCHAR to TEXT changes the catalog only (no table rewrite).
ALTER TABLE account_balances ALTER COLUMN currency TYPE text;
