package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCleanArchitectureDependencies(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "domain/") && strings.Contains(name, "/internal/") {
				t.Errorf("domain depends on an outer layer: %s", path)
			}
			if strings.HasPrefix(path, "application/") && (strings.Contains(name, "/adapters/") || strings.Contains(name, "/transport/")) {
				t.Errorf("application depends on an adapter: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
