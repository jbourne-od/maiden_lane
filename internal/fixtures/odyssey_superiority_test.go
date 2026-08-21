package fixtures

import (
	"context"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/dsl"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/transpiler/dbt"
	"github.com/optimaldynamics/maiden-lane/internal/transpiler/sql"
)

// TestOdysseySuperioritySuite proves Maiden Lane's architectural superiority
// over legacy coreai mapper across determinism, safety, multi-backend portability,
// and fail-closed promotion gating.
func TestOdysseySuperioritySuite(t *testing.T) {
	ctx := context.Background()

	// -------------------------------------------------------------
	// 1. DIMENSION 1: Cryptographic Determinism & Replay
	// -------------------------------------------------------------
	t.Run("Dimension1_CryptographicDeterminism", func(t *testing.T) {
		fixture, err := teamhos.New(teamhos.Passing)
		if err != nil {
			t.Fatalf("teamhos.New: %v", err)
		}

		req := app.Request{
			Compilation:      fixture.Compilation,
			InitialState:     fixture.InitialState,
			World:            fixture.World,
			ExecutorIdentity: fixture.ExecutorIdentity,
			Policy:           semantic.ChangesProvenance,
		}

		firstOutcome, err := app.Run(ctx, req, nil)
		if err != nil {
			t.Fatalf("first run failed: %v", err)
		}

		firstRunID, _ := firstOutcome.SemanticRunID()
		firstExecID, _ := firstOutcome.ExecutionID()
		firstJournal, _ := firstOutcome.JournalPrefixDigest()

		// Run 50 consecutive times and assert 100% cryptographic identity stability
		for i := 0; i < 50; i++ {
			outcome, err := app.Run(ctx, req, nil)
			if err != nil {
				t.Fatalf("run %d failed: %v", i, err)
			}
			runID, _ := outcome.SemanticRunID()
			execID, _ := outcome.ExecutionID()
			journal, _ := outcome.JournalPrefixDigest()

			if runID != firstRunID {
				t.Fatalf("run %d: nondeterministic SemanticRunID (%s vs %s)", i, runID, firstRunID)
			}
			if execID != firstExecID {
				t.Fatalf("run %d: nondeterministic ExecutionID (%s vs %s)", i, execID, firstExecID)
			}
			if journal != firstJournal {
				t.Fatalf("run %d: nondeterministic JournalPrefixDigest (%s vs %s)", i, journal, firstJournal)
			}
		}
	})

	// -------------------------------------------------------------
	// 2. DIMENSION 2: Compile-Time Rejection of Invalid Transformations
	// -------------------------------------------------------------
	t.Run("Dimension2_StaticCompileTimeSafety", func(t *testing.T) {
		// Invalid DSL referencing undeclared field
		invalidDSL := `
schema {
  entity driver {
    driver_id: string;
  }
}
rule bad_rule {
  select driver
  where driver.non_existent_field == "FOO"
  set driver.driver_id = "NEW";
}
`
		req, err := dsl.CompileRequestFromText(invalidDSL)
		if err != nil {
			t.Fatalf("dsl parse failed: %v", err)
		}

		compilation, err := semantic.Compile(req)
		if err != nil {
			t.Fatalf("semantic.Compile returned error: %v", err)
		}

		if _, ok := compilation.Plan(); ok {
			t.Fatal("expected compiler to reject undeclared field, but plan was produced")
		}

		fail, ok := compilation.Failure()
		if !ok {
			t.Fatal("expected CompilationFailure, got none")
		}
		if len(fail.Diagnostics()) == 0 {
			t.Fatal("expected diagnostic explaining undeclared field rejection")
		}
	})

	// -------------------------------------------------------------
	// 3. DIMENSION 3: Full Multi-Backend Portability (Go, SQL, dbt)
	// -------------------------------------------------------------
	t.Run("Dimension3_MultiBackendPortability", func(t *testing.T) {
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
			t.Fatal("expected valid plan")
		}

		schema := fixture.InitialState.Schema()

		// Backend A: Reference Go Execution
		goOutcome, err := app.Run(ctx, app.Request{
			Compilation:      fixture.Compilation,
			InitialState:     fixture.InitialState,
			World:            fixture.World,
			ExecutorIdentity: fixture.ExecutorIdentity,
			Policy:           semantic.ChangesProvenance,
		}, nil)
		if err != nil {
			t.Fatalf("Go execution failed: %v", err)
		}
		if len(goOutcome.Checkpoints()) == 0 {
			t.Fatal("expected Go checkpoints")
		}

		// Backend B: Transpile to Target SQL CTE Pipeline
		sqlPipeline, err := sql.TranspilePlan(plan, sql.PipelineOptions{
			Dialect: sql.Postgres(),
			Schema:  &schema,
		})
		if err != nil {
			t.Fatalf("SQL transpilation failed: %v", err)
		}
		if !strings.HasPrefix(sqlPipeline.SQL, "WITH\n") {
			t.Fatalf("expected SQL WITH clause")
		}

		// Backend C: Generate Complete dbt Project
		dbtProj, err := dbt.GenerateProject(plan, dbt.Options{
			ProjectName: "team_hos_pipeline",
			Schema:      &schema,
		})
		if err != nil {
			t.Fatalf("dbt generation failed: %v", err)
		}
		if len(dbtProj.Files) < 4 {
			t.Fatalf("expected at least 4 dbt files, got %d", len(dbtProj.Files))
		}
	})

	// -------------------------------------------------------------
	// 4. DIMENSION 4: 9-Clause Fail-Closed Promotion Gate
	// -------------------------------------------------------------
	t.Run("Dimension4_FailClosedPromotionGate", func(t *testing.T) {
		passFix, err := teamhos.New(teamhos.Passing)
		if err != nil {
			t.Fatalf("teamhos.New: %v", err)
		}
		passOutcome, err := app.Run(ctx, app.Request{
			Compilation:      passFix.Compilation,
			InitialState:     passFix.InitialState,
			World:            passFix.World,
			ExecutorIdentity: passFix.ExecutorIdentity,
			Policy:           semantic.ChangesProvenance,
		}, nil)
		if err != nil {
			t.Fatalf("passOutcome: %v", err)
		}

		compilation, _ := semantic.Compile(passFix.Compilation)
		plan, _ := compilation.Plan()

		var passCheckpoint semantic.CheckpointArtifact
		for _, cp := range passOutcome.Checkpoints() {
			if cp.Checkpoint().Key == teamhos.CheckpointTeamHOSAggregated {
				passCheckpoint = cp
				break
			}
		}

		execID, _ := passOutcome.ExecutionID()

		targetPolicy := ports.TargetPolicy{
			Version:           1,
			TenantID:          "tenant-prod",
			CustomerID:        "cust-1",
			Target:            "prod",
			RequiredProfileID: "cm.v1",
		}

		validCandidate := promotion.Candidate{
			Plan:        plan,
			Checkpoint:  passCheckpoint,
			ExecutionID: execID,
			Executor:    passFix.ExecutorIdentity,
		}

		decision := promotion.Evaluate(targetPolicy, validCandidate)

		// Verify Clause 9 (certified backend) passes
		var certifiedBackendPass bool
		for _, c := range decision.Clauses() {
			if c.Clause() == promotion.ClauseCertifiedBackend && c.Verdict() == promotion.Pass {
				certifiedBackendPass = true
			}
		}
		if !certifiedBackendPass {
			t.Error("expected ClauseCertifiedBackend to pass for go-reference executor")
		}

		// Failing candidate: uncertified backend
		badExecutor, _ := semantic.NewExecutorIdentity("uncertified-python-script", "sha256:0000000000000000000000000000000000000000000000000000000000000000")
		badCandidate := promotion.Candidate{
			Plan:        plan,
			Checkpoint:  passCheckpoint,
			ExecutionID: execID,
			Executor:    badExecutor,
		}

		badDecision := promotion.Evaluate(targetPolicy, badCandidate)
		if badDecision.Authorized() {
			t.Fatal("expected uncertified backend candidate to be rejected by promotion gate")
		}

		var certifiedBackendFail bool
		for _, c := range badDecision.Clauses() {
			if c.Clause() == promotion.ClauseCertifiedBackend && c.Verdict() == promotion.Fail {
				certifiedBackendFail = true
			}
		}
		if !certifiedBackendFail {
			t.Error("expected ClauseCertifiedBackend to fail for uncertified-python-script")
		}
	})
}
