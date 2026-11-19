-- The code of a node's failed load of a rollout's target version, where the
-- console acts on it: 'outdated_contract' when the runtime refused a package
-- built for an older plugin contract (CONTRACTS §112.10). The node reports it
-- with the failed instance only while the instance entry exists; a rollout
-- that fails in preparation removes it, so the outcome row keeps it and the
-- plugin list derives its refusal from there.
ALTER TABLE plugin_rollout_nodes ADD COLUMN IF NOT EXISTS code varchar(40) NOT NULL DEFAULT '';
