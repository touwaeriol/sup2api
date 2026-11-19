package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/peer"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/updatesource"
	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//go:embed schema.sql
var schema string
var ErrConflict = errors.New("updater state changed; repeat preflight")

type Store struct {
	Updates           *UpdateService
	telemetrySequence atomic.Uint64
	DB                *gorm.DB
	Cluster           string
	Locks             LockRunner
	Redis             redis.UniversalClient
	RevokePeer        func(context.Context, string) error
	nodeDirectory     atomic.Pointer[[]Node]
	validNodes        sync.Map // node\x00boot -> time.Time until which the identity is known valid
}

// schemaLock makes shells that start together take turns installing the
// schema, so concurrent CREATE IF NOT EXISTS cannot collide.
const schemaLock = "SELECT pg_advisory_xact_lock(hashtext('sub2api-shell-schema'));\n"

// EnsureSchema installs or extends the shell schema behind schemaLock.
func (s *Store) EnsureSchema(ctx context.Context) error { return execLocked(s.db(ctx), schema) }

// execLocked runs schemaLock and script as one Exec without parameters. GORM
// passes such text through unchanged, and pgx sends a statement without
// arguments over the simple protocol, where PostgreSQL runs the whole
// multi-statement text as one implicit transaction: the transaction-scoped
// lock holds until the last statement, and a failure undoes all of them.
func execLocked(db *gorm.DB, script string) error {
	return db.Exec(schemaLock + script).Error
}
func (s *Store) InitCluster(ctx context.Context, primary, baseline string) error {
	return s.db(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&clusterRecord{s.Cluster, primary, baseline}).Error
}
func (s *Store) ClusterState(ctx context.Context) (primary, baseline string, revision int64, err error) {
	err = s.clusterRow(s.db(ctx)).Select("primary_node,baseline,revision").Row().Scan(&primary, &baseline, &revision)
	return
}
func (s *Store) PutRelease(ctx context.Context, r Release) error {
	res := createRelease(s.db(ctx), r)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		old, e := s.Release(ctx, r.Digest)
		if e != nil {
			return e
		}
		if old.Manifest.ReleaseID != r.Manifest.ReleaseID {
			return ErrConflict
		}
	}
	return nil
}

// createRelease stores r unless a release of its digest is stored already
// (RowsAffected 0).
func createRelease(db *gorm.DB, r Release) *gorm.DB {
	return db.Clauses(clause.OnConflict{Columns: columns("digest"), DoNothing: true}).Create(newReleaseRecord(r))
}

func newReleaseRecord(r Release) *releaseRecord {
	rec := &releaseRecord{Digest: r.Digest, ReleaseID: r.Manifest.ReleaseID, Manifest: r.Raw, BundleBase: r.BundleBase, Line: updatesource.LineCore}
	if r.SourceAssets != nil {
		rec.SourceAssets, _ = json.Marshal(r.SourceAssets)
		rec.SourceLocator = r.SourceLocator
	}
	return rec
}

// Release is the core release digest; a gateway release is never one.
func (s *Store) Release(ctx context.Context, digest string) (r Release, err error) {
	r.Digest = digest
	err = s.scanRelease(ctx, updatesource.LineCore, digest, &r.Manifest, &r.Raw, &r.BundleBase, &r.SourceAssets, &r.SourceLocator)
	return
}

// GatewayRelease is the gateway release digest.
func (s *Store) GatewayRelease(ctx context.Context, digest string) (r GatewayRelease, err error) {
	var base string
	r.Digest = digest
	err = s.scanRelease(ctx, updatesource.LineGateway, digest, &r.Manifest, &r.Raw, &base, &r.SourceAssets, &r.SourceLocator)
	return
}

