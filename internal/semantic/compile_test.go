package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

const testCompilerVersion CompilerSemanticsVersion = "maiden-lane.compiler-semantics.v1"

// Production break caught: adding HOS reads to team formation or omitting an
// aggregate dependency would couple the valid C1 prefix to suffix-only facts.
func TestCompileDerivesExactTeamHOSAccess(t *testing.T) {
	result, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		t.Fatal("no plan")
	}
	form := plan.MustTransformation("form_team.v1")
	if form.Reads("driver.hos_anchor") || form.Reads("driver.hos_elapsed_hours") || form.Reads("driver.hos_driving_hours") {
		t.Fatalf("T1 reads suffix-only HOS: %v", form.ReadSet())
	}
	if !form.Reads("driver.assignment_key") {
		t.Fatalf("T1 does not read grouping field: %v", form.ReadSet())
	}
	aggregate := plan.MustTransformation("aggregate_team_hos.v1")
	for _, path := range []FieldPath{
		"driver.hos_anchor",
		"driver.hos_elapsed_hours",
		"driver.hos_driving_hours",
		"team.aggregation_anchor",
		"team.elapsed_duration_hours",
		"team.driving_duration_hours",
	} {
		if !aggregate.Reads(path) {
			t.Fatalf("T2 does not read %s; reads=%v", path, aggregate.ReadSet())
		}
	}
	if got := aggregate.Dependencies(); !slices.Equal(got, []RuleID{"form_team.v1"}) {
		t.Fatalf("T2 dependencies=%v", got)
	}
}

// Production break caught: broadening or ambiguously populating the certified
// operator union would admit meaning the compiler cannot statically analyze.
func TestCompileRejectsInvalidClosedDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompileRequest)
		code   CompilationDiagnosticCode
	}{
		{
			name: "unknown field",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[0].Form.GroupingField = "driver.unknown"
			},
			code: UnknownField,
		},
		{
			name: "unsupported operator tag",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[0].Operator = OperatorKind(99)
			},
			code: UnsupportedOperator,
		},
		{
			name: "multiple active union variants",
			mutate: func(req *CompileRequest) {
				aggregate := *req.Rules.Transformations[1].Aggregate
				req.Rules.Transformations[0].Aggregate = &aggregate
			},
			code: UnsupportedOperator,
		},
		{
			name: "missing typed output key",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[0].Form.OutputKey = nil
			},
			code: UnsupportedOperator,
		},
		{
			name: "declared derived access disagreement",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[0].DeclaredReads = []FieldPath{"driver.assignment_key", "driver.hos_anchor"}
			},
			code: DeclaredAccessMismatch,
		},
		{
			name: "unresolved output slot",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[1].Aggregate.Target = OutputSlotReference{Rule: "missing.v1", Slot: "team"}
			},
			code: UnsupportedOperator,
		},
		{
			name: "dependency cycle",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[0].After = []RuleID{"aggregate_team_hos.v1"}
			},
			code: DependencyCycle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := compileFixtureRequest(t, false)
			tt.mutate(&req)
			result, err := Compile(req)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			failure, ok := result.Failure()
			if !ok {
				t.Fatal("invalid declaration compiled")
			}
			if _, ok := result.Plan(); ok {
				t.Fatal("invalid compilation exposed a plan")
			}
			if profiles := result.Profiles(); profiles != nil {
				t.Fatalf("invalid compilation exposed profiles: %v", profiles)
			}
			if failure.Digest() == "" || result.InputDigest() == "" {
				t.Fatal("canonicalizable failure lacks compilation identities")
			}
			if got := failure.Diagnostics(); len(got) == 0 || got[0].Code() != tt.code {
				t.Fatalf("diagnostics=%v, want first code %s", got, tt.code)
			}
		})
	}
}

// Production break caught: collecting diagnostics in authored traversal order
// would make an invalid request's canonical failure identity nondeterministic.
func TestCompileOrdersDiagnosticsByClosedCodeRank(t *testing.T) {
	req := compileFixtureRequest(t, false)
	left := cloneTransformation(req.Rules.Transformations[0])
	left.ID = "conflict_left.v1"
	left.Form.OutputSlot = "left"
	right := cloneTransformation(req.Rules.Transformations[0])
	right.ID = "conflict_right.v1"
	right.Form.OutputSlot = "right"
	req.Rules.Transformations = append(req.Rules.Transformations, left, right)
	req.Rules.Transformations[1].Aggregate.RequiredSourceTuple = []FieldPath{"driver.unknown"}
	req.Rules.Transformations[0].Operator = OperatorKind(99)
	req.Rules.Transformations[0].After = []RuleID{"aggregate_team_hos.v1"}
	req.Profiles[1].Implies = []ProfileKey{"missing.v1"}

	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	failure, ok := result.Failure()
	if !ok {
		t.Fatal("invalid request compiled")
	}
	want := []CompilationDiagnosticCode{UnknownField, UnsupportedOperator, DeclaredAccessMismatch, WriteConflictUnresolved, DependencyCycle, ProfileOrderUnprovable}
	got := make([]CompilationDiagnosticCode, 0, len(failure.Diagnostics()))
	for _, diagnostic := range failure.Diagnostics() {
		if len(got) == 0 || got[len(got)-1] != diagnostic.Code() {
			got = append(got, diagnostic.Code())
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostic codes=%v, want %v", got, want)
	}
}

// Production break caught: returning mutable plan/profile slices would permit
// caller-owned declarations or getter results to change accepted identities.
func TestCompileDefensivelyCopiesInputsAndOutputs(t *testing.T) {
	req := compileFixtureRequest(t, false)
	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		t.Fatal("no plan")
	}
	planBytes := plan.CanonicalBytes()
	profiles := result.Profiles()
	profileBytes := profiles[0].CanonicalBytes()

	req.Rules.Transformations[0].DeclaredReads[0] = "driver.hos_anchor"
	req.Profiles[0].Requirements[0].Field = "team.aggregation_anchor"
	transformations := plan.Transformations()
	transformations[0] = CompiledTransformation{}
	profiles[0] = CompiledProfile{}
	plan.CanonicalBytes()[0] ^= 0xff

	if !bytes.Equal(plan.CanonicalBytes(), planBytes) {
		t.Fatal("plan changed through caller-owned memory")
	}
	gotProfiles := result.Profiles()
	if !bytes.Equal(gotProfiles[0].CanonicalBytes(), profileBytes) {
		t.Fatal("profile changed through caller-owned memory")
	}
}

