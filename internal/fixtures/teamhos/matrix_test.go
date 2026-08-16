package teamhos_test

import (
	"slices"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// This file holds the constitutional matrix for the complete slice: one proof
// that non-semantic authoring order never reaches an identity, and one proof
// that every input which should reach an identity actually does.
//
// The two are complements. Determinism alone is satisfied by a constant, and
// sensitivity alone is satisfied by hashing the whole request; only together do
// they pin the canonical form to exactly the semantics it claims to cover.

// lifecycleIdentities is every identity the passing incident produces, in
// lifecycle order. Comparing whole values rather than a spot check means a new
// artifact identity cannot be added without deciding which column it belongs in.
type lifecycleIdentities struct {
	planID            semantic.PlanID
	profileIDs        []semantic.ProfileID
	worldID           semantic.WorldID
	inputID           semantic.InputID
	runID             semantic.SemanticRunID
	executionID       semantic.ExecutionID
	stateDigests      []semantic.StateDigest
	patchDigests      []semantic.PatchDigest
	prefixDigests     []semantic.JournalPrefixDigest
	checkpointIDs     []semantic.CheckpointArtifactID
	checkpointDigests []semantic.CheckpointArtifactDigest
	assessmentIDs     []semantic.AssessmentID
	assessmentDigests []semantic.AssessmentDigest
	verdicts          []semantic.ReadinessVerdict
}

// replayable is everything that must survive a change of executor: the
// certified meaning of the run, excluding who computed it.
func (i lifecycleIdentities) replayable() lifecycleIdentities {
	replay := i
	replay.executionID = ""
	return replay
}

func (i lifecycleIdentities) equal(other lifecycleIdentities) bool {
	return i.planID == other.planID && i.worldID == other.worldID &&
		i.inputID == other.inputID && i.runID == other.runID &&
		i.executionID == other.executionID &&
		slices.Equal(i.profileIDs, other.profileIDs) &&
		slices.Equal(i.stateDigests, other.stateDigests) &&
		slices.Equal(i.patchDigests, other.patchDigests) &&
		slices.Equal(i.prefixDigests, other.prefixDigests) &&
		slices.Equal(i.checkpointIDs, other.checkpointIDs) &&
		slices.Equal(i.checkpointDigests, other.checkpointDigests) &&
		slices.Equal(i.assessmentIDs, other.assessmentIDs) &&
		slices.Equal(i.assessmentDigests, other.assessmentDigests) &&
		slices.Equal(i.verdicts, other.verdicts)
}

// driveLifecycle walks the complete passing lifecycle through the public pure
// APIs and collects every identity it produces.
func driveLifecycle(t *testing.T, in teamhos.Inputs) lifecycleIdentities {
	t.Helper()
	binding, plan, cm, optimizer := mustBoundRun(t, in)

	collected := lifecycleIdentities{
		planID:       plan.ID(),
		profileIDs:   []semantic.ProfileID{cm.ID(), optimizer.ID()},
		worldID:      binding.WorldID(),
		inputID:      binding.InputID(),
		runID:        binding.SemanticRunID(),
		executionID:  binding.ExecutionID(),
		stateDigests: []semantic.StateDigest{in.InitialState.Digest()},
	}

	state, journal := in.InitialState, semantic.NewJournal()
	for _, step := range []struct {
		rule       semantic.RuleID
		checkpoint semantic.CheckpointKey
	}{
		{teamhos.RuleFormTeam, teamhos.CheckpointTeamFormed},
		{teamhos.RuleAggregateTeamHOS, teamhos.CheckpointTeamHOSAggregated},
	} {
		outcome := mustAcceptedTransition(t, binding, step.rule, state, journal)
		state, journal = outcome.State(), outcome.Journal()
		collected.stateDigests = append(collected.stateDigests, state.Digest())
		collected.patchDigests = append(collected.patchDigests, outcome.Patch().Digest())
		collected.prefixDigests = append(collected.prefixDigests, journal.PrefixDigest(binding))

		artifact := mustSealed(t, semantic.SealRequest{
			Binding: binding, Checkpoint: step.checkpoint, State: state,
			Journal: journal, InvariantResults: outcome.InvariantResults(),
		})
		collected.checkpointIDs = append(collected.checkpointIDs, artifact.ID())
		collected.checkpointDigests = append(collected.checkpointDigests, artifact.Digest())

		for _, profile := range []semantic.CompiledProfile{cm, optimizer} {
			assessment := mustAssessed(t, artifact, state, profile)
			collected.assessmentIDs = append(collected.assessmentIDs, assessment.ID())
			collected.assessmentDigests = append(collected.assessmentDigests, assessment.Digest())
			collected.verdicts = append(collected.verdicts, assessment.Verdict())
		}
	}
	return collected
}

// Production break caught: any authored order leaking into a canonical
// encoding would make two semantically identical programs produce different
// artifact identities, so replay and cross-backend comparison would compare
// accidents of authoring rather than meaning.
func TestLifecycleIdentityIgnoresCombinedAuthoringOrder(t *testing.T) {
	natural := driveLifecycle(t, mustInputs(t, teamhos.Passing))
	shuffled := driveLifecycle(t, reversedAuthoringOrder(t, mustInputs(t, teamhos.Passing)))

	if !natural.equal(shuffled) {
		t.Fatalf("authoring order changed the lifecycle:\nnatural  = %+v\nshuffled = %+v", natural, shuffled)
	}
	// A shuffle that silently produced nothing would compare two empty values.
	if len(natural.checkpointIDs) != 2 || len(natural.assessmentIDs) != 4 || len(natural.patchDigests) != 2 {
		t.Fatalf("lifecycle did not run to completion: %+v", natural)
	}
	if !slices.Equal(natural.verdicts, []semantic.ReadinessVerdict{
		semantic.Ready, semantic.NeedsInput, semantic.Ready, semantic.Ready,
	}) {
		t.Fatalf("verdicts=%v", natural.verdicts)
	}
}

// reversedAuthoringOrder reverses every sequence whose order the design
// declares non-semantic: schema declarations, rules, checkpoints, declared
// accesses, the explicit source pair, profiles, requirement atoms, and the
// initial state's entities and relations.
func reversedAuthoringOrder(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()

	entities := slices.Clone(in.Compilation.Schema.EntityDeclarations())
	slices.Reverse(entities)
	for i := range entities {
		fields := slices.Clone(entities[i].Fields)
		slices.Reverse(fields)
		entities[i].Fields = fields
	}
	relations := slices.Clone(in.Compilation.Schema.RelationDeclarations())
	slices.Reverse(relations)
	schema, err := semantic.NewSchema(entities, relations)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	in.Compilation.Schema = schema.Declaration()

	transformations := slices.Clone(in.Compilation.Rules.Transformations)
	slices.Reverse(transformations)
	for i := range transformations {
		reads := slices.Clone(transformations[i].DeclaredReads)
		slices.Reverse(reads)
		transformations[i].DeclaredReads = reads
		writes := slices.Clone(transformations[i].DeclaredWrites)
		slices.Reverse(writes)
		transformations[i].DeclaredWrites = writes
		if form := transformations[i].Form; form != nil {
			sources := slices.Clone(form.Sources)
			slices.Reverse(sources)
			reversed := *form
			reversed.Sources = sources
			transformations[i].Form = &reversed
		}
	}
	in.Compilation.Rules.Transformations = transformations

	checkpoints := slices.Clone(in.Compilation.Rules.Checkpoints)
	slices.Reverse(checkpoints)
	in.Compilation.Rules.Checkpoints = checkpoints

	profiles := slices.Clone(in.Compilation.Profiles)
	slices.Reverse(profiles)
	for i := range profiles {
		requirements := slices.Clone(profiles[i].Requirements)
		slices.Reverse(requirements)
		profiles[i].Requirements = requirements
	}
	in.Compilation.Profiles = profiles

	stateEntities := slices.Clone(in.InitialState.Entities())
	slices.Reverse(stateEntities)
	stateRelations := slices.Clone(in.InitialState.Relations())
	slices.Reverse(stateRelations)
	state, err := semantic.NewState(in.InitialState.Schema(), in.InitialState.InputLineageID(),
		stateEntities, stateRelations)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	in.InitialState = state
	return in
}

// Production break caught: an input that should change an identity but does
// not would let two genuinely different runs collide on one artifact ID, and an
// input that should not change one but does would break replay. Determinism
// tests cannot catch either direction on their own.
func TestLifecycleIdentitySensitivityMatrix(t *testing.T) {
	baseline := driveLifecycle(t, mustInputs(t, teamhos.Passing))

	tests := []struct {
		name string
		// mutate returns an input differing in exactly one dimension.
		mutate func(*testing.T, teamhos.Inputs) teamhos.Inputs
		// expect describes which identities must differ from the baseline.
		expectPlanChanged        bool
		expectProfilesChanged    bool
		expectStateChanged       bool
		expectRunChanged         bool
		expectCheckpointsChanged bool
	}{
		{
			name:                     "lineage",
			mutate:                   withLineage,
			expectStateChanged:       true,
			expectRunChanged:         true,
			expectCheckpointsChanged: true,
		},
		{
			name:                     "observation",
			mutate:                   withChangedObservation,
			expectStateChanged:       true,
			expectRunChanged:         true,
			expectCheckpointsChanged: true,
		},
		{
			name:                     "world",
			mutate:                   withPinnedWorldReference,
			expectRunChanged:         true,
			expectCheckpointsChanged: true,
		},
		{
			name:                     "plan",
			mutate:                   withChangedOutputSlot,
			expectPlanChanged:        true,
			expectRunChanged:         true,
			expectCheckpointsChanged: true,
		},
		{
			name:                  "profile",
			mutate:                withRelaxedOptimizerProfile,
			expectProfilesChanged: true,
		},
		// The provenance policy dimension has no row. ProvenancePolicy is a
		// closed union whose only valid value in this slice is changes.v1, so
		// there is no second lawful value to vary. The policy is encoded into
		// the run and checkpoint identities by construction, and BindRun
		// rejects an invalid policy outright; a row here could only be written
		// by inventing a policy the design has not ratified. Add the row when a
		// second policy is ratified, not before.
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := driveLifecycle(t, test.mutate(t, mustInputs(t, teamhos.Passing)))

			if changed := got.planID != baseline.planID; changed != test.expectPlanChanged {
				t.Errorf("plan identity changed=%t, want %t", changed, test.expectPlanChanged)
			}
			if changed := !slices.Equal(got.profileIDs, baseline.profileIDs); changed != test.expectProfilesChanged {
				t.Errorf("profile identities changed=%t, want %t", changed, test.expectProfilesChanged)
			}
			if changed := !slices.Equal(got.stateDigests, baseline.stateDigests); changed != test.expectStateChanged {
				t.Errorf("state digests changed=%t, want %t", changed, test.expectStateChanged)
			}
			if changed := got.runID != baseline.runID; changed != test.expectRunChanged {
				t.Errorf("semantic run identity changed=%t, want %t", changed, test.expectRunChanged)
			}
			if changed := !slices.Equal(got.checkpointIDs, baseline.checkpointIDs); changed != test.expectCheckpointsChanged {
				t.Errorf("checkpoint identities changed=%t, want %t", changed, test.expectCheckpointsChanged)
			}
			// Whenever a checkpoint identity moves, its content digest must move
			// with it: one ID resolving to two digests is the integrity failure
			// the whole sealing contract exists to prevent.
			if idsChanged := !slices.Equal(got.checkpointIDs, baseline.checkpointIDs); idsChanged {
				if slices.Equal(got.checkpointDigests, baseline.checkpointDigests) {
					t.Error("checkpoint IDs changed while their content digests did not")
				}
			}
			// AssessmentID is derived from exactly the checkpoint artifact and
			// the profile, so it must move when either moves and stay put when
			// neither does. This catches a readiness answer silently reattached
			// to the wrong checkpoint or profile.
			wantAssessmentsChanged := test.expectCheckpointsChanged || test.expectProfilesChanged
			if changed := !slices.Equal(got.assessmentIDs, baseline.assessmentIDs); changed != wantAssessmentsChanged {
				t.Errorf("assessment identities changed=%t, want %t", changed, wantAssessmentsChanged)
			}
		})
	}
}

