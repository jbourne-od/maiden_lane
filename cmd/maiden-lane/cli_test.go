package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISubcommands(t *testing.T) {
	tempDir := t.TempDir()

	// 1. .ml rule file with embedded schema
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

	// 2. Separate .json schema file
	schemaJSON := filepath.Join(tempDir, "schema.json")
	schemaJSONContent := `{
  "entities": [
    {
      "kind": "driver",
      "fields": [
        {"name": "driver_id", "type": "string"},
        {"name": "assignment_status", "type": "string"}
      ]
    }
  ]
}`
	if err := os.WriteFile(schemaJSON, []byte(schemaJSONContent), 0600); err != nil {
		t.Fatalf("write schema.json: %v", err)
	}

	// 3. Rule file without embedded schema (uses --schema flag)
	ruleOnlyML := filepath.Join(tempDir, "rule_only.ml")
	ruleOnlyContent := `
rule form_team_v1 {
  select driver
  where not (driver.assignment_status == "assigned")
  set driver.assignment_status = "assigned";
}
`
	if err := os.WriteFile(ruleOnlyML, []byte(ruleOnlyContent), 0600); err != nil {
		t.Fatalf("write rule_only.ml: %v", err)
	}

	// 4. Two JSON state files for diffing
	stateAJSON := filepath.Join(tempDir, "state_a.json")
	stateAContent := `{
  "schema": {
    "entities": [
      {
        "kind": "driver",
        "fields": [{"name": "driver_id", "type": "string"}, {"name": "status", "type": "string"}]
      }
    ]
  },
  "entities": [
    {
      "kind": "driver",
      "id": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
      "fields": {"driver_id": "D1", "status": "AVAILABLE"}
    }
  ]
}`
	stateBJSON := filepath.Join(tempDir, "state_b.json")
	stateBContent := `{
  "schema": {
    "entities": [
      {
        "kind": "driver",
        "fields": [{"name": "driver_id", "type": "string"}, {"name": "status", "type": "string"}]
      }
    ]
  },
  "entities": [
    {
      "kind": "driver",
      "id": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
      "fields": {"driver_id": "D1", "status": "ASSIGNED"}
    }
  ]
}`
	if err := os.WriteFile(stateAJSON, []byte(stateAContent), 0600); err != nil {
		t.Fatalf("write state_a.json: %v", err)
	}
	if err := os.WriteFile(stateBJSON, []byte(stateBContent), 0600); err != nil {
		t.Fatalf("write state_b.json: %v", err)
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

	// 2. Compile with embedded schema
	t.Run("compile_embedded_schema", func(t *testing.T) {
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

	// 3. Compile with external JSON schema flag
	t.Run("compile_json_schema_flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"compile", ruleOnlyML, "--schema", schemaJSON}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("compile with json schema exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Plan compiled successfully!") {
			t.Errorf("compile output missing success message: %s", stdout.String())
		}
	})

	// 4. Run (Go executor)
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

	// 5. Run unsupported backend fails
	t.Run("run_unsupported_backend_fails", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"run", sampleML, "--backend", "sql"}, &stdout, &stderr, deps)
		if code == 0 {
			t.Fatal("expected run --backend sql to fail with guidance to use transpile sql, got 0")
		}
		if !strings.Contains(stderr.String(), "unsupported execution backend") {
			t.Errorf("expected unsupported backend message in stderr, got: %s", stderr.String())
		}
	})

	// 6. Transpile SQL
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

	// 7. Transpile dbt
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

	// 8. Diff between two state files
	t.Run("diff_two_state_files", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"diff", stateAJSON, stateBJSON}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("diff exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Identical: false") {
			t.Errorf("diff output should report Identical: false, got: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Modified Entities: 1") {
			t.Errorf("diff output should report Modified Entities: 1, got: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "status: baseline=AVAILABLE, candidate=ASSIGNED") {
			t.Errorf("diff output missing field diff: %s", stdout.String())
		}
	})

	// 9. Gate evaluation fail-closed
	t.Run("gate_fail_closed_on_unauthorized_candidate", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"gate"}, &stdout, &stderr, deps)
		if code == 0 {
			t.Fatal("expected gate evaluation on empty candidate to fail closed with non-zero exit code")
		}
		if !strings.Contains(stdout.String(), "9-Clause Promotion Gate Evaluation:") {
			t.Errorf("gate output missing header: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Authorized: false") {
			t.Errorf("gate output missing Authorized: false: %s", stdout.String())
		}
		if !strings.Contains(stderr.String(), "promotion gate refused") {
			t.Errorf("gate stderr missing promotion gate refused error: %s", stderr.String())
		}
	})

	// 10. Titan real-world customer showcase pipeline
	t.Run("titan_customer_showcase", func(t *testing.T) {
		titanML := filepath.Join("..", "..", "examples", "customers", "titan", "titan_orders.ml")
		titanState := filepath.Join("..", "..", "examples", "customers", "titan", "titan_input.json")
		if _, err := os.Stat(titanML); os.IsNotExist(err) {
			t.Skip("titan showcase files not in expected relative path")
		}

		// Compile
		var stdout, stderr bytes.Buffer
		code := processMain([]string{"compile", titanML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("titan compile exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Plan compiled successfully!") {
			t.Errorf("titan compile missing success message: %s", stdout.String())
		}

		// Run with state
		stdout.Reset()
		stderr.Reset()
		code = processMain([]string{"run", titanML, "--state", titanState}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("titan run exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Execution completed successfully!") {
			t.Errorf("titan run missing success message: %s", stdout.String())
		}

		// Transpile SQL
		stdout.Reset()
		stderr.Reset()
		code = processMain([]string{"transpile", "sql", titanML}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("titan transpile sql exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "WITH\n") {
			t.Errorf("titan transpile sql missing WITH: %s", stdout.String())
		}

		// Transpile dbt
		dbtDir := filepath.Join(tempDir, "titan_dbt")
		stdout.Reset()
		stderr.Reset()
		code = processMain([]string{"transpile", "dbt", titanML, "--out", dbtDir}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("titan transpile dbt exit code = %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Generated dbt project") {
			t.Errorf("titan transpile dbt missing success: %s", stdout.String())
		}
	})
}
