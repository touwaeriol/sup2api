package pkg

import (
	"errors"

	"github.com/robfig/cron/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Static manifest validation lives in the SDK (sdk/manifest/check) so that
// the host, the packaging CLI and a plugin's own tests run the same rules.
// This file is the host's wrapper: it keeps the API server modules import and
// turns the SDK's field errors into the core error clients see.

// KnownCapabilities lists the capability ids host 0.1 understands.
var KnownCapabilities = check.KnownCapabilities

// Resource caps besides the configurable memory cap.
const (
	MaxCPU          = check.MaxCPU
	MaxThreads      = check.MaxThreads
	MaxOpenFiles    = check.MaxOpenFiles
	MaxHookTimeout  = check.MaxHookTimeout
	MinRankTimeout  = check.MinRankTimeout
	MaxRankTimeout  = check.MaxRankTimeout
	MaxPromptBytes  = check.MaxPromptBytes
	RequiredArchAMD = check.RequiredArchAMD
	RequiredArchARM = check.RequiredArchARM
)

// EndpointOwner is a gateway endpoint of a platform declared by another
// installed plugin.
type EndpointOwner = check.EndpointOwner

// PlatformOwner is a platform declared by another installed plugin.
type PlatformOwner = check.PlatformOwner

// ChannelOwner is a notification channel declared by another installed
// plugin.
type ChannelOwner = check.ChannelOwner

// ValidateOptions carries host facts needed by Validate.
type ValidateOptions = check.ValidateOptions

// OthersFromManifests collects the platforms and endpoints declared by the
// given manifests of other installed plugins (for ValidateOptions).
func OthersFromManifests(ms []*manifest.Manifest) ([]PlatformOwner, []EndpointOwner) {
	return check.OthersFromManifests(ms)
}

// ChannelsFromManifests collects the notification channels declared by the
// given manifests of other installed plugins (for ValidateOptions).
func ChannelsFromManifests(ms []*manifest.Manifest) []ChannelOwner {
	return check.ChannelsFromManifests(ms)
}

// Validate checks a manifest against the package contents and host facts.
// All problems are reported together as invalid_argument with
// details.fields.
func Validate(m *manifest.Manifest, files map[string][]byte, opt ValidateOptions) error {
	err := check.Validate(m, files, opt)
	if err == nil {
		return nil
	}
	var ve *check.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	fields := make([]core.FieldError, len(ve.Fields))
	for i, f := range ve.Fields {
		fields[i] = core.FieldError{Field: f.Field, Code: f.Code, Message: f.Message}
	}
	return core.InvalidFields(fields...).WithMessage("plugin manifest validation failed")
}

// CheckContract refuses a package built for an older plugin contract
// (check.OutdatedPackage: runtime "none", an upstream block, a removed
// capability or manifest field) with the one message the runtime gives when
// it refuses to load such a package. It reads the manifest only: a binary
// of an older protocol shows when it is loaded.
func CheckContract(m *manifest.Manifest, files map[string][]byte, hostVersion string) error {
	msg, outdated := check.OutdatedPackage(m, files["manifest.json"], hostVersion)
	if !outdated {
		return nil
	}
	return core.InvalidFields(core.FieldError{Field: "manifest", Code: check.CodeOutdatedContract, Message: msg}).WithMessage(msg)
}

// Conflicts reports the platforms, gateway endpoints and notification
// channels of m that other plugins already hold (check.Conflicts).
func Conflicts(m *manifest.Manifest, others []PlatformOwner, endpoints []EndpointOwner, channels []ChannelOwner) []core.FieldError {
	fs := check.Conflicts(m, others, endpoints, channels)
	out := make([]core.FieldError, len(fs))
	for i, f := range fs {
		out[i] = core.FieldError{Field: f.Field, Code: f.Code, Message: f.Message}
	}
	return out
}

// HostCompatible matches a semver range against the host version, ignoring
// any pre-release/build suffix of the host version.
func HostCompatible(constraint, hostVersion string) (bool, error) {
	return check.HostCompatible(constraint, hostVersion)
}

// ParseSchedule parses a job schedule (5-field cron or "@every <d>").
func ParseSchedule(s string) (cron.Schedule, error) { return check.ParseSchedule(s) }

// MigrationFiles lists *.sql files directly under dir, sorted.
func MigrationFiles(files map[string][]byte, dir string) []string {
	return check.MigrationFiles(files, dir)
}

// EndpointsConflict reports whether two gateway endpoints could both match
// one request (same method, overlapping path patterns).
func EndpointsConflict(a, b manifest.Endpoint) bool { return check.EndpointsConflict(a, b) }
