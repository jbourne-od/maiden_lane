package teamhos_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Ratified literals under test come from the walking-skeleton design
// (docs/superpowers/specs/2026-08-13-progressive-semantic-spine-design.md):
// lineage descriptor and source keys from section 4.2, driver tuples from
// sections 10.1 and 10.2, profiles from sections 7.2 and 7.3, and the
// progressive plan shape from section 3.3.
const (
	lineageNamespace = "maiden-lane.sanitized-fixture"
	lineageRootKey   = "team-hos-team-ab"
	sourceKeyA       = "A"
	sourceKeyB       = "B"
	assignmentKey    = "X"
)

// Production break caught: a fixture that admits an undeclared variant would
// let later app/observability tests silently run an unratified incident.
func TestNewRejectsUnknownVariants(t *testing.T) {
	for _, variant := range []teamhos.Variant{0, 3, 99} {
		if _, err := teamhos.New(variant); err == nil {
			t.Fatalf("New(%d) accepted an unratified variant", variant)
		}
	}
}

// Production break caught: drifting rule, checkpoint, or profile declarations
// would compile a plan other than the ratified two-transition spine.
func TestPassingFixtureCompilesToRatifiedPlan(t *testing.T) {
	in, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	compiled, err := semantic.Compile(in.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compiled.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	if got := planRuleIDs(plan); !slices.Equal(got, []semantic.RuleID{teamhos.RuleFormTeam, teamhos.RuleAggregateTeamHOS}) {
		t.Fatalf("rules=%v", got)
	}
	wantCheckpoints := []semantic.CheckpointDeclaration{
		{Key: teamhos.CheckpointTeamFormed, After: teamhos.RuleFormTeam},
		{Key: teamhos.CheckpointTeamHOSAggregated, After: teamhos.RuleAggregateTeamHOS},
	}
	if got := plan.Checkpoints(); !slices.Equal(got, wantCheckpoints) {
		t.Fatalf("checkpoints=%v, want %v", got, wantCheckpoints)
	}
	profiles := compiled.Profiles()
	if len(profiles) != 2 || profiles[0].Key() != teamhos.ProfileCM || profiles[1].Key() != teamhos.ProfileOptimizer {
		t.Fatalf("profiles=%v", profiles)
	}
	for _, profile := range profiles {
		if profile.ID() == "" {
			t.Fatalf("profile %s compiled without a ProfileID", profile.Key())
		}
	}
}

// Production break caught: any drift from the ratified sanitized incident
// content would silently change every golden identity downstream.
func TestFixtureEncodesRatifiedContent(t *testing.T) {
	lineage, err := semantic.NewInputLineageID(lineageNamespace, lineageRootKey)
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	refA := semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", sourceKeyA)}
	refB := semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", sourceKeyB)}

	type tuple struct {
		anchor  string
		elapsed int64
		driving int64
	}
	tests := []struct {
		name    string
		variant teamhos.Variant
		a, b    tuple
	}{
		{name: "passing", variant: teamhos.Passing, a: tuple{"T0", 10, 8}, b: tuple{"T0", 7, 6}},
		{name: "anchor mismatch", variant: teamhos.AnchorMismatch, a: tuple{"T0", 10, 8}, b: tuple{"T1", 7, 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := mustInputs(t, tt.variant)
			state := in.InitialState
			if state.InputLineageID() != lineage {
				t.Fatalf("lineage=%s, want ratified descriptor digest %s", state.InputLineageID(), lineage)
			}
			entities := state.Entities()
			if len(entities) != 2 {
				t.Fatalf("S0 entities=%d, want exactly the two source drivers", len(entities))
			}
			if len(state.Relations()) != 0 {
				t.Fatalf("S0 relations=%v, want none", state.Relations())
			}
			driverA, ok := state.Entity(refA)
			if !ok {
				t.Fatalf("driver %s absent from S0", sourceKeyA)
			}
			driverB, ok := state.Entity(refB)
			if !ok {
				t.Fatalf("driver %s absent from S0", sourceKeyB)
			}
			for _, driver := range []semantic.Entity{driverA, driverB} {
				assertStringField(t, driver, "assignment_key", assignmentKey)
			}
			assertAtomField(t, driverA, "hos_anchor", tt.a.anchor)
			assertInt64Field(t, driverA, "hos_elapsed_hours", tt.a.elapsed)
			assertInt64Field(t, driverA, "hos_driving_hours", tt.a.driving)
			assertAtomField(t, driverB, "hos_anchor", tt.b.anchor)
			assertInt64Field(t, driverB, "hos_elapsed_hours", tt.b.elapsed)
			assertInt64Field(t, driverB, "hos_driving_hours", tt.b.driving)

			if got := len(in.World.References()); got != 0 {
				t.Fatalf("world references=%d, want explicit empty world", got)
			}
			emptyWorld, err := semantic.NewWorld(nil)
			if err != nil {
				t.Fatalf("NewWorld: %v", err)
			}
			if in.World.ID() != emptyWorld.ID() {
				t.Fatalf("WorldID=%s, want canonical empty world %s", in.World.ID(), emptyWorld.ID())
			}
			if in.Policy != semantic.ChangesProvenance {
				t.Fatalf("policy=%d, want changes.v1", in.Policy)
			}
		})
	}
}

// Production break caught: the two ratified variants must share lineage and
// plan while differing only in driver B's HOS anchor observation.
func TestVariantsDifferOnlyInDriverBAnchor(t *testing.T) {
	passing := mustInputs(t, teamhos.Passing)
	mismatch := mustInputs(t, teamhos.AnchorMismatch)

	passingPlan := mustPlan(t, passing.Compilation)
	mismatchPlan := mustPlan(t, mismatch.Compilation)
	if passingPlan.ID() != mismatchPlan.ID() {
		t.Fatalf("variant PlanIDs differ: %s != %s", passingPlan.ID(), mismatchPlan.ID())
	}
	if passing.InitialState.InputLineageID() != mismatch.InitialState.InputLineageID() {
		t.Fatal("variants do not share the ratified input lineage")
	}
	if passing.InitialState.Digest() == mismatch.InitialState.Digest() {
		t.Fatal("variants share InitialStateDigest despite different S0 content")
	}

	lineage := passing.InitialState.InputLineageID()
	refA := semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", sourceKeyA)}
	refB := semantic.EntityRef{Kind: "driver", ID: semantic.SourceEntityID(lineage, "driver", sourceKeyB)}
	passingA, _ := passing.InitialState.Entity(refA)
	mismatchA, _ := mismatch.InitialState.Entity(refA)
	assertSameFields(t, "driver A", passingA, mismatchA, nil)
	passingB, _ := passing.InitialState.Entity(refB)
	mismatchB, _ := mismatch.InitialState.Entity(refB)
	assertSameFields(t, "driver B", passingB, mismatchB, []semantic.FieldName{"hos_anchor"})
}

// Production break caught: dropping a ratified requirement atom or the proved
// CM ordering would change which consumers a checkpoint can satisfy.
func TestFixtureDeclaresRatifiedProfileRequirements(t *testing.T) {
	in := mustInputs(t, teamhos.Passing)
	compiled, err := semantic.Compile(in.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	profiles := compiled.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("profiles=%d, want 2", len(profiles))
	}
	cm, optimizer := profiles[0], profiles[1]
	if cm.Key() != teamhos.ProfileCM || optimizer.Key() != teamhos.ProfileOptimizer {
		t.Fatalf("profile keys=(%s,%s)", cm.Key(), optimizer.Key())
	}
	if got := requirementCodes(cm); !slices.Equal(got, []semantic.RequirementCode{semantic.TeamAssignmentKeyRequired}) {
		t.Fatalf("cm requirements=%v", got)
	}
	wantOptimizer := []semantic.RequirementCode{
		semantic.TeamAggregationAnchorRequired,
		semantic.TeamAssignmentKeyRequired,
		semantic.TeamDrivingDurationRequired,
		semantic.TeamElapsedDurationRequired,
	}
	if got := requirementCodes(optimizer); !slices.Equal(got, wantOptimizer) {
		t.Fatalf("optimizer requirements=%v, want %v", got, wantOptimizer)
	}
	proofs := optimizer.Proofs()
	if len(proofs) != 1 || proofs[0].Target() != teamhos.ProfileCM {
		t.Fatalf("optimizer proofs=%v, want proved cm.v1 ordering", proofs)
	}
}

// Production break caught: the closed aggregate declaration must carry the
// ratified max reductions and predicate roles, not a rewritten policy.
func TestFixtureDeclaresRatifiedAggregateReductions(t *testing.T) {
	in := mustInputs(t, teamhos.Passing)
	plan := mustPlan(t, in.Compilation)
	declaration := plan.MustTransformation(teamhos.RuleAggregateTeamHOS).Declaration()
	aggregate := declaration.Aggregate
	if aggregate == nil {
		t.Fatal("aggregate payload absent")
	}
	if aggregate.Target != (semantic.OutputSlotReference{Rule: teamhos.RuleFormTeam, Slot: "team"}) {
		t.Fatalf("aggregate target=%v, want T1 team output slot", aggregate.Target)
	}
	if aggregate.Anchor != (semantic.FieldCopy{Source: "driver.hos_anchor", Destination: "team.aggregation_anchor"}) {
		t.Fatalf("anchor copy=%v", aggregate.Anchor)
	}
	wantReductions := []semantic.FieldReduction{
		{Kind: semantic.ReduceInt64Max, Source: "driver.hos_driving_hours", Destination: "team.driving_duration_hours"},
		{Kind: semantic.ReduceInt64Max, Source: "driver.hos_elapsed_hours", Destination: "team.elapsed_duration_hours"},
	}
	reductions := aggregate.Reductions
	slices.SortFunc(reductions, func(a, b semantic.FieldReduction) int {
		return strings.Compare(string(a.Destination), string(b.Destination))
	})
	if !slices.Equal(reductions, wantReductions) {
		t.Fatalf("reductions=%v, want ratified componentwise maxima %v", reductions, wantReductions)
	}
	var sawSourceOrder, sawResultOrder bool
	for _, predicate := range aggregate.Predicates {
		if predicate.Kind == semantic.LessOrEqualFields {
			sawSourceOrder = slices.Equal(predicate.Fields, []semantic.FieldPath{"driver.hos_driving_hours", "driver.hos_elapsed_hours"})
		}
	}
	for _, predicate := range aggregate.ResultPredicates {
		if predicate.Kind == semantic.LessOrEqualFields {
			sawResultOrder = slices.Equal(predicate.Fields, []semantic.FieldPath{"team.driving_duration_hours", "team.elapsed_duration_hours"})
		}
	}
	if !sawSourceOrder || !sawResultOrder {
		t.Fatalf("driving <= elapsed predicate roles missing: source=%t result=%t", sawSourceOrder, sawResultOrder)
	}
}

// Production break caught: nondeterministic fixture construction would give
// two identical requests different semantic identities.
func TestFreshFixtureCallsCompileToIdenticalIdentities(t *testing.T) {
	for _, variant := range []teamhos.Variant{teamhos.Passing, teamhos.AnchorMismatch} {
		first := mustInputs(t, variant)
		second := mustInputs(t, variant)
		firstCompiled, err := semantic.Compile(first.Compilation)
		if err != nil {
			t.Fatalf("Compile first: %v", err)
		}
		secondCompiled, err := semantic.Compile(second.Compilation)
		if err != nil {
			t.Fatalf("Compile second: %v", err)
		}
		firstPlan, ok := firstCompiled.Plan()
		if !ok {
			t.Fatal("first fixture did not compile")
		}
		secondPlan, ok := secondCompiled.Plan()
		if !ok {
			t.Fatal("second fixture did not compile")
		}
		if firstPlan.ID() != secondPlan.ID() || !bytes.Equal(firstPlan.CanonicalBytes(), secondPlan.CanonicalBytes()) {
			t.Fatalf("variant %d plan drift: %s != %s", variant, firstPlan.ID(), secondPlan.ID())
		}
		if firstCompiled.InputDigest() != secondCompiled.InputDigest() {
			t.Fatalf("variant %d compiler input drift", variant)
		}
		firstProfiles, secondProfiles := firstCompiled.Profiles(), secondCompiled.Profiles()
		for i := range firstProfiles {
			if firstProfiles[i].ID() != secondProfiles[i].ID() {
				t.Fatalf("variant %d profile %d drift", variant, i)
			}
		}
		if first.InitialState.Digest() != second.InitialState.Digest() {
			t.Fatalf("variant %d InitialStateDigest drift", variant)
		}
		if first.World.ID() != second.World.ID() || first.Policy != second.Policy {
			t.Fatalf("variant %d world/policy drift", variant)
		}
		if first.ExecutorIdentity != second.ExecutorIdentity {
			t.Fatalf("variant %d executor identity drift", variant)
		}
	}
}

// Production break caught: shared backing arrays between New calls would let
// one test's mutation rewrite another test's ratified declarations.
func TestMutatingOneInputsCannotAffectAnotherCall(t *testing.T) {
	baseline := mustInputs(t, teamhos.Passing)
	baselineCompiled, err := semantic.Compile(baseline.Compilation)
	if err != nil {
		t.Fatalf("Compile baseline: %v", err)
	}
	baselinePlan, _ := baselineCompiled.Plan()
	baselineProfiles := baselineCompiled.Profiles()

	victim := mustInputs(t, teamhos.Passing)
	victim.Compilation.CompilerSemanticsVersion = "mutated.v9"
	victim.Compilation.Rules.Transformations[0].ID = "mutated.v1"
	victim.Compilation.Rules.Transformations[0].DeclaredReads[0] = "driver.hos_anchor"
	victim.Compilation.Rules.Transformations[0].Form.Sources[0].CanonicalSourceKey = "Z"
	victim.Compilation.Rules.Transformations[0].Form.CopiedFields[0].Destination = "team.aggregation_anchor"
	victim.Compilation.Rules.Transformations[1].Aggregate.Predicates[0].Fields[0] = "driver.mutated"
	victim.Compilation.Rules.Transformations[1].Aggregate.Reductions[0].Kind = 0
	victim.Compilation.Rules.Transformations[1].Aggregate.RequiredSourceTuple[0] = "driver.mutated"
	victim.Compilation.Rules.Checkpoints[0].Key = "mutated_checkpoint.v1"
	victim.Compilation.Profiles[0].Requirements[0].Field = "team.aggregation_anchor"
	victim.Compilation.Profiles[1].Requirements = victim.Compilation.Profiles[1].Requirements[:1]
	victim.Compilation.Profiles[1].Implies[0] = "mutated.v1"
	victim.Policy = semantic.ProvenancePolicy(99)
	// Immutable semantic values expose only copying getters; mutate those
	// copies too so any aliasing regression in either package is caught.
	stateEntities := victim.InitialState.Entities()
	if len(stateEntities) > 0 {
		stateEntities[0] = semantic.Entity{}
	}
	victim.InitialState.CanonicalBytes()[0] ^= 0xff
	worldReferences := victim.World.References()
	_ = append(worldReferences, semantic.WorldReference{})

	fresh := mustInputs(t, teamhos.Passing)
	freshCompiled, err := semantic.Compile(fresh.Compilation)
	if err != nil {
		t.Fatalf("Compile fresh: %v", err)
	}
	freshPlan, ok := freshCompiled.Plan()
	if !ok {
		t.Fatal("fresh fixture did not compile after mutation of an earlier result")
	}
	if freshPlan.ID() != baselinePlan.ID() || !bytes.Equal(freshPlan.CanonicalBytes(), baselinePlan.CanonicalBytes()) {
		t.Fatal("mutating one Inputs changed a later call's plan")
	}
	freshProfiles := freshCompiled.Profiles()
	if len(freshProfiles) != len(baselineProfiles) {
		t.Fatalf("profiles=%d, want %d", len(freshProfiles), len(baselineProfiles))
	}
	for i := range freshProfiles {
		if freshProfiles[i].ID() != baselineProfiles[i].ID() {
			t.Fatal("mutating one Inputs changed a later call's compiled profile")
		}
	}
	if fresh.InitialState.Digest() != baseline.InitialState.Digest() {
		t.Fatal("mutating one Inputs changed a later call's initial state")
	}
	if fresh.World.ID() != baseline.World.ID() || fresh.Policy != semantic.ChangesProvenance {
		t.Fatal("mutating one Inputs changed a later call's world or policy")
	}
}

// Production break caught: the passing incident must walk the complete pure
// kernel lifecycle with the ratified verdicts at every boundary.
func TestDirectKernelLifecyclePassing(t *testing.T) {
	in := mustInputs(t, teamhos.Passing)
	binding, plan, cm, optimizer := mustBoundRun(t, in)
	_ = plan

	t1 := mustAcceptedTransition(t, binding, teamhos.RuleFormTeam, in.InitialState, semantic.NewJournal())
	c1 := mustSealed(t, semantic.SealRequest{
		Binding: binding, Checkpoint: teamhos.CheckpointTeamFormed,
		State: t1.State(), Journal: t1.Journal(), InvariantResults: t1.InvariantResults(),
	})
	assertVerdict(t, c1, t1.State(), cm, semantic.Ready, nil)
	assertVerdict(t, c1, t1.State(), optimizer, semantic.NeedsInput, []semantic.RequirementCode{
		semantic.TeamAggregationAnchorRequired,
		semantic.TeamDrivingDurationRequired,
		semantic.TeamElapsedDurationRequired,
	})

	t2 := mustAcceptedTransition(t, binding, teamhos.RuleAggregateTeamHOS, t1.State(), t1.Journal())
	team := mustTeamEntity(t, t2.State())
	assertStringField(t, team, "assignment_key", assignmentKey)
	assertAtomField(t, team, "aggregation_anchor", "T0")
	assertInt64Field(t, team, "elapsed_duration_hours", 10)
	assertInt64Field(t, team, "driving_duration_hours", 8)

	c2 := mustSealed(t, semantic.SealRequest{
		Binding: binding, Checkpoint: teamhos.CheckpointTeamHOSAggregated,
		State: t2.State(), Journal: t2.Journal(), InvariantResults: t2.InvariantResults(),
	})
	assertVerdict(t, c2, t2.State(), cm, semantic.Ready, nil)
	assertVerdict(t, c2, t2.State(), optimizer, semantic.Ready, nil)

	if got := journalRuleIDs(t2.Journal()); !slices.Equal(got, []semantic.RuleID{teamhos.RuleFormTeam, teamhos.RuleAggregateTeamHOS}) {
		t.Fatalf("accepted journal=%v", got)
	}
}

// Production break caught: an anchor mismatch that materialized a patch,
// grew the journal, disturbed sealed C1 bytes, or produced any C2 would
// break the ratified failed-suffix preservation contract.
func TestDirectKernelLifecycleAnchorMismatch(t *testing.T) {
	in := mustInputs(t, teamhos.AnchorMismatch)
	binding, _, cm, optimizer := mustBoundRun(t, in)

	t1 := mustAcceptedTransition(t, binding, teamhos.RuleFormTeam, in.InitialState, semantic.NewJournal())
	c1Request := semantic.SealRequest{
		Binding: binding, Checkpoint: teamhos.CheckpointTeamFormed,
		State: t1.State(), Journal: t1.Journal(), InvariantResults: t1.InvariantResults(),
	}
	c1 := mustSealed(t, c1Request)
	c1Bytes := c1.CanonicalBytes()
	cmBefore := mustAssessed(t, c1, t1.State(), cm)
	optimizerBefore := mustAssessed(t, c1, t1.State(), optimizer)
	if cmBefore.Verdict() != semantic.Ready || optimizerBefore.Verdict() != semantic.NeedsInput {
		t.Fatalf("C1 verdicts=(%s,%s)", cmBefore.Verdict(), optimizerBefore.Verdict())
	}
	journalBefore := journalEntryDigests(t1.Journal())

	t2, err := semantic.ExecuteTransition(binding, teamhos.RuleAggregateTeamHOS, t1.State(), t1.Journal())
	if err != nil {
		t.Fatalf("ExecuteTransition returned a Go error for a semantic rejection: %v", err)
	}
	failure, ok := t2.Failure()
	if !ok {
		t.Fatal("anchor mismatch was accepted")
	}
	if failure.Kind() != semantic.ProtectedInvariantFailed {
		t.Fatalf("failure kind=%s", failure.Kind())
	}
	if failure.InvariantCode() != semantic.HOSAnchorMismatch {
		t.Fatalf("invariant code=%s, want %s", failure.InvariantCode(), semantic.HOSAnchorMismatch)
	}
	if digest, present := failure.ProposedPatchDigest(); present {
		t.Fatalf("pre-patch rejection carries patch digest %s", digest)
	}
	if t2.HasPatch() {
		t.Fatal("rejected transition exposed a materialized patch")
	}
	if got := journalEntryDigests(t2.Journal()); !slices.Equal(got, journalBefore) {
		t.Fatalf("rejected T2 changed accepted journal: %v != %v", got, journalBefore)
	}
	if t2.State().Digest() != t1.State().Digest() {
		t.Fatal("rejected T2 changed the authoritative predecessor state")
	}

	// The sealed C1 claim, manifest bytes, and assessments must be exactly
	// reproducible after the failed suffix.
	c1After := mustSealed(t, c1Request)
	if c1After.ID() != c1.ID() || c1After.Digest() != c1.Digest() || !bytes.Equal(c1After.CanonicalBytes(), c1Bytes) {
		t.Fatal("C1 is not byte-identical after the failed suffix")
	}
	cmAfter := mustAssessed(t, c1After, t1.State(), cm)
	optimizerAfter := mustAssessed(t, c1After, t1.State(), optimizer)
	if cmAfter.Digest() != cmBefore.Digest() || !bytes.Equal(cmAfter.CanonicalBytes(), cmBefore.CanonicalBytes()) {
		t.Fatal("C1 CM assessment changed after the failed suffix")
	}
	if optimizerAfter.Digest() != optimizerBefore.Digest() || !bytes.Equal(optimizerAfter.CanonicalBytes(), optimizerBefore.CanonicalBytes()) {
		t.Fatal("C1 optimizer assessment changed after the failed suffix")
	}

	// No C2 exists: sealing the aggregated boundary over the T1-only prefix
	// must refuse without producing an artifact.
	c2Outcome, err := semantic.Seal(semantic.SealRequest{
		Binding: binding, Checkpoint: teamhos.CheckpointTeamHOSAggregated,
		State: t1.State(), Journal: t1.Journal(), InvariantResults: t1.InvariantResults(),
	})
	if err != nil {
		t.Fatalf("Seal C2: %v", err)
	}
	if c2Outcome.Sealed() {
		t.Fatal("C2 sealed after rejected T2")
	}
	if _, ok := c2Outcome.Artifact(); ok {
		t.Fatal("refused C2 exposed a partial artifact")
	}
}

// Production break caught: a production binary importing the fixture would
// smuggle the non-production max-reduction policy toward runtime meaning.
func TestProductionPackagesDoNotImportFixture(t *testing.T) {
	const fixtureImport = "github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	root := filepath.Join("..", "..", "..")
	for _, dir := range []string{"cmd", filepath.Join("internal", "httpapi"), filepath.Join("internal", "observability")} {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		walkErr := filepath.WalkDir(path, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(file, ".go") {
				return nil
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			for _, imported := range parsed.Imports {
				if strings.Trim(imported.Path.Value, `"`) == fixtureImport {
					t.Errorf("production file %s imports the team-HOS fixture package", file)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", path, walkErr)
		}
	}
}

func mustInputs(t *testing.T, variant teamhos.Variant) teamhos.Inputs {
	t.Helper()
	in, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("New(%d): %v", variant, err)
	}
	return in
}

func mustPlan(t *testing.T, request semantic.CompileRequest) semantic.Plan {
	t.Helper()
	compiled, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compiled.Plan()
	if !ok {
		failure, _ := compiled.Failure()
		t.Fatalf("fixture did not compile: %v", failure.Diagnostics())
	}
	return plan
}

func mustBoundRun(t *testing.T, in teamhos.Inputs) (semantic.RunBinding, semantic.Plan, semantic.CompiledProfile, semantic.CompiledProfile) {
	t.Helper()
	compiled, err := semantic.Compile(in.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compiled.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	profiles := compiled.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("profiles=%d, want 2", len(profiles))
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: in.InitialState, World: in.World,
		ExecutorIdentity: in.ExecutorIdentity, Policy: in.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return binding, plan, profiles[0], profiles[1]
}

func mustAcceptedTransition(t *testing.T, binding semantic.RunBinding, rule semantic.RuleID, state semantic.State, journal semantic.Journal) semantic.TransitionOutcome {
	t.Helper()
	outcome, err := semantic.ExecuteTransition(binding, rule, state, journal)
	if err != nil {
		t.Fatalf("ExecuteTransition(%s): %v", rule, err)
	}
	if failure, ok := outcome.Failure(); ok {
		t.Fatalf("ExecuteTransition(%s) rejected: %s", rule, failure.Code())
	}
	return outcome
}

func mustSealed(t *testing.T, request semantic.SealRequest) semantic.CheckpointArtifact {
	t.Helper()
	outcome, err := semantic.Seal(request)
	if err != nil {
		t.Fatalf("Seal(%s): %v", request.Checkpoint, err)
	}
	artifact, ok := outcome.Artifact()
	if !ok {
		failure, _ := outcome.Failure()
		t.Fatalf("Seal(%s) refused: %s", request.Checkpoint, failure.Code())
	}
	return artifact
}

func mustAssessed(t *testing.T, checkpoint semantic.CheckpointArtifact, state semantic.State, profile semantic.CompiledProfile) semantic.Assessment {
	t.Helper()
	outcome, err := semantic.Assess(semantic.AssessmentRequest{Checkpoint: checkpoint, State: state, Profile: profile})
	if err != nil {
		t.Fatalf("Assess(%s): %v", profile.Key(), err)
	}
	assessment, ok := outcome.Assessment()
	if !ok {
		failure, _ := outcome.Failure()
		t.Fatalf("Assess(%s) refused: %s", profile.Key(), failure.Code())
	}
	return assessment
}

func assertVerdict(t *testing.T, checkpoint semantic.CheckpointArtifact, state semantic.State, profile semantic.CompiledProfile, want semantic.ReadinessVerdict, wantMissing []semantic.RequirementCode) {
	t.Helper()
	assessment := mustAssessed(t, checkpoint, state, profile)
	if assessment.Verdict() != want {
		t.Fatalf("%s verdict=%s, want %s", profile.Key(), assessment.Verdict(), want)
	}
	missing := make([]semantic.RequirementCode, 0)
	for _, entity := range assessment.EntityResults() {
		for _, result := range entity.Results() {
			if !result.Satisfied() {
				missing = append(missing, result.Code())
			}
		}
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	if wantMissing == nil {
		if len(missing) != 0 {
			t.Fatalf("%s missing codes=%v, want none", profile.Key(), missing)
		}
		return
	}
	if !slices.Equal(missing, wantMissing) {
		t.Fatalf("%s missing codes=%v, want %v", profile.Key(), missing, wantMissing)
	}
}

func mustTeamEntity(t *testing.T, state semantic.State) semantic.Entity {
	t.Helper()
	var teams []semantic.Entity
	for _, entity := range state.Entities() {
		if entity.Ref().Kind == "team" {
			teams = append(teams, entity)
		}
	}
	if len(teams) != 1 {
		t.Fatalf("teams=%d, want exactly one", len(teams))
	}
	return teams[0]
}

func planRuleIDs(plan semantic.Plan) []semantic.RuleID {
	ids := make([]semantic.RuleID, 0, 2)
	for _, transformation := range plan.Transformations() {
		ids = append(ids, transformation.Declaration().ID)
	}
	return ids
}

func journalRuleIDs(journal semantic.Journal) []semantic.RuleID {
	ids := make([]semantic.RuleID, 0)
	for _, entry := range journal.Entries() {
		ids = append(ids, entry.RuleID())
	}
	return ids
}

func journalEntryDigests(journal semantic.Journal) []semantic.JournalEntryDigest {
	digests := make([]semantic.JournalEntryDigest, 0)
	for _, entry := range journal.Entries() {
		digests = append(digests, entry.Digest())
	}
	return digests
}

func requirementCodes(profile semantic.CompiledProfile) []semantic.RequirementCode {
	requirements := profile.Declaration().Requirements
	codes := make([]semantic.RequirementCode, 0, len(requirements))
	for _, requirement := range requirements {
		codes = append(codes, requirement.Code)
	}
	return codes
}

func assertStringField(t *testing.T, entity semantic.Entity, name semantic.FieldName, want string) {
	t.Helper()
	value, ok := entity.Field(name)
	if !ok {
		t.Fatalf("%v field %s absent", entity.Ref(), name)
	}
	got, ok := value.String()
	if !ok || value.Kind() != semantic.ValueString || got != want {
		t.Fatalf("%v field %s=%q (kind %d), want string %q", entity.Ref(), name, got, value.Kind(), want)
	}
}

func assertAtomField(t *testing.T, entity semantic.Entity, name semantic.FieldName, want string) {
	t.Helper()
	value, ok := entity.Field(name)
	if !ok {
		t.Fatalf("%v field %s absent", entity.Ref(), name)
	}
	got, ok := value.String()
	if !ok || value.Kind() != semantic.ValueAtom || got != want {
		t.Fatalf("%v field %s=%q (kind %d), want atom %q", entity.Ref(), name, got, value.Kind(), want)
	}
}

func assertInt64Field(t *testing.T, entity semantic.Entity, name semantic.FieldName, want int64) {
	t.Helper()
	value, ok := entity.Field(name)
	if !ok {
		t.Fatalf("%v field %s absent", entity.Ref(), name)
	}
	got, ok := value.Int64()
	if !ok || got != want {
		t.Fatalf("%v field %s=%d, want %d", entity.Ref(), name, got, want)
	}
}

func assertSameFields(t *testing.T, label string, left, right semantic.Entity, except []semantic.FieldName) {
	t.Helper()
	leftFields, rightFields := left.Fields(), right.Fields()
	if len(leftFields) != len(rightFields) {
		t.Fatalf("%s field counts differ: %d != %d", label, len(leftFields), len(rightFields))
	}
	for name, leftValue := range leftFields {
		rightValue, ok := rightFields[name]
		if !ok {
			t.Fatalf("%s field %s absent from second variant", label, name)
		}
		if slices.Contains(except, name) {
			if leftValue.Equal(rightValue) {
				t.Fatalf("%s field %s should differ across variants", label, name)
			}
			continue
		}
		if !leftValue.Equal(rightValue) {
			t.Fatalf("%s field %s differs across variants", label, name)
		}
	}
}
