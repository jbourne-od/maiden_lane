package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

// Production break caught: an assessor that inspected raw HOS fields, dropped
// per-entity evidence, or collapsed CM and optimizer would break the ratified
// consumer-relative readiness contract at C1.
func TestAssessC1ForCMAndOptimizer(t *testing.T) {
	c1, state, cm, optimizer := readinessFixtureC1(t)
	cmOutcome, err := Assess(AssessmentRequest{Checkpoint: c1, State: state, Profile: cm})
	if err != nil {
		t.Fatalf("Assess CM: %v", err)
	}
	cmAssessment := mustAssessment(t, cmOutcome)
	if got := cmAssessment.Verdict(); got != Ready {
		t.Fatalf("CM=%s", got)
	}
	cmResults := cmAssessment.EntityResults()
	if len(cmResults) != 2 || len(cmResults[0].Results()) != 2 {
		t.Fatalf("CM entity results=%v", cmResults)
	}
	if result := cmResults[0].Results()[0]; !result.Satisfied() || result.Code() != "DriverAssignmentRequired" || len(result.FactRefs()) != 1 {
		t.Fatalf("CM satisfied result=%v", result)
	}

	optimizerOutcome, err := Assess(AssessmentRequest{Checkpoint: c1, State: state, Profile: optimizer})
	if err != nil {
		t.Fatalf("Assess optimizer: %v", err)
	}
	assessment := mustAssessment(t, optimizerOutcome)
	if assessment.Verdict() != NeedsInput {
		t.Fatalf("optimizer=%s", assessment.Verdict())
	}
	assertMissingRequirementCodes(t, assessment,
		"DriverDrivingDurationRequired", "DriverElapsedDurationRequired", "DriverReconciledAnchorRequired")
	if assessment.CheckpointArtifactID() != c1.ID() || assessment.ProfileID() != optimizer.ID() {
		t.Fatal("assessment does not link its exact checkpoint/profile question")
	}
	if cmAssessment.ID() == assessment.ID() {
		t.Fatal("distinct profiles produced one assessment identity")
	}
}

// Production break caught: a passing T2 that still reported optimizer
// needs_input would block promotion evidence for a lawful complete C2.
func TestAssessC2ReadyForBothProfiles(t *testing.T) {
	fixture := newReadinessFixture(t)
	for _, profile := range []CompiledProfile{fixture.cm, fixture.optimizer} {
		outcome, err := Assess(AssessmentRequest{Checkpoint: fixture.c2Artifact, State: fixture.c2.State(), Profile: profile})
		if err != nil {
			t.Fatalf("Assess %s: %v", profile.Key(), err)
		}
		assessment := mustAssessment(t, outcome)
		if assessment.Verdict() != Ready {
			t.Fatalf("%s verdict=%s, want %s", profile.Key(), assessment.Verdict(), Ready)
		}
		for _, entity := range assessment.EntityResults() {
			for _, result := range entity.Results() {
				if !result.Satisfied() {
					t.Fatalf("%s left %s unsatisfied at C2", profile.Key(), result.Code())
				}
			}
		}
	}
}

// Production break caught: an assessment that changed state bytes, accepted
// history, or the sealed manifest would transform instead of assess
// (Inviolate 19) and would corrupt the immutable artifact spine.
func TestAssessMutatesNoSemanticArtifact(t *testing.T) {
	fixture := newReadinessFixture(t)
	state := fixture.c1.State()
	journal := fixture.c1.Journal()
	stateBefore := state.CanonicalBytes()
	prefixBefore := journal.PrefixDigest(fixture.binding)
	checkpointBefore := fixture.c1Artifact.CanonicalBytes()

	for _, profile := range []CompiledProfile{fixture.cm, fixture.optimizer} {
		if _, err := Assess(AssessmentRequest{Checkpoint: fixture.c1Artifact, State: state, Profile: profile}); err != nil {
			t.Fatalf("Assess %s: %v", profile.Key(), err)
		}
	}

	if !bytes.Equal(state.CanonicalBytes(), stateBefore) {
		t.Fatal("assessment changed state canonical bytes")
	}
	if journal.PrefixDigest(fixture.binding) != prefixBefore || len(journal.Entries()) != 1 {
		t.Fatal("assessment changed accepted journal history")
	}
	if !bytes.Equal(fixture.c1Artifact.CanonicalBytes(), checkpointBefore) {
		t.Fatal("assessment changed sealed checkpoint bytes")
	}
}

