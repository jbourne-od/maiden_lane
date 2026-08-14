package semantic

import (
	"bytes"
	"slices"
	"testing"
)

var testGoExecutor = mustExecutorIdentityForTests("go", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

// Production break caught: reading HOS while forming the team, using source
// order in identity, or committing fewer than all three structural operations
// would destroy the lawful deterministic C1 prefix.
func TestExecuteFormTeamCommitsAtomicPatchWithoutReadingHOS(t *testing.T) {
	plan, state, world := executionFixture(t, false, nil)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)

	outcome, err := ExecuteTransition(binding, "form_team.v1", state, NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if failure, ok := outcome.Failure(); ok {
		t.Fatalf("T1 rejected: %s", failure.Code())
	}
	patch := outcome.Patch()
	if !outcome.HasPatch() {
		t.Fatal("accepted transition has no patch")
	}
	assertOperationKinds(t, patch, OperationInsert, OperationRelate, OperationRelate)
	if got := len(outcome.Journal().Entries()); got != 1 {
		t.Fatalf("journal entries=%d, want 1", got)
	}
	for _, source := range state.Entities() {
		got, ok := outcome.State().Entity(source.Ref())
		if !ok || !entitiesEqual(got, source) {
			t.Fatalf("source %v was not preserved", source.Ref())
		}
	}

	reversedPlan, reversedState, reversedWorld := executionFixture(t, true, nil)
	reversedBinding := mustBindRun(t, reversedPlan, reversedState, reversedWorld, testGoExecutor)
	reversed, err := ExecuteTransition(reversedBinding, "form_team.v1", reversedState, NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition reversed: %v", err)
	}
	if reversedFailure, ok := reversed.Failure(); ok {
		t.Fatalf("reversed T1 rejected: %s", reversedFailure.Code())
	}
	if !bytes.Equal(outcome.State().CanonicalBytes(), reversed.State().CanonicalBytes()) {
		t.Fatal("source declaration/input order changed T1 result")
	}
}

// Production break caught: treating explicit incompatible team sources as a
// no-op, creating a patch before T1 preconditions pass, or appending rejection
// history would conceal a protected semantic failure.
func TestExecuteFormTeamRejectsAssignmentFailuresBeforePatch(t *testing.T) {
	tests := []struct {
		name   string
		fields [2]map[FieldName]Value
		code   InvariantCode
	}{
		{name: "absent", fields: [2]map[FieldName]Value{{}, {"assignment_key": mustString(t, "X")}}, code: TeamAssignmentKeyInvalid},
		{name: "empty", fields: [2]map[FieldName]Value{{"assignment_key": mustString(t, "")}, {"assignment_key": mustString(t, "X")}}, code: TeamAssignmentKeyInvalid},
		{name: "mismatch", fields: [2]map[FieldName]Value{{"assignment_key": mustString(t, "X")}, {"assignment_key": mustString(t, "Y")}}, code: TeamAssignmentKeyMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, state, world := executionFixture(t, false, &test.fields)
			binding := mustBindRun(t, plan, state, world, testGoExecutor)
			journal := NewJournal()
			outcome, err := ExecuteTransition(binding, "form_team.v1", state, journal)
			if err != nil {
				t.Fatalf("ExecuteTransition: %v", err)
			}
			failure := mustTransitionFailure(t, outcome)
			if got := failure.InvariantCode(); got != test.code {
				t.Fatalf("code=%s, want %s", got, test.code)
			}
			if _, ok := failure.ProposedPatchDigest(); ok {
				t.Fatal("precondition rejection materialized a patch")
			}
			if got := len(outcome.Journal().Entries()); got != 0 {
				t.Fatalf("rejection appended %d entries", got)
			}
			if !bytes.Equal(outcome.State().CanonicalBytes(), state.CanonicalBytes()) {
				t.Fatal("rejection changed predecessor")
			}
		})
	}
}