// Production break caught: resolving an unknown checkpoint boundary as prefix
// zero would permit a declaration that cannot identify a real plan state.
func TestCompileRejectsCheckpointAtUnknownBoundary(t *testing.T) {
	req := compileFixtureRequest(t, false)
	req.Rules.Checkpoints[0].After = "missing.v1"

	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	failure, ok := result.Failure()
	if !ok {
		t.Fatal("checkpoint at unknown boundary compiled")
	}
	if got := failure.Diagnostics()[0].Code(); got != UnsupportedOperator {
		t.Fatalf("diagnostic=%s, want %s", got, UnsupportedOperator)
	}
}

// Production break caught: validating only operator-derived paths would let an
// unknown authored access hide behind the access-mismatch diagnostic.
func TestCompileReportsUnknownDeclaredAccess(t *testing.T) {
	req := compileFixtureRequest(t, false)
	req.Rules.Transformations[0].DeclaredReads = []FieldPath{"driver.assignment_key", "driver.unknown"}
	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	failure, ok := result.Failure()
	if !ok {
		t.Fatal("unknown declared access compiled")
	}
	want := []CompilationDiagnosticCode{UnknownField, DeclaredAccessMismatch}
	got := make([]CompilationDiagnosticCode, 0)
	for _, diagnostic := range failure.Diagnostics() {
		if len(got) == 0 || got[len(got)-1] != diagnostic.Code() {
			got = append(got, diagnostic.Code())
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostic codes=%v, want %v", got, want)
	}
}

// Production break caught: canonical rule-name order must not silently choose
// which of two unordered writers owns the same semantic target.
func TestCompileRejectsUnorderedWriteConflict(t *testing.T) {
	req, _ := compileGoldenVectorRequest(t)
	second := cloneTransformation(req.Rules.Transformations[0])
	second.ID = "g"
	second.Form.OutputSlot = "p"
	req.Rules.Transformations = append(req.Rules.Transformations, second)

	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	failure, ok := result.Failure()
	if !ok {
		t.Fatal("unordered overlapping writers received a plan")
	}
	if got := failure.Diagnostics()[0].Code(); got != WriteConflictUnresolved {
		t.Fatalf("diagnostic=%s, want %s", got, WriteConflictUnresolved)
	}
	if _, ok := result.Plan(); ok {
		t.Fatal("write conflict exposed PlanID")
	}
}

// Production break caught: rejecting writers that have a real dependency path
// would ignore the author-declared semantic order and overconstrain valid plans.
func TestCompileAcceptsOrderedOverlappingWriters(t *testing.T) {
	req, _ := compileGoldenVectorRequest(t)
	middle := cloneTransformation(req.Rules.Transformations[0])
	middle.ID = "g"
	middle.Form.OutputSlot = "p"
	middle.After = []RuleID{"f"}
	last := cloneTransformation(req.Rules.Transformations[0])
	last.ID = "h"
	last.Form.OutputSlot = "q"
	last.After = []RuleID{"g"}
	req.Rules.Transformations = append(req.Rules.Transformations, middle, last)

	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		failure, _ := result.Failure()
		t.Fatalf("ordered writers rejected: %v", failure.Diagnostics())
	}
	if got := plan.MustTransformation("h").Dependencies(); !slices.Equal(got, []RuleID{"g"}) {
		t.Fatalf("dependencies=%v, want [g]", got)
	}
}

// Production break caught: omitting the resolved output-slot entity from T2's
// access contract would hide its destination before-image read and update.
func TestCompileDerivesAggregateTargetEntityReadAndWrite(t *testing.T) {
	result, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		t.Fatal("no plan")
	}
	accesses := plan.MustTransformation("aggregate_team_hos.v1").Accesses()
	entities := make([]SemanticAccess, 0)
	for _, access := range accesses {
		if access.Kind == AccessEntity {
			entities = append(entities, access)
		}
	}
	want := []SemanticAccess{
		{Kind: AccessEntity, Mode: AccessRead, EntityKind: "driver"},
		{Kind: AccessEntity, Mode: AccessRead, EntityKind: "team"},
		{Kind: AccessEntity, Mode: AccessWrite, EntityKind: "team"},
	}
	if !slices.Equal(entities, want) {
		t.Fatalf("aggregate entity accesses=%v, want %v", entities, want)
	}
}

