package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"
)

// Market index format v2, CONTRACTS §11.1, built from the registry files
// (CONTRACTS §135). Unsigned: the trust is the registry's review.
const indexFormatVersion = 2

type marketIndex struct {
	Version     int              `json:"version"`
	GeneratedAt string           `json:"generated_at"`
	Publishers  []indexPublisher `json:"publishers"`
	Plugins     []indexPlugin    `json:"plugins"`
}

type indexPlugin struct {
	regPlugin
	Versions []regVersion `json:"versions"`
}

// cmdIndex validates a registry checkout and writes its index.json.
func cmdIndex(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("index", stderr)
	root := fs.String("registry", "", "registry checkout (publishers/, plugins/) (required)")
	out := fs.String("out", "", "output file (default: stdout)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *root == "" {
		return errors.New("--registry is required")
	}
	reg, err := loadRegistry(*root)
	if err != nil {
		return err
	}
	raw, err := encodeJSON(buildIndex(reg, time.Now().UTC()))
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = stdout.Write(raw)
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %s (%d publishers, %d plugins)\n", *out, len(reg.publishers), len(reg.plugins))
	return nil
}

// buildIndex lists every publisher by id, every plugin by key and each
// plugin's versions newest first.
func buildIndex(reg *registry, now time.Time) *marketIndex {
	idx := &marketIndex{Version: indexFormatVersion, GeneratedAt: now.Format(time.RFC3339),
		Publishers: []indexPublisher{}, Plugins: []indexPlugin{}}
	for _, id := range slices.Sorted(maps.Keys(reg.publishers)) {
		idx.Publishers = append(idx.Publishers, reg.publishers[id].indexPublisher)
	}
	for _, key := range slices.Sorted(maps.Keys(reg.plugins)) {
		e := reg.plugins[key]
		p := indexPlugin{regPlugin: e.plugin, Versions: []regVersion{}}
		for _, v := range e.sortedVersions() {
			p.Versions = append(p.Versions, *v)
		}
		idx.Plugins = append(idx.Plugins, p)
	}
	return idx
}
