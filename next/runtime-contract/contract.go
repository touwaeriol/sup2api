// Package runtimecontract defines the versioned wire protocol between shell and core.
// It intentionally has no dependency on the core or plugin SDK.
package runtimecontract

import "time"

// Protocol 2 adds separate migration/plugin preparation permits and explicit
// schema boundaries. Protocol 1 cores reject these fields and are not peers.
const Protocol = 2

// These describe the persisted business formats this release actually supports.
const ClusterProtocolVersion = 1
const TaskProtocolVersion = 1

// HostAPIVersion is the version of the plugin Host API (sdk/protocol uses
// this constant). Releases with different values cannot run side by side.
// 10: the single Execute path replaced BuildUpstreamRequest and the usage
// callbacks.
const HostAPIVersion = 10

// Plugin package limits, applied by the core (unpack, upload, download), the
// shell's package store and the pack tool alike: about four times the
// largest built-in package (CONTRACTS §34).
const (
	PluginPackageMaxBytes  = 64 << 20  // compressed archive
	PluginUnpackedMaxBytes = 256 << 20 // sum of uncompressed file sizes
	PluginPackageMaxFiles  = 2000      // regular files
	// PluginPackageTransferTimeout bounds one package transfer: a market
	// download, a core's request through its shell and the shell's fetch
	// from another node.
	PluginPackageTransferTimeout = 10 * time.Minute
)

// ClusterChangeLock serializes core plans and plugin mutation submissions.
const ClusterChangeLock = "system:cluster-change"

const (
	HelloPath     = "/v1/hello"
	StatusPath    = "/v1/status"
	PreparePath   = "/v1/prepare"
	AdmissionPath = "/v1/admission"
	DrainPath     = "/v1/drain"
	ShutdownPath  = "/v1/shutdown"
)

type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (r Range) Contains(v int) bool { return r.Min > 0 && v >= r.Min && v <= r.Max }

type Hello struct {
	CoreVersion     string `json:"core_version"`
	Protocol        int    `json:"protocol"`
	NodeID          string `json:"node_id"`
	BootID          string `json:"boot_id"`
	ReleaseDigest   string `json:"release_digest"`
	ClusterProtocol Range  `json:"cluster_protocol"`
	TaskProtocol    Range  `json:"task_protocol"`
	HostAPIVersion  int    `json:"host_api_version"`
}

type Status struct {
	Hello
	Mode               string   `json:"mode"`
	Ready              bool     `json:"ready"`
	DrainComplete      bool     `json:"drain_complete"`
	ActiveHTTP         int64    `json:"active_http"`
	ActiveBackground   int64    `json:"active_background"`
	PendingUsageWrites int64    `json:"pending_usage_writes"`
	PluginRevision     string   `json:"plugin_revision,omitempty"`
	SchemaContract     string   `json:"schema_contract,omitempty"`
	Blockers           []string `json:"blockers,omitempty"`
}
type Admission struct {
	BootID            string `json:"boot_id"`
	ReleaseDigest     string `json:"release_digest"`
	Revision          int64  `json:"revision"`
	ServeHTTP         bool   `json:"serve_http"`
	ClaimBackground   bool   `json:"claim_background"`
	CoordinatePlugins bool   `json:"coordinate_plugins"`
}
type PrepareRequest struct {
	BootID         string `json:"boot_id"`
	ReleaseDigest  string `json:"release_digest"`
	AllowMigration bool   `json:"allow_migration"`
	Bootstrap      bool   `json:"bootstrap"`
	// Migration is independent from first-install bootstrap. A normal update
	// must never re-seed administrators or bundled plugin targets.
	ExpectedSchemaBefore string `json:"expected_schema_before,omitempty"`
	ExpectedSchemaAfter  string `json:"expected_schema_after,omitempty"`
	// Permits plugin rollout recovery during preparation while all HTTP and
	// business task admission remains closed. Required when old cores stopped.
	CoordinatePlugins bool `json:"coordinate_plugins,omitempty"`
}
type DrainRequest struct {
	OperationID string    `json:"operation_id"`
	Deadline    time.Time `json:"deadline"`
	// Graceful says the shell already routes new requests elsewhere, so the
	// core may let requests in flight finish (within the deadline, capped on
	// the core side) before it reports DrainComplete.
	Graceful bool `json:"graceful,omitempty"`
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}
type Platform struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	RuntimeABI   string `json:"runtime_abi"`
	BundleDigest string `json:"bundle_digest"`
	BundleBytes  int64  `json:"bundle_bytes"`
	Files        []File `json:"files"`
}

// Manifest is a core release manifest, published as a plain JSON document.
// The release digest is the SHA-256 of the exact published bytes, not of a
// reserialization, allowing independently implemented publishers.
type Manifest struct {
	CoreVersion         string     `json:"core_version"`
	ManifestVersion     int        `json:"manifest_version"`
	ReleaseID           string     `json:"release_id"`
	SourceCommit        string     `json:"source_commit"`
	BuildID             string     `json:"build_id"`
	CreatedAt           time.Time  `json:"created_at"`
	Platforms           []Platform `json:"platforms"`
	ShellProtocol       Range      `json:"shell_protocol"`
	CoreControlProtocol Range      `json:"core_control_protocol"`
	ClusterProtocol     Range      `json:"cluster_protocol"`
	TaskProtocol        Range      `json:"task_protocol"`
	SchemaBefore        string     `json:"schema_before"`
	SchemaAfter         string     `json:"schema_after"`
	Strategy            string     `json:"strategy"`
	HostAPIVersion      int        `json:"host_api_version"`
	PluginCapabilities  []string   `json:"plugin_capabilities,omitempty"`
}