// Production break caught: writing the grouping value for every declared copy
// would execute semantics different from the compiled FieldCopy source.
func TestExecuteFormTeamReadsEachCompiledCopiedField(t *testing.T) {
	req := compileFixtureRequest(t, false)
	entities := req.Schema.EntityDeclarations()
	for i := range entities {
		switch entities[i].Kind {
		case "driver":
			entities[i].Fields = append(entities[i].Fields, FieldDeclaration{Name: "dispatch_label", Kind: ValueString})
		case "team":
			entities[i].Fields = append(entities[i].Fields, FieldDeclaration{Name: "dispatch_label", Kind: ValueString})
		}
	}
	schema, err := NewSchema(entities, req.Schema.RelationDeclarations())
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	req.Schema = schema.Declaration()
	form := req.Rules.Transformations[0].Form
	form.CopiedFields = append(form.CopiedFields, FieldCopy{Source: "driver.dispatch_label", Destination: "team.dispatch_label"})
	req.Rules.Transformations[0].DeclaredReads = append(req.Rules.Transformations[0].DeclaredReads, "driver.dispatch_label")
	req.Rules.Transformations[0].DeclaredWrites = append(req.Rules.Transformations[0].DeclaredWrites, "team.dispatch_label")
	compilation, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("copy fixture did not compile")
	}
	lineage, _ := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	drivers := []Entity{
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"), map[FieldName]Value{"assignment_key": mustString(t, "X"), "dispatch_label": mustString(t, "dispatch")}),
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "B"), map[FieldName]Value{"assignment_key": mustString(t, "X"), "dispatch_label": mustString(t, "dispatch")}),
	}
	state, err := NewState(schema, lineage, drivers, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	world, _ := NewWorld(nil)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	outcome := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	team, _ := outcome.State().Entity(insertedRef(t, outcome.Patch()))
	assertFieldEquals(t, team, "dispatch_label", mustString(t, "dispatch"))
}

// Production break caught: deriving synthetic identity from GroupingField
// instead of the compiled OutputKey.Field makes a distinct semantic output key
// inert and can collide outputs whose grouping assignment is unchanged.
func TestExecuteFormTeamUsesDistinctCompiledOutputKey(t *testing.T) {
	request := compileFixtureRequest(t, false)
	form := &request.Rules.Transformations[0]
	form.Form.OutputKey.Field = "driver.hos_anchor"
	form.DeclaredReads = append(form.DeclaredReads, "driver.hos_anchor")
	compilation, err := Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("distinct-output-key fixture did not compile")
	}

	execute := func(anchor string) (EntityRef, State) {
		fields := passingDriverFields(t)
		fields[0]["hos_anchor"] = mustAtom(t, anchor)
		fields[1]["hos_anchor"] = mustAtom(t, anchor)
		_, state, world := executionFixture(t, false, &fields)
		binding := mustBindRun(t, plan, state, world, testGoExecutor)
		outcome := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
		return insertedRef(t, outcome.Patch()), state
	}

	first, firstState := execute("T0")
	again, _ := execute("T0")
	changed, _ := execute("T9")
	if first != again {
		t.Fatalf("same compiled output key produced nondeterministic refs %v and %v", first, again)
	}
	if first == changed {
		t.Fatal("changing only the compiled output key did not change synthetic identity")
	}
	want, err := syntheticEntityID(firstState.InputLineageID(), "team", "form_team.v1",
		[]EntityRef{firstState.Entities()[0].Ref(), firstState.Entities()[1].Ref()}, mustAtom(t, "T0"))
	if err != nil {
		t.Fatalf("syntheticEntityID: %v", err)
	}
	if first.ID != want {
		t.Fatalf("team ID=%s, want output-key-derived %s", first.ID, want)
	}
}