// Production break caught: duplicate normalized operator members or multiple
// declarations writing one destination make composition ambiguous and must not
// acquire distinct canonical identities.
func TestCompileRejectsAmbiguousOperatorCollections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompileRequest)
	}{
		{
			name: "duplicate normalized predicate",
			mutate: func(req *CompileRequest) {
				duplicate := req.Rules.Transformations[1].Aggregate.Predicates[0]
				duplicate.Fields = slices.Clone(duplicate.Fields)
				slices.Reverse(duplicate.Fields)
				req.Rules.Transformations[1].Aggregate.Predicates = append(req.Rules.Transformations[1].Aggregate.Predicates, duplicate)
			},
		},
		{
			name: "duplicate reduction",
			mutate: func(req *CompileRequest) {
				req.Rules.Transformations[1].Aggregate.Reductions = append(
					req.Rules.Transformations[1].Aggregate.Reductions,
					req.Rules.Transformations[1].Aggregate.Reductions[0],
				)
			},
		},
		{
			name: "conflicting reduction destination",
			mutate: func(req *CompileRequest) {
				reductions := req.Rules.Transformations[1].Aggregate.Reductions
				reductions[1].Destination = reductions[0].Destination
				req.Rules.Transformations[1].DeclaredWrites = []FieldPath{
					"team.aggregation_anchor", reductions[0].Destination,
				}
			},
		},
		{
			name: "conflicting copied-field destination",
			mutate: func(req *CompileRequest) {
				declaration := req.Schema
				entities := declaration.EntityDeclarations()
				for i := range entities {
					if entities[i].Kind == "driver" {
						entities[i].Fields = append(entities[i].Fields, FieldDeclaration{Name: "other_assignment", Kind: ValueString})
					}
				}
				schema, err := NewSchema(entities, declaration.RelationDeclarations())
				if err != nil {
					t.Fatalf("NewSchema: %v", err)
				}
				req.Schema = schema.Declaration()
				req.Rules.Transformations[0].Form.CopiedFields = append(
					req.Rules.Transformations[0].Form.CopiedFields,
					FieldCopy{Source: "driver.other_assignment", Destination: "team.assignment_key"},
				)
				req.Rules.Transformations[0].DeclaredReads = append(
					req.Rules.Transformations[0].DeclaredReads, "driver.other_assignment",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := compileFixtureRequest(t, false)
			tt.mutate(&req)
			if _, err := Compile(req); err == nil {
				t.Fatal("ambiguous operator collection reached canonical compilation")
			}
		})
	}
}

// Production break caught: using authoring, field, source-pair, predicate,
// checkpoint, or profile order in canonical construction would drift IDs.
func TestCompileIgnoresNonsemanticDeclarationOrder(t *testing.T) {
	forward, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile forward: %v", err)
	}
	reverse, err := Compile(compileFixtureRequest(t, true))
	if err != nil {
		t.Fatalf("Compile reverse: %v", err)
	}
	forwardPlan, ok := forward.Plan()
	if !ok {
		t.Fatal("forward plan absent")
	}
	reversePlan, ok := reverse.Plan()
	if !ok {
		t.Fatal("reverse plan absent")
	}
	if forwardPlan.ID() != reversePlan.ID() || !bytes.Equal(forwardPlan.CanonicalBytes(), reversePlan.CanonicalBytes()) {
		t.Fatalf("plan drift: %s != %s", forwardPlan.ID(), reversePlan.ID())
	}
	forwardProfiles, reverseProfiles := forward.Profiles(), reverse.Profiles()
	if len(forwardProfiles) != 2 || len(reverseProfiles) != 2 {
		t.Fatalf("profile counts=%d/%d", len(forwardProfiles), len(reverseProfiles))
	}
	for i := range forwardProfiles {
		if forwardProfiles[i].ID() != reverseProfiles[i].ID() || !bytes.Equal(forwardProfiles[i].CanonicalBytes(), reverseProfiles[i].CanonicalBytes()) {
			t.Fatalf("profile %d drift: %s != %s", i, forwardProfiles[i].ID(), reverseProfiles[i].ID())
		}
	}
	if forward.InputDigest() != reverse.InputDigest() {
		t.Fatalf("compiler input drift: %s != %s", forward.InputDigest(), reverse.InputDigest())
	}
}

// Production break caught: omitting exact source references from plan content
// would allow different explicit transformation scopes to share a PlanID.
func TestCompilePlanIdentityChangesWithSourceReference(t *testing.T) {
	baseline, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile baseline: %v", err)
	}
	changedRequest := compileFixtureRequest(t, false)
	changedRequest.Rules.Transformations[0].Form.Sources[1].CanonicalSourceKey = "C"
	changed, err := Compile(changedRequest)
	if err != nil {
		t.Fatalf("Compile changed: %v", err)
	}
	baselinePlan, _ := baseline.Plan()
	changedPlan, _ := changed.Plan()
	if baselinePlan.ID() == changedPlan.ID() {
		t.Fatal("source reference change preserved PlanID")
	}
}

// Production break caught: allowing profiles to enter plan identity or omitting
// them from compiler-input/profile identity would collapse semantic layers.
func TestCompileProfileChangeDoesNotChangePlanIdentity(t *testing.T) {
	baseline, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile baseline: %v", err)
	}
	changedRequest := compileFixtureRequest(t, false)
	changedRequest.Profiles[1].Requirements = changedRequest.Profiles[1].Requirements[:3]
	changedRequest.Profiles[1].Implies = nil
	changed, err := Compile(changedRequest)
	if err != nil {
		t.Fatalf("Compile changed: %v", err)
	}
	baselinePlan, _ := baseline.Plan()
	changedPlan, _ := changed.Plan()
	if baselinePlan.ID() != changedPlan.ID() {
		t.Fatal("profile-only change altered PlanID")
	}
	if baseline.InputDigest() == changed.InputDigest() {
		t.Fatal("profile-only change preserved CompilationInputDigest")
	}
	baselineProfiles, changedProfiles := baseline.Profiles(), changed.Profiles()
	if baselineProfiles[1].ID() == changedProfiles[1].ID() {
		t.Fatal("profile-only change preserved ProfileID")
	}
}

