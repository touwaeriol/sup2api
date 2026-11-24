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
// often: the handshake compares it with what the plugin calls itself.
var bareKeyAllowed = map[string]int{
	"plugin/grpcruntime/instance.go": 2,
}

// The core keys a plugin by its id (CONTRACTS §137): production code reads
// m.ID() of a manifest, not m.Key (x.Manifest.Key or m.Key), outside the
// few places bareKeyAllowed lists.
func TestNoBareManifestKey(t *testing.T) {
	t.Parallel()
	found := bareKeyReads(t, filepath.Join("..", ".."))
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

// bareKeyReads counts the reads of a manifest's bare key in each Go file
// under root (tests aside), by path relative to root.
func bareKeyReads(t *testing.T, root string) map[string]int {
	fset := token.NewFileSet()
	found := map[string]int{}
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
	return found
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
