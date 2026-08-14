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

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Variant selects one of the two ratified golden lifecycle variants.
type Variant uint8

const (
	// Passing is the golden variant whose T2 aggregates (T0, 10, 8).
	Passing Variant = iota + 1
	// AnchorMismatch differs from Passing only in driver B's hos_anchor and
	// deterministically rejects T2 with HOS_ANCHOR_MISMATCH.
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

// newSchema declares the ratified driver/team fields (design section 4.1)
// and the directed team --member--> driver relation. Field presence beyond
// construction is a rule-boundary obligation, so no field is required at
// construction: driver HOS observations are optional inputs and the team
// aggregate tuple is wholly absent at C1.
func newSchema() (semantic.Schema, error) {
	return semantic.NewSchema(
		[]semantic.EntityDeclaration{
			{Kind: "driver", Fields: []semantic.FieldDeclaration{
				{Name: "assignment_key", Kind: semantic.ValueString},
				{Name: "hos_anchor", Kind: semantic.ValueAtom},
				{Name: "hos_elapsed_hours", Kind: semantic.ValueInt64},
				{Name: "hos_driving_hours", Kind: semantic.ValueInt64},
			}},
			{Kind: "team", Fields: []semantic.FieldDeclaration{
				{Name: "assignment_key", Kind: semantic.ValueString},
				{Name: "aggregation_anchor", Kind: semantic.ValueAtom},
				{Name: "elapsed_duration_hours", Kind: semantic.ValueInt64},
				{Name: "driving_duration_hours", Kind: semantic.ValueInt64},
			}},
		},
		[]semantic.RelationDeclaration{
			{Kind: "member", FromKind: "team", ToKind: "driver"},
		},
	)
}

// newInitialState builds S0: exactly the two source drivers, no team, and no
// relations (design section 4.2). Both variants share the pinned lineage and
// source identities; only driver B's hos_anchor observation differs.
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

// newCompileRequest instantiates the two closed ratified transformations
// (design sections 3.3, 5, and 6), the two checkpoint declarations, and the
// CM/optimizer profile declarations with the declared cm.v1 implication
// (design section 7.4).
func newCompileRequest(schema semantic.Schema) semantic.CompileRequest {
	form := semantic.TransformationDeclaration{
		ID:             RuleFormTeam,
		Operator:       semantic.OperatorFormRelatedEntity,
		DeclaredReads:  []semantic.FieldPath{"driver.assignment_key"},
		DeclaredWrites: []semantic.FieldPath{"team.assignment_key"},
		Form: &semantic.FormRelatedEntityDeclaration{
			SourceKind: "driver",
			Sources: []semantic.SourceReference{
				{Kind: "driver", CanonicalSourceKey: driverSourceKeyA},
				{Kind: "driver", CanonicalSourceKey: driverSourceKeyB},
			},
			OutputKind:    "team",
			OutputSlot:    "team",
			GroupingField: "driver.assignment_key",
			SourceCount:   2,
			CopiedFields: []semantic.FieldCopy{
				{Source: "driver.assignment_key", Destination: "team.assignment_key"},
			},
			RelationKind: "member",
			OutputKey:    &semantic.OutputKeyExpression{Kind: semantic.OutputKeyCommonSourceField, Field: "driver.assignment_key"},
		},
	}
	aggregate := semantic.TransformationDeclaration{
		ID:       RuleAggregateTeamHOS,
		Operator: semantic.OperatorAggregateRelatedFields,
		DeclaredReads: []semantic.FieldPath{
			"driver.hos_anchor", "driver.hos_driving_hours", "driver.hos_elapsed_hours",
			"team.aggregation_anchor", "team.driving_duration_hours", "team.elapsed_duration_hours",
		},
		DeclaredWrites: []semantic.FieldPath{
			"team.aggregation_anchor", "team.driving_duration_hours", "team.elapsed_duration_hours",
		},
		Aggregate: &semantic.AggregateRelatedFieldsDeclaration{
			Target:       semantic.OutputSlotReference{Rule: RuleFormTeam, Slot: "team"},
			RelationKind: "member",
			SourceKind:   "driver",
			RequiredSourceTuple: []semantic.FieldPath{
				"driver.hos_anchor", "driver.hos_elapsed_hours", "driver.hos_driving_hours",
			},
			Predicates: []semantic.AggregatePredicate{
				{Kind: semantic.CompleteTuple, Fields: []semantic.FieldPath{"driver.hos_anchor", "driver.hos_elapsed_hours", "driver.hos_driving_hours"}},
				{Kind: semantic.NonNegativeInt, Fields: []semantic.FieldPath{"driver.hos_elapsed_hours"}},
				{Kind: semantic.NonNegativeInt, Fields: []semantic.FieldPath{"driver.hos_driving_hours"}},
				{Kind: semantic.EqualFieldAcrossSources, Fields: []semantic.FieldPath{"driver.hos_anchor"}},
				{Kind: semantic.LessOrEqualFields, Fields: []semantic.FieldPath{"driver.hos_driving_hours", "driver.hos_elapsed_hours"}},
			},
			Anchor: semantic.FieldCopy{Source: "driver.hos_anchor", Destination: "team.aggregation_anchor"},
			// Componentwise maxima: a fixture-only reconciliation envelope.
			// See the package documentation's non-production caveat.
			Reductions: []semantic.FieldReduction{
				{Kind: semantic.ReduceInt64Max, Source: "driver.hos_elapsed_hours", Destination: "team.elapsed_duration_hours"},
				{Kind: semantic.ReduceInt64Max, Source: "driver.hos_driving_hours", Destination: "team.driving_duration_hours"},
			},
			ResultPredicates: []semantic.AggregatePredicate{
				{Kind: semantic.CompleteTuple, Fields: []semantic.FieldPath{"team.aggregation_anchor", "team.elapsed_duration_hours", "team.driving_duration_hours"}},
				{Kind: semantic.NonNegativeInt, Fields: []semantic.FieldPath{"team.elapsed_duration_hours"}},
				{Kind: semantic.NonNegativeInt, Fields: []semantic.FieldPath{"team.driving_duration_hours"}},
				{Kind: semantic.LessOrEqualFields, Fields: []semantic.FieldPath{"team.driving_duration_hours", "team.elapsed_duration_hours"}},
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

// newProfileDeclarations declares the ratified CM and optimizer completeness
// profiles (design sections 7.1 through 7.4): identical explicit team scope,
// universal aggregation, field-presence atoms, and the declared
// cm.v1 <= optimizer.v1 ordering claim the compiler must prove.
func newProfileDeclarations() []semantic.ProfileDeclaration {
	scope := semantic.ProfileScope{Kind: semantic.AllEntitiesOfKind, EntityKind: "team"}
	return []semantic.ProfileDeclaration{
		{
			Key:         ProfileCM,
			Scope:       scope,
			Aggregation: semantic.AllSelected,
			Requirements: []semantic.RequirementAtom{
				{Code: semantic.TeamAssignmentKeyRequired, Kind: semantic.FieldPresent, Field: "team.assignment_key"},
			},
		},
		{
			Key:         ProfileOptimizer,
			Scope:       scope,
			Aggregation: semantic.AllSelected,
			Requirements: []semantic.RequirementAtom{
				{Code: semantic.TeamAssignmentKeyRequired, Kind: semantic.FieldPresent, Field: "team.assignment_key"},
				{Code: semantic.TeamAggregationAnchorRequired, Kind: semantic.FieldPresent, Field: "team.aggregation_anchor"},
				{Code: semantic.TeamElapsedDurationRequired, Kind: semantic.FieldPresent, Field: "team.elapsed_duration_hours"},
				{Code: semantic.TeamDrivingDurationRequired, Kind: semantic.FieldPresent, Field: "team.driving_duration_hours"},
			},
			Implies: []semantic.ProfileKey{ProfileCM},
		},
	}
}
