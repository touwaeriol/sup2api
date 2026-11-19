-- Protocol binding of an account (DESIGN-2026-11-17-PROTOCOL-BINDING):
-- protocols lists the inbound protocols of the account type's own platforms
-- the account serves, NULL meaning all of them; model_protocols is
-- [{models, protocols}], the inbound protocols a listed model is served on
-- ("*" = every model no other entry lists), which is also where a
-- cross-platform conversion is enabled. Nothing is backfilled: an account
-- without model_protocols serves no cross-platform conversion.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS protocols       text[];
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS model_protocols jsonb NOT NULL DEFAULT '[]';