// Production break caught: dropping a failing selected team from the results
// would let universal aggregation silently omit a non-ready in-scope entity
// (Inviolate 19).
func TestAssessCannotOmitSecondIncompleteTeam(t *testing.T) {
	fixture := newReadinessFixture(t)
	complete := fixture.c2.State()
	incompleteRef := EntityRef{Kind: "driver", ID: SourceEntityID(complete.InputLineageID(), "driver", "third")}
	incomplete := mustEntity(t, incompleteRef.Kind, incompleteRef.ID, map[FieldName]Value{
		"assignment_key": mustString(t, "Y"),
	})
	state, err := NewState(complete.Schema(), complete.InputLineageID(), append(complete.Entities(), incomplete), complete.Relations())
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	outcome, err := Assess(AssessmentRequest{Checkpoint: syntheticCheckpointOverState(t, state), State: state, Profile: fixture.optimizer})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	assessment := mustAssessment(t, outcome)
	if assessment.Verdict() != Ready && assessment.Verdict() != NeedsInput {
		t.Fatalf("verdict=%q is outside the closed vocabulary", assessment.Verdict())
	}
	if assessment.Verdict() != NeedsInput {
		t.Fatal("incomplete selected driver did not force needs_input")
	}
	entities := assessment.EntityResults()
	if len(entities) != 3 {
		t.Fatalf("selected entities=%d, want every driver in scope", len(entities))
	}
	var found bool
	for _, entity := range entities {
		if entity.Entity() != incompleteRef {
			for _, result := range entity.Results() {
				if !result.Satisfied() {
					t.Fatalf("complete driver %v reported missing %s", entity.Entity(), result.Code())
				}
			}
			continue
		}
		found = true
		missing := missingCodes(entity.Results())
		want := []RequirementCode{"DriverAssignmentStatusRequired", "DriverDrivingDurationRequired", "DriverElapsedDurationRequired", "DriverReconciledAnchorRequired"}
		if !slices.Equal(missing, want) {
			t.Fatalf("incomplete driver missing=%v, want %v", missing, want)
		}
	}
	if !found {
		t.Fatal("non-ready in-scope driver was dropped from the assessment")
	}
}

// Production break caught: classifying an established-run deterministic
// content or link defect as a Go error (or vice versa) would either discard
// typed integrity evidence or manufacture assessments for malformed inputs.
func TestAssessLinkDefectClassification(t *testing.T) {
	fixture := newReadinessFixture(t)
	valid, err := Assess(AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: fixture.cm})
	if err != nil {
		t.Fatalf("Assess valid: %v", err)
	}
	validAssessment := mustAssessment(t, valid)
	conflict := validAssessment
	conflict.canonical = append(conflict.CanonicalBytes(), 0xff)
	conflict.digest = AssessmentDigest(canonicalDigest(conflict.canonical))

	foreignProfile := goldenSchemaProfile(t)

	typedTests := []struct {
		name    string
		request AssessmentRequest
		code    IntegrityCode
		kind    ArtifactKind
	}{
		{name: "corrupted checkpoint manifest bytes", code: ArtifactDigestMismatch, kind: ArtifactCheckpoint,
			request: AssessmentRequest{Checkpoint: corruptManifestCheckpoint(fixture.c1Artifact), State: fixture.c1.State(), Profile: fixture.cm}},
		{name: "corrupted checkpoint claim identity", code: ArtifactDigestMismatch, kind: ArtifactCheckpoint,
			request: AssessmentRequest{Checkpoint: corruptIDCheckpoint(fixture.c1Artifact), State: fixture.c1.State(), Profile: fixture.cm}},
		{name: "state does not match checkpoint state digest", code: ArtifactLinkInconsistent, kind: ArtifactState,
			request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c2.State(), Profile: fixture.cm}},
		{name: "corrupted state content", code: ArtifactDigestMismatch, kind: ArtifactState,
			request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: corruptStateCopy(fixture.c1.State()), Profile: fixture.cm}},
		{name: "profile pinned to different schema", code: ArtifactLinkInconsistent, kind: ArtifactCompiledProfile,
			request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: foreignProfile}},
		{name: "corrupted profile content", code: ArtifactDigestMismatch, kind: ArtifactCompiledProfile,
			request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: corruptProfileCopy(fixture.cm)}},
		{name: "one assessment ID with two digests", code: AssessmentIdentityConflict, kind: ArtifactReadinessAssessment,
			request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: fixture.cm, KnownAssessments: []Assessment{conflict}}},
	}
	for _, test := range typedTests {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := Assess(test.request)
			if err != nil {
				t.Fatalf("established-run defect escaped as Go error: %v", err)
			}
			assertAssessIntegrity(t, outcome, test.code, test.kind)
			if _, ok := outcome.Assessment(); ok {
				t.Fatal("typed integrity failure still produced an assessment identity")
			}
		})
	}

	malformedProfile := fixture.cm
	malformedProfile.declaration.Scope.Kind = ProfileScopeKind(99)
	errorTests := []struct {
		name    string
		request AssessmentRequest
	}{
		{name: "uninitialized checkpoint", request: AssessmentRequest{State: fixture.c1.State(), Profile: fixture.cm}},
		{name: "uninitialized state", request: AssessmentRequest{Checkpoint: fixture.c1Artifact, Profile: fixture.cm}},
		{name: "uninitialized profile", request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State()}},
		{name: "unsupported profile scope kind", request: AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: malformedProfile}},
	}
	for _, test := range errorTests {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := Assess(test.request)
			if err == nil {
				t.Fatal("malformed non-established input did not return a Go error")
			}
			if _, ok := outcome.Assessment(); ok || outcome.Assessed() {
				t.Fatal("Go-error path produced an assessment identity")
			}
			if _, ok := outcome.Failure(); ok {
				t.Fatal("Go-error path produced a typed semantic failure")
			}
		})
	}
}

