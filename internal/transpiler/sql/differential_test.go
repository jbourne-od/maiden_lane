package sql

import (
	"context"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
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

	schema := fixture.InitialState.Schema()

	// 2. Transpile Plan to SQL
	sqlPipeline, err := TranspilePlan(plan, PipelineOptions{
		Dialect: Postgres(),
		Schema:  &schema,
	})
	if err != nil {
		t.Fatalf("TranspilePlan failed: %v", err)
	}

	if sqlPipeline.SQL == "" {
		t.Fatal("expected non-empty SQL pipeline")
	}

	// Verify step projections exist in SQL pipeline and include all driver fields
	if _, ok := sqlPipeline.FinalEntityViews["driver"]; !ok {
		t.Fatal("missing final view for driver entity in SQL pipeline")
	}

	// 3. Construct certified SQL Candidate and evaluate Promotion Gate
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

	// Verify promotion.IsCertifiedBackend("sql") and "dbt"
	if !promotion.IsCertifiedBackend("sql") {
		t.Errorf("expected sql to be recognized as a certified backend")
	}
	if !promotion.IsCertifiedBackend("dbt") {
		t.Errorf("expected dbt to be recognized as a certified backend")
	}

	// Evaluate Promotion Gate Clause 9 on certified Candidate
	targetPolicy := ports.TargetPolicy{
		Version:           1,
		TenantID:          "tenant-1",
		CustomerID:        "cust-1",
		Target:            "prod",
		RequiredProfileID: "cm.v1",
	}

	candidate := promotion.Candidate{
		Plan:        plan,
		Checkpoint:  lastCheckpoint,
		ExecutionID: sqlRunBinding.ExecutionID(),
		Executor:    sqlExecutor,
	}

	decision := promotion.Evaluate(targetPolicy, candidate)
	var clause9Verdict promotion.Verdict
	for _, cr := range decision.Clauses() {
		if cr.Clause() == promotion.ClauseCertifiedBackend {
			clause9Verdict = cr.Verdict()
			break
		}
	}
	if clause9Verdict != promotion.Pass {
		t.Errorf("expected ClauseCertifiedBackend to pass for sql executor, got verdict=%v", clause9Verdict)
	}

	// Test uncertified backend is rejected by promotion gate
	uncertifiedExecutor, err := semantic.NewExecutorIdentity("custom-warehouse", "sha256:2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	if promotion.IsCertifiedBackend(uncertifiedExecutor.Backend()) {
		t.Errorf("custom-warehouse should NOT be a certified backend")
	}

	uncertifiedCandidate := promotion.Candidate{
		Plan:        plan,
		Checkpoint:  lastCheckpoint,
		ExecutionID: sqlRunBinding.ExecutionID(), // Using binding that doesn't match uncertified executor
		Executor:    uncertifiedExecutor,
	}

	uncertifiedDecision := promotion.Evaluate(targetPolicy, uncertifiedCandidate)
	var uncertClause9Verdict promotion.Verdict
	for _, cr := range uncertifiedDecision.Clauses() {
		if cr.Clause() == promotion.ClauseCertifiedBackend {
			uncertClause9Verdict = cr.Verdict()
			break
		}
	}
	if uncertClause9Verdict == promotion.Pass {
		t.Errorf("expected ClauseCertifiedBackend to fail for uncertified executor, got verdict=%v", uncertClause9Verdict)
	}
}

func TestSameKindMergeAndSplitTranspilation(t *testing.T) {
	ctx := TranspileContext{
		Dialect: Postgres(),
		EntityFields: map[string][]string{
			"trip": {"depot", "hours", "status"},
		},
	}

	// Same-kind merge: trip -> trip with RetainSources: false
	mergeDecl := semantic.MergeEntitiesDeclaration{
		Selector: semantic.Selector{
			Kind:    "trip",
			GroupBy: &semantic.Expr{Kind: semantic.ExprField, Field: "trip.depot"},
		},
		TargetKind:    "trip",
		Discriminator: intLit(1),
		Guard:         intLit(1),
		Assignments: []semantic.FieldAssignment{
			{Target: "trip.hours", Value: semantic.Expr{Kind: semantic.ExprSum, Field: "trip.hours"}},
		},
		RetainSources: false,
	}

	step, err := TranspileMergeEntities(ctx, 0, "merge_trips", mergeDecl, "stg_entities_trip", "stg_entities_trip", "stg_relations")
	if err != nil {
		t.Fatalf("TranspileMergeEntities: %v", err)
	}

	outTargetCTE, ok := step.OutputTables["trip"]
	if !ok {
		t.Fatal("missing trip in output tables for same-kind merge")
	}

	var foundOutputCTE bool
	for _, cte := range step.CTEs {
		if cte.Name == outTargetCTE {
			foundOutputCTE = true
			if !strings.Contains(cte.Query, "NOT IN") {
				t.Errorf("expected NOT IN filter in same-kind merge query: %s", cte.Query)
			}
			if !strings.Contains(cte.Query, "UNION ALL") {
				t.Errorf("expected UNION ALL in same-kind merge query: %s", cte.Query)
			}
		}
	}
	if !foundOutputCTE {
		t.Fatalf("did not find output CTE %s in generated CTEs", outTargetCTE)
	}
}

func TestRelationGuardsWithEntityKindQualifiers(t *testing.T) {
	ctx := TranspileContext{
		Dialect: Postgres(),
	}

	relDecl := semantic.RelateEntitiesDeclaration{
		RelationKind: "assigned_to",
		FromSelector: semantic.Selector{Kind: "driver"},
		ToSelector:   semantic.Selector{Kind: "truck"},
		Guard: semantic.Expr{
			Kind: semantic.ExprEqual,
			Args: []semantic.Expr{
				fieldExpr("driver.depot"),
				fieldExpr("truck.depot"),
			},
		},
	}

	step, err := TranspileRelateEntities(ctx, 0, "assign_trucks", relDecl, "stg_entities_driver", "stg_entities_truck", "stg_relations")
	if err != nil {
		t.Fatalf("TranspileRelateEntities: %v", err)
	}

	var foundCandidateCTE bool
	for _, cte := range step.CTEs {
		if strings.Contains(cte.Name, "candidates") {
			foundCandidateCTE = true
			if !strings.Contains(cte.Query, `f."depot" = t."depot"`) {
				t.Errorf("expected f.depot = t.depot in candidate query, got:\n%s", cte.Query)
			}
		}
	}
	if !foundCandidateCTE {
		t.Fatal("missing candidates CTE")
	}
}
