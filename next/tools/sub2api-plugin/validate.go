package main

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
)

// Manifest validation is the host's, not the CLI's: sdk/manifest/check holds
// the one implementation of the rules, so `pack` rejects exactly what an
// installing server would reject (ARCHITECTURE 6.6, CONTRACTS §13) instead of
// a hand-kept subset that drifted from it.
//
// check.ValidateOptions.Tooling relaxes the three checks that only an
// installing host can make: hostCompat against a host version, the runtime
// binaries and the native UI entry. checkPackage reports those itself, with
// the CLI's own messages and its --allow-missing-ui escape hatch.

// validateManifest returns the manifest problems as "field: message" lines.
func validateManifest(m *manifest.Manifest, files map[string][]byte) []string {
	fields, ok := check.Fields(check.Validate(m, files, check.ValidateOptions{Tooling: true}))
	if !ok {
		return nil
	}
	return fieldLines(fields)
}

func fieldLines(fields []check.FieldError) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Field + ": " + f.Message
	}
	return out
}
