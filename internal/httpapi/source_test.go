package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceFile is one hand-written file of this package, read so that boundary
// tests can assert properties of the code itself rather than of its behavior.
type sourceFile struct {
	name     string
	contents string
}

// nonGeneratedSourceFiles returns this package's hand-written production Go
// source.
//
// Generated output is excluded because it lives in its own package and is
// reproduced from api/openapi.yaml; asserting against it would be asserting
// against the generator. Test files are excluded because the rule under
// assertion is about production code: a test that checks "no digest is
// constructed here" necessarily contains the very literal it forbids, and
// fixtures legitimately need literal digests to pin world references.
func nonGeneratedSourceFiles(t *testing.T) []sourceFile {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	files := make([]sourceFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		files = append(files, sourceFile{name: name, contents: string(contents)})
	}
	if len(files) == 0 {
		t.Fatal("source scan found no files")
	}
	return files
}
