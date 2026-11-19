-- The first-party plugin claude_oauth (account types claude_oauth and
-- claude_setup_token) is removed (core 0.1.304, CONTRACTS §116): no
-- production deployment installed it. Its last source is commit 77158211c.
-- This uninstalls it the way plugin/install.Uninstall does with purge
-- (without the node barrier: every node is stopped while this release
-- migrates). It declared a user permission (claude_oauth:manage), so its
-- permission and grants go too; it never declared a database permission, so
-- the schema and role statements only cover a hand-made one, as
-- dbschema.DropTx does. Its KV entries live in Valkey, not here: the OAuth
-- sessions there expire on their own (30 minutes).
--
-- Kept on purpose:
--   - accounts with plugin_key 'claude_oauth' (and their groups, usage rows,
--     ledger entries, balance snapshots): they show as orphaned, like the
--     accounts of any uninstalled plugin; an administrator deletes them in
--     the console;
--   - usage_logs, balance_ledger and audit_logs rows: billing history;
--   - plugin_executions, plugin_history and plugin_egress_logs rows: the
--     plugin's operating history, kept by install.Uninstall too.
--
-- One way: the way back is the pre-upgrade backup. Safe to run again.

SET LOCAL lock_timeout = '10s';

-- Tasks of its jobs (scheduled_tasks has no foreign key to plugins).
DELETE FROM scheduled_tasks WHERE plugin_key = 'claude_oauth';

-- Its permissions go with their grants: casbin_rule names a permission by
-- key, so no foreign key cascades them (authz.DeletePlugin), and the roles
-- that held one move their revision.
SELECT 1 FROM authz_meta WHERE id = 1 FOR UPDATE;

WITH gone AS (
    DELETE FROM casbin_rule c USING permissions p
    WHERE p.plugin_key = 'claude_oauth' AND c.ptype = 'p' AND c.v1 = p.key
    RETURNING c.v0
)
UPDATE roles SET revision = revision + 1
WHERE 'r:' || key IN (SELECT v0 FROM gone);

DELETE FROM permissions WHERE plugin_key = 'claude_oauth';

UPDATE authz_meta SET version = version + 1, policy_version = policy_version + 1
WHERE id = 1 AND EXISTS (SELECT 1 FROM plugins WHERE key = 'claude_oauth');

-- The key stays with its publisher (install.retireKey); the removed keys
-- (install.removedKeys) refuse any new install of it.
INSERT INTO plugin_key_retirements (plugin_key, publisher_id)
SELECT key, publisher_id FROM plugins WHERE key = 'claude_oauth'
ON CONFLICT (plugin_key) DO UPDATE SET publisher_id = EXCLUDED.publisher_id, retired_by = NULL, retired_at = now();

-- Cascades the versions (with their staged grants), the host permission
-- grants, event cursors and dead letters, job runs, rollouts (with their
-- node results and cleanup barrier), runtime nodes, the uninstall barrier,
-- plugin migrations and sticky rules.
DELETE FROM plugins WHERE key = 'claude_oauth';
DELETE FROM plugin_egress_domains WHERE plugin_key = 'claude_oauth';

DROP SCHEMA IF EXISTS plg_claude_oauth CASCADE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles
               WHERE rolname = 'plg_claude_oauth'
                 AND shobj_description(oid, 'pg_authid') =
                     'sub2api plugin role; database=' || current_database() || '; plugin=claude_oauth') THEN
        EXECUTE 'DROP OWNED BY plg_claude_oauth';
        EXECUTE 'DROP ROLE plg_claude_oauth';
    END IF;
END $$;