// Production break caught: letting executor identity reach any artifact other
// than ExecutionID would make two certified backends unable to produce the same
// checkpoint for one semantic run, which is the whole point of the split.
func TestLifecycleIdentityExcludesExecutorFromEverythingButExecutionID(t *testing.T) {
	baseline := driveLifecycle(t, mustInputs(t, teamhos.Passing))

	other := mustInputs(t, teamhos.Passing)
	identity, err := semantic.NewExecutorIdentity("go",
		"sha256:3d1c8f2b6a5e4d7c9b0a1f2e3d4c5b6a7988776655443322110ffeeddccbbaa9")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	other.ExecutorIdentity = identity
	changed := driveLifecycle(t, other)

	if changed.executionID == baseline.executionID {
		t.Fatal("a different executor produced the same ExecutionID")
	}
	if !changed.replayable().equal(baseline.replayable()) {
		t.Fatalf("executor identity leaked into replayable identity:\nbaseline = %+v\nchanged  = %+v",
			baseline.replayable(), changed.replayable())
	}
}

// Production break caught: an attempt counter reaching canonical identity would
// make a retried run produce different artifacts for identical meaning. This
// slice has no AttemptID at all, which is the strongest possible form of the
// exclusion; the test pins that absence so reintroducing the concept must be a
// deliberate decision reviewed against this contract.
func TestNoAttemptConceptExistsInTheSemanticSurface(t *testing.T) {
	in := mustInputs(t, teamhos.Passing)
	binding, _, _, _ := mustBoundRun(t, in)

	// Repeating the identical run is the observable meaning of a second
	// attempt: every identity, including ExecutionID, must be reproduced.
	first := driveLifecycle(t, in)
	second := driveLifecycle(t, mustInputs(t, teamhos.Passing))
	if !first.equal(second) {
		t.Fatalf("re-running identical inputs produced different identities:\nfirst  = %+v\nsecond = %+v", first, second)
	}
	if binding.ExecutionID() != first.executionID {
		t.Fatal("binding and lifecycle disagree on ExecutionID for identical inputs")
	}
}

