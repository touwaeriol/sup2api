-- The first-party plugin growth (referral rewards and daily check-in) is
-- removed (core 0.1.304, CONTRACTS §125): the repository records no
-- production deployment installing it. Its last source is commit d466a6daa.
-- This uninstalls it the way plugin/install.Uninstall does with purge
-- (without the node barrier: every node is stopped while this release
-- migrates), as 0111 did for claude_oauth. It declared user permissions
-- (growth:use, growth:stats:read, and growth:read, growth:manage kept as
-- removed), so its permissions and grants go too; it declared a database
-- permission, so its schema plg_growth (referral codes, commissions,
-- check-ins) is dropped, and a legacy role of it carrying the core's marker,
-- as dbschema.DropTx does. It kept no KV entries; its ledger.credit daily
-- counters in Valkey expire on their own (48 hours).
--
-- Kept on purpose:
--   - usage_logs, balance_ledger and audit_logs rows: billing history (the
--     commissions and check-in rewards it credited stay in the ledger as
--     plugin_credit rows);
--   - plugin_executions, plugin_history and plugin_egress_logs rows: the
--     plugin's operating history, kept by install.Uninstall too.
-- It declared no account types, so no account names it.
--
-- One way: the way back is the pre-upgrade backup. Safe to run again.

SET LOCAL lock_timeout = '10s';

-- Tasks of its jobs (scheduled_tasks has no foreign key to plugins).
DELETE FROM scheduled_tasks WHERE plugin_key = 'growth';

-- Its permissions go with their grants: casbin_rule names a permission by
-- key, so no foreign key cascades them (authz.DeletePlugin), and the roles
-- that held one move their revision.
SELECT 1 FROM authz_meta WHERE id = 1 FOR UPDATE;

WITH gone AS (
    DELETE FROM casbin_rule c USING permissions p
    WHERE p.plugin_key = 'growth' AND c.ptype = 'p' AND c.v1 = p.key
    RETURNING c.v0
)
UPDATE roles SET revision = revision + 1
WHERE 'r:' || key IN (SELECT v0 FROM gone);

DELETE FROM permissions WHERE plugin_key = 'growth';

UPDATE authz_meta SET version = version + 1, policy_version = policy_version + 1
WHERE id = 1 AND EXISTS (SELECT 1 FROM plugins WHERE key = 'growth');

-- The key stays with its publisher (install.retireKey); the removed keys
-- (install.removedKeys) refuse any new install of it.
INSERT INTO plugin_key_retirements (plugin_key, publisher_id)
SELECT key, publisher_id FROM plugins WHERE key = 'growth'
ON CONFLICT (plugin_key) DO UPDATE SET publisher_id = EXCLUDED.publisher_id, retired_by = NULL, retired_at = now();

-- Cascades the versions (with their staged grants), the host permission
-- grants, event cursors and dead letters, job runs, rollouts (with their
-- node results and cleanup barrier), runtime nodes, the uninstall barrier
-- and sticky rules.
DELETE FROM plugins WHERE key = 'growth';
DELETE FROM plugin_egress_domains WHERE plugin_key = 'growth';

-- The schema and its migration records (plugin_migrations has no foreign key
-- to plugins since 0002), as dbschema.DropTx drops them.
DROP SCHEMA IF EXISTS plg_growth CASCADE;
DELETE FROM plugin_migrations WHERE plugin_key = 'growth';
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles
               WHERE rolname = 'plg_growth'
                 AND shobj_description(oid, 'pg_authid') =
                     'sub2api plugin role; database=' || current_database() || '; plugin=growth') THEN
        EXECUTE 'DROP OWNED BY plg_growth';
        EXECUTE 'DROP ROLE plg_growth';
    END IF;
END $$;