// scanRelease reads release digest of line into its parts: raw receives the
// stored manifest bytes, manifest their decoding.
func (s *Store) scanRelease(ctx context.Context, line, digest string, manifest any, raw *[]byte, base *string, assets *map[string]updatesource.Asset, locator *string) error {
	var a []byte
	err := releaseRow(s.db(ctx), line, digest).Select("manifest,bundle_base,source_assets,source_locator").Row().Scan(raw, base, &a, locator)
	if err != nil {
		return err
	}
	err = json.Unmarshal(*raw, manifest)
	if err == nil && a != nil {
		err = json.Unmarshal(a, assets)
	}
	return err
}
func (s *Store) Releases(ctx context.Context) ([]Release, error) {
	var ids []string
	if err := s.db(ctx).Table("updater.releases").Where("line = ?", updatesource.LineCore).Order("created_at DESC").Limit(100).Pluck("digest", &ids).Error; err != nil {
		return nil, err
	}
	out := []Release{}
	for _, id := range ids {
		r, e := s.Release(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Store) Register(ctx context.Context, n Node) error {
	return s.WithRegistration(ctx, n.ID, func(ctx context.Context) error { return s.RegisterLocked(ctx, n) })
}

// RegisterLocked is called by peer registration after checking Redis for a
// conflicting live boot, while holding the same short registration lock.
func (s *Store) RegisterLocked(ctx context.Context, n Node) error {
	if n.ID == "" || n.ShellBootID == "" || n.PeerProtocol != PeerProtocol || n.Strategy != PrimaryFirst {
		return errors.New("node identity or shell capabilities are unsupported")
	}
	// On a conflict an enabled node of this cluster takes the new identity
	// and platform. Under a new shell boot it starts over (not ready, not
	// stopped, in maintenance, not offloading, joining the plan
	// the registration found); under the same boot it keeps that state.
	const sameBoot = "CASE WHEN updater.nodes.shell_boot_id=EXCLUDED.shell_boot_id THEN updater.nodes."
	set := clause.AssignmentColumns([]string{"peer_url", "shell_boot_id", "rolling", "os", "arch", "runtime_abi", "peer_protocol", "strategy", "gateway_version", "self_update"})
	set = append(set, clause.Assignments(map[string]any{
		"ready":        gorm.Expr(sameBoot + "ready ELSE false END"),
		"stopped":      gorm.Expr(sameBoot + "stopped ELSE false END"),
		"mode":         gorm.Expr(sameBoot + "mode ELSE 'maintenance' END"),
		"joining_plan": gorm.Expr(sameBoot + "joining_plan ELSE EXCLUDED.joining_plan END"),
		"offloading":   gorm.Expr("updater.nodes.offloading AND updater.nodes.shell_boot_id=EXCLUDED.shell_boot_id"),
		"last_seen":    gorm.Expr("now()"),
	})...)
	conflict := clause.OnConflict{Columns: columns("node_id"), DoUpdates: set,
		Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "updater.nodes.cluster_id=EXCLUDED.cluster_id AND updater.nodes.enabled"}}}}
	return s.withChange(ctx, func(ctx context.Context) error {
		return s.withTx(ctx, func(tx *gorm.DB) error {
			// A node that joins during a plan waits for it (joining_plan).
			var plan string
			if err := s.activeUpgrade(tx).Select("id").Row().Scan(&plan); err != nil && !gormpg.IsNoRows(err) {
				return err
			}
			res := tx.Clauses(conflict).Create(&nodeRecord{NodeID: n.ID, ClusterID: s.Cluster, PeerURL: n.PeerURL, ShellBootID: n.ShellBootID,
				OS: n.OS, Arch: n.Arch, RuntimeABI: n.RuntimeABI, PeerProtocol: n.PeerProtocol, Strategy: n.Strategy, JoiningPlan: plan, Rolling: true, GatewayVersion: n.GatewayVersion, SelfUpdate: n.SelfUpdate})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errors.New("node is disabled or belongs to another cluster")
			}
			return nil
		})
	})
}
func (s *Store) Heartbeat(ctx context.Context, n Node) error {
	observedLocally := time.Now()
	var accepted bool
	// Named parameters: each value is written once however often the
	// statement uses it. GORM ends a name only at a blank, comma, quote,
	// parenthesis or semicolon, so a typed one is CAST(@x AS t), not @x::t.
	err := s.db(ctx).Raw(`WITH changed AS (
 UPDATE updater.nodes SET release_digest=@release,core_boot_id=@core,mode=@mode,ready=@ready,
 last_seen=now(),route_revision=@route,stopped=@stopped,offloading=@offloading,telemetry_boot_id=@boot,error=''
 WHERE node_id=@node AND cluster_id=@cluster AND shell_boot_id=@boot AND enabled AND (
 (release_digest,core_boot_id,mode,ready,route_revision,stopped,offloading,telemetry_boot_id)
 IS DISTINCT FROM (CAST(@release AS text),CAST(@core AS text),CAST(@mode AS text),CAST(@ready AS boolean),CAST(@route AS bigint),CAST(@stopped AS boolean),CAST(@offloading AS boolean),CAST(@boot AS text)))
 RETURNING node_id)
 SELECT EXISTS(SELECT 1 FROM changed) OR EXISTS(SELECT 1 FROM updater.nodes WHERE node_id=@node AND cluster_id=@cluster AND shell_boot_id=@boot AND enabled)`, map[string]any{
		"release": n.ReleaseDigest, "core": n.CoreBootID, "mode": n.Mode, "ready": n.Ready, "route": n.RouteRevision,
		"stopped": n.Stopped, "offloading": n.Offloading, "boot": n.ShellBootID, "cluster": s.Cluster, "node": n.ID,
	}).Row().Scan(&accepted)
	if err != nil {
		return err
	}
	if !accepted {
		return ErrConflict
	}
	return s.reportTelemetry(ctx, n, observedLocally)
}
func (s *Store) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.nodes(s.db(ctx)).Select("node_id,peer_url,shell_boot_id,release_digest,core_boot_id,mode,ready,last_seen,error,os,arch,runtime_abi,route_revision,stopped,enabled,peer_protocol,strategy,joining_plan,offloading,telemetry_boot_id,gateway_version,self_update").Order("node_id").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		if err = rows.Scan(&n.ID, &n.PeerURL, &n.ShellBootID, &n.ReleaseDigest, &n.CoreBootID, &n.Mode, &n.Ready, &n.LastSeen, &n.Error, &n.OS, &n.Arch, &n.RuntimeABI, &n.RouteRevision, &n.Stopped, &n.Enabled, &n.PeerProtocol, &n.Strategy, &n.JoiningPlan, &n.Offloading, &n.TelemetryBootID, &n.GatewayVersion, &n.SelfUpdate); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, s.RefreshTelemetry(ctx, out)
}

// NodeDirectory returns the most recent heartbeat snapshot. Authorization and
// routing use this cached view; revocation (DisableNode) and the upgrade
// checks (Preflight, baseline recovery, the migration barrier) read PG.
func (s *Store) NodeDirectory() []Node {
	if ptr := s.nodeDirectory.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

func (s *Store) Preflight(ctx context.Context, digest string) (Preflight, error) {
	var gateway bool
	db := s.db(ctx)
	if err := gormpg.ExistsQuery(db, releaseRow(db, updatesource.LineGateway, digest)).Row().Scan(&gateway); err != nil {
		return Preflight{}, err
	}
	if gateway {
		return s.gatewayPreflight(ctx, digest)
	}
	p := Preflight{ReleaseDigest: digest, Nodes: []string{}, Blockers: []string{}, BlockerCodes: []Issue{}, Strategy: PrimaryFirst}
	primary, base, rev, err := s.ClusterState(ctx)
	if err != nil {
		return p, err
	}
	p.PrimaryNode = primary
	p.ExpectedRevision = rev
	target, err := s.Release(ctx, digest)
	if err != nil {
		return p, err
	}
	current, err := s.Release(ctx, base)
	if err != nil {
		return p, err
	}
	p.block(issues(issueReleaseIncompatible, Compatibility(current, target))...)
	pluginBlockers, err := s.PluginCompatibility(ctx, target.Manifest.CoreVersion)
	if err != nil {
		return p, err
	}
	p.block(issues(issuePluginIncompatible, pluginBlockers)...)
	if err = s.blockBusy(ctx, &p); err != nil {
		return p, err
	}
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return p, err
	}
	if enabledNodes(nodes) == 0 {
		p.block(Issue{Code: issueNoNodes, Message: "upgrade requires at least one registered node"})
	}
	for _, n := range nodes {
		if !n.Enabled {
			// A disabled node neither blocks the preflight nor joins the
			// plan: it could not run its steps.
			continue
		}
		p.Nodes = append(p.Nodes, n.ID)
		if n.PeerProtocol != PeerProtocol || n.Strategy != PrimaryFirst {
			p.block(nodeIssue(issueNodeUnsupported, n.ID, "node lacks enabled current shell/peer capabilities: "+n.ID))
		}
		if !n.ServingLocal(time.Now()) {
			p.block(nodeIssue(issueNodeNotReady, n.ID, "node not locally ready: "+n.ID))
		}
		if n.ReleaseDigest != base {
			p.block(nodeIssue(issueNodeOffBaseline, n.ID, "node not on approved baseline: "+n.ID))
		}
		found := false
		for _, plat := range target.Manifest.Platforms {
			if plat.OS == n.OS && plat.Arch == n.Arch && plat.RuntimeABI == n.RuntimeABI {
				found = true
			}
		}
		if !found {
			p.block(nodeIssue(issueNodeUnsupported, n.ID, "unsupported node platform: "+n.ID))
		}
	}
	if !orderPrimaryFirst(p.Nodes, primary) {
		p.block(primaryUnregistered)
	}
	if rollingEligible(target, nodes, 2) {
		p.Strategy = Rolling
	}
	return s.activeBlocker(ctx, p)
}

// activeBlocker adds the blocker of a plan already in flight to p.
func (s *Store) activeBlocker(ctx context.Context, p Preflight) (Preflight, error) {
	var active bool
	db := s.db(ctx)
	if err := gormpg.ExistsQuery(db, s.activeUpgrade(db)).Row().Scan(&active); err != nil {
		return p, err
	}
	if active {
		p.block(Issue{Code: issueUpgradeActive, Message: "another upgrade is active"})
	}
	return p, nil
}

