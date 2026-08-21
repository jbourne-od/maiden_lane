package semantic

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// productionImportAllowlist is the complete set of packages the pure semantic
// kernel may import. It is an allowlist rather than a denylist on purpose: a
// denylist silently admits whatever nobody thought to forbid, while this test
// fails the moment the kernel reaches for anything new, forcing the question to
// be answered deliberately (Inviolate 12, AGENTS.md section 3.3).
var productionImportAllowlist = []string{
	"bytes",
	"crypto/sha256",
	"encoding/binary",
	"encoding/hex",
	"fmt",
	"slices",
	"sort",
	// strconv was admitted deliberately, which is what this allowlist is for. Value
	// implements encoding.TextMarshaler so a declaration containing a literal can survive
	// the plan store, and rebuilding an int64 from text needs a parser. strconv is pure and
	// deterministic: an identical input produces an identical output on every machine, which
	// is the property Inviolate 12 protects. The alternative was encoding/json in the kernel,
	// which is the coupling this list exists to refuse.
	"strconv",
	"strings",
	"unicode/utf8",
}

// testImportAllowlist additionally admits the testing package. Test files share
// the kernel's purity constraints: a test that reached for a clock, the
// environment, or the network could make a semantic result appear reproducible
// only on the machine that ran it.
var testImportAllowlist = append(slices.Clone(productionImportAllowlist), "testing")

// forbiddenImportPrefixes names the operational capabilities that must never
// appear, whether or not the allowlists are later widened. Anything here can
// make an identical semantic input produce a different semantic output.
var forbiddenImportPrefixes = []string{
	"github.com/optimaldynamics/maiden-lane/internal/app",
	"github.com/optimaldynamics/maiden-lane/internal/observability",
	"github.com/optimaldynamics/maiden-lane/internal/fixtures",
	"github.com/optimaldynamics/maiden-lane/internal/httpapi",
	"go.opentelemetry.io/",
	"github.com/aws/",
	"stochflow",
	"log",
	"log/slog",
	"net",
	"net/http",
	"os",
	"os/exec",
	"path/filepath",
	"io/ioutil",
	"time",
	"math/rand",
	"crypto/rand",
	"github.com/google/uuid",
	"database/sql",
	"encoding/json",
	"reflect",
}

// boundaryCheckerFile is this file. The import checker must read the package
// directory and parse Go source to do its job, so it necessarily imports os,
// path/filepath, and go/parser: the one file that enforces the boundary cannot
// also sit inside it. The exemption is exactly one named file, and
// TestSemanticBoundaryExemptionIsExactlyThisFile keeps it that way.
const boundaryCheckerFile = "boundary_test.go"

// Production break caught: a single operational import inside the kernel would
// let wall-clock time, the environment, the network, or telemetry influence a
// semantic identity, breaking replay for every artifact this package signs.
func TestSemanticKernelImportsStayPureAndClosed(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	inspected := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == boundaryCheckerFile {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		allowed := productionImportAllowlist
		if strings.HasSuffix(name, "_test.go") {
			allowed = testImportAllowlist
		}
		inspected++

		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			if !slices.Contains(allowed, path) {
				t.Errorf("%s imports %q, which is outside the semantic allowlist", name, path)
			}
			for _, forbidden := range forbiddenImportPrefixes {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Errorf("%s imports operational package %q", name, path)
				}
			}
		}
	}

	// A path or build-tag mistake that inspected nothing would make this test
	// pass while proving nothing at all.
	if inspected == 0 {
		t.Fatal("import walk inspected no Go files")
	}
}

// Production break caught: growing the exemption, or letting it name a file
// that no longer exists, would carve an unchecked hole in the kernel boundary.
func TestSemanticBoundaryExemptionIsExactlyThisFile(t *testing.T) {
	if _, err := os.Stat(boundaryCheckerFile); err != nil {
		t.Fatalf("exempt file %q does not exist: %v", boundaryCheckerFile, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), boundaryCheckerFile, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", boundaryCheckerFile, err)
	}
	// The checker earns its exemption only by importing what checking requires
	// and nothing else. Anything beyond this set belongs in a checked file.
	permitted := []string{"go/parser", "go/token", "os", "path/filepath", "slices", "strings", "testing"}
	for _, imported := range parsed.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if !slices.Contains(permitted, path) {
			t.Errorf("the exempt boundary checker imports %q, which its exemption does not cover", path)
		}
	}
}

// Production break caught: an allowlist that drifted ahead of the code would
// quietly pre-authorize an import nobody has reviewed.
func TestSemanticImportAllowlistHasNoUnusedEntries(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	used := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range parsed.Imports {
			used[strings.Trim(imported.Path.Value, `"`)] = true
		}
	}
	for _, allowed := range productionImportAllowlist {
		if !used[allowed] {
			t.Errorf("allowlist entry %q is no longer imported; remove it rather than pre-authorizing it", allowed)
		}
	}
}