// Production break caught: proving implication from profile names or a partial
// requirement set would falsely promise OptimizerReady implies CMReady.
func TestCompileRejectsUnprovableOptimizerImpliesCM(t *testing.T) {
	req := compileFixtureRequest(t, false)
	req.Profiles[1].Requirements = req.Profiles[1].Requirements[1:]
	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	failure, ok := result.Failure()
	if !ok {
		t.Fatal("invalid profiles compiled")
	}
	if got := failure.Diagnostics()[0].Code(); got != ProfileOrderUnprovable {
		t.Fatalf("diagnostic=%s, want %s", got, ProfileOrderUnprovable)
	}
}

// Production break caught: dropping a transformation or checkpoint from the
// compiled execution contract would change the fixed progressive spine.
func TestCompileProducesExactProgressiveShape(t *testing.T) {
	result, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		t.Fatal("no plan")
	}
	transformations := plan.Transformations()
	if len(transformations) != 2 || transformations[0].Declaration().ID != "form_team.v1" || transformations[1].Declaration().ID != "aggregate_team_hos.v1" {
		t.Fatalf("transformations=%v", transformations)
	}
	checkpoints := plan.Checkpoints()
	want := []CheckpointDeclaration{{Key: "team_formed.v1", After: "form_team.v1"}, {Key: "team_hos_aggregated.v1", After: "aggregate_team_hos.v1"}}
	if !slices.Equal(checkpoints, want) {
		t.Fatalf("checkpoints=%v, want %v", checkpoints, want)
	}
}

// Production break caught: sorting the left and right operands of a <= atom
// would silently reverse business meaning when lexical field order differs.
func TestCompilePreservesLessOrEqualOperandRoles(t *testing.T) {
	req := compileFixtureRequest(t, false)
	want := []FieldPath{"driver.hos_elapsed_hours", "driver.hos_driving_hours"}
	for i := range req.Rules.Transformations[1].Aggregate.Predicates {
		predicate := &req.Rules.Transformations[1].Aggregate.Predicates[i]
		if predicate.Kind == LessOrEqualFields {
			predicate.Fields = slices.Clone(want)
		}
	}
	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := result.Plan()
	if !ok {
		t.Fatal("no plan")
	}
	declaration := plan.MustTransformation("aggregate_team_hos.v1").Declaration()
	for _, predicate := range declaration.Aggregate.Predicates {
		if predicate.Kind == LessOrEqualFields && !slices.Equal(predicate.Fields, want) {
			t.Fatalf("<= operands=%v, want %v", predicate.Fields, want)
		}
	}
}