// Production break caught: binding unchecked/corrupt semantic artifacts or an
// unsupported execution contract would manufacture identities for a request
// that was never validly established.
func TestRunBindingFailsClosedBeforeProducingIdentities(t *testing.T) {
	plan, state, world := executionFixture(t, false, nil)
	otherSchema := fixtureSchemaWithExtraTeamField(t)
	mismatchedState, err := NewState(otherSchema, state.InputLineageID(), state.Entities(), nil)
	if err != nil {
		t.Fatalf("NewState mismatched: %v", err)
	}

	tests := []struct {
		name string
		req  RunBindingRequest
	}{
		{name: "schema mismatch", req: RunBindingRequest{Plan: plan, InitialState: mismatchedState, World: world, ExecutorIdentity: testGoExecutor, Policy: ChangesProvenance}},
		{name: "corrupt state", req: RunBindingRequest{Plan: plan, InitialState: State{schema: state.schema, lineage: state.lineage, entities: state.entities, canonical: state.canonical, digest: StateDigest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")}, World: world, ExecutorIdentity: testGoExecutor, Policy: ChangesProvenance}},
		{name: "corrupt world", req: RunBindingRequest{Plan: plan, InitialState: state, World: World{references: world.references, canonical: world.canonical, id: WorldID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")}, ExecutorIdentity: testGoExecutor, Policy: ChangesProvenance}},
		{name: "unsupported executor", req: RunBindingRequest{Plan: plan, InitialState: state, World: world, ExecutorIdentity: ExecutorIdentity{backend: "UPPER", version: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Policy: ChangesProvenance}},
		{name: "unsupported policy", req: RunBindingRequest{Plan: plan, InitialState: state, World: world, ExecutorIdentity: testGoExecutor, Policy: ProvenancePolicy(99)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding, err := BindRun(test.req)
			if err == nil {
				t.Fatal("BindRun accepted invalid request")
			}
			if binding.SemanticRunID() != "" || binding.ExecutionID() != "" {
				t.Fatal("failed binding exposed run identities")
			}
		})
	}
}

// Production break caught: aggregating from ambient entities, using the wrong
// reduction, or omitting absent before-images would make the T2 result differ
// from its compiled closed declaration.
func TestExecuteAggregateTeamHOSCommitsDeclaredMaximums(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())

	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	patch := t2.Patch()
	if !t2.HasPatch() {
		t.Fatal("accepted aggregate has no patch")
	}
	assertOperationKinds(t, patch, OperationUpdate)
	update, ok := patch.Operations()[0].Update()
	if !ok {
		t.Fatal("aggregate patch is not Update")
	}
	wantNames := []FieldName{"aggregation_anchor", "driving_duration_hours", "elapsed_duration_hours"}
	gotNames := make([]FieldName, 0, len(update.Fields()))
	for _, field := range update.Fields() {
		gotNames = append(gotNames, field.Name)
		if field.Before.Present() {
			t.Fatalf("field %s before-image is present", field.Name)
		}
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("update fields=%v, want %v", gotNames, wantNames)
	}
	team, ok := t2.State().Entity(update.Target())
	if !ok {
		t.Fatal("updated team missing")
	}
	assertFieldEquals(t, team, "aggregation_anchor", mustAtom(t, "T0"))
	assertFieldEquals(t, team, "elapsed_duration_hours", NewInt64Value(10))
	assertFieldEquals(t, team, "driving_duration_hours", NewInt64Value(8))
	if got := len(t2.Journal().Entries()); got != 2 {
		t.Fatalf("journal entries=%d, want 2", got)
	}
}

// Production break caught: validating anchor equality after patch creation or
// treating any protected T2 rejection as committed history would make a failed
// suffix contaminate the accepted prefix.
func TestExecuteAggregateTeamHOSRejectsProtectedInputsBeforePatch(t *testing.T) {
	tests := []struct {
		name   string
		fields [2]map[FieldName]Value
		code   InvariantCode
	}{
		{name: "incomplete", fields: [2]map[FieldName]Value{
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10)},
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
		}, code: HOSTupleIncomplete},
		{name: "negative", fields: [2]map[FieldName]Value{
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(-1)},
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
		}, code: HOSDurationInvalid},
		{name: "driving exceeds elapsed", fields: [2]map[FieldName]Value{
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(11)},
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
		}, code: HOSDurationInvalid},
		{name: "anchor mismatch", fields: [2]map[FieldName]Value{
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(8)},
			{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T1"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
		}, code: HOSAnchorMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, state, world := executionFixture(t, false, &test.fields)
			binding := mustBindRun(t, plan, state, world, testGoExecutor)
			t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
			prefix := t1.Journal().PrefixDigest(binding)
			outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
			if err != nil {
				t.Fatalf("ExecuteTransition: %v", err)
			}
			failure := mustTransitionFailure(t, outcome)
			if got := failure.InvariantCode(); got != test.code {
				t.Fatalf("code=%s, want %s", got, test.code)
			}
			if _, ok := failure.ProposedPatchDigest(); ok {
				t.Fatal("precondition rejection materialized a patch")
			}
			if got := outcome.Journal().PrefixDigest(binding); got != prefix {
				t.Fatal("rejection entered accepted history")
			}
			if !bytes.Equal(outcome.State().CanonicalBytes(), t1.State().CanonicalBytes()) {
				t.Fatal("rejection changed C1")
			}
		})
	}
}

// Production break caught: treating two present empty anchor atoms as a valid
// common source anchor would materialize and commit an empty aggregate anchor.
func TestExecuteAggregateTeamHOSRejectsEmptySourceAnchorBeforePatch(t *testing.T) {
	fields := passingDriverFields(t)
	fields[0]["hos_anchor"] = mustAtom(t, "")
	fields[1]["hos_anchor"] = mustAtom(t, "")
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.InvariantCode() != HOSTupleIncomplete {
		t.Fatalf("code=%s, want %s", failure.InvariantCode(), HOSTupleIncomplete)
	}
	if outcome.HasPatch() {
		t.Fatal("empty source anchor materialized a patch")
	}
	if len(outcome.Journal().Entries()) != 1 {
		t.Fatal("empty source anchor entered accepted history")
	}
}

// Production break caught: assuming RequiredSourceTuple subsumes an explicit
// CompleteTuple predicate would accept a missing field the compiled predicate reads.
func TestExecuteAggregateEvaluatesDeclaredCompleteTupleFields(t *testing.T) {
	req := compileFixtureRequest(t, false)
	req.Rules.Transformations[1].Aggregate.RequiredSourceTuple = []FieldPath{"driver.hos_elapsed_hours", "driver.hos_driving_hours"}
	compilation, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	fields := [2]map[FieldName]Value{
		{"assignment_key": mustString(t, "X"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(8)},
		{"assignment_key": mustString(t, "X"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
	}
	_, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if got := mustTransitionFailure(t, outcome).InvariantCode(); got != HOSTupleIncomplete {
		t.Fatalf("code=%s", got)
	}
}

// Production break caught: evaluating canonically sorted predicate tags as
// runtime order would report anchor mismatch before the required source tuple
// inequality check.
func TestExecuteAggregateTeamHOSChecksDurationBeforeAnchorEquality(t *testing.T) {
	fields := [2]map[FieldName]Value{
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(11)},
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T1"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
	}
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if got := mustTransitionFailure(t, outcome).InvariantCode(); got != HOSDurationInvalid {
		t.Fatalf("code=%s, want %s", got, HOSDurationInvalid)
	}
	for _, result := range mustTransitionFailure(t, outcome).InvariantResults() {
		if result.Code() == HOSAnchorMismatch {
			t.Fatal("failure report fabricated an unevaluated anchor result")
		}
	}
}

// Production break caught: returning deterministic corruption through the Go
// error channel after a run exists would discard the typed integrity outcome.
func TestExecuteTransitionReturnsTypedIntegrityFailureForCorruptEstablishedArtifacts(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	corrupt := state
	corrupt.digest = StateDigest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	outcome, err := ExecuteTransition(binding, "form_team.v1", corrupt, NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.Kind() != ArtifactIntegrityFailed || failure.Code() != string(ArtifactDigestMismatch) {
		t.Fatalf("failure=(%s,%s)", failure.Kind(), failure.Code())
	}
	if outcome.State().Digest() != state.Digest() || len(outcome.Journal().Entries()) != 0 {
		t.Fatal("integrity failure did not preserve verified bound input")
	}

	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	corrupt = t1.State()
	corrupt.digest = StateDigest("sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	outcome, err = ExecuteTransition(binding, "aggregate_team_hos.v1", corrupt, t1.Journal())
	if err != nil {
		t.Fatalf("ExecuteTransition after C1: %v", err)
	}
	failure = mustTransitionFailure(t, outcome)
	if failure.Kind() != ArtifactIntegrityFailed || outcome.State().Digest() != t1.State().Digest() || len(outcome.Journal().Entries()) != 1 {
		t.Fatal("integrity rejection discarded the verified C1 frontier")
	}
}

// Production break caught: flattening journal verification errors loses the
// exact integrity code, implicated artifact, digest evidence, and verified
// prefix required to diagnose an established-run corruption safely.
func TestExecuteTransitionClassifiesJournalIntegrityFailures(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	validEntries := t2.Journal().Entries()

	tests := []struct {
		name         string
		mutate       func([]JournalEntry)
		code         IntegrityCode
		kind         ArtifactKind
		verified     int
		wantExpected bool
		wantObserved bool
		expected     Digest
		observed     Digest
	}{
		{name: "embedded patch self digest", mutate: func(entries []JournalEntry) {
			entries[1].patch.digest = PatchDigest("sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
			canonical, _ := encodeJournalEntry(entries[1])
			entries[1].canonical, entries[1].digest = canonical, JournalEntryDigest(canonicalDigest(canonical))
		}, code: ArtifactDigestMismatch, kind: ArtifactPatch, verified: 1, wantExpected: true, wantObserved: true,
			expected: Digest(validEntries[1].patch.digest), observed: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
		{name: "entry self digest", mutate: func(entries []JournalEntry) {
			entries[1].digest = JournalEntryDigest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		}, code: ArtifactDigestMismatch, kind: ArtifactJournalEntry, verified: 1, wantExpected: true, wantObserved: true,
			expected: Digest(validEntries[1].digest), observed: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{name: "result replay divergence", mutate: func(entries []JournalEntry) {
			entries[1].result = StateDigest("sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
			canonical, _ := encodeJournalEntry(entries[1])
			entries[1].canonical, entries[1].digest = canonical, JournalEntryDigest(canonicalDigest(canonical))
		}, code: ReplayDivergence, kind: ArtifactJournalEntry, verified: 1, wantExpected: true, wantObserved: true,
			expected: Digest(t2.State().Digest()), observed: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
		{name: "predecessor link", mutate: func(entries []JournalEntry) {
			entries[1].predecessor = state.Digest()
			canonical, _ := encodeJournalEntry(entries[1])
			entries[1].canonical, entries[1].digest = canonical, JournalEntryDigest(canonicalDigest(canonical))
		}, code: ArtifactLinkInconsistent, kind: ArtifactJournalEntry, verified: 1},
		{name: "plan rule link", mutate: func(entries []JournalEntry) {
			entries[1].rule = "form_team.v1"
			canonical, _ := encodeJournalEntry(entries[1])
			entries[1].canonical, entries[1].digest = canonical, JournalEntryDigest(canonicalDigest(canonical))
		}, code: ArtifactLinkInconsistent, kind: ArtifactJournalEntry, verified: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := cloneJournalEntries(validEntries)
			test.mutate(entries)
			journal := Journal{entries: entries}
			outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t2.State(), journal)
			if err != nil {
				t.Fatalf("ExecuteTransition: %v", err)
			}
			failure := mustTransitionFailure(t, outcome)
			integrity, ok := failure.ArtifactIntegrity()
			if !ok {
				t.Fatal("failure is not artifact integrity")
			}
			if integrity.Code() != test.code || integrity.ArtifactKind() != test.kind {
				t.Fatalf("integrity=(%s,%s), want (%s,%s)", integrity.Code(), integrity.ArtifactKind(), test.code, test.kind)
			}
			wantArtifact := Digest(canonicalDigest(entries[1].canonical))
			if test.kind == ArtifactPatch {
				wantArtifact = Digest(canonicalDigest(entries[1].patch.canonical))
			}
			if integrity.Artifact().Digest() != wantArtifact {
				t.Fatalf("artifact digest=%s, want implicated entry content %s", integrity.Artifact().Digest(), wantArtifact)
			}
			gotExpected, hasExpected := integrity.ExpectedDigest()
			gotObserved, hasObserved := integrity.ObservedDigest()
			if hasExpected != test.wantExpected || hasObserved != test.wantObserved {
				t.Fatalf("optional evidence expected=(%t,%t), want (%t,%t)", hasExpected, hasObserved, test.wantExpected, test.wantObserved)
			}
			if gotExpected != test.expected || gotObserved != test.observed {
				t.Fatalf("digest evidence=(%s,%s), want (%s,%s)", gotExpected, gotObserved, test.expected, test.observed)
			}
			last, ok := integrity.LastVerifiedStateDigest()
			if !ok || last != t1.State().Digest() {
				t.Fatalf("last verified state=(%s,%t), want C1", last, ok)
			}
			refs := integrity.References()
			if len(refs) != 2 || refs[0].Kind() != ArtifactState || refs[0].Digest() != Digest(t1.State().Digest()) || refs[1] != integrity.Artifact() {
				t.Fatalf("integrity references=%v", refs)
			}
			if len(outcome.Journal().Entries()) != test.verified || outcome.State().Digest() != t1.State().Digest() {
				t.Fatal("integrity outcome did not preserve the exact independently verified C1 prefix")
			}
		})
	}
}

// Production break caught: allowing callers to select a non-next or repeated
// compiled rule would create an invalid accepted-plan prefix.
func TestExecuteTransitionRequiresNextCompiledRule(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	if _, err := ExecuteTransition(binding, "aggregate_team_hos.v1", state, NewJournal()); err == nil {
		t.Fatal("T2 executed before T1")
	}
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	if _, err := ExecuteTransition(binding, "form_team.v1", t1.State(), t1.Journal()); err == nil {
		t.Fatal("T1 repeated")
	}
	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	if _, err := ExecuteTransition(binding, "aggregate_team_hos.v1", t2.State(), t2.Journal()); err == nil {
		t.Fatal("T2 repeated")
	}
}

// Production break caught: trusting cached binding identities would allow a
// tampered run or execution ID to authorize execution or prefix identity.
func TestRunBindingRecomputesLayeredIdentities(t *testing.T) {
	plan, state, world := executionFixture(t, false, nil)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	for name, mutate := range map[string]func(*RunBinding){
		"input": func(b *RunBinding) {
			b.inputID = InputID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		},
		"run": func(b *RunBinding) {
			b.semanticRunID = SemanticRunID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		},
		"policy": func(b *RunBinding) {
			b.policyID = ProvenancePolicyID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		},
		"execution": func(b *RunBinding) {
			b.executionID = ExecutionID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		},
	} {
		t.Run(name, func(t *testing.T) {
			corrupt := binding
			mutate(&corrupt)
			if _, err := ExecuteTransition(corrupt, "form_team.v1", state, NewJournal()); err == nil {
				t.Fatal("corrupt binding accepted")
			}
		})
	}
}

// Production break caught: retaining optional caller pointers would let a
// failure report change beneath its frozen canonical bytes and digest.
func TestArtifactIntegrityFailureCopiesOptionalInputs(t *testing.T) {
	last := StateDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	expected := Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	observed := Digest("sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	report, err := newArtifactIntegrityFailure(SemanticRunID("sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), ArtifactDigestMismatch,
		ArtifactRef{kind: ArtifactState, digest: observed}, &last, nil, &expected, &observed, nil)
	if err != nil {
		t.Fatalf("newArtifactIntegrityFailure: %v", err)
	}
	wantBytes, wantDigest := report.CanonicalBytes(), report.Digest()
	last, expected, observed = "", "", ""
	if !bytes.Equal(report.CanonicalBytes(), wantBytes) || report.Digest() != wantDigest {
		t.Fatal("caller pointer mutation changed failure report")
	}
	if got, _ := report.LastVerifiedStateDigest(); got == "" {
		t.Fatal("last state followed caller pointer")
	}
	if got, _ := report.ExpectedDigest(); got == "" {
		t.Fatal("expected followed caller pointer")
	}
	if got, _ := report.ObservedDigest(); got == "" {
		t.Fatal("observed followed caller pointer")
	}
}

// Production break caught: encoding invariant evidence references in runtime
// evaluation order makes a semantically identical failure digest depend on an
// executor refactor even though runtime result order remains inspectable.
func TestProtectedFailureCanonicalizesInvariantEvidenceRefs(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	declarations := plan.MustTransformation("aggregate_team_hos.v1").Invariants()
	first := invariantResult(declarations[0], true, nil, nil)
	second := invariantResult(declarations[1], false, nil, nil)

	a, err := protectedFailure(binding, "aggregate_team_hos.v1", state.Digest(), protectedInvariantCode, "", HOSTupleIncomplete,
		[]InvariantResult{first, second}, nil, nil, nil)
	if err != nil {
		t.Fatalf("protectedFailure a: %v", err)
	}
	b, err := protectedFailure(binding, "aggregate_team_hos.v1", state.Digest(), protectedInvariantCode, "", HOSTupleIncomplete,
		[]InvariantResult{second, first}, nil, nil, nil)
	if err != nil {
		t.Fatalf("protectedFailure b: %v", err)
	}
	if a.Digest() != b.Digest() || !bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes()) {
		t.Fatal("evidence reference order/duplicates changed failure identity")
	}
	refs := a.InvariantEvidenceRefs()
	if len(refs) != 2 || refs[0].DeclarationKey() >= refs[1].DeclarationKey() {
		t.Fatalf("invariant refs are not sorted and deduplicated: %v", refs)
	}
	results := a.InvariantResults()
	if len(results) != 2 || results[1].DeclarationKey() != second.DeclarationKey() {
		t.Fatal("canonical evidence normalization changed truthful runtime result order")
	}
}

// Production break caught: selecting one contributor on an equal maximum
// would make provenance depend on source traversal order.
func TestExecuteAggregateTeamHOSRetainsAllMaximumTies(t *testing.T) {
	fields := [2]map[FieldName]Value{
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(8)},
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(8)},
	}
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	evidence := t2.Journal().Entries()[1].Evidence()
	for _, field := range []FieldName{"hos_elapsed_hours", "hos_driving_hours"} {
		count := 0
		for _, fact := range evidence {
			if fact.Field() == field {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("tie field %s contributors=%d, want 2; evidence=%v", field, count, evidence)
		}
	}
}

// Production break caught: accepting an emitted field that differs from the
// declared source anchor or reduction would commit a patch whose values do not
// implement the compiled aggregate operator.
func TestExecuteAggregateTeamHOSDetectsEmittedAggregateMismatch(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", t1.State(), t1.Journal())
	teamRef := insertedRef(t, t1.Journal().Entries()[0].Patch())
	team, _ := t2.State().Entity(teamRef)
	mutatedFields := team.Fields()
	mutatedFields["elapsed_duration_hours"] = NewInt64Value(9)
	mutated := replaceEntityInState(t, t2.State(), mustEntity(t, teamRef.Kind, teamRef.ID, mutatedFields))
	declaration := plan.MustTransformation("aggregate_team_hos.v1").Declaration().Aggregate
	expected := map[FieldName]Value{
		"aggregation_anchor":     mustAtom(t, "T0"),
		"elapsed_duration_hours": NewInt64Value(10),
		"driving_duration_hours": NewInt64Value(8),
	}
	if validateAggregateCandidate(mutated, teamRef, expected, declaration.ResultPredicates) {
		t.Fatal("emitted reduction mismatch passed candidate validation")
	}
}

// Production break caught: emitted-boundary validation that checks only
// presence and equality would certify an empty aggregate anchor.
func TestValidateAggregateCandidateRejectsEmptyEmittedAnchor(t *testing.T) {
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, testGoExecutor)
	t1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	teamRef := insertedRef(t, t1.Patch())
	team, _ := t1.State().Entity(teamRef)
	emitted := team.Fields()
	emitted["aggregation_anchor"] = mustAtom(t, "")
	emitted["elapsed_duration_hours"] = NewInt64Value(10)
	emitted["driving_duration_hours"] = NewInt64Value(8)
	candidate := replaceEntityInState(t, t1.State(), mustEntity(t, teamRef.Kind, teamRef.ID, emitted))
	aggregate := plan.MustTransformation("aggregate_team_hos.v1").Declaration().Aggregate
	expected := map[FieldName]Value{
		"aggregation_anchor":     mustAtom(t, ""),
		"elapsed_duration_hours": NewInt64Value(10),
		"driving_duration_hours": NewInt64Value(8),
	}
	if validateAggregateCandidate(candidate, teamRef, expected, aggregate.ResultPredicates) {
		t.Fatal("empty emitted aggregate anchor passed boundary validation")
	}
}

func mustExecutorIdentityForTests(backend string, version Digest) ExecutorIdentity {
	identity, err := NewExecutorIdentity(backend, version)
	if err != nil {
		panic(err)
	}
	return identity
}

func executionFixture(t *testing.T, reverse bool, fieldOverride *[2]map[FieldName]Value) (Plan, State, World) {
	t.Helper()
	compilation, err := Compile(compileFixtureRequest(t, reverse))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	schema := compileFixtureSchema(t, reverse)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	fields := [2]map[FieldName]Value{
		{"assignment_key": mustString(t, "X")},
		{"assignment_key": mustString(t, "X")},
	}
	if fieldOverride != nil {
		fields = *fieldOverride
	}
	entities := []Entity{
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"), fields[0]),
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "B"), fields[1]),
	}
	if reverse {
		entities[0], entities[1] = entities[1], entities[0]
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return plan, state, world
}

func mustBindRun(t *testing.T, plan Plan, state State, world World, executor ExecutorIdentity) RunBinding {
	t.Helper()
	binding, err := BindRun(RunBindingRequest{Plan: plan, InitialState: state, World: world, ExecutorIdentity: executor, Policy: ChangesProvenance})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return binding
}

func mustTransitionFailure(t *testing.T, outcome TransitionOutcome) FailureReport {
	t.Helper()
	failure, ok := outcome.Failure()
	if !ok {
		t.Fatal("transition accepted, want rejection")
	}
	return failure
}

func mustAcceptedTransition(t *testing.T, binding RunBinding, rule RuleID, state State, journal Journal) TransitionOutcome {
	t.Helper()
	outcome, err := ExecuteTransition(binding, rule, state, journal)
	if err != nil {
		t.Fatalf("ExecuteTransition(%s): %v", rule, err)
	}
	if failure, ok := outcome.Failure(); ok {
		t.Fatalf("ExecuteTransition(%s): %s", rule, failure.Code())
	}
	return outcome
}

func passingDriverFields(t *testing.T) [2]map[FieldName]Value {
	t.Helper()
	return [2]map[FieldName]Value{
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(10), "hos_driving_hours": NewInt64Value(8)},
		{"assignment_key": mustString(t, "X"), "hos_anchor": mustAtom(t, "T0"), "hos_elapsed_hours": NewInt64Value(7), "hos_driving_hours": NewInt64Value(6)},
	}
}

func assertFieldEquals(t *testing.T, entity Entity, name FieldName, want Value) {
	t.Helper()
	got, ok := entity.Field(name)
	if !ok || !got.Equal(want) {
		t.Fatalf("field %s=(%v,%t), want %v", name, got, ok, want)
	}
}

func insertedRef(t *testing.T, patch Patch) EntityRef {
	t.Helper()
	for _, operation := range patch.Operations() {
		if insert, ok := operation.Insert(); ok {
			return insert.Entity().Ref()
		}
	}
	t.Fatal("patch has no insert")
	return EntityRef{}
}

func assertOperationKinds(t *testing.T, patch Patch, want ...OperationKind) {
	t.Helper()
	operations := patch.Operations()
	if len(operations) != len(want) {
		t.Fatalf("operation count=%d, want %d", len(operations), len(want))
	}
	for index, operation := range operations {
		if operation.Kind() != want[index] {
			t.Fatalf("operation[%d]=%d, want %d", index, operation.Kind(), want[index])
		}
	}
}