// Production break caught: retaining caller-held or getter-returned slices
// would let an already published assessment mutate beneath its digest.
func TestAssessmentImmutability(t *testing.T) {
	fixture := newReadinessFixture(t)
	known := []Assessment{mustAssess(t, fixture.c1Artifact, fixture.c1.State(), fixture.cm)}
	outcome, err := Assess(AssessmentRequest{Checkpoint: fixture.c1Artifact, State: fixture.c1.State(), Profile: fixture.optimizer, KnownAssessments: known})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	assessment := mustAssessment(t, outcome)
	wantBytes, wantDigest, wantID := assessment.CanonicalBytes(), assessment.Digest(), assessment.ID()

	// Mutate the caller-owned constructor input and every getter result.
	known[0] = Assessment{}
	returnedBytes := assessment.CanonicalBytes()
	returnedBytes[0] ^= 0xff
	entities := assessment.EntityResults()
	if len(entities) == 0 {
		t.Fatal("assessment has no entity results to mutate")
	}
	results := entities[0].Results()
	results[0] = RequirementResult{}
	facts := entities[0].Results()[1].FactRefs()
	if len(facts) > 0 {
		facts[0] = FactRef{}
	}
	entities[0] = EntityAssessment{}

	if assessment.Digest() != wantDigest || assessment.ID() != wantID || !bytes.Equal(assessment.CanonicalBytes(), wantBytes) {
		t.Fatal("caller mutation changed assessment canonical identity")
	}
	fresh := assessment.EntityResults()
	if len(fresh) == 0 || fresh[0].Entity() == (EntityRef{}) {
		t.Fatal("getter mutation leaked into internal assessment results")
	}
	if fresh[0].Results()[0].Code() == "" {
		t.Fatal("mutating a returned result slice changed the assessment")
	}
}