// Production break caught: changing any v1 compiler-artifact tag, field order,
// count, union marker, or digest input would silently rename accepted plans,
// profiles, or canonical invalid-plan answers.
func TestCompileCanonicalGoldenVectors(t *testing.T) {
	const (
		wantSchemaHex                              = "00000000000000156d616964656e2d6c616e652e736368656d612e76310000000000000002000000000000000161000000000000000100000000000000016701000000000000000001620000000000000002000000000000000167010000000000000000017802000000000000000001000000000000000172000000000000000162000000000000000161"
		wantSchemaDigest  SchemaDigest             = "sha256:f2123251d01510616e5b444374cb4a0cacd18158a293e5ae9515088adf07bec6"
		wantRulesHex                               = "00000000000000166d616964656e2d6c616e652e72756c657365742e763100000000000000010000000000000001660100000000000000010000000000000003612e6700000000000000010000000000000003622e67000000000000000001000000000000000161000000000000000200000000000000016100000000000000013100000000000000016100000000000000013200000000000000016200000000000000016f0000000000000003612e67000000000000000200000000000000010000000000000003612e670000000000000003622e6700000000000000017201010000000000000003612e670000000000000000050000000000000011662f30312d736f757263652d666f756e6400000000000000194445434c415245445f534f555243455f4e4f545f464f554e440100000000000000000000000000000001660000000000000010662f30322d736f757263652d6b696e64000000000000001c4445434c415245445f534f555243455f4b494e445f494e56414c49440100000000000000000000000000000001660000000000000013662f30332d67726f7570696e672d76616c6964000000000000001b5445414d5f41535349474e4d454e545f4b45595f494e56414c49440100000000000000010000000000000003612e670000000000000001660000000000000013662f30342d67726f7570696e672d657175616c000000000000001c5445414d5f41535349474e4d454e545f4b45595f4d49534d415443480100000000000000010000000000000003612e670000000000000001660000000000000017662f30352d6d656d6265722d63617264696e616c697479000000000000001f5445414d5f4d454d4245525f43415244494e414c4954595f494e56414c4944020000000000000000000000000000000166000000000000000100000000000000026331000000000000000166"
		wantRulesDigest   RulesetDigest            = "sha256:e16c076e567a8c32df9407385f83c18395bba9fa65bb73287d80d4bdc3ee73be"
		wantInputHex                               = "00000000000000206d616964656e2d6c616e652e636f6d70696c6174696f6e2d696e7075742e7631f2123251d01510616e5b444374cb4a0cacd18158a293e5ae9515088adf07bec6e16c076e567a8c32df9407385f83c18395bba9fa65bb73287d80d4bdc3ee73be00000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631000000000000000200000000000000016301000000000000000162010000000000000001000000000000001c5445414d5f41535349474e4d454e545f4b45595f5245515549524544010000000000000003622e6700000000000000000000000000000001700100000000000000016201000000000000000200000000000000205445414d5f4147475245474154494f4e5f414e43484f525f5245515549524544010000000000000003622e78000000000000001c5445414d5f41535349474e4d454e545f4b45595f5245515549524544010000000000000003622e670000000000000001000000000000000163"
		wantInputDigest   CompilationInputDigest   = "sha256:64f54d4c3d8d61640b3c779f20657cc82c841a5680990181d78410ae96720dce"
		wantPlanHex                                = "00000000000000136d616964656e2d6c616e652e706c616e2e7631f2123251d01510616e5b444374cb4a0cacd18158a293e5ae9515088adf07bec6e16c076e567a8c32df9407385f83c18395bba9fa65bb73287d80d4bdc3ee73be00000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e763100000000000000010000000000000001660100000000000000010000000000000003612e6700000000000000010000000000000003622e67000000000000000001000000000000000161000000000000000200000000000000016100000000000000013100000000000000016100000000000000013200000000000000016200000000000000016f0000000000000003612e67000000000000000200000000000000010000000000000003612e670000000000000003622e6700000000000000017201010000000000000003612e670000000000000000010000000000000003612e6700000000000000010000000000000003622e6700000000000000050101000000000000000161000000000000000000000000000000000102000000000000000162000000000000000000000000000000000202000000000000000000000000000000017200000000000000000301000000000000000000000000000000000000000000000003612e670302000000000000000000000000000000000000000000000003622e670000000000000000000000000000000000000000000000050000000000000011662f30312d736f757263652d666f756e6400000000000000194445434c415245445f534f555243455f4e4f545f464f554e440100000000000000000000000000000001660000000000000010662f30322d736f757263652d6b696e64000000000000001c4445434c415245445f534f555243455f4b494e445f494e56414c49440100000000000000000000000000000001660000000000000013662f30332d67726f7570696e672d76616c6964000000000000001b5445414d5f41535349474e4d454e545f4b45595f494e56414c49440100000000000000010000000000000003612e670000000000000001660000000000000013662f30342d67726f7570696e672d657175616c000000000000001c5445414d5f41535349474e4d454e545f4b45595f4d49534d415443480100000000000000010000000000000003612e670000000000000001660000000000000017662f30352d6d656d6265722d63617264696e616c697479000000000000001f5445414d5f4d454d4245525f43415244494e414c4954595f494e56414c4944020000000000000000000000000000000166000000000000000100000000000000026331000000000000000166000000000000000a6368616e6765732e7631"
		wantPlanID        PlanID                   = "sha256:98687456cfa5d197f10a023a8bdf9fd8a95ef008154ba08b93b729f352c43180"
		wantCMHex                                  = "000000000000001f6d616964656e2d6c616e652e636f6d70696c65642d70726f66696c652e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631f2123251d01510616e5b444374cb4a0cacd18158a293e5ae9515088adf07bec600000000000000016301000000000000000162010000000000000001000000000000001c5445414d5f41535349474e4d454e545f4b45595f5245515549524544010000000000000003622e6700000000000000000000000000000000"
		wantCMID          ProfileID                = "sha256:4b25a6cad46e65cedcda0f4e7e37ff66cc3a303e78479904f8d9bb0c9e1668fb"
		wantOptimizerHex                           = "000000000000001f6d616964656e2d6c616e652e636f6d70696c65642d70726f66696c652e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631f2123251d01510616e5b444374cb4a0cacd18158a293e5ae9515088adf07bec60000000000000001700100000000000000016201000000000000000200000000000000205445414d5f4147475245474154494f4e5f414e43484f525f5245515549524544010000000000000003622e78000000000000001c5445414d5f41535349474e4d454e545f4b45595f5245515549524544010000000000000003622e670000000000000001000000000000000163000000000000000100000000000000016301"
		wantOptimizerID   ProfileID                = "sha256:38a503b6b92ae6d64fcacd9653f014707e10f0edf237707d1879ea9cea542271"
		wantFailureHex                             = "00000000000000226d616964656e2d6c616e652e636f6d70696c6174696f6e2d6661696c7572652e7631ba98d3dc182fc4b0674a4e6b7ae1e9d6627281e9454a95c702ab0abe0e56de9a000000000000000c494e56414c49445f504c414e0000000000000001000000000000001850524f46494c455f4f524445525f554e50524f5641424c45000000000000000170000000000000000163"
		wantFailureDigest CompilationFailureDigest = "sha256:9ee313bafdbf5646ba8bcbade241c179e8f1c070309ee90fa44c0bc295b8d756"
	)
	req, schema := compileGoldenVectorRequest(t)
	rules, err := normalizeRuleset(req.Rules)
	if err != nil {
		t.Fatalf("normalizeRuleset: %v", err)
	}
	rulesBytes, err := encodeRuleset(rules)
	if err != nil {
		t.Fatalf("encodeRuleset: %v", err)
	}
	profiles, err := normalizeProfiles(req.Profiles)
	if err != nil {
		t.Fatalf("normalizeProfiles: %v", err)
	}
	inputBytes, err := encodeCompilationInput(schema.Digest(), RulesetDigest(canonicalDigest(rulesBytes)), profiles, req.CompilerSemanticsVersion)
	if err != nil {
		t.Fatalf("encodeCompilationInput: %v", err)
	}
	result, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, _ := result.Plan()
	compiledProfiles := result.Profiles()

	assertCanonicalVector(t, "schema", schema.CanonicalBytes(), wantSchemaHex, string(schema.Digest()), string(wantSchemaDigest))
	assertCanonicalVector(t, "ruleset", rulesBytes, wantRulesHex, string(canonicalDigest(rulesBytes)), string(wantRulesDigest))
	assertCanonicalVector(t, "compiler input", inputBytes, wantInputHex, string(result.InputDigest()), string(wantInputDigest))
	assertCanonicalVector(t, "plan", plan.CanonicalBytes(), wantPlanHex, string(plan.ID()), string(wantPlanID))
	assertCanonicalVector(t, "CM profile", compiledProfiles[0].CanonicalBytes(), wantCMHex, string(compiledProfiles[0].ID()), string(wantCMID))
	assertCanonicalVector(t, "optimizer profile", compiledProfiles[1].CanonicalBytes(), wantOptimizerHex, string(compiledProfiles[1].ID()), string(wantOptimizerID))

	invalid := req
	invalid.Profiles = []ProfileDeclaration{cloneProfile(req.Profiles[0]), cloneProfile(req.Profiles[1])}
	invalid.Profiles[1].Requirements = invalid.Profiles[1].Requirements[:1]
	failed, err := Compile(invalid)
	if err != nil {
		t.Fatalf("Compile invalid: %v", err)
	}
	failure, ok := failed.Failure()
	if !ok {
		t.Fatal("invalid profile compiled")
	}
	assertCanonicalVector(t, "compilation failure", failure.CanonicalBytes(), wantFailureHex, string(failure.Digest()), string(wantFailureDigest))
}

