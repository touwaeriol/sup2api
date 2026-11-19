-- Plugins use the core's database user (no per-plugin login role any more,
-- DESIGN-2026-11-18-PLUGIN-DB-CORE-USER): the core records plugin migrations
-- itself, so the definer functions plugin roles used for that (0003, 0023)
-- go. Dropping them also drops their EXECUTE grants to those roles; the roles
-- are converted and dropped by the core at start (dbschema).
DROP FUNCTION IF EXISTS plugin_migrations_applied(varchar);
DROP FUNCTION IF EXISTS plugin_migration_record(varchar, varchar, varchar);