// blockBusy adds the blockers of plugin changes in progress and of cores the
// gateway does not manage.
func (s *Store) blockBusy(ctx context.Context, p *Preflight) error {
	for _, c := range []struct {
		code  string
		check func(context.Context) ([]string, error)
	}{{issuePluginsChanging, s.PluginMutationBlockers}, {issueCoreUnmanaged, s.UnmanagedBlockers}} {
		blockers, err := c.check(ctx)
		if err != nil {
			return err
		}
		p.block(issues(c.code, blockers)...)
	}
	return nil
}

var primaryUnregistered = Issue{Code: issuePrimaryUnregistered, Message: "primary is not registered"}

// orderPrimaryFirst sorts nodes, the primary first, and reports whether the
// primary is among them.
func orderPrimaryFirst(nodes []string, primary string) bool {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i] == primary {
			return nodes[j] != primary
		}
		if nodes[j] == primary {
			return false
		}
		return nodes[i] < nodes[j]
	})
	return len(nodes) > 0 && nodes[0] == primary
}

// rollingEligible decides whether a plan for target may restart the nodes
// one at a time (Rolling). The release must keep the schema and be declared
// rolling, with at least two enabled nodes serving so that one can take the
// other's traffic. Anything else upgrades primary-first.
func rollingEligible(target Release, nodes []Node, minServing int) bool {
	return len(rollingBlockers(target, nodes, minServing)) == 0
}

// rollingBlockers are the reasons rollingEligible rejects a rolling plan.
func rollingBlockers(target Release, nodes []Node, minServing int) []Issue {
	m := target.Manifest
	var out []Issue
	if m.Strategy != "rolling" {
		out = append(out, Issue{Code: issueMaintenanceRelease, Message: "the release is declared for a maintenance update"})
	}
	if m.SchemaBefore == "" || m.SchemaBefore != m.SchemaAfter {
		out = append(out, Issue{Code: issueSchemaChange, Message: "the release changes the database schema"})
	}
	serving := 0
	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		if n.ServingLocal(time.Now()) {
			serving++
		}
	}
	if serving < minServing {
		out = append(out, Issue{Code: issueTooFewServing, Params: map[string]string{"min": strconv.Itoa(minServing)},
			Message: fmt.Sprintf("fewer than %d nodes serve, so no node can take over another's traffic", minServing)})
	}
	return out
}

// Compatibility validates the declared migration start and executable protocols.
// Database changes run with all followers stopped; equality is not required.
func Compatibility(old, target Release) []string {
	out := []string{}
	a, b := old.Manifest, target.Manifest
	if _, err := semver.NewVersion(b.CoreVersion); err != nil {
		out = append(out, "target core_version must be explicit semantic version")
	}
	if b.Strategy != "maintenance" && b.Strategy != "rolling" {
		out = append(out, "unsupported release strategy")
	}
	if a.SchemaAfter == "" || b.SchemaBefore != a.SchemaAfter || b.SchemaAfter == "" {
		out = append(out, "target migration must start at the approved baseline schema")
	}
	if b.Strategy == "rolling" && b.SchemaBefore != b.SchemaAfter {
		out = append(out, "schema-changing releases must declare maintenance strategy")
	}
	if !b.ShellProtocol.Contains(rc.Protocol) || !b.CoreControlProtocol.Contains(rc.Protocol) {
		out = append(out, "release requires a different shell/control protocol")
	}
	if b.ClusterProtocol != (rc.Range{Min: rc.ClusterProtocolVersion, Max: rc.ClusterProtocolVersion}) || b.TaskProtocol != (rc.Range{Min: rc.TaskProtocolVersion, Max: rc.TaskProtocolVersion}) || b.HostAPIVersion != rc.HostAPIVersion {
		out = append(out, "target business protocols or Host API are not implemented by this shell release")
	}
	if a.BuildID == b.BuildID {
		out = append(out, "target build is already installed")
	}
	return out
}

func rollbackCompatibility(from, to Release) []string {
	out := []string{}
	if from.Manifest.SchemaBefore != to.Manifest.SchemaAfter || from.Manifest.SchemaAfter != to.Manifest.SchemaAfter || from.Manifest.ClusterProtocol != to.Manifest.ClusterProtocol || from.Manifest.TaskProtocol != to.Manifest.TaskProtocol || from.Manifest.HostAPIVersion != to.Manifest.HostAPIVersion {
		out = append(out, "automatic baseline recovery is forbidden across schema or business protocol changes")
	}
	return out
}

// PluginCompatibility checks every currently serving or activating version,
// including third-party plugins. Candidate admission repeats the runtime check.
func (s *Store) PluginCompatibility(ctx context.Context, version string) ([]string, error) {
	db := s.db(ctx)
	if present, err := gormpg.RelationExists(db, "public.plugins"); err != nil || !present {
		return nil, err // shell-only bootstrap database has no plugins yet
	}
	var missing bool
	if err := gormpg.ExistsQuery(db, db.Table("plugins").Where("status IN ('enabled','upgrading') AND COALESCE(active_version,'')=''")).Row().Scan(&missing); err != nil {
		return nil, err
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return []string{"target core_version is missing or invalid"}, nil
	}
	clean, err := v.SetPrerelease("")
	if err != nil {
		return nil, err
	}
	// Every version a plugin serves or moves between, with its host range.
	rows, err := db.Table("plugins p").Distinct("p.key,versions.version,COALESCE(v.manifest->>'hostCompat','')").
		Joins("LEFT JOIN plugin_rollouts r ON r.plugin_key=p.key AND r.phase IN ('preparing','activating')").
		Joins("CROSS JOIN LATERAL (SELECT p.active_version AS version UNION SELECT r.from_version UNION SELECT r.target_version) versions").
		Joins("LEFT JOIN plugin_versions v ON v.plugin_key=p.key AND v.version=versions.version").
		Where("p.status IN ('enabled','upgrading') AND versions.version IS NOT NULL").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	if missing {
		out = append(out, "an enabled plugin has no active approved version")
	}
	for rows.Next() {
		var key, ver, expression string
		if err = rows.Scan(&key, &ver, &expression); err != nil {
			return nil, err
		}
		constraint, e := semver.NewConstraint(expression)
		if expression == "" || e != nil || !constraint.Check(&clean) {
			out = append(out, fmt.Sprintf("plugin %s@%s is incompatible with core %s", key, ver, version))
		}
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, digest string, revision int64, key, actor string) (Plan, error) {
	var out Plan
	err := s.withChange(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.createLocked(ctx, digest, revision, key, actor)
		return err
	})
	if err == nil {
		s.NotifyUpgrade(ctx)
	}
	return out, err
}
func (s *Store) createLocked(ctx context.Context, digest string, revision int64, key, actor string) (Plan, error) {
	var empty Plan
	if key == "" || len(key) > 128 {
		return empty, errors.New("idempotency_key is required and must be at most 128 bytes")
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", digest, revision)))
	requestHash := hex.EncodeToString(hash[:])
	var id, priorHash string
	err := s.idempotentUpgrade(s.db(ctx), key).Select("id,request_hash").Row().Scan(&id, &priorHash)
	if err == nil {
		if priorHash != requestHash {
			return empty, ErrConflict
		}
		return s.Plan(ctx, id)
	}
	if !gormpg.IsNoRows(err) {
		return empty, err
	}
	pf, err := s.Preflight(ctx, digest)
	if err != nil {
		return empty, err
	}
	if len(pf.Blockers) > 0 {
		return empty, errors.New(strings.Join(pf.Blockers, "; "))
	}
	if pf.ExpectedRevision != revision {
		return empty, ErrConflict
	}
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		res := s.clusterRow(tx).Where("revision = ?", revision).Update("revision", gorm.Expr("revision+1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrConflict
		}
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		id = hex.EncodeToString(random)
		nodes, _ := json.Marshal(pf.Nodes)
		if err := s.createUpgrade(tx, id, digest, nodes, actor, key, requestHash, pf.Strategy); err != nil {
			return err
		}
		return insertEvents(tx, eventRow{id, "created", createdMessage(pf.Strategy)})
	}); err != nil {
		return empty, err
	}
	return s.Plan(ctx, id)
}

