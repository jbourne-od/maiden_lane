// Package teamhos supplies the ratified sanitized team-HOS golden incident
// fixture as plain typed data for semantic, application, and observability
// tests. Every literal is fixed by the ratified walking-skeleton design
// (docs/superpowers/specs/2026-08-13-progressive-semantic-spine-design.md);
// the package declares data only and implements no transformer, patch
// executor, readiness evaluator, or canonicalizer.
//
// Non-production caveat: aggregate_team_hos.v1 declares componentwise
// int64-maximum reductions over the driver HOS elapsed and driving hours.
// That max reduction is only a deterministic, symmetric, hand-calculable
// reconciliation envelope for this sanitized golden incident. It is not
// production team-HOS semantics and must not be promoted into a production
// rule without separate domain approval. Production binaries do not import
// this package.
package teamhos

import (
	"fmt"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Variant selects one of the two ratified golden lifecycle variants.
type Variant uint8

const (
	// Passing is the golden variant whose T2 aggregates (T0, 10, 8).
	Passing Variant = iota + 1
	// AnchorMismatch differs from Passing only in driver B's hos_anchor and
	// deterministically rejects T2 with SELECTION_GUARD_UNSATISFIED.
	AnchorMismatch
)

// Stable fixture identifiers for the ratified rule, checkpoint, and profile
// kinds. Later application and observability tests use these as their
// bounded operational vocabulary.
const (
	RuleFormTeam         semantic.RuleID = "form_team.v1"
	RuleAggregateTeamHOS semantic.RuleID = "aggregate_team_hos.v1"

	CheckpointTeamFormed        semantic.CheckpointKey = "team_formed.v1"
	CheckpointTeamHOSAggregated semantic.CheckpointKey = "team_hos_aggregated.v1"

	// CheckpointTeamHOSRevised is the same checkpoint under a revised key, which is what
	// ComparisonPlans renames to. It is declared here rather than inline so a reader
	// finds both keys in one place.
	CheckpointTeamHOSRevised semantic.CheckpointKey = "team_hos_aggregated.v2"

	ProfileCM        semantic.ProfileKey = "cm.v1"
	ProfileOptimizer semantic.ProfileKey = "optimizer.v1"
)

// Ratified sanitized fixture literals. The lineage descriptor and source keys
// come from design section 4.2; the driver observations from sections 10.1
// and 10.2. Labels A and B are sanitized source keys inside the fixture, not
// customer identifiers.
const (
	lineageNamespace = "maiden-lane.sanitized-fixture"
	lineageRootKey   = "team-hos-team-ab"

	driverSourceKeyA = "A"
	driverSourceKeyB = "B"

	commonAssignmentKey = "X"

	passingAnchor  = "T0"
	mismatchAnchor = "T1"

	driverAElapsedHours int64 = 10
	driverADrivingHours int64 = 8
	driverBElapsedHours int64 = 7
	driverBDrivingHours int64 = 6

	compilerSemanticsVersion semantic.CompilerSemanticsVersion = "maiden-lane.compiler-semantics.v1"

	// The fixture executor is the design's sanitized `go` backend with a
	// fixed literal version digest; it is a technical identity for tests,
	// never semantic content.
	executorBackend                 = "go"
	executorVersion semantic.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// Inputs carries one complete, independently owned set of semantic inputs
// for one ratified variant.
type Inputs struct {
	Compilation      semantic.CompileRequest
	InitialState     semantic.State
	World            semantic.World
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

// New returns a fresh Inputs value for one ratified variant. Every call
// rebuilds all declarations and state from the ratified literals, so
// mutating one returned value can never affect another call's result.
func New(variant Variant) (Inputs, error) {
	if variant != Passing && variant != AnchorMismatch {
		return Inputs{}, fmt.Errorf("teamhos: unknown fixture variant %d", variant)
	}
	schema, err := newSchema()
	if err != nil {
		return Inputs{}, fmt.Errorf("teamhos: schema: %w", err)
	}
	state, err := newInitialState(schema, variant)
	if err != nil {
		return Inputs{}, fmt.Errorf("teamhos: initial state: %w", err)
	}
	world, err := semantic.NewWorld(nil)
	if err != nil {
		return Inputs{}, fmt.Errorf("teamhos: empty world: %w", err)
	}
	executor, err := semantic.NewExecutorIdentity(executorBackend, executorVersion)
	if err != nil {
		return Inputs{}, fmt.Errorf("teamhos: executor identity: %w", err)
	}
	return Inputs{
		Compilation:      newCompileRequest(schema),
		InitialState:     state,
		World:            world,
		ExecutorIdentity: executor,
		Policy:           semantic.ChangesProvenance,
	}, nil
}

// newSchema declares the ratified driver fields.
func newSchema() (semantic.Schema, error) {
	return semantic.NewSchema(
		[]semantic.EntityDeclaration{
			{Kind: "driver", Fields: []semantic.FieldDeclaration{
				{Name: "assignment_key", Kind: semantic.ValueString},
				{Name: "hos_anchor", Kind: semantic.ValueAtom},
				{Name: "hos_elapsed_hours", Kind: semantic.ValueInt64},
				{Name: "hos_driving_hours", Kind: semantic.ValueInt64},
				{Name: "assignment_status", Kind: semantic.ValueString},
				{Name: "reconciled_anchor", Kind: semantic.ValueAtom},
				{Name: "elapsed_duration_hours", Kind: semantic.ValueInt64},
				{Name: "driving_duration_hours", Kind: semantic.ValueInt64},
			}},
		},
		nil,
	)
}

// newInitialState builds S0: exactly the two source drivers.
func newInitialState(schema semantic.Schema, variant Variant) (semantic.State, error) {
	lineage, err := semantic.NewInputLineageID(lineageNamespace, lineageRootKey)
	if err != nil {
		return semantic.State{}, err
	}
	assignment, err := semantic.NewStringValue(commonAssignmentKey)
	if err != nil {
		return semantic.State{}, err
	}
	anchorA, err := semantic.NewAtomValue(passingAnchor)
	if err != nil {
		return semantic.State{}, err
	}
	anchorBToken := passingAnchor
	if variant == AnchorMismatch {
		anchorBToken = mismatchAnchor
	}
	anchorB, err := semantic.NewAtomValue(anchorBToken)
	if err != nil {
		return semantic.State{}, err
	}
	driverA, err := semantic.NewEntity(
		semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", driverSourceKeyA)},
		map[semantic.FieldName]semantic.Value{
			"assignment_key":    assignment,
			"hos_anchor":        anchorA,
			"hos_elapsed_hours": semantic.NewInt64Value(driverAElapsedHours),
			"hos_driving_hours": semantic.NewInt64Value(driverADrivingHours),
		},
	)
	if err != nil {
		return semantic.State{}, err
	}
	driverB, err := semantic.NewEntity(
		semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", driverSourceKeyB)},
		map[semantic.FieldName]semantic.Value{
			"assignment_key":    assignment,
			"hos_anchor":        anchorB,
			"hos_elapsed_hours": semantic.NewInt64Value(driverBElapsedHours),
			"hos_driving_hours": semantic.NewInt64Value(driverBDrivingHours),
		},
	)
	if err != nil {
		return semantic.State{}, err
	}
	return semantic.NewState(schema, lineage, []semantic.Entity{driverA, driverB}, nil)
}

// ComparisonPlans compiles two ratified plans that differ only by a renamed checkpoint.
func ComparisonPlans() (baseline, candidate semantic.Plan, err error) {
	schema, err := newSchema()
	if err != nil {
		return semantic.Plan{}, semantic.Plan{}, fmt.Errorf("teamhos: schema: %w", err)
	}
	baseline, err = compilePlan(newCompileRequest(schema))
	if err != nil {
		return semantic.Plan{}, semantic.Plan{}, fmt.Errorf("teamhos: baseline plan: %w", err)
	}

	renamedRequest := newCompileRequest(schema)
	checkpoints := slices.Clone(renamedRequest.Rules.Checkpoints)
	renamed := false
	for i := range checkpoints {
		if checkpoints[i].Key == CheckpointTeamHOSAggregated {
			checkpoints[i].Key = CheckpointTeamHOSRevised
			renamed = true
		}
	}
	if !renamed {
		return semantic.Plan{}, semantic.Plan{},
			fmt.Errorf("teamhos: the ruleset no longer declares %s", CheckpointTeamHOSAggregated)
	}
	renamedRequest.Rules.Checkpoints = checkpoints
	candidate, err = compilePlan(renamedRequest)
	if err != nil {
		return semantic.Plan{}, semantic.Plan{}, fmt.Errorf("teamhos: candidate plan: %w", err)
	}

	if baseline.ID() == candidate.ID() {
		return semantic.Plan{}, semantic.Plan{},
			fmt.Errorf("teamhos: the two comparison plans are identical")
	}
	return baseline, candidate, nil
}

// compilePlan compiles one request and refuses anything that is not a plan.
func compilePlan(request semantic.CompileRequest) (semantic.Plan, error) {
	compilation, err := semantic.Compile(request)
	if err != nil {
		return semantic.Plan{}, err
	}
	if failure, refused := compilation.Failure(); refused {
		return semantic.Plan{}, fmt.Errorf("did not compile: %v", failure.Diagnostics())
	}
	plan, ok := compilation.Plan()
	if !ok {
		return semantic.Plan{}, fmt.Errorf("compilation produced neither plan nor failure")
	}
	return plan, nil
}

func exprFieldPtr(path semantic.FieldPath) *semantic.Expr {
	return &semantic.Expr{Kind: semantic.ExprField, Field: path}
}

func stringExpr(s string) semantic.Expr {
	v, _ := semantic.NewStringValue(s)
	return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &v}
}

func int64Expr(n int64) semantic.Expr {
	v := semantic.NewInt64Value(n)
	return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &v}
}

// newCompileRequest instantiates the two closed ratified transformations
// using SelectAndAssign.
func newCompileRequest(schema semantic.Schema) semantic.CompileRequest {
	form := semantic.TransformationDeclaration{
		ID:             RuleFormTeam,
		Operator:       semantic.OperatorSelectAndAssign,
		DeclaredReads:  []semantic.FieldPath{"driver.assignment_key"},
		DeclaredWrites: []semantic.FieldPath{"driver.assignment_status"},
		SelectAssign: &semantic.SelectAssignDeclaration{
			Selector: semantic.Selector{
				Kind:    "driver",
				GroupBy: exprFieldPtr("driver.assignment_key"),
				Members: semantic.Cardinality{Kind: semantic.CardinalityExactly, Count: 2},
			},
			Guard: semantic.Expr{Kind: semantic.ExprAllEqual, Field: "driver.assignment_key"},
			Assignments: []semantic.FieldAssignment{
				{Target: "driver.assignment_status", Value: stringExpr("assigned")},
			},
		},
	}
	aggregate := semantic.TransformationDeclaration{
		ID:       RuleAggregateTeamHOS,
		Operator: semantic.OperatorSelectAndAssign,
		DeclaredReads: []semantic.FieldPath{
			"driver.assignment_key",
			"driver.hos_anchor",
			"driver.hos_driving_hours",
			"driver.hos_elapsed_hours",
		},
		DeclaredWrites: []semantic.FieldPath{
			"driver.driving_duration_hours",
			"driver.elapsed_duration_hours",
			"driver.reconciled_anchor",
		},
		After: []semantic.RuleID{RuleFormTeam},
		SelectAssign: &semantic.SelectAssignDeclaration{
			Selector: semantic.Selector{
				Kind:    "driver",
				GroupBy: exprFieldPtr("driver.assignment_key"),
				Members: semantic.Cardinality{Kind: semantic.CardinalityExactly, Count: 2},
			},
			Guard: semantic.Expr{
				Kind: semantic.ExprAll,
				Args: []semantic.Expr{
					{Kind: semantic.ExprAllEqual, Field: "driver.hos_anchor"},
					{
						Kind: semantic.ExprAllMembers,
						Args: []semantic.Expr{
							{
								Kind: semantic.ExprNot,
								Args: []semantic.Expr{
									{
										Kind: semantic.ExprLess,
										Args: []semantic.Expr{
											{Kind: semantic.ExprField, Field: "driver.hos_elapsed_hours"},
											int64Expr(0),
										},
									},
								},
							},
						},
					},
					{
						Kind: semantic.ExprAllMembers,
						Args: []semantic.Expr{
							{
								Kind: semantic.ExprNot,
								Args: []semantic.Expr{
									{
										Kind: semantic.ExprLess,
										Args: []semantic.Expr{
											{Kind: semantic.ExprField, Field: "driver.hos_driving_hours"},
											int64Expr(0),
										},
									},
								},
							},
						},
					},
					{
						Kind: semantic.ExprAllMembers,
						Args: []semantic.Expr{
							{
								Kind: semantic.ExprAny,
								Args: []semantic.Expr{
									{
										Kind: semantic.ExprLess,
										Args: []semantic.Expr{
											{Kind: semantic.ExprField, Field: "driver.hos_driving_hours"},
											{Kind: semantic.ExprField, Field: "driver.hos_elapsed_hours"},
										},
									},
									{
										Kind: semantic.ExprEqual,
										Args: []semantic.Expr{
											{Kind: semantic.ExprField, Field: "driver.hos_driving_hours"},
											{Kind: semantic.ExprField, Field: "driver.hos_elapsed_hours"},
										},
									},
								},
							},
						},
					},
				},
			},
			Assignments: []semantic.FieldAssignment{
				{Target: "driver.reconciled_anchor", Value: semantic.Expr{Kind: semantic.ExprField, Field: "driver.hos_anchor"}},
				{Target: "driver.elapsed_duration_hours", Value: semantic.Expr{Kind: semantic.ExprMax, Field: "driver.hos_elapsed_hours"}},
				{Target: "driver.driving_duration_hours", Value: semantic.Expr{Kind: semantic.ExprMax, Field: "driver.hos_driving_hours"}},
			},
		},
	}
	return semantic.CompileRequest{
		Schema: schema.Declaration(),
		Rules: semantic.RulesetDeclaration{
			Transformations: []semantic.TransformationDeclaration{form, aggregate},
			Checkpoints: []semantic.CheckpointDeclaration{
				{Key: CheckpointTeamFormed, After: RuleFormTeam},
				{Key: CheckpointTeamHOSAggregated, After: RuleAggregateTeamHOS},
			},
		},
		Profiles:                 newProfileDeclarations(),
		CompilerSemanticsVersion: compilerSemanticsVersion,
	}
}

const (
	DriverAssignmentRequired        semantic.RequirementCode = "driver_assignment_required"
	DriverAggregationAnchorRequired semantic.RequirementCode = "driver_aggregation_anchor_required"
	DriverElapsedDurationRequired   semantic.RequirementCode = "driver_elapsed_duration_required"
	DriverDrivingDurationRequired   semantic.RequirementCode = "driver_driving_duration_required"
)

// newProfileDeclarations declares the ratified CM and optimizer completeness profiles.
func newProfileDeclarations() []semantic.ProfileDeclaration {
	scope := semantic.ProfileScope{Kind: semantic.AllEntitiesOfKind, EntityKind: "driver"}
	return []semantic.ProfileDeclaration{
		{
			Key:         ProfileCM,
			Scope:       scope,
			Aggregation: semantic.AllSelected,
			Requirements: []semantic.RequirementAtom{
				{Code: DriverAssignmentRequired, Kind: semantic.FieldPresent, Field: "driver.assignment_status"},
			},
		},
		{
			Key:         ProfileOptimizer,
			Scope:       scope,
			Aggregation: semantic.AllSelected,
			Requirements: []semantic.RequirementAtom{
				{Code: DriverAssignmentRequired, Kind: semantic.FieldPresent, Field: "driver.assignment_status"},
				{Code: DriverAggregationAnchorRequired, Kind: semantic.FieldPresent, Field: "driver.reconciled_anchor"},
				{Code: DriverElapsedDurationRequired, Kind: semantic.FieldPresent, Field: "driver.elapsed_duration_hours"},
				{Code: DriverDrivingDurationRequired, Kind: semantic.FieldPresent, Field: "driver.driving_duration_hours"},
			},
			Implies: []semantic.ProfileKey{ProfileCM},
		},
	}
}
