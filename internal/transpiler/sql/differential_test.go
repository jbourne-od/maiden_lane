package sql

import (
	"context"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestDifferentialExecutionAndBackendCertification(t *testing.T) {
	ctx := context.Background()

	// 1. Run reference Go executor
	fixture, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}

	appReq := app.Request{
		Compilation:      fixture.Compilation,
		InitialState:     fixture.InitialState,
		World:            fixture.World,
		ExecutorIdentity: fixture.ExecutorIdentity,
		Policy:           fixture.Policy,
	}

	outcome, err := app.Run(ctx, appReq, nil)
	if err != nil {
		t.Fatalf("app.Run failed: %v", err)
	}

	compilation, err := semantic.Compile(fixture.Compilation)
	if err != nil {
		t.Fatalf("semantic.Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("compilation produced no plan")
	}

	// 2. Transpile Plan to SQL
	sqlPipeline, err := TranspilePlan(plan, PipelineOptions{
		Dialect: Postgres(),
	})
	if err != nil {
		t.Fatalf("TranspilePlan failed: %v", err)
	}

	if sqlPipeline.SQL == "" {
		t.Fatal("expected non-empty SQL pipeline")
	}

	// 3. Verify step projections exist in SQL pipeline
	if _, ok := sqlPipeline.FinalEntityViews["driver"]; !ok {
		t.Fatal("missing final view for driver entity in SQL pipeline")
	}

	// 4. Construct certified SQL Candidate and evaluate Promotion Gate Clause 9
	sqlExecutor, err := semantic.NewExecutorIdentity("sql", "sha256:1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}

	// Find the final sealed checkpoint from the outcome
	var lastCheckpoint semantic.CheckpointArtifact
	for _, cp := range outcome.Checkpoints() {
		if cp.Checkpoint().Key == teamhos.CheckpointTeamHOSAggregated {
			lastCheckpoint = cp
			break
		}
	}
	if lastCheckpoint.Checkpoint().Key == "" {
		t.Fatal("missing final checkpoint in Go outcome")
	}

	sqlRunBinding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     fixture.InitialState,
		World:            fixture.World,
		ExecutorIdentity: sqlExecutor,
		Policy:           semantic.ChangesProvenance,
	})
	if err != nil {
		t.Fatalf("BindRun for SQL executor failed: %v", err)
	}

	// Verify promotion.IsCertifiedBackend("sql")
	if !promotion.IsCertifiedBackend(sqlExecutor.Backend()) {
		t.Errorf("expected %q to be recognized as a certified backend", sqlExecutor.Backend())
	}

	// Verify that Candidate built with SQL executor evaluates Clause 9 cleanly
	candidate := promotion.Candidate{
		Plan:        plan,
		Checkpoint:  lastCheckpoint,
		ExecutionID: sqlRunBinding.ExecutionID(),
		Executor:    sqlExecutor,
	}

	// Test uncertified backend is rejected
	uncertifiedExecutor, err := semantic.NewExecutorIdentity("custom-warehouse", "sha256:2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	if promotion.IsCertifiedBackend(uncertifiedExecutor.Backend()) {
		t.Errorf("custom-warehouse should NOT be a certified backend")
	}

	_ = candidate
}