func compileGoldenVectorRequest(t *testing.T) (CompileRequest, Schema) {
	t.Helper()
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "a", Fields: []FieldDeclaration{{Name: "g", Kind: ValueString}}},
		{Kind: "b", Fields: []FieldDeclaration{{Name: "g", Kind: ValueString}, {Name: "x", Kind: ValueAtom}}},
	}, []RelationDeclaration{{Kind: "r", FromKind: "b", ToKind: "a"}})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	request := CompileRequest{
		Schema: schema.Declaration(),
		Rules: RulesetDeclaration{
			Transformations: []TransformationDeclaration{{
				ID: "f", Operator: OperatorFormRelatedEntity,
				DeclaredReads: []FieldPath{"a.g"}, DeclaredWrites: []FieldPath{"b.g"},
				Form: &FormRelatedEntityDeclaration{
					SourceKind: "a", Sources: []SourceReference{{Kind: "a", CanonicalSourceKey: "1"}, {Kind: "a", CanonicalSourceKey: "2"}},
					OutputKind: "b", OutputSlot: "o", GroupingField: "a.g", SourceCount: 2,
					CopiedFields: []FieldCopy{{Source: "a.g", Destination: "b.g"}}, RelationKind: "r",
					OutputKey: &OutputKeyExpression{Kind: OutputKeyCommonSourceField, Field: "a.g"},
				},
			}},
			Checkpoints: []CheckpointDeclaration{{Key: "c1", After: "f"}},
		},
		Profiles: []ProfileDeclaration{
			{Key: "c", Scope: ProfileScope{Kind: AllEntitiesOfKind, EntityKind: "b"}, Aggregation: AllSelected,
				Requirements: []RequirementAtom{{Code: TeamAssignmentKeyRequired, Kind: FieldPresent, Field: "b.g"}}},
			{Key: "p", Scope: ProfileScope{Kind: AllEntitiesOfKind, EntityKind: "b"}, Aggregation: AllSelected,
				Requirements: []RequirementAtom{{Code: TeamAggregationAnchorRequired, Kind: FieldPresent, Field: "b.x"}, {Code: TeamAssignmentKeyRequired, Kind: FieldPresent, Field: "b.g"}}, Implies: []ProfileKey{"c"}},
		},
		CompilerSemanticsVersion: testCompilerVersion,
	}
	return request, schema
}

func assertCanonicalVector(t *testing.T, name string, got []byte, wantHex, gotDigest, wantDigest string) {
	t.Helper()
	if actual := hex.EncodeToString(got); actual != wantHex {
		t.Fatalf("%s canonical hex\n got: %s\nwant: %s", name, actual, wantHex)
	}
	if gotDigest != wantDigest {
		t.Fatalf("%s digest=%s, want %s", name, gotDigest, wantDigest)
	}
}