// Production break caught: an optimizer profile that could be ready while CM
// is not would falsify the compiler-proved implication ordering. Bounded
// exhaustive generation over every present/absent combination of the four
// optimizer-required team fields (single teams and every two-team pair).
func TestAssessOptimizerReadyImpliesCMReady(t *testing.T) {
	fixture := newReadinessFixture(t)
	schema := compileFixtureSchema(t, false)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-implication")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	fieldNames := []FieldName{"assignment_key", "reconciled_anchor", "elapsed_duration_hours", "driving_duration_hours"}
	fieldValues := map[FieldName]Value{
		"assignment_key":         mustString(t, "X"),
		"reconciled_anchor":      mustAtom(t, "T0"),
		"elapsed_duration_hours": NewInt64Value(10),
		"driving_duration_hours": NewInt64Value(8),
	}
	driverWithFields := func(key string, mask int) Entity {
		fields := make(map[FieldName]Value)
		for bit, name := range fieldNames {
			if mask&(1<<bit) != 0 {
				fields[name] = fieldValues[name]
			}
		}
		return mustEntity(t, "driver", SourceEntityID(lineage, "driver", key), fields)
	}

	assessVerdict := func(state State, profile CompiledProfile) ReadinessVerdict {
		return mustAssess(t, syntheticCheckpointOverState(t, state), state, profile).Verdict()
	}
	checkState := func(entities []Entity) {
		state, err := NewState(schema, lineage, entities, nil)
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		optimizerVerdict := assessVerdict(state, fixture.optimizer)
		cmVerdict := assessVerdict(state, fixture.cm)
		if optimizerVerdict == Ready && cmVerdict != Ready {
			t.Fatalf("Ready(optimizer) did not imply Ready(cm) for entities=%v", entities)
		}
	}

	for mask := 0; mask < 1<<len(fieldNames); mask++ {
		checkState([]Entity{driverWithFields("solo", mask)})
		for second := 0; second < 1<<len(fieldNames); second++ {
			checkState([]Entity{driverWithFields("first", mask), driverWithFields("second", second)})
		}
	}
}

// Production break caught: entity insertion order or the order of the known
// assessment set entering identity would break deterministic replay of the
// same semantic question and answer.
func TestAssessmentIdentityIgnoresConstructionOrder(t *testing.T) {
	fixture := newReadinessFixture(t)
	schema := compileFixtureSchema(t, false)
	state := fixture.c2.State()
	forward, err := NewState(schema, state.InputLineageID(), state.Entities(), state.Relations())
	if err != nil {
		t.Fatalf("NewState forward: %v", err)
	}
	reversedEntities := state.Entities()
	slices.Reverse(reversedEntities)
	reversedRelations := state.Relations()
	slices.Reverse(reversedRelations)
	backward, err := NewState(schema, state.InputLineageID(), reversedEntities, reversedRelations)
	if err != nil {
		t.Fatalf("NewState backward: %v", err)
	}

	first := mustAssess(t, syntheticCheckpointOverState(t, forward), forward, fixture.optimizer)
	cmKnown := mustAssess(t, syntheticCheckpointOverState(t, forward), forward, fixture.cm)
	knownForward := []Assessment{cmKnown, first}
	knownBackward := []Assessment{first, cmKnown}

	outcome, err := Assess(AssessmentRequest{Checkpoint: syntheticCheckpointOverState(t, backward), State: backward, Profile: fixture.optimizer, KnownAssessments: knownBackward})
	if err != nil {
		t.Fatalf("Assess backward: %v", err)
	}
	second := mustAssessment(t, outcome)
	outcome, err = Assess(AssessmentRequest{Checkpoint: syntheticCheckpointOverState(t, forward), State: forward, Profile: fixture.optimizer, KnownAssessments: knownForward})
	if err != nil {
		t.Fatalf("Assess forward: %v", err)
	}
	third := mustAssessment(t, outcome)

	if first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatal("entity insertion order changed assessment identity")
	}
	if first.ID() != third.ID() || first.Digest() != third.Digest() {
		t.Fatal("known-assessment order changed assessment identity")
	}
}