func withLineage(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()
	lineage, err := semantic.NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab-second-load")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	// Entity IDs are lineage-derived, so the entities must be rebuilt rather
	// than carried across: reusing them would test nothing.
	entities := make([]semantic.Entity, 0, 2)
	for _, entity := range in.InitialState.Entities() {
		ref := entity.Ref()
		rebuilt, err := semantic.NewEntity(semantic.EntityRef{
			Kind: ref.Kind,
			ID:   semantic.SourceEntityID(lineage, ref.Kind, sourceKeyForRebuild(t, in, ref)),
		}, entity.Fields())
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		entities = append(entities, rebuilt)
	}
	state, err := semantic.NewState(in.InitialState.Schema(), lineage, entities, in.InitialState.Relations())
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	in.InitialState = state
	return in
}

// sourceKeyForRebuild recovers which ratified source key produced an entity by
// matching its lineage-derived ID, so a rebuilt state keeps the same drivers.
func sourceKeyForRebuild(t *testing.T, in teamhos.Inputs, ref semantic.EntityRef) string {
	t.Helper()
	lineage := in.InitialState.InputLineageID()
	for _, key := range []string{sourceKeyA, sourceKeyB} {
		if semantic.SourceEntityID(lineage, ref.Kind, key) == ref.ID {
			return key
		}
	}
	t.Fatalf("entity %v does not correspond to a ratified source key", ref)
	return ""
}