func compileFixtureRequest(t *testing.T, reverse bool) CompileRequest {
	t.Helper()
	schema := compileFixtureSchema(t, reverse)
	form := TransformationDeclaration{
		ID:             "form_team.v1",
		Operator:       OperatorFormRelatedEntity,
		DeclaredReads:  []FieldPath{"driver.assignment_key"},
		DeclaredWrites: []FieldPath{"team.assignment_key"},
		Form: &FormRelatedEntityDeclaration{
			SourceKind:    "driver",
			Sources:       []SourceReference{{Kind: "driver", CanonicalSourceKey: "A"}, {Kind: "driver", CanonicalSourceKey: "B"}},
			OutputKind:    "team",
			OutputSlot:    "team",
			GroupingField: "driver.assignment_key",
			SourceCount:   2,
			CopiedFields:  []FieldCopy{{Source: "driver.assignment_key", Destination: "team.assignment_key"}},
			RelationKind:  "member",
			OutputKey:     &OutputKeyExpression{Kind: OutputKeyCommonSourceField, Field: "driver.assignment_key"},
		},
	}
	aggregate := TransformationDeclaration{
		ID:       "aggregate_team_hos.v1",
		Operator: OperatorAggregateRelatedFields,
		DeclaredReads: []FieldPath{
			"driver.hos_anchor", "driver.hos_driving_hours", "driver.hos_elapsed_hours",
			"team.aggregation_anchor", "team.driving_duration_hours", "team.elapsed_duration_hours",
		},
		DeclaredWrites: []FieldPath{"team.aggregation_anchor", "team.driving_duration_hours", "team.elapsed_duration_hours"},
		Aggregate: &AggregateRelatedFieldsDeclaration{
			Target:              OutputSlotReference{Rule: "form_team.v1", Slot: "team"},
			RelationKind:        "member",
			SourceKind:          "driver",
			RequiredSourceTuple: []FieldPath{"driver.hos_anchor", "driver.hos_elapsed_hours", "driver.hos_driving_hours"},
			Predicates: []AggregatePredicate{
				{Kind: CompleteTuple, Fields: []FieldPath{"driver.hos_anchor", "driver.hos_elapsed_hours", "driver.hos_driving_hours"}},
				{Kind: NonNegativeInt, Fields: []FieldPath{"driver.hos_elapsed_hours"}},
				{Kind: NonNegativeInt, Fields: []FieldPath{"driver.hos_driving_hours"}},
				{Kind: EqualFieldAcrossSources, Fields: []FieldPath{"driver.hos_anchor"}},
				{Kind: LessOrEqualFields, Fields: []FieldPath{"driver.hos_driving_hours", "driver.hos_elapsed_hours"}},
			},
			Anchor: FieldCopy{Source: "driver.hos_anchor", Destination: "team.aggregation_anchor"},
			Reductions: []FieldReduction{
				{Kind: ReduceInt64Max, Source: "driver.hos_elapsed_hours", Destination: "team.elapsed_duration_hours"},
				{Kind: ReduceInt64Max, Source: "driver.hos_driving_hours", Destination: "team.driving_duration_hours"},
			},
			ResultPredicates: []AggregatePredicate{
				{Kind: CompleteTuple, Fields: []FieldPath{"team.aggregation_anchor", "team.elapsed_duration_hours", "team.driving_duration_hours"}},
				{Kind: NonNegativeInt, Fields: []FieldPath{"team.elapsed_duration_hours"}},
				{Kind: NonNegativeInt, Fields: []FieldPath{"team.driving_duration_hours"}},
				{Kind: LessOrEqualFields, Fields: []FieldPath{"team.driving_duration_hours", "team.elapsed_duration_hours"}},
			},
		},
	}
	if reverse {
		slices.Reverse(form.Form.Sources)
		form.DeclaredReads = slices.Clone(form.DeclaredReads)
		slices.Reverse(aggregate.DeclaredReads)
		slices.Reverse(aggregate.DeclaredWrites)
		slices.Reverse(aggregate.Aggregate.RequiredSourceTuple)
		slices.Reverse(aggregate.Aggregate.Predicates)
		slices.Reverse(aggregate.Aggregate.Reductions)
		slices.Reverse(aggregate.Aggregate.ResultPredicates)
	}
	rules := []TransformationDeclaration{form, aggregate}
	checkpoints := []CheckpointDeclaration{{Key: "team_formed.v1", After: "form_team.v1"}, {Key: "team_hos_aggregated.v1", After: "aggregate_team_hos.v1"}}
	profiles := compileFixtureProfiles(reverse)
	if reverse {
		slices.Reverse(rules)
		slices.Reverse(checkpoints)
		slices.Reverse(profiles)
	}
	return CompileRequest{
		Schema: schema.Declaration(),
		Rules: RulesetDeclaration{
			Transformations: rules,
			Checkpoints:     checkpoints,
		},
		Profiles:                 profiles,
		CompilerSemanticsVersion: testCompilerVersion,
	}
}

func compileFixtureSchema(t *testing.T, reverse bool) Schema {
	t.Helper()
	entities := []EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "hos_anchor", Kind: ValueAtom},
			{Name: "hos_elapsed_hours", Kind: ValueInt64},
			{Name: "hos_driving_hours", Kind: ValueInt64},
		}},
		{Kind: "team", Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "aggregation_anchor", Kind: ValueAtom},
			{Name: "elapsed_duration_hours", Kind: ValueInt64},
			{Name: "driving_duration_hours", Kind: ValueInt64},
		}},
	}
	if reverse {
		for i := range entities {
			slices.Reverse(entities[i].Fields)
		}
		slices.Reverse(entities)
	}
	schema, err := NewSchema(entities, []RelationDeclaration{{Kind: "member", FromKind: "team", ToKind: "driver"}})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func compileFixtureProfiles(reverse bool) []ProfileDeclaration {
	cm := ProfileDeclaration{
		Key:         "cm.v1",
		Scope:       ProfileScope{Kind: AllEntitiesOfKind, EntityKind: "team"},
		Aggregation: AllSelected,
		Requirements: []RequirementAtom{{
			Code:  TeamAssignmentKeyRequired,
			Kind:  FieldPresent,
			Field: "team.assignment_key",
		}},
	}
	optimizer := ProfileDeclaration{
		Key:         "optimizer.v1",
		Scope:       ProfileScope{Kind: AllEntitiesOfKind, EntityKind: "team"},
		Aggregation: AllSelected,
		Requirements: []RequirementAtom{
			{Code: TeamAssignmentKeyRequired, Kind: FieldPresent, Field: "team.assignment_key"},
			{Code: TeamAggregationAnchorRequired, Kind: FieldPresent, Field: "team.aggregation_anchor"},
			{Code: TeamElapsedDurationRequired, Kind: FieldPresent, Field: "team.elapsed_duration_hours"},
			{Code: TeamDrivingDurationRequired, Kind: FieldPresent, Field: "team.driving_duration_hours"},
		},
		Implies: []ProfileKey{"cm.v1"},
	}
	if reverse {
		slices.Reverse(optimizer.Requirements)
	}
	return []ProfileDeclaration{cm, optimizer}
}