// Production break caught: changing the v1 assessment tag, field order,
// verdict token, result marker, evidence layout, or identity formula without
// a version migration would silently rename existing assessment artifacts.
//
// Every expected literal below was constructed INDEPENDENTLY of the
// production encoder: a one-off python3 script built purely from the
// documented v1 encoding tables in canonical.go computed these hex/digest
// values, which are hard-coded here and never regenerated from Go output.
func TestAssessmentCanonicalVectors(t *testing.T) {
	schema, profile := goldenSchemaAndProfile(t)
	lineage, err := NewInputLineageID("maiden-lane.readiness-vector", "assessment")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	if lineage != "sha256:e5f320f586e0c37cb0aedfa7112fca00280ef26a5b7aa5897979a1d17ce85ad5" {
		t.Fatalf("vector lineage drifted: %s", lineage)
	}
	entityID := SourceEntityID(lineage, "b", "1")
	if entityID != "sha256:4d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e1" {
		t.Fatalf("vector entity ID drifted: %s", entityID)
	}

	tests := []struct {
		name          string
		fields        map[FieldName]Value
		stateDigest   StateDigest
		idHex         string
		assessmentID  AssessmentID
		assessmentHex string
		digest        AssessmentDigest
		verdict       ReadinessVerdict
	}{
		{
			name:          "ready",
			fields:        map[FieldName]Value{"g": mustString(t, "X"), "x": mustAtom(t, "T0")},
			stateDigest:   "sha256:3f782780c51b15f1e5d5fe39c7595d80857c93258de2b4949bc98a643acd5817",
			idHex:         "000000000000001c6d616964656e2d6c616e652e6173736573736d656e742d69642e76316224a7a204464c7491ea7009787b3e53cbc61da7c905f32490e3b478b86ed7dcab63b577f75697ab6be236751d891d2840eb0a158fbc693a75eda259772a511b",
			assessmentID:  "sha256:c96e7fe621026311241a49d3c5ccd564506a53cc1114007f15cd90a7abb63ab5",
			assessmentHex: "00000000000000236d616964656e2d6c616e652e72656164696e6573732d6173736573736d656e742e763100000000000000236d616964656e2d6c616e652e6173736573736d656e742d73656d616e746963732e76316224a7a204464c7491ea7009787b3e53cbc61da7c905f32490e3b478b86ed7dcab63b577f75697ab6be236751d891d2840eb0a158fbc693a75eda259772a511b0000000000000005726561647900000000000000010000000000000001624d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e10000000000000002000000000000000c625f675f72657175697265640100000000000000010000000000000001624d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e1000000000000000167000000000000000c625f785f72657175697265640100000000000000010000000000000001624d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e10000000000000001780000000000000000",
			digest:        "sha256:1f05fc1c90eb69c2f04e0b93592fd313e1973122f1295e17869cab97bb9be1b6",
			verdict:       Ready,
		},
		{
			name:          "needs_input",
			fields:        map[FieldName]Value{"g": mustString(t, "X")},
			stateDigest:   "sha256:6956bb85032600ae955d7c21563b6ddff1c649941e4ad2c720b5ca15fa4247a0",
			idHex:         "000000000000001c6d616964656e2d6c616e652e6173736573736d656e742d69642e76314d5087c27554035f79f579fe97467e7703bc637f063e2beb62fb677e83caa69bab63b577f75697ab6be236751d891d2840eb0a158fbc693a75eda259772a511b",
			assessmentID:  "sha256:b07256e866bb279ee2cc7bc077c9ceef493dd6e13a99ac0e55c3b204f2a67a69",
			assessmentHex: "00000000000000236d616964656e2d6c616e652e72656164696e6573732d6173736573736d656e742e763100000000000000236d616964656e2d6c616e652e6173736573736d656e742d73656d616e746963732e76314d5087c27554035f79f579fe97467e7703bc637f063e2beb62fb677e83caa69bab63b577f75697ab6be236751d891d2840eb0a158fbc693a75eda259772a511b000000000000000b6e656564735f696e70757400000000000000010000000000000001624d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e10000000000000002000000000000000c625f675f72657175697265640100000000000000010000000000000001624d8cc35e11383c2e26063e688076708413fa2689be1f5b547241aec89d0489e1000000000000000167000000000000000c625f785f72657175697265640200000000000000000000000000000000",
			digest:        "sha256:304a0756d142c15adf493b04b0b65bff95018e1bace21778150d440606e8616a",
			verdict:       NeedsInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entity := mustEntity(t, "b", entityID, test.fields)
			state, err := NewState(schema, lineage, []Entity{entity}, nil)
			if err != nil {
				t.Fatalf("NewState: %v", err)
			}
			if state.Digest() != test.stateDigest {
				t.Fatalf("vector state digest drifted: %s", state.Digest())
			}
			assessment := mustAssess(t, syntheticCheckpointOverState(t, state), state, profile)
			idBytes, err := encodeAssessmentID(assessment.CheckpointArtifactID(), profile.ID())
			if err != nil {
				t.Fatalf("encodeAssessmentID: %v", err)
			}
			if got := hex.EncodeToString(idBytes); got != test.idHex || assessment.ID() != test.assessmentID {
				t.Fatalf("assessment ID vector=(%s,%s), want (%s,%s)", got, assessment.ID(), test.idHex, test.assessmentID)
			}
			if got := hex.EncodeToString(assessment.CanonicalBytes()); got != test.assessmentHex || assessment.Digest() != test.digest {
				t.Fatalf("assessment vector=(%s,%s), want (%s,%s)", got, assessment.Digest(), test.assessmentHex, test.digest)
			}
			if assessment.Verdict() != test.verdict {
				t.Fatalf("verdict=%s, want %s", assessment.Verdict(), test.verdict)
			}
		})
	}
}

