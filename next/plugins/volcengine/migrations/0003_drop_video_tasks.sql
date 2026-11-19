-- plg_volcengine 0003: drop the video task ledger of 0002. Runs with
-- search_path pinned to the plugin schema.
--
-- Since forwarding v2 the core owns video tasks (owner, account, model,
-- reservation and polling), and no released version reads or writes this
-- table: the submit path stopped writing it, and the only reader,
-- ResolveModel, ran solely for an endpoint declaring request.modelSource
-- "plugin", which no released manifest of this plugin declares. A node still
-- on an older version therefore never touches it. Safe to run again.

DROP TABLE IF EXISTS video_tasks;