// createUpgrade inserts plan id (status running) for the release digest over
// nodes, a JSON list. Its source_digest is the cluster's baseline as tx sees
// it.
func (s *Store) createUpgrade(tx *gorm.DB, id, digest string, nodes []byte, actor, key, requestHash, strategy string) error {
	return tx.Table("updater.upgrades").Create(map[string]any{
		"id": id, "cluster_id": s.Cluster, "release_digest": digest, "nodes": nodes, "actor": actor,
		"idempotency_key": key, "request_hash": requestHash, "strategy": strategy,
		"source_digest": gorm.Expr("(?)", s.clusterRow(tx).Select("baseline")),
	}).Error
}

// createdMessage is the first event of a plan; the console shows it.
func createdMessage(strategy string) string {
	switch strategy {
	case Rolling:
		return "rolling update approved: nodes restart one at a time while the others serve"
	case GatewayRolling:
		return "gateway update approved: nodes switch gateways one at a time while the others serve"
	case GatewayRollback:
		return "gateway rollback approved: nodes return to their previous gateways one at a time"
	}
	return "primary-first maintenance update approved"
}
func (s *Store) Plan(ctx context.Context, id string) (p Plan, err error) {
	var nodes []byte
	err = upgradeRow(s.db(ctx), id).Where("cluster_id = ?", s.Cluster).Select("id,release_digest,nodes,status,cursor,error,actor,created_at,strategy,source_digest").Row().Scan(&p.ID, &p.ReleaseDigest, &nodes, &p.Status, &p.Cursor, &p.Error, &p.Actor, &p.CreatedAt, &p.Strategy, &p.SourceDigest)
	if err != nil {
		return
	}
	if err = json.Unmarshal(nodes, &p.Nodes); err != nil {
		return
	}
	p.Steps = []Step{}
	rows, err := s.db(ctx).Table("updater.steps").Select("upgrade_id,step_id,node_id,action,target_digest,peer_node,status,error").Where("upgrade_id = ?", id).Order("step_id").Rows()
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		if err = rows.Scan(&st.UpgradeID, &st.ID, &st.NodeID, &st.Action, &st.TargetDigest, &st.PeerNode, &st.Status, &st.Error); err != nil {
			return
		}
		p.Steps = append(p.Steps, st)
	}
	err = rows.Err()
	return
}
func (s *Store) Plans(ctx context.Context) ([]Plan, error) {
	var ids []string
	if err := s.db(ctx).Table("updater.upgrades").Where("cluster_id = ?", s.Cluster).Order("created_at DESC").Limit(50).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := []Plan{}
	for _, id := range ids {
		p, e := s.Plan(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Store) Events(ctx context.Context, id string, after int64) ([]Event, error) {
	out := []Event{}
	err := s.db(ctx).Table("updater.events e").Joins("JOIN updater.upgrades u ON u.id=e.upgrade_id").
		Select("e.id,e.kind,e.message,e.created_at").Where("e.upgrade_id=? AND u.cluster_id=? AND e.id>?", id, s.Cluster, after).
		Order("e.id").Limit(200).Find(&out).Error
	return out, err
}
func (s *Store) Action(ctx context.Context, id, action, actor string) (Plan, error) {
	if action == "rollback" {
		return s.Rollback(ctx, id, actor)
	}
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		var status string
		var cursor int
		if err := upgradeRow(tx, id).Where("cluster_id = ?", s.Cluster).Clauses(forUpdate()).Select("status,cursor").Row().Scan(&status, &cursor); err != nil {
			return err
		}
		switch action {
		case "pause":
			if status != "running" && status != "paused" {
				return ErrConflict
			}
			status = "paused"
		case "resume":
			if status != "paused" {
				return ErrConflict
			}
			status = "running"
			if err := tx.Table("updater.steps").Where("upgrade_id = ? AND status = 'failed'", id).Updates(map[string]any{"status": "pending", "error": ""}).Error; err != nil {
				return err
			}
		case "cancel":
			var disruptive bool
			if err := gormpg.ExistsQuery(tx, tx.Table("updater.steps").Where("upgrade_id = ? AND action NOT IN ('prepare','gw-prepare')", id)).Row().Scan(&disruptive); err != nil {
				return err
			}
			if disruptive {
				return errors.New("cannot cancel after routing or process changes; pause and resume the same plan")
			}
			if status != "running" && status != "paused" {
				return ErrConflict
			}
			status = "cancelled"
		default:
			return errors.New("unsupported action; rollback requires an independently approved compatible release")
		}
		if err := upgradeRow(tx, id).Updates(map[string]any{"status": status, "error": "", "updated_at": gorm.Expr("now()")}).Error; err != nil {
			return err
		}
		return insertEvents(tx, eventRow{id, action, "operator " + actor})
	}); err != nil {
		return Plan{}, err
	}
	s.NotifyUpgrade(ctx)
	return s.Plan(ctx, id)
}