// Production break caught: treating an empty explicit selection as
// needs_input (or as an error) would contradict the documented vacuous-ready
// semantics for universal field-presence profiles.
func TestAssessEmptyScopeIsVacuouslyReady(t *testing.T) {
	fixture := newReadinessFixture(t)
	schema := compileFixtureSchema(t, false)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-empty-scope")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	// Empty state: the explicit `all driver entities` scope selects nothing.
	state, err := NewState(schema, lineage, []Entity{}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	assessment := mustAssess(t, syntheticCheckpointOverState(t, state), state, fixture.optimizer)
	// Universal aggregation over an empty explicit selection is vacuously
	// ready: there is no selected entity whose requirement can be missing.
	if assessment.Verdict() != Ready {
		t.Fatalf("empty scope verdict=%s, want vacuous %s", assessment.Verdict(), Ready)
	}
	if got := assessment.EntityResults(); len(got) != 0 {
		t.Fatalf("empty scope produced %d entity results", len(got))
	}
}

type readinessFixture struct {
	binding    RunBinding
	c1         TransitionOutcome
	c2         TransitionOutcome
	c1Artifact CheckpointArtifact
	c2Artifact CheckpointArtifact
	cm         CompiledProfile
	optimizer  CompiledProfile
}

func newReadinessFixture(t *testing.T) readinessFixture {
	t.Helper()
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)
	c1Artifact := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	c2Artifact := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_hos_aggregated.v1", State: c2.State(), Journal: c2.Journal(), InvariantResults: c2.InvariantResults()})
	cm, optimizer := fixtureCompiledProfiles(t)
	return readinessFixture{binding: binding, c1: c1, c2: c2, c1Artifact: c1Artifact, c2Artifact: c2Artifact, cm: cm, optimizer: optimizer}
}

func readinessFixtureC1(t *testing.T) (CheckpointArtifact, State, CompiledProfile, CompiledProfile) {
	t.Helper()
	fixture := newReadinessFixture(t)
	return fixture.c1Artifact, fixture.c1.State(), fixture.cm, fixture.optimizer
}

func fixtureCompiledProfiles(t *testing.T) (CompiledProfile, CompiledProfile) {
	t.Helper()
	compilation, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	profiles := compilation.Profiles()
	if len(profiles) != 2 || profiles[0].Key() != "cm.v1" || profiles[1].Key() != "optimizer.v1" {
		t.Fatalf("fixture profiles=%v", profiles)
	}
	return profiles[0], profiles[1]
}

// syntheticCheckpointOverState builds an internally consistent sealed-shape
// artifact whose replay links are fixed literals, so profile tests can assess
// lawful states that the two-transition fixture cannot itself reach.
func syntheticCheckpointOverState(t *testing.T, state State) CheckpointArtifact {
	t.Helper()
	artifact := CheckpointArtifact{
		checkpoint:            CheckpointDeclaration{Key: "c1", After: "f"},
		checkpointID:          "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		planID:                "sha256:6666666666666666666666666666666666666666666666666666666666666666",
		semanticRunID:         "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		inputID:               "sha256:7777777777777777777777777777777777777777777777777777777777777777",
		initialStateDigest:    "sha256:8888888888888888888888888888888888888888888888888888888888888888",
		worldID:               "sha256:9999999999999999999999999999999999999999999999999999999999999999",
		policyID:              "sha256:5555555555555555555555555555555555555555555555555555555555555555",
		stateDigest:           state.Digest(),
		journalPrefixDigest:   "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		invariantResultDigest: "sha256:4444444444444444444444444444444444444444444444444444444444444444",
	}
	claim, err := encodeCheckpointArtifactID(artifact.semanticRunID, artifact.checkpointID, artifact.stateDigest, artifact.journalPrefixDigest, artifact.invariantResultDigest, artifact.policyID)
	if err != nil {
		t.Fatalf("encodeCheckpointArtifactID: %v", err)
	}
	artifact.id = CheckpointArtifactID(canonicalDigest(claim))
	manifest, err := encodeCheckpointArtifact(artifact)
	if err != nil {
		t.Fatalf("encodeCheckpointArtifact: %v", err)
	}
	artifact.canonical = manifest
	artifact.digest = CheckpointArtifactDigest(canonicalDigest(manifest))
	return artifact
}

