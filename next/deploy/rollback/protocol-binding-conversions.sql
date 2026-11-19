-- The cross-platform conversions the accounts really served lately, to export
-- before the upgrade that adds the protocol binding of an account (migration
-- 0106, CONTRACTS §110; protocol-binding-release.md). After that upgrade an
-- account converts a request from another platform only for the models its
-- model_protocols enables the inbound protocol for, so every line printed
-- here answers 404 until the account is configured.
--
-- Read-only. It reads usage_logs by created_at (usage_logs_created_idx), the
-- last :days days only (30 unless given), and runs on a database before or
-- after the upgrade alike: it reads no column of 0106.
--
--   psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 --csv \
--     -f protocol-binding-conversions.sql > conversions-before-upgrade.csv
--   psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -v days=7 -f protocol-binding-conversions.sql
--
-- One line per (account, inbound protocol, client model):
--
--   route             converted   = the request was converted for an account
--                                   type that does not declare the platform of
--                                   the inbound protocol: closed after the
--                                   upgrade until enabled.
--                     local_count = a count_tokens request the core estimated
--                                   itself. Closed after the upgrade only when
--                                   the account type does not declare the
--                                   platform of the protocol (an OpenAI account
--                                   answering /v1/messages/count_tokens); an
--                                   account type of that platform keeps
--                                   answering it.
--   enable_protocol   the protocol to tick for the model on the account: the
--                     inbound protocol, or for a count_tokens protocol the
--                     generation protocol it follows.
--   model             the model the client asked for, before the account's
--                     model mapping: the name model_protocols matches.
--   account_state     deleted, or the account's status now.
--
-- A request converted between two endpoints of one platform (a codex account
-- serving /v1/chat/completions through Responses) is not listed: that keeps
-- working without any configuration.

\if :{?days}
\else
\set days 30
\endif

START TRANSACTION READ ONLY;

SELECT u.account_id,
       a.name                                   AS account_name,
       u.plugin_key || '/' || u.account_type    AS account_type,
       CASE WHEN a.id IS NULL OR a.deleted_at IS NOT NULL THEN 'deleted' ELSE a.status END AS account_state,
       CASE WHEN u.upstream_protocol = 'local' THEN 'local_count' ELSE 'converted' END     AS route,
       u.protocol                               AS inbound_protocol,
       CASE u.protocol
            WHEN 'anthropic.count_tokens' THEN 'anthropic.messages'
            WHEN 'gemini.count_tokens'    THEN 'gemini.generate'
            ELSE u.protocol
       END                                      AS enable_protocol,
       u.model,
       u.upstream_protocol,
       u.conversion ->> 'grade'                 AS grade,
       count(*)                                 AS requests,
       count(*) FILTER (WHERE u.success)        AS succeeded,
       max(u.created_at)                        AS last_at
FROM usage_logs u
LEFT JOIN accounts a ON a.id = u.account_id
WHERE u.created_at >= now() - make_interval(days => :days)
  AND u.account_id IS NOT NULL
  AND u.conversion IS NOT NULL
  AND u.upstream_protocol <> ''
  AND (u.upstream_protocol = 'local'
       OR split_part(u.protocol, '.', 1) <> split_part(u.upstream_protocol, '.', 1))
GROUP BY u.account_id, a.id, a.name, a.deleted_at, a.status, u.plugin_key, u.account_type,
         u.protocol, u.model, u.upstream_protocol, u.conversion ->> 'grade'
ORDER BY u.account_id, route, u.protocol, requests DESC, u.model;

COMMIT;