// Rollback replaces a paused plan with a new, fully journaled rolling plan.
// Its only target is the cluster's last completed baseline; it never switches
// files directly and never authorizes a schema or protocol downgrade.
func (s *Store) Rollback(ctx context.Context, id, actor string) (Plan, error) {
	var out Plan
	err := s.withChange(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.rollbackWithBarrier(ctx, id, actor)
		return err
	})
	if err == nil {
		s.NotifyUpgrade(ctx)
	}
	return out, err
}
func (s *Store) rollbackWithBarrier(ctx context.Context, id, actor string) (Plan, error) {
	p, err := s.Plan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.Status == "superseded" {
		return s.rollbackLocked(ctx, id, actor)
	}
	if p.Status != "paused" {
		return Plan{}, errors.New("pause the active plan before requesting baseline recovery")
	}
	if s.Locks == nil {
		return Plan{}, errors.New("rollback coordination is unavailable")
	}
	// A paused plan can still have an already-running step. Drain those
	// critical sections before choosing a traffic anchor for the new plan.
	names := append([]string(nil), p.Nodes...)
	sort.Strings(names)
	keys := []string{"system:upgrade:" + s.Cluster}
	for _, node := range names {
		keys = append(keys, "system:upgrade-node:"+s.Cluster+":"+node)
	}
	bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var out Plan
	var acquire func(context.Context, int) error
	acquire = func(lockCtx context.Context, i int) error {
		if i == len(keys) {
			var e error
			out, e = s.rollbackLocked(lockCtx, id, actor)
			return e
		}
		ran, e := s.Locks.WithLock(lockCtx, keys[i], 25*time.Second, func(next context.Context) error { return acquire(next, i+1) })
		if e != nil {
			return e
		}
		if !ran {
			return errors.New("an update step is still running; keep the plan paused and retry recovery")
		}
		return nil
	}
	if err = acquire(bounded, 0); err != nil {
		return Plan{}, err
	}
	return out, nil
}
func (s *Store) rollbackLocked(ctx context.Context, id, actor string) (Plan, error) {
	var planID string
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		var status, target, strategy string
		var raw []byte
		if err := upgradeRow(tx, id).Where("cluster_id = ?", s.Cluster).Clauses(forUpdate()).Select("status,release_digest,nodes,strategy").Row().Scan(&status, &target, &raw, &strategy); err != nil {
			return err
		}
		key := "rollback:" + id
		if status == "superseded" {
			// A retried rollback returns the plan the first attempt created.
			return s.idempotentUpgrade(tx, key).Select("id").Row().Scan(&planID)
		}
		if status != "paused" {
			return errors.New("pause the active plan before requesting baseline recovery")
		}
		if err := firstBlocker(ctx, s.PluginMutationBlockers, s.UnmanagedBlockers); err != nil {
			return err
		}
		var baseline string
		var revision int64
		if err := s.clusterRow(tx).Clauses(forUpdate()).Select("baseline,revision").Row().Scan(&baseline, &revision); err != nil {
			return err
		}
		if gatewayStrategy(strategy) {
			// The members switch back to the gateways they ran before, on
			// the baseline core they kept.
			if strategy == GatewayRollback {
				return errors.New("a gateway rollback cannot be rolled back; resume it after resolving its blocker")
			}
			var err error
			planID, err = s.supersede(tx, id, actor, target, raw, key, fmt.Sprintf("%s:%d", target, revision), GatewayRollback)
			return err
		}
		if target == baseline {
			return errors.New("this plan already targets the completed baseline; resume it after resolving its blocker")
		}
		from, err := s.Release(ctx, target)
		if err != nil {
			return err
		}
		to, err := s.Release(ctx, baseline)
		if err != nil {
			return err
		}
		if blockers := rollbackCompatibility(from, to); len(blockers) > 0 {
			return fmt.Errorf("baseline recovery is incompatible: %s", strings.Join(blockers, "; "))
		}
		blockers, err := s.PluginCompatibility(ctx, to.Manifest.CoreVersion)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return fmt.Errorf("baseline no longer supports active plugins: %s", strings.Join(blockers, "; "))
		}
		var members []string
		if err = json.Unmarshal(raw, &members); err != nil {
			return err
		}
		if len(members) < 1 {
			return errors.New("baseline recovery has no members")
		}
		primary, _, _, err := s.ClusterState(ctx)
		if err != nil {
			return err
		}
		sort.SliceStable(members, func(i, j int) bool {
			if members[i] == primary {
				return members[j] != primary
			}
			if members[j] == primary {
				return false
			}
			return members[i] < members[j]
		})
		if members[0] != primary {
			return errors.New("configured primary is not a plan member")
		}
		encoded, _ := json.Marshal(members)
		// The recovery restarts the members one at a time when the baseline
		// release qualifies and the nodes can. One serving node is enough:
		// a member the superseded plan left stopped yields nothing and is
		// simply restarted on the baseline, then takes the other's traffic.
		strategy = PrimaryFirst
		if nodes, err := s.Nodes(ctx); err == nil && rollingEligible(to, nodes, 1) {
			strategy = Rolling
		}
		planID, err = s.supersede(tx, id, actor, baseline, encoded, key, fmt.Sprintf("%s:%d", baseline, revision), strategy)
		return err
	}); err != nil {
		return Plan{}, err
	}
	return s.Plan(ctx, planID)
}

// supersede replaces the paused plan id by a new recovery plan of digest
// over nodes (JSON) under strategy, in tx, and returns the new plan's id.
func (s *Store) supersede(tx *gorm.DB, id, actor, digest string, nodes []byte, key, requestHash, strategy string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	planID := hex.EncodeToString(random)
	if err := upgradeRow(tx, id).Updates(map[string]any{"status": "superseded", "error": "", "updated_at": gorm.Expr("now()")}).Error; err != nil {
		return "", err
	}
	if err := s.createUpgrade(tx, planID, digest, nodes, actor, key, requestHash, strategy); err != nil {
		return "", err
	}
	if err := s.clusterRow(tx).Update("revision", gorm.Expr("revision+1")).Error; err != nil {
		return "", err
	}
	superseded, recovery := "replaced by baseline recovery "+planID, "recover completed baseline from "+id+" by "+actor
	if strategy == GatewayRollback {
		superseded, recovery = "replaced by gateway rollback "+planID, "return the nodes of "+id+" to their previous gateways, by "+actor
	}
	return planID, insertEvents(tx, eventRow{id, "superseded", superseded}, eventRow{planID, "recovery", recovery})
}

const clusterChangeLock = rc.ClusterChangeLock

func (s *Store) withChange(ctx context.Context, fn func(context.Context) error) error {
	if s.Locks == nil {
		return errors.New("cluster change coordination unavailable")
	}
	ran, err := s.Locks.WithLock(ctx, clusterChangeLock, 30*time.Second, fn)
	if err != nil {
		return err
	}
	if !ran {
		return errors.New("another cluster change is in progress")
	}
	return nil
}

// withTx runs fn inside a transaction: it commits when fn returns nil and rolls
// back otherwise.
func (s *Store) withTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return s.db(ctx).Transaction(fn)
}

// db is the pool bound to ctx.
func (s *Store) db(ctx context.Context) *gorm.DB { return s.DB.WithContext(ctx) }

// clusterRow is this cluster's updater.clusters row.
func (s *Store) clusterRow(db *gorm.DB) *gorm.DB {
	return db.Table("updater.clusters").Where("cluster_id = ?", s.Cluster)
}

