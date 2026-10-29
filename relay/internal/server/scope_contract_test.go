package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// Read the real Desktop default declaration so either module changing it breaks
// the contract test instead of silently testing a second copied list.
func desktopDefaultScopes(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "..", "desktop", "internal", "remote", "service.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var scopes []string
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "defaultScopes" {
			return true
		}
		literal, ok := value.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatal("default scopes must be inspectable")
		}
		for _, element := range literal.Elts {
			item, ok := element.(*ast.BasicLit)
			if !ok {
				t.Fatal("nonliteral scope")
			}
			scope, err := strconv.Unquote(item.Value)
			if err != nil {
				t.Fatal(err)
			}
			scopes = append(scopes, scope)
		}
		return false
	})
	if len(scopes) == 0 {
		t.Fatal("Desktop default scopes not found")
	}
	return scopes
}

func TestDesktopScopeContractAndLegacySubsets(t *testing.T) {
	scopes := desktopDefaultScopes(t)
	if !validScopes(scopes) {
		t.Fatalf("Desktop defaults rejected: %v", scopes)
	}
	var legacy []string
	for _, scope := range scopes {
		if !validScopes([]string{scope}) {
			t.Fatalf("explicit subset rejected: %s", scope)
		}
		if scope != "conversation.create" {
			legacy = append(legacy, scope)
		}
	}
	if len(legacy) != 8 || !validScopes(legacy) {
		t.Fatal("legacy grant rejected")
	}
	if validScopes(append(scopes, "system.shutdown")) || validScopes([]string{"status.read", "status.read"}) {
		t.Fatal("invalid scope accepted")
	}
}