func withChangedObservation(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()
	entities := slices.Clone(in.InitialState.Entities())
	replaced := false
	for i, entity := range entities {
		elapsed, ok := entity.Field("hos_elapsed_hours")
		if !ok {
			continue
		}
		if value, present := elapsed.Int64(); !present || value != 7 {
			continue
		}
		fields := entity.Fields()
		// Driver B observes nine elapsed hours instead of seven. The tuple stays
		// lawful, so the run still commits; only its meaning differs.
		fields["hos_elapsed_hours"] = semantic.NewInt64Value(9)
		rebuilt, err := semantic.NewEntity(entity.Ref(), fields)
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		entities[i], replaced = rebuilt, true
	}
	if !replaced {
		t.Fatal("fixture no longer contains the expected driver B hos_elapsed_hours observation")
	}
	state, err := semantic.NewState(in.InitialState.Schema(), in.InitialState.InputLineageID(),
		entities, in.InitialState.Relations())
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	in.InitialState = state
	return in
}

func withPinnedWorldReference(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()
	reference, err := semantic.NewWorldReference(semantic.WorldReferenceConfiguration,
		"sha256:1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("NewWorldReference: %v", err)
	}
	world, err := semantic.NewWorld([]semantic.WorldReference{reference})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	in.World = world
	return in
}

func withChangedOutputSlot(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()
	transformations := slices.Clone(in.Compilation.Rules.Transformations)
	changed := false
	for i := range transformations {
		if transformations[i].Form == nil {
			continue
		}
		form := *transformations[i].Form
		form.OutputSlot = "formed_team_slot.v1"
		transformations[i].Form = &form
		changed = true
	}
	if !changed {
		t.Fatal("fixture no longer declares a form transformation")
	}
	for i := range transformations {
		if transformations[i].Aggregate == nil {
			continue
		}
		aggregate := *transformations[i].Aggregate
		aggregate.Target.Slot = "formed_team_slot.v1"
		transformations[i].Aggregate = &aggregate
	}
	in.Compilation.Rules.Transformations = transformations
	return in
}

func withRelaxedOptimizerProfile(t *testing.T, in teamhos.Inputs) teamhos.Inputs {
	t.Helper()
	profiles := slices.Clone(in.Compilation.Profiles)
	relaxed := false
	for i := range profiles {
		if profiles[i].Key != teamhos.ProfileOptimizer {
			continue
		}
		// Dropping the driving-duration requirement keeps optimizer strictly
		// above cm, so profile ordering stays provable and only the profile
		// identity moves.
		requirements := make([]semantic.RequirementAtom, 0, len(profiles[i].Requirements))
		for _, atom := range profiles[i].Requirements {
			if atom.Code == semantic.TeamDrivingDurationRequired {
				continue
			}
			requirements = append(requirements, atom)
		}
		profiles[i].Requirements, relaxed = requirements, true
	}
	if !relaxed {
		t.Fatal("fixture no longer declares the optimizer profile")
	}
	in.Compilation.Profiles = profiles
	return in
}