// nodes are this cluster's updater.nodes rows; nodeRow is one of them.
func (s *Store) nodes(db *gorm.DB) *gorm.DB {
	return db.Table("updater.nodes").Where("cluster_id = ?", s.Cluster)
}
func (s *Store) nodeRow(db *gorm.DB, node string) *gorm.DB {
	return db.Table("updater.nodes").Where("cluster_id = ? AND node_id = ?", s.Cluster, node)
}

// admissionRow is node's updater.node_admissions row in this cluster.
func (s *Store) admissionRow(db *gorm.DB, node string) *gorm.DB {
	return db.Table("updater.node_admissions").Where("cluster_id = ? AND node_id = ?", s.Cluster, node)
}

// revokedAdmission withdraws every permission of an admission and bumps its
// revision, so the core sees the change.
func revokedAdmission() map[string]any {
	return map[string]any{"serve_http": false, "claim_background": false, "coordinate_plugins": false, "revision": gorm.Expr("revision+1")}
}

// releaseRow is the updater.releases row digest of line.
func releaseRow(db *gorm.DB, line, digest string) *gorm.DB {
	return db.Table("updater.releases").Where("digest = ? AND line = ?", digest, line)
}

// upgradeRow is the updater.upgrades row id; activeUpgrade is this cluster's
// running or paused plan, idempotentUpgrade the plan created under key.
func upgradeRow(db *gorm.DB, id string) *gorm.DB {
	return db.Table("updater.upgrades").Where("id = ?", id)
}
func (s *Store) activeUpgrade(db *gorm.DB) *gorm.DB {
	return db.Table("updater.upgrades").Where("cluster_id = ? AND status IN ('running','paused')", s.Cluster)
}
func (s *Store) idempotentUpgrade(db *gorm.DB, key string) *gorm.DB {
	return db.Table("updater.upgrades").Where("cluster_id = ? AND idempotency_key = ?", s.Cluster, key)
}

// stepRow is step stepID of plan upgradeID.
func stepRow(db *gorm.DB, upgradeID string, stepID int) *gorm.DB {
	return db.Table("updater.steps").Where("upgrade_id = ? AND step_id = ?", upgradeID, stepID)
}

// appendBlockers runs each check and appends its blockers, stopping at the first
// check that fails.
func appendBlockers(ctx context.Context, dst []string, checks ...func(context.Context) ([]string, error)) ([]string, error) {
	for _, check := range checks {
		blockers, err := check(ctx)
		if err != nil {
			return dst, err
		}
		dst = append(dst, blockers...)
	}
	return dst, nil
}

// firstBlocker fails when any check reports a blocker. It is the Preflight
// variant for callers that must refuse the whole operation instead of listing.
func firstBlocker(ctx context.Context, checks ...func(context.Context) ([]string, error)) error {
	blockers, err := appendBlockers(ctx, nil, checks...)
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		return errors.New(strings.Join(blockers, "; "))
	}
	return nil
}

func (s *Store) WithRegistration(ctx context.Context, node string, fn func(context.Context) error) error {
	if s.Locks == nil {
		return errors.New("node registration coordination unavailable")
	}
	ran, err := s.Locks.WithLock(ctx, "system:node-registration:"+s.Cluster+":"+node, 15*time.Second, fn)
	if err != nil {
		return err
	}
	if !ran {
		return errors.New("node registration is busy")
	}
	return nil
}

// validateNodeCacheTTL bounds how long a positive ValidateNode answer is
// reused. The private listener asks twice per request (source and self);
// without the cache an unauthenticated flood turns into PG load. Every
// authorized operation still reads the node row afresh (AuthorizePeer), so a
// disabled node is refused there at once; only the identity check lags.
const validateNodeCacheTTL = 2 * time.Second

func (s *Store) ValidateNode(ctx context.Context, node, boot string) error {
	cacheKey := node + "\x00" + boot
	if until, ok := s.validNodes.Load(cacheKey); ok && time.Now().Before(until.(time.Time)) {
		return nil
	}
	var ok bool
	db := s.db(ctx)
	err := gormpg.ExistsQuery(db, s.nodeRow(db, node).Where("shell_boot_id = ? AND enabled AND peer_protocol = ? AND strategy = ?", boot, PeerProtocol, PrimaryFirst)).Row().Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		s.validNodes.Delete(cacheKey)
		return errors.New("node boot is disabled or not registered")
	}
	s.validNodes.Store(cacheKey, time.Now().Add(validateNodeCacheTTL))
	return nil
}

// forgetNode drops cached identity answers of node on this gateway.
func (s *Store) forgetNode(node string) {
	s.validNodes.Range(func(k, _ any) bool {
		if key, _ := k.(string); strings.HasPrefix(key, node+"\x00") {
			s.validNodes.Delete(k)
		}
		return true
	})
}
func (s *Store) DisableNode(ctx context.Context, node string) error {
	return s.WithRegistration(ctx, node, func(ctx context.Context) error {
		res := s.nodeRow(s.db(ctx), node).Updates(map[string]any{"enabled": false, "ready": false})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return sql.ErrNoRows
		}
		s.forgetNode(node)
		s.NotifyUpgrade(ctx)
		if err := s.admissionRow(s.db(ctx), node).Updates(revokedAdmission()).Error; err != nil {
			return err
		}
		if s.RevokePeer == nil {
			return errors.New("node disabled; Redis revocation pending")
		}
		if err := s.RevokePeer(ctx, node); err != nil {
			return errors.New("node disabled; Redis revocation pending")
		}
		return nil
	})
}

// AuthorizePeer classifies failures for the private listener: a stale or
// disabled source is 401, a disallowed operation 403, storage errors 503.
func (s *Store) AuthorizePeer(ctx context.Context, source, boot, target, scope, digest string) error {
	var offloading bool
	err := s.nodeRow(s.db(ctx), source).Where("shell_boot_id = ? AND enabled AND peer_protocol = ? AND strategy = ?", boot, PeerProtocol, PrimaryFirst).Select("offloading").Row().Scan(&offloading)
	if gormpg.IsNoRows(err) {
		return peer.ErrUnauthorized
	}
	if err != nil {
		return peer.ErrUnavailable
	}
	primary, baseline, _, err := s.ClusterState(ctx)
	if err != nil {
		return peer.ErrUnavailable
	}
	var enabled bool
	if err = s.nodeRow(s.db(ctx), target).Select("enabled").Row().Scan(&enabled); err != nil {
		return peer.ErrUnavailable
	}
	if enabled && source != target && scope == "forward" && offloading {
		// An overloaded serving node may hand new requests to any serving node,
		// the primary's to a follower included. The private router still only
		// serves them locally and never forwards them again.
		return nil
	}
	if enabled && source != target && scope == "plugin-artifact" {
		// A package a node lacks may be read from any enabled node that holds
		// it; no node is special. Packages are digest-addressed and verified
		// by the reader; only referenced ones are served.
		referenced, err := s.packageReferenced(ctx, digest)
		if err != nil {
			return peer.ErrUnavailable
		}
		if !referenced {
			return fmt.Errorf("%w: plugin package is not referenced by any version", peer.ErrForbidden)
		}
		return nil
	}
	if !enabled || target != primary || source == primary {
		return fmt.Errorf("%w: peer direction is not permitted", peer.ErrForbidden)
	}
	if scope == "forward" {
		return nil
	}
	if scope != "core-artifact" {
		return fmt.Errorf("%w: peer scope is not permitted", peer.ErrForbidden)
	}
	db := s.db(ctx)
	rows, err := db.Table("updater.releases").Select("digest,manifest").Where("line = ? AND (digest = ? OR digest IN (?))", updatesource.LineCore, baseline, s.activeUpgrade(db).Select("release_digest")).Rows()
	if err != nil {
		return peer.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var release string
		var raw []byte
		if err = rows.Scan(&release, &raw); err != nil {
			return peer.ErrUnavailable
		}
		if release == digest {
			return nil
		}
		var m rc.Manifest
		if err = json.Unmarshal(raw, &m); err != nil {
			return peer.ErrUnavailable
		}
		for _, p := range m.Platforms {
			if p.BundleDigest == digest {
				return nil
			}
		}
	}
	if err = rows.Err(); err != nil {
		return peer.ErrUnavailable
	}
	return fmt.Errorf("%w: artifact is not approved for this cluster", peer.ErrForbidden)
}

