-- The image share of a request's input and output tokens (CONTRACTS §118):
-- an OpenAI image generation or edit reports them inside input_tokens and
-- output_tokens, and the price variables img / img_o bill them apart. They
-- are part of input_tokens / output_tokens, not added to them, so the totals,
-- usage_hourly and the account usage trigger are unchanged.
--
-- ADD COLUMN with a constant default only changes the catalog: no table
-- rewrite. Fail fast when the lock is not to be had (0108's reason).
SET LOCAL lock_timeout = '10s';

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS image_input_tokens  bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS image_output_tokens bigint NOT NULL DEFAULT 0;
