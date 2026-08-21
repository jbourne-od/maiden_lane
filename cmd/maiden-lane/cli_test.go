package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISubcommands(t *testing.T) {
	// Setup sample .ml rule file with embedded schema
	tempDir := t.TempDir()
	sampleML := filepath.Join(tempDir, "rules.ml")
	mlContent := `
schema {
  entity driver {
    driver_id: string;
    assignment_status: string;
  }
}

rule form_team_v1 {
  select driver
  where not (driver.assignment_status == "assigned")
  set driver.assignment_status = "assigned";
}

checkpoint team_formed_v1 after form_team_v1;
`
	if err := os.WriteFile(sampleML, []byte(mlContent), 0600); err != nil {
		t.Fatalf("write sample.ml: %v", err)
	}

	deps := productionDeps()

	// 1. Version
	t.Run("version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"version"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Errorf("version exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "maiden-lane version") {
			t.Errorf("version output missing version string: %s", stdout.String())
		}
	})

	// 2. Compile
	t.Run("compile", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"compile", sampleML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("compile exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Plan compiled successfully!") {
			t.Errorf("compile output missing success message: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Plan ID:") {
			t.Errorf("compile output missing Plan ID: %s", stdout.String())
		}
	})

	// 3. Run (Go executor)
	t.Run("run_go", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"run", sampleML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("run exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Execution completed successfully!") {
			t.Errorf("run output missing success message: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Semantic Run ID:") {
			t.Errorf("run output missing Semantic Run ID: %s", stdout.String())
		}
	})

	// 4. Run (SQL backend)
	t.Run("run_sql", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"run", sampleML, "--backend", "sql"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("run sql exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "WITH\n") {
			t.Errorf("run sql output missing WITH CTE: %s", stdout.String())
		}
	})

	// 5. Transpile SQL
	t.Run("transpile_sql", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"transpile", "sql", sampleML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("transpile sql exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "WITH\n") {
			t.Errorf("transpile sql output missing WITH CTE: %s", stdout.String())
		}
	})

	// 6. Transpile dbt
	t.Run("transpile_dbt", func(t *testing.T) {
		dbtDir := filepath.Join(tempDir, "dbt_out")
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"transpile", "dbt", sampleML, "--out", dbtDir}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("transpile dbt exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Generated dbt project") {
			t.Errorf("transpile dbt output missing success message: %s", stdout.String())
		}
		if _, err := os.Stat(filepath.Join(dbtDir, "dbt_project.yml")); os.IsNotExist(err) {
			t.Error("missing generated dbt_project.yml")
		}
	})

	// 7. Diff
	t.Run("diff", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"diff", sampleML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("diff exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Semantic State Diff:") {
			t.Errorf("diff output missing header: %s", stdout.String())
		}
	})

	// 8. Gate
	t.Run("gate", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"gate"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("gate exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "9-Clause Promotion Gate Evaluation:") {
			t.Errorf("gate output missing header: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "certified_backend") {
			t.Errorf("gate output missing certified_backend clause: %s", stdout.String())
		}
	})
}