// AuthorizeTarget decides whether self may send its node key to u: only to
// the primary, or to a serving node while self offloads because of CPU load.
// Uses the cached node directory for routing decisions.
func (s *Store) AuthorizeTarget(ctx context.Context, self string, u *url.URL, offloading bool) error {
	primary, _, _, err := s.ClusterState(ctx)
	if err != nil {
		return peer.ErrUnavailable
	}
	nodes := s.NodeDirectory()
	if nodes == nil {
		var err error
		nodes, err = s.Nodes(ctx)
		if err != nil {
			return peer.ErrUnavailable
		}
	}
	for _, n := range nodes {
		target, err := url.Parse(n.PeerURL)
		if err == nil && n.Enabled && n.ID != self && target.Scheme == u.Scheme && target.Host == u.Host && (n.ID == primary || (offloading && n.Mode == "local")) {
			return nil
		}
	}
	return peer.ErrForbidden
}

// AuthorizeAnyNodeTarget lets self send its node key to any other enabled
// node of the cluster. Only the plugin package client uses it: a package is
// fetched from whichever node holds it (AuthorizePeer's plugin-artifact rule
// is the receiving side).
func (s *Store) AuthorizeAnyNodeTarget(ctx context.Context, self string, u *url.URL) error {
	nodes := s.NodeDirectory()
	if nodes == nil {
		var err error
		nodes, err = s.Nodes(ctx)
		if err != nil {
			return peer.ErrUnavailable
		}
	}
	for _, n := range nodes {
		target, err := url.Parse(n.PeerURL)
		if err == nil && n.Enabled && n.ID != self && target.Scheme == u.Scheme && target.Host == u.Host {
			return nil
		}
	}
	return peer.ErrForbidden
}

// UnmanagedBlockers reads the existing core registry. A healthy old core that
// never registered a managed identity must not survive the migration barrier.
// Every live core must be managed and run the boot PG records for its enabled
// node. PG is read, not the heartbeat's NodeDirectory, whose snapshot predates
// the boot the node's own heartbeat wrote. A core that registered in Redis
// after its shell's last heartbeat is still reported until the next one, so a
// false blocker lasts under one heartbeat.
func (s *Store) UnmanagedBlockers(ctx context.Context) ([]string, error) {
	if s.Redis == nil {
		return nil, errors.New("core registry verification unavailable")
	}
	boots, err := s.Redis.ZRange(ctx, "node:live", 0, -1).Result()
	if err != nil {
		return nil, err
	}
	var nodes []struct{ NodeID, CoreBootID string }
	if err = s.nodes(s.db(ctx)).Where("enabled").Select("node_id,core_boot_id").Scan(&nodes).Error; err != nil {
		return nil, err
	}
	known := map[string]string{}
	for _, n := range nodes {
		known[n.NodeID] = n.CoreBootID
	}
	out := []string{}
	for _, boot := range boots {
		info, err := s.Redis.HGetAll(ctx, "node:info:"+boot).Result()
		if err != nil {
			return nil, err
		}
		if len(info) == 0 {
			continue
		}
		if info["managed"] != "true" || info["core_boot_id"] == "" || known[info["node_id"]] != info["core_boot_id"] {
			out = append(out, "unmanaged or unrecognized active core: "+info["node_id"])
		}
	}
	return out, nil
}
func (s *Store) RecordStop(ctx context.Context, n Node) error {
	var id string
	var raw []byte
	err := s.activeUpgrade(s.db(ctx)).Select("id,nodes").Row().Scan(&id, &raw)
	if gormpg.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var members []string
	if err = json.Unmarshal(raw, &members); err != nil {
		return err
	}
	member := false
	for _, v := range members {
		if v == n.ID {
			member = true
		}
	}
	var step *int
	if member {
		var x int
		err = s.db(ctx).Table("updater.steps").Select("step_id").Where("upgrade_id = ? AND node_id = ? AND action = 'stop'", id, n.ID).Order("step_id DESC").Limit(1).Row().Scan(&x)
		if gormpg.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		step = &x
	}
	// The confirmation names the core boot the node reports for this shell
	// boot while it is stopped; a node that restarted since confirms nothing.
	var core string
	err = s.nodeRow(s.db(ctx), n.ID).Where("shell_boot_id = ? AND stopped", n.ShellBootID).Select("core_boot_id").Row().Scan(&core)
	if gormpg.IsNoRows(err) {
		return errors.New("stop confirmation identity changed")
	}
	if err != nil {
		return err
	}
	set := append(clause.AssignmentColumns([]string{"shell_boot_id", "step_id", "core_boot_id"}), clause.Assignment{Column: clause.Column{Name: "confirmed_at"}, Value: gorm.Expr("now()")})
	return s.db(ctx).Clauses(clause.OnConflict{Columns: columns("cluster_id", "node_id", "upgrade_id"), DoUpdates: set}).
		Create(&stopConfirmation{ClusterID: s.Cluster, NodeID: n.ID, ShellBootID: n.ShellBootID, UpgradeID: id, StepID: step, CoreBootID: core}).Error
}
func (s *Store) PluginMutationBlockers(ctx context.Context) ([]string, error) {
	db := s.db(ctx)
	checks := []struct {
		table string
		busy  *gorm.DB
	}{
		{"public.plugin_rollouts", db.Table("plugin_rollouts").Where("phase IN ('preparing','activating')")},
		{"public.plugin_uninstalls", db.Table("plugin_uninstalls")},
		{"public.plugin_rollout_cleanup", db.Table("plugin_rollout_cleanup").Where("state = 'cleanup_pending'")},
	}
	for _, c := range checks {
		present, err := gormpg.RelationExists(db, c.table)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		var busy bool
		if err := gormpg.ExistsQuery(db, c.busy).Row().Scan(&busy); err != nil {
			return nil, err
		}
		if busy {
			return []string{"plugin changes or runtime cleanup are still active"}, nil
		}
	}
	return nil, nil
}
func (s *Store) EnableNode(ctx context.Context, node string) error {
	return s.WithRegistration(ctx, node, func(ctx context.Context) error {
		return s.withChange(ctx, func(ctx context.Context) error {
			db := s.db(ctx)
			res := s.nodeRow(db, node).Updates(map[string]any{"enabled": true, "ready": false, "stopped": false, "mode": "maintenance",
				"joining_plan": gorm.Expr("COALESCE((?),'')", s.activeUpgrade(db).Select("id"))})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return sql.ErrNoRows
			}
			s.NotifyUpgrade(ctx)
			return nil
		})
	})
}

