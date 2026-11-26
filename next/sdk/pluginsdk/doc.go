// Package pluginsdk is the Go SDK for sub2api-next gRPC process plugins.
//
// A plugin is a normal Go binary whose main calls Serve with a value that
// implements one or more capability interfaces:
//
//	//go:embed manifest.json
//	var manifestJSON []byte
//
//	func main() {
//		pluginsdk.Serve(&myPlugin{}, pluginsdk.WithManifest(manifestJSON))
//	}
//
// Capability interfaces mirror the generated pluginv1.XxxServiceServer
// interfaces (without the mustEmbed method):
//
//	Platform   -> PlatformService   ("platform.adapter.v1"; Execute from
//	              its UpstreamBuilder via ExecuteDefault, or its own Executor)
//	TaskMonitor -> PlatformService.Monitor ("platform.monitor.v1")
//	Hook       -> HookService       ("gateway.hook.v1")
//	JobRunner  -> AppService.RunJob ("app.jobs.v1", see JobMux)
//	EventHandler -> AppService.OnEvents ("app.events.v1")
//	BroadcastHandler -> AppService.OnBroadcast ("app.broadcast.v1", see BroadcastMux)
//	HTTP       -> HTTPService       ("http.routes.v1")
//	AccountRanker -> SchedulerService.RankAccounts ("scheduler.rank.v1")
//	NotificationChannel -> NotificationService.DeliverNotification ("notify.channel.v1")
//	PaymentGateway -> PaymentGatewayService ("payment.gateway.v1", see PaymentGatewayMux)
//	Migration  -> MigrationService  ("migration.data.v1")
//
// A Platform may also implement the optional PlatformService calls, found
// by type assertion and answered UNIMPLEMENTED otherwise: ModelLister,
// ModelResolver, ModelAvailabilityChecker, QuotaReader, CredentialRefresher
// and BalanceReader. The last three only describe upstream requests and
// read their answers; the host sends them. Package standard implements
// ModelLister and BalanceReader for API key account types.
//
// "platform.tasks.v1" and "platform.websocket.v1" name no interface: they say
// what Execute does on the endpoints of the manifest and are reported when
// the manifest declares them.
//
// # The request path
//
// A gateway request has one path. For every attempt on an account the host
// calls Execute with the whole request body, already rewritten by the host
// (protocol conversion, model mapping, forced streaming, the operator's
// parameter overrides), the allow-listed client headers, the account with
// its credentials and the attempt number. The plugin builds the final
// upstream request (Upstream; it signs it itself with package reqsign when
// the upstream needs that) and hands it to the host's HTTP client. It opens
// no socket, sets no size limit, never retries and never picks another
// account. The client has three calls, all bound to the context of the call
// they are made in:
//
//   - Relay: the answer goes to the client. The host only validates the
//     request and sends it after Execute returned ReportHostUsage, so the plugin is
//     not part of the exchange.
//   - RelayAndWait: for endpoints whose usage the plugin reads (request flag
//     extract_usage) and task submissions (task_submission). The host sends
//     now and returns the observation of the answer, which the plugin turns into
//     ReportUsage or ReportTask, or the facts of a failed exchange, which
//     it turns into ReportClassification.
//   - Fetch: a non-streaming exchange whose whole answer comes back to the plugin, for
//     auxiliary requests before the relay and for the upstream query of
//     Monitor. Hosts are limited to the manifest's externalServices and the
//     hosts of the account's own upstream addresses (upstreamFields).
//
// Every request the host sends for the plugin goes to a host the manifest
// declares in externalServices (or a subdomain of one) or that the account
// names among its upstream addresses (upstreamFields); its Host header is
// the URL's (manifest.Manifest.UpstreamHostAllowed, CONTRACTS §112.17).
//
// An execution relays at most once. Usage and task facts travel only in the
// value Execute returns; there is no reporting callback and the host does
// not make up for a response that states nothing. ExecuteDefault does all of
// this for a platform that implements UpstreamBuilder: it picks the form
// from the two flags and cannot forget the report. The SDK runs it for a
// Platform that implements UpstreamBuilder and no Executor; Serve refuses a
// Platform with neither.
//
// A plugin sets no size limit: its gRPC server accepts messages up to gRPC's
// maximum, and the gateway's request body limit is a system setting the
// host enforces.
//
// The SDK implements PluginService itself. Optional lifecycle interfaces
// (Initializer, Configurer, HealthChecker, Shutdowner) let the plugin hook
// into InitHost, Configure, Health and Shutdown. After InitHost the plugin
// reaches the host through the Host interface (log, KV, database, authz,
// ledger, cluster broadcasts via Publish, cluster locks via Locks).
// Settings[T] reads a section of the core's system settings (types in
// package protocol, CONTRACTS §107): kept in the process and read again
// once the host says the section changed, so call it where the value is
// used instead of copying it at Init.
//
// # Running on several nodes
//
// Every node of a cluster runs its own process of the plugin; write the
// plugin for that from the start (CONTRACTS §27.4):
//
//   - Init runs once per process, i.e. on every node, and so does any
//     goroutine it starts. Declare periodic work as manifest jobs[]: each
//     trigger runs on one node for the whole cluster.
//   - Prices, attribution and all ledger writes are the host's: Execute
//     states upstream facts in its return value and nothing else.
//   - Upstream async work uses endpoint.task: Execute returns the accepted
//     Task and TaskMonitor observes it. The host records owner and original
//     account before success, coordinates the observations and serves shared
//     snapshots on every node. Do not poll the same task from jobs[], Init
//     or client query handlers. In Monitor, query the upstream once with
//     Fetch and return the result; the host records progress and settlement
//     atomically and binds the request to the account's proxy and limits.
//     Generic Egress is a separate permission and does not inherit them.
//   - Host.Locks gives cross-node mutual exclusion for other work. The locks
//     are not fenced, so the guarded work must still be idempotent.
//   - KV has no compare-and-set: two nodes doing read-modify-write on one key
//     can lose an update. Use the database (a transaction) or a lock.
//   - Publish is best effort and the publishing node does not receive its
//     own message: apply a change locally, then publish it.
//   - OnEvents is delivered at least once (idempotent on Event.Id), and
//     MigrateData runs once per rollout (the host records it), but a
//     record lost after a successful call means it runs again; both must
//     be idempotent (CONTRACTS §11.7, §36). So must migrations/*.sql.
//
// # Database
//
// A plugin with a database (manifest "database", grant "db.schema") keeps
// its tables in the core's schema beside the core's and other plugins'
// (CONTRACTS §130). Its migrations create relations (tables, indexes,
// sequences, views) and types only, named with manifest.TablePrefix of the
// plugin id (manifest.PluginID: the key of a sub2api plugin,
// "_<publisher>__<key>" of any other, CONTRACTS §137):
// plg_<plugin id>__<name>, at most 63 bytes, PostgreSQL's own names included
// (<table>_pkey, <table>_<column>_key, <table>_<column>_seq carry the
// prefix when the table does; an index or constraint named by hand must
// carry it too). Functions, triggers, operators and every other kind of
// object are refused, prefixed or not. The plugin has one database
// identity, which owns its tables and reaches nothing else: the core's
// tables and other plugins' refuse it. Tables outside the plugin are asked
// for in the manifest's database.access (table, columns, privileges, host
// permission "db.core_read"), granted on the administrator's consent;
// writing the core's tables directly bypasses its caches, authorization
// versions and audit, so use the Host API where there is one.
//
// Host.DB returns a GORM handle (package pgdb) with search_path on the
// core's schema, at most 4 connections per node unless WithDBPoolSize says
// otherwise. Its DSN names the virtual address protocol.DatabaseAddress,
// without a password: the SDK dials it through the host's tunnel, strict
// network mode or not, and the core logs in as the plugin's identity; any
// other address (a unit test's database) is dialled directly. The identity
// has no CREATE at run time: it reads and writes its tables, and only
// migrations change them.
// The handle is bound to the context Host.DB was called with: ask again for
// each call (it is cheap, the connections are shared) and never keep one,
// e.g. from Init for a goroutine; a kept handle fails every statement once
// its context is done, with an error that names this as a possible cause
// (an ordinary cancellation or timeout gets the same error). Plugin code
// does not import pgx.
//
// Statements are built by GORM on the models (Model/Table, Where, Select,
// Create, Updates, Delete, clause.OnConflict, clause.Returning), not
// written out: Raw and Exec only combine built queries ("(?) UNION ALL
// (?)", pgdb.Exists, pgdb.InsertSelect), and pgdb.NextIDs, LockTable and
// TryXactLock cover sequences and locks; package hierarchy keeps a tree in
// a table (a parent and a materialized path per row). SQL text (of Raw, Exec, Where,
// Select, Order, Joins, Table, gorm.Expr, clause.Expr ...) is a constant:
// values go in as ? parameters, and a column a request picks through a
// fixed whitelist into clause.Column or clause.OrderByColumn. TestNoRawSQL
// in package pgdb checks the SDK and the plugins of the repository.
//
// Array parameters and columns are pgdb.Array; a test double of Host
// returns a *gorm.DB (pgdb.Open). Errors wrap the driver's. Those of Exec,
// Create, Save, Find, Scan and the like are translated too: a unique
// violation also matches gorm.ErrDuplicatedKey (foreign key and check
// violations gorm.ErrForeignKeyViolated and gorm.ErrCheckConstraintViolated),
// and First/Take report a missing row as gorm.ErrRecordNotFound. Those of
// Raw(...).Row() and Rows() are not: test for a duplicate key with
// pgdb.IsUniqueViolation, which holds on either path.
//
// The tables are described twice, and the two must agree:
//
//   - The GORM models of the plugin's internal/model package are the one
//     description to edit. The package exports every model:
//
//     func All() []any { return []any{&Order{}, &Item{}} }
//
//     Each model names its table (TableName) and spells every column out
//     as it is in the database: `gorm:"type:jsonb;not null;default:'{}'"`,
//     a PostgreSQL array as a pgdb.Array[T] with its type (type:text[]),
//     an index, check or foreign key constraint by its name
//     (index:<name>, check:<name>,<expr>, constraint:<name>,). An index's
//     name carries the plugin's prefix: GORM names one declared without a
//     name (index, uniqueIndex) idx_<table>_<column>, which check refuses.
//     A unique column is `unique`, which
//     takes any unique constraint on just that column for it; the name GORM
//     gives one it creates, uni_<table>_<column>, has no prefix, so write a
//     new one in the migration (the column's own UNIQUE, which PostgreSQL
//     names <table>_<column>_key) or declare uniqueIndex:<prefixed name>.
//     A model that drops `unique` from a column whose constraint has
//     another name than uni_<table>_<column> cannot be diffed (check lists
//     the constraint; drop it by hand). Turn GORM's automatic
//     created_at/updated_at off (autoCreateTime:false, autoUpdateTime:false)
//     where the database default fills them.
//
//   - migrations/*.sql are what the host runs: in file name order, each in
//     its own transaction, logged in as the plugin's identity (CREATE on the
//     core's schema granted for the run only) with search_path on that
//     schema, and idempotent. Before a transaction commits the host checks
//     everything the identity owns in the core's schema, not only what the
//     file created: a relation or type without the plugin's prefix, or an
//     object of any other kind, rolls the file back. The host records each in the plugin's own table
//     plg_<plugin id>__schema_migrations (and runs a changed one again); the
//     migrations leave that table alone. The plugin never runs AutoMigrate.
//
// The sub2api-plugin command keeps the two together:
//
//	sub2api-plugin db diff --dir <plugin> [--name add_score]  # writes migrations/NNNN_add_score.sql
//	sub2api-plugin db check --dir <plugin>                    # fails while they disagree; pack runs it
//
// Both compare on the test PostgreSQL (TEST_DATABASE_URL, or the local
// server of package testpg), in one transaction they roll back
// (pgdbtool.Drift). They run migrations/ in a scratch schema standing in
// for the core's, as a scratch NOLOGIN role that may only create in that
// schema, as the host runs them as the plugin's identity: a statement that
// needs more (ALTER ... OWNER TO, CREATE SCHEMA, CREATE EXTENSION) fails
// here as on install, and what the role owns outside the schema (where
// PUBLIC may create: public on PostgreSQL 14 and older, pg_temp) is listed
// as a difference. The comparison role must be able to create roles. After each
// file, as the host checks, they list with the file's name what the host
// refuses (DriftReport.Refused): a relation or type without the plugin's
// prefix (an index or sequence PostgreSQL named after a table included),
// an object of any other kind (a function, a trigger, ...), and a name
// longer than 63 bytes in the file. The scratch schema has none of the
// core's tables, so migrations that use the tables of database.access
// cannot be checked (check says so when a relation is missing). They let
// AutoMigrate build All() in a second, empty schema and compare the two
// catalogs: the tables; each column's type, NOT NULL and default (by value:
// '{}' and '{}'::jsonb, 0 and '0'::numeric are the same); primary keys; and
// indexes and constraints by definition (pg_get_indexdef,
// pg_get_constraintdef), so that an index of other columns, a uniqueIndex
// turned plain, another CHECK expression or ON DELETE is a difference even
// under the same name, while the names GORM gives unique constraints
// (uni_<table>_<column>), foreign keys and checks need not be the
// migrations'. A model's TableName must not name a schema, and it and the
// model's index names must start with the plugin's prefix. To reach
// internal/model they generate a main package in the plugin module and go
// run it (pgdbtool.DriftMain).
//
// diff writes the DDL AutoMigrate runs on the migrated schema, made
// idempotent, as the next migration, numbered with as many digits as the
// existing ones (the host runs them in file name order), after running it
// twice on a scratch schema and finding no difference left; or says nothing
// is to do. It writes nothing, and prints what is left for a hand-written
// migration, when a change can lose data or fail on the rows of a live
// table (a column type, a drop, SET NOT NULL, a NOT NULL column without a
// default, a CHECK, UNIQUE or foreign key on a table that already exists:
// marked "!"), when the schemas differ in what AutoMigrate does not change
// (it never adds NOT NULL to an existing column, changes a primary key or
// redefines an index or constraint it finds by name), or when the schema
// has what no model maps: a table, column, index or constraint (a field
// removed from the code). Views, sequences and types that no model can
// describe are declared by the model package:
//
//	func Unmanaged() []string { return []string{"view plg_shop__order_totals", "sequence plg_shop__ticket_seq"} }
//
// (kinds: view, materialized view, sequence, type); check fails on any other and
// on a declared one the migrations do not create. Their definitions are not
// compared. check fails on any difference; pack runs it with the overlay's
// migrations.
//
// The test PostgreSQL is TEST_DATABASE_URL when set. Otherwise diff, check
// and pack start the local server of package testpg (downloaded once) and
// stop it again when they started it and no other program (another pack,
// a test) uses it.
//
// Tests run the real migrations: pluginsdktest.NewSchema makes a schema
// standing in for the core's, ApplyMigrations runs them in it and
// FakeHost.SetDSN hands it to Host.DB; the prefixed tables work there as in
// production.
//
// The pluginsdktest sub-package runs a plugin in-process over bufconn with a
// fake host, for unit tests: Harness.Execute and Harness.Monitor make the
// calls the way the host does and return the relays and fetches the plugin
// made; Harness.Balance makes a balance query, checking the hosts of its
// requests as the host does.
package pluginsdk
