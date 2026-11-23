package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// The registry is read from a pull request's checkout: only regular files
// and real directories count, a symbolic link (or any other kind of file) is
// an error, so nothing outside the checkout is ever read.

// readRegular reads rel below root, refusing anything but a regular file.
func readRegular(root, rel string) ([]byte, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", rel)
	}
	return os.ReadFile(p)
}

// readDirIfExists lists rel below root (nothing when it is missing),
// refusing anything but a real directory.
func readDirIfExists(root, rel string) ([]fs.DirEntry, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", rel)
	}
	return os.ReadDir(p)
}

// jsonFiles lists the regular *.json files of rel; any other entry is a
// problem.
func jsonFiles(root, rel string, errs *[]error) []string {
	entries, err := readDirIfExists(root, rel)
	addErr(errs, err)
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), regJSONExt) {
			*errs = append(*errs, fmt.Errorf("%s: unexpected entry %q", rel, e.Name()))
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// onlyEntries fails when rel holds anything but the named regular files and
// real directories.
func onlyEntries(root, rel string, files, dirs []string) error {
	entries, err := readDirIfExists(root, rel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		ok := (e.Type().IsRegular() && slices.Contains(files, e.Name())) || (e.IsDir() && slices.Contains(dirs, e.Name()))
		if !ok {
			return fmt.Errorf("%s: unexpected entry %q", rel, e.Name())
		}
	}
	return nil
}

// fileChange is one file that differs between two checkouts.
type fileChange struct {
	path string // slash-separated, relative to the checkout
	kind string // changeAdded, changeModified or changeDeleted
}

const (
	changeAdded    = "added"
	changeModified = "modified"
	changeDeleted  = "deleted"
)

// diffTrees lists the files that differ between two checkouts, by path.
func diffTrees(baseRoot, headRoot string) ([]fileChange, error) {
	base, err := treeHashes(baseRoot)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	head, err := treeHashes(headRoot)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	all := maps.Clone(base)
	maps.Copy(all, head)
	var out []fileChange
	for _, p := range slices.Sorted(maps.Keys(all)) {
		old, inBase := base[p]
		cur, inHead := head[p]
		switch {
		case !inBase:
			out = append(out, fileChange{p, changeAdded})
		case !inHead:
			out = append(out, fileChange{p, changeDeleted})
		case old != cur:
			out = append(out, fileChange{p, changeModified})
		}
	}
	return out, nil
}

// treeHashes maps every file below root (except .git) to its SHA-256; a
// symbolic link or any other non-regular file is an error.
func treeHashes(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.Name() == ".git" && p != root:
			return skipEntry(d)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file", relSlash(root, p))
		}
		sum, _, err := fileSHA256(p)
		files[relSlash(root, p)] = sum
		return err
	})
	return files, err
}

// skipEntry skips a directory, or ignores a file, in a walk.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return path.Clean(filepath.ToSlash(rel))
}