// ConfirmStoppedCore is called only by the local supervisor after proving that
// its recorded process group is gone. Redis expiry is never this proof.
func (s *Store) ConfirmStoppedCore(ctx context.Context, node, shellBoot, coreBoot string) error {
	if coreBoot == "" {
		return nil
	}
	return s.withTx(ctx, func(tx *gorm.DB) error {
		var current bool
		if err := s.nodeRow(tx, node).Clauses(forUpdate()).Select("shell_boot_id = ?", shellBoot).Row().Scan(&current); err != nil {
			return err
		}
		if !current {
			return ErrConflict
		}
		// The core's plugin runtime rows of its boot, in the tables its schema
		// has: the runtimes stopped, the pending cleanups done.
		cleaned := map[string]any{"state": "cleaned", "updated_at": gorm.Expr("now()")}
		for _, item := range []struct {
			table string
			rows  *gorm.DB
			set   map[string]any
		}{
			{"public.plugin_runtime_nodes", tx.Table("plugin_runtime_nodes").Where("boot_id = ?", coreBoot), map[string]any{"stopped": true, "updated_at": gorm.Expr("now()")}},
			{"public.plugin_rollout_cleanup", tx.Table("plugin_rollout_cleanup").Where("boot_id = ? AND state = 'cleanup_pending'", coreBoot), cleaned},
			{"public.plugin_rollout_nodes", tx.Table("plugin_rollout_nodes").Where("boot_id = ? AND state = 'cleanup_pending'", coreBoot), cleaned},
		} {
			present, err := gormpg.RelationExists(tx, item.table)
			if err != nil {
				return err
			}
			if present {
				if err := item.rows.Updates(item.set).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Store) packageReferenced(ctx context.Context, sum string) (bool, error) {
	db := s.db(ctx)
	if present, err := gormpg.RelationExists(db, "public.plugin_versions"); err != nil || !present {
		return false, err
	}
	var referenced bool
	err := gormpg.ExistsQuery(db, db.Table("plugin_versions").Where("package_sha256 = ?", sum)).Row().Scan(&referenced)
	return referenced, err
}

// ReferencedPackages lists every plugin package digest a version names.
func (s *Store) ReferencedPackages(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	db := s.db(ctx)
	if present, err := gormpg.RelationExists(db, "public.plugin_versions"); err != nil || !present {
		return out, err
	}
	var sums []string
	if err := db.Table("plugin_versions").Distinct().Pluck("lower(package_sha256)", &sums).Error; err != nil {
		return nil, err
	}
	for _, sum := range sums {
		out[sum] = true
	}
	return out, nil
}

// SetPrimary designates a new primary node. Must run under cluster-change lock
// with no active upgrade plan. Validates that the target node is enabled and
// has the current baseline artifacts.
func (s *Store) SetPrimary(ctx context.Context, node string) error {
	return s.withChange(ctx, func(ctx context.Context) error {
		if err := s.withTx(ctx, func(tx *gorm.DB) error {
			var active bool
			if err := gormpg.ExistsQuery(tx, s.activeUpgrade(tx)).Row().Scan(&active); err != nil {
				return err
			}
			if active {
				return errors.New("cannot change primary while an upgrade is active")
			}
			_, baseline, _, err := s.ClusterState(ctx)
			if err != nil {
				return err
			}
			var enabled bool
			var digest string
			if err = s.nodeRow(tx, node).Select("enabled,release_digest").Row().Scan(&enabled, &digest); err != nil {
				if gormpg.IsNoRows(err) {
					return fmt.Errorf("node %q is not registered", node)
				}
				return err
			}
			if !enabled {
				return fmt.Errorf("node %q is disabled", node)
			}
			if digest != baseline {
				return fmt.Errorf("node %q is not on baseline %s", node, baseline)
			}
			// The node runs the baseline (release_digest above), so it holds that
			// release. Plugin packages do not depend on the primary: every node
			// fetches a missing one from the online nodes (PluginBlobs).
			return s.clusterRow(tx).Update("primary_node", node).Error
		}); err != nil {
			return err
		}
		fmt.Printf("primary node changed to %s\n", node)
		return nil
	})
}

// RemoveNode permanently removes a disabled node from the cluster. The node
// must be disabled and silent for 5 minutes: a node stopped by hand never
// gets the stopped mark a plan's stop step writes, so silence is the proof
// that it is gone. Audit records are preserved.
func (s *Store) RemoveNode(ctx context.Context, node string) error {
	return s.withChange(ctx, func(ctx context.Context) error {
		if err := s.withTx(ctx, func(tx *gorm.DB) error {
			var enabled bool
			var lastSeen time.Time
			if err := s.nodeRow(tx, node).Select("enabled,last_seen").Row().Scan(&enabled, &lastSeen); err != nil {
				if gormpg.IsNoRows(err) {
					return fmt.Errorf("node %q is not registered", node)
				}
				return err
			}
			if enabled {
				return fmt.Errorf("node %q is still enabled; disable it first", node)
			}
			if time.Since(lastSeen) < 5*time.Minute {
				return fmt.Errorf("node %q was seen recently (%v ago); stop it and wait until it has been silent for 5 minutes", node, time.Since(lastSeen).Round(time.Second))
			}
			primary, _, _, err := s.ClusterState(ctx)
			if err != nil {
				return err
			}
			if node == primary {
				return fmt.Errorf("node %q is the primary; designate a new primary first", node)
			}
			// Delete the node and its admission. Plan steps, stop
			// confirmations and events stay as the audit record.
			if err = s.admissionRow(tx, node).Delete(map[string]any{}).Error; err != nil {
				return err
			}
			return s.nodeRow(tx, node).Delete(map[string]any{}).Error
		}); err != nil {
			return err
		}
		fmt.Printf("node %s removed from cluster\n", node)
		return nil
	})
}

// PlanStatus returns the current upgrade plan status including blocked reason.
func (s *Store) PlanStatus(ctx context.Context) (id, status, blockedReason string, err error) {
	err = s.activeUpgrade(s.db(ctx)).Select("id,status,blocked_reason").Limit(1).Row().Scan(&id, &status, &blockedReason)
	if gormpg.IsNoRows(err) {
		return "", "", "", nil
	}
	return
}