func goldenSchemaAndProfile(t *testing.T) (Schema, CompiledProfile) {
	t.Helper()
	request, schema := compileGoldenVectorRequest(t)
	compilation, err := Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	profiles := compilation.Profiles()
	if len(profiles) != 2 || profiles[1].Key() != "p" {
		t.Fatalf("golden profiles=%v", profiles)
	}
	return schema, profiles[1]
}

func goldenSchemaProfile(t *testing.T) CompiledProfile {
	t.Helper()
	_, profile := goldenSchemaAndProfile(t)
	return profile
}

func corruptManifestCheckpoint(input CheckpointArtifact) CheckpointArtifact {
	input.canonical = corruptCanonicalCopy(input.CanonicalBytes())
	return input
}

func corruptIDCheckpoint(input CheckpointArtifact) CheckpointArtifact {
	input.canonical = input.CanonicalBytes()
	input.id = CheckpointArtifactID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	return input
}

func corruptStateCopy(input State) State {
	input.canonical = corruptCanonicalCopy(input.canonical)
	return input
}

func corruptProfileCopy(input CompiledProfile) CompiledProfile {
	input.canonical = corruptCanonicalCopy(input.CanonicalBytes())
	return input
}

func mustAssess(t *testing.T, checkpoint CheckpointArtifact, state State, profile CompiledProfile) Assessment {
	t.Helper()
	outcome, err := Assess(AssessmentRequest{Checkpoint: checkpoint, State: state, Profile: profile})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	return mustAssessment(t, outcome)
}

func mustAssessment(t *testing.T, outcome AssessmentOutcome) Assessment {
	t.Helper()
	assessment, ok := outcome.Assessment()
	if !ok || !outcome.Assessed() {
		code := ""
		if failure, hasFailure := outcome.Failure(); hasFailure {
			code = failure.Code()
		}
		t.Fatalf("assessment refused: %s", code)
	}
	return assessment
}

func missingCodes(results []RequirementResult) []RequirementCode {
	missing := make([]RequirementCode, 0, len(results))
	for _, result := range results {
		if !result.Satisfied() {
			missing = append(missing, result.Code())
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

func assertMissingRequirementCodes(t *testing.T, assessment Assessment, want ...RequirementCode) {
	t.Helper()
	all := make([]RequirementCode, 0)
	for _, entity := range assessment.EntityResults() {
		all = append(all, missingCodes(entity.Results())...)
	}
	slices.Sort(all)
	all = slices.Compact(all)
	wanted := slices.Clone(want)
	slices.Sort(wanted)
	if !slices.Equal(all, wanted) {
		t.Fatalf("missing requirement codes=%v, want exactly %v", all, wanted)
	}
}

func assertAssessIntegrity(t *testing.T, outcome AssessmentOutcome, code IntegrityCode, kind ArtifactKind) {
	t.Helper()
	if outcome.Assessed() {
		t.Fatal("defective request produced an assessment")
	}
	failure, ok := outcome.Failure()
	if !ok {
		t.Fatal("assessment refusal has no typed failure")
	}
	integrity, ok := failure.ArtifactIntegrity()
	if !ok || integrity.Code() != code || integrity.ArtifactKind() != kind {
		t.Fatalf("integrity=(%v,%v,%v), want (%v,%v,true)", integrity.Code(), integrity.ArtifactKind(), ok, code, kind)
	}
}
