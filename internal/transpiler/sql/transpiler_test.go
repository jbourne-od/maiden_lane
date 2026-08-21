package sql

import (
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestTranspileTeamHOSPlan(t *testing.T) {
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

	pipeline, err := TranspilePlan(plan, PipelineOptions{
		Dialect: Postgres(),
	})
	if err != nil {
		t.Fatalf("TranspilePlan failed: %v", err)
	}

	if pipeline.SQL == "" {
		t.Fatal("expected non-empty SQL output")
	}

	// Verify CTE structure
	if !strings.HasPrefix(pipeline.SQL, "WITH\n") {
		t.Errorf("expected SQL to start with WITH clause, got:\n%s", pipeline.SQL)
	}

	// Verify step CTEs exist
	if !strings.Contains(pipeline.SQL, "step_0_form_team_v1") {
		t.Errorf("expected step_0_form_team_v1 in SQL, got:\n%s", pipeline.SQL)
	}
	if !strings.Contains(pipeline.SQL, "step_1_aggregate_team_hos_v1") {
		t.Errorf("expected step_1_aggregate_team_hos_v1 in SQL, got:\n%s", pipeline.SQL)
	}

	// Verify checkpoints were captured
	if len(pipeline.CheckpointViews) == 0 {
		t.Errorf("expected checkpoint views, got 0")
	}
	if _, ok := pipeline.CheckpointViews[string(teamhos.CheckpointTeamFormed)]; !ok {
		t.Errorf("missing checkpoint view for %s", teamhos.CheckpointTeamFormed)
	}
	if _, ok := pipeline.CheckpointViews[string(teamhos.CheckpointTeamHOSAggregated)]; !ok {
		t.Errorf("missing checkpoint view for %s", teamhos.CheckpointTeamHOSAggregated)
	}
}

func TestTranspileStructuralOperators(t *testing.T) {
	d := Postgres()
	ctx := TranspileContext{Dialect: d}

	// 1. InsertEntity
	t.Run("InsertEntity", func(t *testing.T) {
		decl := semantic.InsertEntityDeclaration{
			Selector: semantic.Selector{
				Kind: "driver",
			},
			TargetKind:    "team",
			Discriminator: intLit(1),
			Guard:         intLit(1),
			Assignments: []semantic.FieldAssignment{
				{Target: "team.name", Value: strLit("Alpha")},
			},
		}
		step, err := TranspileInsertEntity(ctx, 0, "create_team", decl, "stg_entities_driver", "stg_entities_team")
		if err != nil {
			t.Fatalf("TranspileInsertEntity: %v", err)
		}
		if step.Operator != semantic.OperatorInsertEntity {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if len(step.CTEs) == 0 {
			t.Fatal("expected CTEs")
		}
		if step.OutputTables["team"] == "" {
			t.Fatal("expected output table for team")
		}
	})

	// 2. DeleteEntity
	t.Run("DeleteEntity", func(t *testing.T) {
		decl := semantic.DeleteEntityDeclaration{
			Selector: semantic.Selector{
				Kind: "driver",
			},
			Guard: intLit(1),
		}
		step, err := TranspileDeleteEntity(ctx, 1, "delete_driver", decl, "stg_entities_driver")
		if err != nil {
			t.Fatalf("TranspileDeleteEntity: %v", err)
		}
		if step.Operator != semantic.OperatorDeleteEntity {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if len(step.CTEs) == 0 {
			t.Fatal("expected CTEs")
		}
	})

	// 3. RelateEntities & UnrelateEntities
	t.Run("RelateEntities", func(t *testing.T) {
		decl := semantic.RelateEntitiesDeclaration{
			RelationKind: "assigned_to",
			FromSelector: semantic.Selector{Kind: "driver"},
			ToSelector:   semantic.Selector{Kind: "team"},
			Guard: semantic.Expr{
				Kind: semantic.ExprEqual,
				Args: []semantic.Expr{
					fieldExpr("from.depot"),
					fieldExpr("to.depot"),
				},
			},
		}
		step, err := TranspileRelateEntities(ctx, 2, "assign_driver", decl, "stg_entities_driver", "stg_entities_team", "stg_relations")
		if err != nil {
			t.Fatalf("TranspileRelateEntities: %v", err)
		}
		if step.Operator != semantic.OperatorRelateEntities {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if step.OutputTables["relations"] == "" {
			t.Fatal("expected relations output table")
		}
	})

	t.Run("UnrelateEntities", func(t *testing.T) {
		decl := semantic.UnrelateEntitiesDeclaration{
			RelationKind: "assigned_to",
			FromSelector: semantic.Selector{Kind: "driver"},
			ToSelector:   semantic.Selector{Kind: "team"},
			Guard:        intLit(1),
		}
		step, err := TranspileUnrelateEntities(ctx, 3, "unassign_driver", decl, "stg_entities_driver", "stg_entities_team", "stg_relations")
		if err != nil {
			t.Fatalf("TranspileUnrelateEntities: %v", err)
		}
		if step.Operator != semantic.OperatorUnrelateEntities {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if step.OutputTables["relations"] == "" {
			t.Fatal("expected relations output table")
		}
	})

	// 4. MergeEntities
	t.Run("MergeEntities", func(t *testing.T) {
		decl := semantic.MergeEntitiesDeclaration{
			Selector: semantic.Selector{
				Kind:    "driver",
				GroupBy: &semantic.Expr{Kind: semantic.ExprField, Field: "driver.depot"},
			},
			TargetKind:    "merged_team",
			Discriminator: intLit(1),
			Guard:         intLit(1),
			Assignments: []semantic.FieldAssignment{
				{Target: "merged_team.total_hours", Value: semantic.Expr{Kind: semantic.ExprSum, Field: "driver.hours"}},
			},
			RetainSources: false,
		}
		step, err := TranspileMergeEntities(ctx, 4, "merge_drivers", decl, "stg_entities_driver", "stg_entities_merged_team", "stg_relations")
		if err != nil {
			t.Fatalf("TranspileMergeEntities: %v", err)
		}
		if step.Operator != semantic.OperatorMergeEntities {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if step.OutputTables["merged_team"] == "" || step.OutputTables["driver"] == "" {
			t.Fatal("expected output tables for merged_team and driver")
		}
	})

	// 5. SplitEntity
	t.Run("SplitEntity", func(t *testing.T) {
		decl := semantic.SplitEntityDeclaration{
			Selector: semantic.Selector{
				Kind: "driver",
			},
			TargetKind: "split_driver",
			Guard:      intLit(1),
			Partitions: []semantic.PartitionDeclaration{
				{
					Discriminator: strLit("part_1"),
					Assignments: []semantic.FieldAssignment{
						{Target: "split_driver.part", Value: strLit("P1")},
					},
				},
				{
					Discriminator: strLit("part_2"),
					Assignments: []semantic.FieldAssignment{
						{Target: "split_driver.part", Value: strLit("P2")},
					},
				},
			},
			RetainSource: false,
		}
		step, err := TranspileSplitEntity(ctx, 5, "split_driver_rule", decl, "stg_entities_driver", "stg_entities_split_driver", "stg_relations")
		if err != nil {
			t.Fatalf("TranspileSplitEntity: %v", err)
		}
		if step.Operator != semantic.OperatorSplitEntity {
			t.Errorf("unexpected operator: %v", step.Operator)
		}
		if step.OutputTables["split_driver"] == "" || step.OutputTables["driver"] == "" {
			t.Fatal("expected output tables for split_driver and driver")
		}
	})
}
