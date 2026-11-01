package repotest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/repotest"
)

// TestTestsUseTrimpathSafeRootDiscovery prevents test helpers from deriving
// filesystem paths from runtime.Caller, whose paths change under -trimpath.
// Keep root discovery in repotest.Root instead.
func TestTestsUseTrimpathSafeRootDiscovery(t *testing.T) {
	root, err := repotest.Root()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			// Exclude local worktrees, dependency trees and this helper package.
			switch entry.Name() {
			case ".git", ".claude", "codex", "vendor", "node_modules":
				return fs.SkipDir
			}
			if filepath.ToSlash(rel) == "internal/repotest" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath != "runtime" {
				continue
			}
			name := "runtime"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				var caller bool
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					pkg, ok := fun.X.(*ast.Ident)
					caller = ok && pkg.Name == name && fun.Sel.Name == "Caller"
				case *ast.Ident:
					caller = name == "." && fun.Name == "Caller"
				}
				if caller {
					t.Errorf("%s:%d: runtime.Caller is not trimpath-safe for repository root discovery; use repotest.Root", filepath.ToSlash(rel), fset.Position(call.Pos()).Line)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository tests: %v", err)
	}
}
