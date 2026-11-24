package pkg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// bareKeyAllowed are the files that may read a manifest's bare key, and how
// often: the handshake compares it with what the plugin calls itself, and
// the outdated-contract message names the plugin as the SDK's does.
var bareKeyAllowed = map[string]int{
	"plugin/grpcruntime/instance.go": 2,
	"plugin/grpcruntime/runtime.go":  1,
}

// The core keys a plugin by its id (CONTRACTS §137): production code reads
// m.ID() of a manifest, not m.Key (x.Manifest.Key or m.Key), outside the
// few places bareKeyAllowed lists.
func TestNoBareManifestKey(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	found := map[string]int{}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Key" && isManifest(sel.X) {
				found[filepath.ToSlash(rel)]++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, n := range found {
		if bareKeyAllowed[file] != n {
			t.Errorf("%s reads a manifest's bare key %d times (allowed %d): use m.ID(), the plugin id", file, n, bareKeyAllowed[file])
		}
	}
	for file := range bareKeyAllowed {
		if found[file] == 0 {
			t.Errorf("bareKeyAllowed lists %s, which reads no bare key", file)
		}
	}
}

// isManifest reports whether x reads as a manifest: x.Manifest or m.
func isManifest(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "Manifest"
	case *ast.Ident:
		return x.Name == "m"
	}
	return false
}
