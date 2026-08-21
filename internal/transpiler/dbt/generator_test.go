package dbt

import (
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestGenerateDBTProject(t *testing.T) {
	fixture, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}

	compilation, err := semantic.Compile(fixture.Compilation)
	if err != nil {
		t.Fatalf("semantic.Compile: %v", err)
	}

	plan, ok := compilation.Plan()
	if !ok {
		fail, _ := compilation.Failure()
		t.Fatalf("compilation did not produce a plan: %v", fail)
	}

	schema := fixture.InitialState.Schema()
	project, err := GenerateProject(plan, Options{
		ProjectName: "od_team_hos_pipeline",
		ProfileName: "prod_warehouse",
		Schema:      &schema,
	})
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}

	if project.Name != "od_team_hos_pipeline" {
		t.Errorf("project name = %q, want od_team_hos_pipeline", project.Name)
	}

	// Verify project files
	filesByPath := make(map[string]string)
	for _, f := range project.Files {
		filesByPath[f.Path] = f.Content
	}

	// 1. dbt_project.yml
	dbtYml, ok := filesByPath["dbt_project.yml"]
	if !ok {
		t.Fatal("missing dbt_project.yml")
	}
	if !strings.Contains(dbtYml, "name: 'od_team_hos_pipeline'") {
		t.Errorf("dbt_project.yml missing project name: %s", dbtYml)
	}

	// 2. Staging models
	if _, ok := filesByPath["models/staging/stg_entities_driver.sql"]; !ok {
		t.Error("missing staging model for driver")
	}
	if _, ok := filesByPath["models/staging/stg_relations.sql"]; !ok {
		t.Error("missing staging model for relations")
	}

	// 3. Transformation models
	var txFound bool
	for path, content := range filesByPath {
		if strings.HasPrefix(path, "models/transformations/tx_") {
			txFound = true
			if !strings.Contains(content, "{{ config(materialized='table') }}") {
				t.Errorf("model %s missing materialized table config", path)
			}
			if !strings.Contains(content, "{{ ref(") {
				t.Errorf("model %s missing ref() dependency", path)
			}
		}
	}
	if !txFound {
		t.Error("no transformation models found")
	}

	// 4. Checkpoint models
	if _, ok := filesByPath["models/checkpoints/chk_team_formed_v1.sql"]; !ok {
		t.Error("missing checkpoint model for team_formed.v1")
	}
	if _, ok := filesByPath["models/checkpoints/chk_team_hos_aggregated_v1.sql"]; !ok {
		t.Error("missing checkpoint model for team_hos_aggregated.v1")
	}
}

func TestDBTMultiTableOperatorGeneration(t *testing.T) {
	fixture, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(fixture.Compilation)
	if err != nil {
		t.Fatalf("semantic.Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("compilation produced no plan")
	}

	schema := fixture.InitialState.Schema()
	project, err := GenerateProject(plan, Options{
		ProjectName: "teamhos_dbt",
		Schema:      &schema,
	})
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}

	var modelPaths []string
	for _, f := range project.Files {
		if strings.HasPrefix(f.Path, "models/transformations/") {
			modelPaths = append(modelPaths, f.Path)
		}
	}
	if len(modelPaths) < 2 {
		t.Errorf("expected at least 2 transformation models, got %d: %v", len(modelPaths), modelPaths)
	}
}
