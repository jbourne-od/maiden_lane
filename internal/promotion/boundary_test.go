package promotion

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// productionImportAllowlist is the complete set of packages the gate may import.
//
// The package doc claims this gate is pure -- no clock, no database, no network,
// no state between evaluations -- and until now that claim was disciplinary. It is
// structural here, in the same allowlist form the kernel uses (Inviolate 12,
// AGENTS.md section 3.3): an allowlist fails the moment the package reaches for
// anything new, while a denylist silently admits whatever nobody thought to forbid.
//
// The stake is specific. A gate that could read a clock would authorize
// differently on replay; one that could reach a database would evaluate against
// whatever storage currently says rather than against the evidence it was handed,
// which is exactly the projection authority this design refuses.
var productionImportAllowlist = []string{
	"github.com/optimaldynamics/maiden-lane/internal/ports",
	"github.com/optimaldynamics/maiden-lane/internal/semantic",
}

// testImportAllowlist additionally admits testing and the golden fixture. Tests
// here judge artifacts the kernel actually produced, because a
// semantic.CheckpointArtifact cannot be built any other way.
var testImportAllowlist = append(slices.Clone(productionImportAllowlist),
	"bytes",
	"slices",
	"strings",
	"testing",
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos",
)

// forbiddenImportPrefixes names capabilities that must never appear whether or not
// the allowlists are later widened.
//
// context is here deliberately, and is the one that will look wrong to a future
// reader: nearly every other package in this codebase takes one. This gate must
// not, because a context parameter is the visible signature of I/O, cancellation,
// and deadlines, none of which a pure function of its arguments can have. If the
// gate ever needs a context, something has been moved into it that belongs in the
// caller that fetches evidence.
//
// internal/app and internal/adapters are forbidden as inversions: the application
// wires the gate, and an adapter is below ports. Importing either would let the
// gate reach for its own inputs rather than be handed them.
var forbiddenImportPrefixes = []string{
	"context",
	"time",
	"os",
	"net",
	"log",
	"log/slog",
	"database/sql",
	"math/rand",
	"crypto/rand",
	"encoding/json",
	"reflect",
	"go.opentelemetry.io/",
	"github.com/aws/",
	"github.com/optimaldynamics/maiden-lane/internal/app",
	"github.com/optimaldynamics/maiden-lane/internal/adapters",
	"github.com/optimaldynamics/maiden-lane/internal/httpapi",
	"github.com/optimaldynamics/maiden-lane/internal/observability",
	"github.com/optimaldynamics/maiden-lane/internal/worker",
}

// boundaryCheckerFile is this file. Checking imports requires reading the package
// directory and parsing Go source, so the one file that enforces the boundary
// cannot also sit inside it. The exemption is exactly one named file.
const boundaryCheckerFile = "boundary_test.go"

func TestPromotionGateImportsStayPureAndClosed(t *testing.T) {
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
				t.Errorf("%s imports %q, which is outside the promotion allowlist", name, path)
			}
			for _, forbidden := range forbiddenImportPrefixes {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Errorf("%s imports operational package %q", name, path)
				}
			}
		}
	}

	// A path or build-tag mistake that inspected nothing would make this test pass
	// while proving nothing at all.
	if inspected == 0 {
		t.Fatal("import walk inspected no Go files")
	}
}

// Growing the exemption, or letting it name a file that no longer exists, would
// carve an unchecked hole in the boundary.
func TestPromotionBoundaryExemptionIsExactlyThisFile(t *testing.T) {
	if _, err := os.Stat(boundaryCheckerFile); err != nil {
		t.Fatalf("exempt file %q does not exist: %v", boundaryCheckerFile, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), boundaryCheckerFile, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", boundaryCheckerFile, err)
	}
	permitted := []string{"go/parser", "go/token", "os", "path/filepath", "slices", "strings", "testing"}
	for _, imported := range parsed.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if !slices.Contains(permitted, path) {
			t.Errorf("the boundary checker imports %q, which belongs in a checked file", path)
		}
	}
}