// Production break caught: a compilation input that shared interior state with
// its compilation would hand every holder a mutable alias into an immutable
// artifact. A stored record is the worst place for that, because one caller's
// mutation would silently change what every later reader believes was compiled.
func TestCompilationInputCannotBeMutatedThroughItsAccessor(t *testing.T) {
	compilation, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	original, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}

	// Reach as deeply as the returned request allows and corrupt everything.
	request := compilation.Input().Request()
	request.CompilerSemanticsVersion = "corrupted"
	for i := range request.Rules.Transformations {
		request.Rules.Transformations[i].ID = "corrupted"
		for j := range request.Rules.Transformations[i].DeclaredReads {
			request.Rules.Transformations[i].DeclaredReads[j] = "corrupted.field"
		}
		if form := request.Rules.Transformations[i].Form; form != nil {
			form.OutputSlot = "corrupted"
			for j := range form.Sources {
				form.Sources[j].CanonicalSourceKey = "corrupted"
			}
		}
		if aggregate := request.Rules.Transformations[i].Aggregate; aggregate != nil {
			aggregate.Target.Slot = "corrupted"
			for j := range aggregate.RequiredSourceTuple {
				aggregate.RequiredSourceTuple[j] = "corrupted.field"
			}
		}
	}
	for i := range request.Rules.Checkpoints {
		request.Rules.Checkpoints[i].Key = "corrupted"
	}
	for i := range request.Profiles {
		request.Profiles[i].Key = "corrupted"
		for j := range request.Profiles[i].Requirements {
			request.Profiles[i].Requirements[j].Code = "CORRUPTED"
		}
	}
	entities := request.Schema.EntityDeclarations()
	for i := range entities {
		entities[i].Kind = "corrupted"
		for j := range entities[i].Fields {
			entities[i].Fields[j].Name = "corrupted"
		}
	}

	// The compilation is unchanged, and a second read is unaffected by the first.
	afterPlan, ok := compilation.Plan()
	if !ok || afterPlan.ID() != original.ID() {
		t.Fatalf("plan identity changed after mutating the returned request: %s", afterPlan.ID())
	}
	second := compilation.Input().Request()
	if second.CompilerSemanticsVersion == "corrupted" {
		t.Fatal("mutating one returned request corrupted the next")
	}
	if got := second.Rules.Transformations[0].ID; got == "corrupted" {
		t.Fatalf("transformation identity leaked a mutation: %s", got)
	}
}

// Production break caught: the retained input must be sufficient to reproduce
// the compilation exactly. A durable adapter rebuilds from it and verifies the
// identities match, so an input that lost anything would make every read of a
// persisted plan fail closed.
func TestCompilationInputRecompilesToTheIdenticalArtifacts(t *testing.T) {
	first, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	firstPlan, ok := first.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}

	second, err := Compile(first.Input().Request())
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	secondPlan, ok := second.Plan()
	if !ok {
		failure, _ := second.Failure()
		t.Fatalf("retained input did not compile: %v", failure.Diagnostics())
	}

	if secondPlan.ID() != firstPlan.ID() {
		t.Fatalf("recompiled planID = %s, want %s", secondPlan.ID(), firstPlan.ID())
	}
	if second.InputDigest() != first.InputDigest() {
		t.Fatalf("recompiled input digest = %s, want %s", second.InputDigest(), first.InputDigest())
	}
	if !bytes.Equal(secondPlan.CanonicalBytes(), firstPlan.CanonicalBytes()) {
		t.Fatal("recompiled plan bytes differ")
	}
	firstProfiles, secondProfiles := first.Profiles(), second.Profiles()
	if len(firstProfiles) != len(secondProfiles) {
		t.Fatalf("profiles = %d, want %d", len(secondProfiles), len(firstProfiles))
	}
	for i := range firstProfiles {
		if firstProfiles[i].ID() != secondProfiles[i].ID() {
			t.Errorf("profile %d = %s, want %s", i, secondProfiles[i].ID(), firstProfiles[i].ID())
		}
	}
}

// Production break caught: the input carries its own identity so an adapter can
// verify a round trip without recompiling twice, and that identity must be the
// one the compilation already reports.
func TestCompilationInputReportsTheCompilationsOwnInputIdentity(t *testing.T) {
	compilation, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got, want := compilation.Input().Digest(), compilation.InputDigest(); got != want {
		t.Fatalf("input digest = %s, want %s", got, want)
	}
	if len(compilation.Input().CanonicalBytes()) == 0 {
		t.Fatal("compilation input has no canonical bytes")
	}
	// The bytes are a defensive copy: mutating them cannot move the digest.
	canonical := compilation.Input().CanonicalBytes()
	for i := range canonical {
		canonical[i] = 0
	}
	if compilation.Input().Digest() != compilation.InputDigest() {
		t.Fatal("mutating returned canonical bytes changed the reported identity")
	}
}

// Production break caught: an invalid program still has an input identity, and
// a caller diagnosing a rejected compilation needs it. It must not panic or
// return a zero value that claims to be an identity.
func TestFailedCompilationStillCarriesItsInput(t *testing.T) {
	request := compileFixtureRequest(t, false)
	request.Rules.Transformations[0].DeclaredReads = append(
		request.Rules.Transformations[0].DeclaredReads, "driver.field_that_does_not_exist")

	compilation, err := Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, ok := compilation.Failure(); !ok {
		t.Fatal("invalid declarations compiled")
	}
	if got, want := compilation.Input().Digest(), compilation.InputDigest(); got != want {
		t.Fatalf("failed compilation input digest = %s, want %s", got, want)
	}
	if compilation.Input().Request().CompilerSemanticsVersion == "" {
		t.Fatal("failed compilation retained no input")
	}
}
