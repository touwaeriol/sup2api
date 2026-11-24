-- Plugin id = publisher + key (CONTRACTS §137).
--
-- From this release on the core keys a plugin by its id: the bare key for
-- the first-party publisher sub2api, _<publisher>__<key> for any other
-- (plugins.key and its foreign keys, the plg_<id>__ tables, data
-- directories, permission keys). A plugin of another publisher installed
-- by an earlier core is stored under its bare key, which now names the
-- sub2api plugin of that key. No rewrite moves it: its tables, roles, data
-- and grants would all have to follow. The upgrade stops instead, naming
-- the plugins, which the operator uninstalls on the release before (and
-- releases their keys, purging what they left) and installs again after it.
--
-- Changes nothing; safe to run again.

DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(k, ', ' ORDER BY k) INTO bad FROM (
        SELECT DISTINCT plugin_key || ' (publisher ' || coalesce(manifest->>'publisher', 'none') || ')' AS k
            FROM plugin_versions
            WHERE manifest->>'publisher' IS DISTINCT FROM 'sub2api') s;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'plugins of publishers other than sub2api are installed: %', bad
            USING HINT = 'uninstall them on the previous release first (and release their keys), then upgrade and install them again';
    END IF;
END $$;
