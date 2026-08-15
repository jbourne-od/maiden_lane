package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Production break caught: an application spine that reordered, skipped, or
// re-evaluated semantic phases would not reproduce the ratified passing
// lifecycle: both transitions committed, both checkpoints sealed, and the
// exact consumer-relative readiness matrix.
func TestRunPassingTeamHOS(t *testing.T) {
	request := requestFromFixture(t, teamhos.Passing)
	result, err := app.Run(t.Context(), request, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	executionStatus, ok := result.ExecutionStatus()
	if !ok || result.Status() != app.SpineSucceeded || executionStatus != app.ExecutionSucceeded {
		t.Fatalf("status=%v executionStatus=%v ok=%v", result.Status(), executionStatus, ok)
	}
	if _, ok := result.CompilationFailure(); ok {
		t.Fatal("passing spine carries a compilation failure")
	}
	if _, ok := result.SemanticFailure(); ok {
		t.Fatal("passing spine carries a semantic failure")
	}
	if _, ok := result.Plan(); !ok {
		t.Fatal("passing spine lost its compiled plan")
	}
	assertAcceptedRules(t, result, teamhos.RuleFormTeam, teamhos.RuleAggregateTeamHOS)
	if got := len(result.Checkpoints()); got != 2 {
		t.Fatalf("checkpoints retained = %d, want 2", got)
	}
	if got := len(result.Profiles()); got != 2 {
		t.Fatalf("profiles retained = %d, want 2", got)
	}
	assertCheckpointReadiness(t, result, teamhos.CheckpointTeamFormed, semantic.Ready, semantic.NeedsInput)
	assertCheckpointReadiness(t, result, teamhos.CheckpointTeamHOSAggregated, semantic.Ready, semantic.Ready)
	state, ok := result.State()
	if !ok {
		t.Fatal("passing spine has no retained state")
	}
	entries := result.Journal().Entries()
	if state.Digest() != entries[len(entries)-1].ResultStateDigest() {
		t.Fatal("retained state is not the accepted journal's final state")
	}
}

// Production break caught: turning the deterministic anchor-mismatch
// rejection into a Go error, or discarding the independently verified C1
// prefix with it, would destroy the accepted history the design guarantees.
func TestRunRejectedTeamHOSPreservesC1(t *testing.T) {
	result, err := app.Run(t.Context(), requestFromFixture(t, teamhos.AnchorMismatch), nil)
	if err != nil {
		t.Fatalf("semantic rejection returned Go error: %v", err)
	}
	if result.Status() != app.SpineFailed {
		t.Fatalf("status = %v, want SpineFailed", result.Status())
	}
	executionStatus, ok := result.ExecutionStatus()
	if !ok || executionStatus != app.ExecutionFailed {
		t.Fatalf("executionStatus=%v ok=%v, want failed/true", executionStatus, ok)
	}
	failure, ok := result.SemanticFailure()
	if !ok || failure.Kind() != semantic.ProtectedInvariantFailed {
		t.Fatalf("failure kind = %q ok=%v", failure.Kind(), ok)
	}
	if failure.InvariantCode() != semantic.HOSAnchorMismatch {
		t.Fatalf("failure code = %q, want HOS_ANCHOR_MISMATCH", failure.Code())
	}
	if _, materialized := failure.ProposedPatchDigest(); materialized {
		t.Fatal("pre-patch anchor mismatch claims a materialized patch")
	}
	assertAcceptedRules(t, result, teamhos.RuleFormTeam)
	assertOnlyCheckpoint(t, result, teamhos.CheckpointTeamFormed)
	assertCheckpointReadiness(t, result, teamhos.CheckpointTeamFormed, semantic.Ready, semantic.NeedsInput)
	if got := len(result.Profiles()); got != 2 {
		t.Fatalf("profiles retained = %d, want the two assessed profiles", got)
	}
	state, ok := result.State()
	if !ok || state.Digest() != result.Journal().Entries()[0].ResultStateDigest() {
		t.Fatal("retained state is not the accepted T1 prefix state")
	}
}

// Production break caught: treating a deterministic invalid-plan compilation
// as a Go error, or pretending an execution existed for it, would violate the
// ratified result matrix for the invalid_plan spine status.
func TestRunInvalidCompilationRequest(t *testing.T) {
	request := requestFromFixture(t, teamhos.Passing)
	for i, transformation := range request.Compilation.Rules.Transformations {
		if transformation.ID == teamhos.RuleFormTeam {
			reads := append([]semantic.FieldPath{}, transformation.DeclaredReads...)
			request.Compilation.Rules.Transformations[i].DeclaredReads = append(reads, "driver.field_that_does_not_exist")
		}
	}
	result, err := app.Run(t.Context(), request, nil)
	if err != nil {
		t.Fatalf("invalid plan returned Go error: %v", err)
	}
	if result.Status() != app.SpineInvalidPlan {
		t.Fatalf("status = %v, want SpineInvalidPlan", result.Status())
	}
	failure, ok := result.CompilationFailure()
	if !ok {
		t.Fatal("invalid plan has no compilation failure value")
	}
	found := false
	for _, diagnostic := range failure.Diagnostics() {
		if diagnostic.Code() == semantic.UnknownField {
			found = true
		}
	}
	if !found {
		t.Fatal("compilation failure lacks the UNKNOWN_FIELD diagnostic")
	}
	if _, ok := result.ExecutionStatus(); ok {
		t.Fatal("invalid plan claims an execution status")
	}
	if _, ok := result.Plan(); ok {
		t.Fatal("invalid plan retained a compiled plan")
	}
	if _, ok := result.SemanticFailure(); ok {
		t.Fatal("invalid plan carries an execution failure report")
	}
	if len(result.Journal().Entries()) != 0 || len(result.Checkpoints()) != 0 || len(result.Assessments()) != 0 || len(result.Profiles()) != 0 {
		t.Fatal("invalid plan retained execution artifacts")
	}
}

// Production break caught: accepting an incomplete canonical request would
// push zero-value semantic inputs into the kernel instead of rejecting them
// as typed invalid input at the application boundary.
func TestRunRejectsIncompleteCanonicalInput(t *testing.T) {
	var invalid app.InvalidInputError

	result, err := app.Run(t.Context(), app.Request{}, nil)
	if err == nil || !errors.As(err, &invalid) {
		t.Fatalf("zero request error = %v, want InvalidInputError", err)
	}
	if invalid.Code != app.InputCompilationRequestIncomplete {
		t.Fatalf("code = %q, want COMPILATION_REQUEST_INCOMPLETE", invalid.Code)
	}
	if result.Status() != app.SpineStatus(0) {
		t.Fatalf("zero request produced a populated result: %v", result.Status())
	}

	request := requestFromFixture(t, teamhos.Passing)
	request.Policy = 0
	result, err = app.Run(t.Context(), request, nil)
	if err == nil || !errors.As(err, &invalid) {
		t.Fatalf("zero policy error = %v, want InvalidInputError", err)
	}
	if invalid.Code != app.InputRunBindingIncomplete {
		t.Fatalf("code = %q, want RUN_BINDING_INPUT_INCOMPLETE", invalid.Code)
	}
	if _, ok := result.Plan(); ok {
		t.Fatal("invalid input retained a compiled plan")
	}
	if _, ok := result.ExecutionStatus(); ok {
		t.Fatal("invalid input claims an execution status")
	}
}

// Production break caught: losing the materialized-patch distinction would
// make an atomically rejected patch observationally identical to the
// pre-patch rejection, breaking the ratified rejected-operation projection.
func TestRunMaterializedPatchRejectionProjectsRejectedOperations(t *testing.T) {
	passing := requestFromFixture(t, teamhos.Passing)
	reference, err := app.Run(t.Context(), passing, nil)
	if err != nil {
		t.Fatalf("reference Run: %v", err)
	}
	finalState, ok := reference.State()
	if !ok {
		t.Fatal("reference run retained no state")
	}

	request := requestFromFixture(t, teamhos.Passing)
	collided, err := stateWithCollidingTeam(request.InitialState, finalState)
	if err != nil {
		t.Fatalf("colliding state: %v", err)
	}
	request.InitialState = collided

	observer := &sequenceObserver{}
	result, err := app.Run(t.Context(), request, observer)
	if err != nil {
		t.Fatalf("operation rejection returned Go error: %v", err)
	}
	failure, ok := result.SemanticFailure()
	if !ok || failure.Kind() != semantic.ProtectedInvariantFailed {
		t.Fatalf("failure kind = %q ok=%v", failure.Kind(), ok)
	}
	if failure.OperationInvariantCode() != semantic.OperationEntityIdentityCollision {
		t.Fatalf("failure code = %q, want OP_ENTITY_IDENTITY_COLLISION", failure.Code())
	}
	if _, materialized := failure.ProposedPatchDigest(); !materialized {
		t.Fatal("operation rejection lost its materialized patch digest")
	}
	assertAcceptedRules(t, result)
	if len(result.Checkpoints()) != 0 || len(result.Assessments()) != 0 {
		t.Fatal("rejected T1 retained checkpoint artifacts")
	}

	projection := findEndProjection(t, observer, app.PhaseExecuteTransition)
	if projection.Result != app.ResultProtectedInvariantFailed || projection.Code != app.CodeOpEntityIdentityCollision {
		t.Fatalf("projection result=%v code=%v", projection.Result, projection.Code)
	}
	if projection.RejectedInserts != 1 || projection.RejectedRelates != 2 || projection.RejectedUpdates != 0 {
		t.Fatalf("rejected operations = %d/%d/%d, want 1/2/0",
			projection.RejectedInserts, projection.RejectedRelates, projection.RejectedUpdates)
	}
	if projection.AcceptedInserts != 0 || projection.AcceptedRelates != 0 || projection.AcceptedUpdates != 0 {
		t.Fatal("rejected patch projected accepted operations")
	}
	if projection.InvariantFailures != 1 {
		t.Fatalf("invariant failures = %d, want 1", projection.InvariantFailures)
	}
}

// Production break caught: an observer that could change the returned result,
// or a panicking observer that could fail the spine, would make telemetry
// authoritative over semantics.
func TestRunObserverVariantsProduceIdenticalResults(t *testing.T) {
	for _, variant := range []teamhos.Variant{teamhos.Passing, teamhos.AnchorMismatch} {
		observers := map[string]app.Observer{
			"nil":       nil,
			"discard":   app.DiscardObserver(),
			"recording": &sequenceObserver{},
			"panicking": panickingObserver{},
		}
		projections := map[string][]byte{}
		for name, observer := range observers {
			result, err := app.Run(t.Context(), requestFromFixture(t, variant), observer)
			if err != nil {
				t.Fatalf("variant %d observer %s: %v", variant, name, err)
			}
			projections[name] = resultProjection(result)
		}
		for name, projection := range projections {
			if !bytes.Equal(projection, projections["nil"]) {
				t.Fatalf("variant %d observer %s changed the spine result", variant, name)
			}
		}
	}
}

// sequenceObserver records the closed observation stream through the public
// contract only.
type sequenceObserver struct {
	begins []app.PhaseObservation
	ends   []app.PhaseObservation
}

func (o *sequenceObserver) BeginPhase(_ context.Context, observation app.PhaseObservation) {
	o.begins = append(o.begins, observation)
}

func (o *sequenceObserver) EndPhase(_ context.Context, observation app.PhaseObservation) {
	o.ends = append(o.ends, observation)
}

type panickingObserver struct{}

func (panickingObserver) BeginPhase(context.Context, app.PhaseObservation) {
	panic("observer begin failure")
}

func (panickingObserver) EndPhase(context.Context, app.PhaseObservation) {
	panic("observer end failure")
}

func requestFromFixture(t *testing.T, variant teamhos.Variant) app.Request {
	t.Helper()
	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	return app.Request{
		Compilation:      inputs.Compilation,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	}
}

// stateWithCollidingTeam rebuilds the fixture initial state with the team
// entity the passing run deterministically synthesizes, so T1's insert must
// collide after materializing its atomic patch.
func stateWithCollidingTeam(initial, final semantic.State) (semantic.State, error) {
	existing := map[semantic.EntityRef]bool{}
	for _, entity := range initial.Entities() {
		existing[entity.Ref()] = true
	}
	entities := initial.Entities()
	for _, entity := range final.Entities() {
		if existing[entity.Ref()] {
			continue
		}
		team, err := semantic.NewEntity(entity.Ref(), entity.Fields())
		if err != nil {
			return semantic.State{}, err
		}
		entities = append(entities, team)
	}
	return semantic.NewState(initial.Schema(), initial.InputLineageID(), entities, nil)
}

func assertAcceptedRules(t *testing.T, result app.SpineResult, rules ...semantic.RuleID) {
	t.Helper()
	entries := result.Journal().Entries()
	if len(entries) != len(rules) {
		t.Fatalf("accepted journal has %d entries, want %d", len(entries), len(rules))
	}
	for i, rule := range rules {
		if entries[i].RuleID() != rule {
			t.Fatalf("accepted rule[%d] = %q, want %q", i, entries[i].RuleID(), rule)
		}
	}
}

func assertOnlyCheckpoint(t *testing.T, result app.SpineResult, key semantic.CheckpointKey) {
	t.Helper()
	checkpoints := result.Checkpoints()
	if len(checkpoints) != 1 || checkpoints[0].Checkpoint().Key != key {
		t.Fatalf("retained checkpoints = %d, want exactly %q", len(checkpoints), key)
	}
}

func assertCheckpointReadiness(t *testing.T, result app.SpineResult, key semantic.CheckpointKey, cm, optimizer semantic.ReadinessVerdict) {
	t.Helper()
	var artifact semantic.CheckpointArtifact
	found := false
	for _, candidate := range result.Checkpoints() {
		if candidate.Checkpoint().Key == key {
			artifact, found = candidate, true
		}
	}
	if !found {
		t.Fatalf("checkpoint %q not retained", key)
	}
	keysByProfile := map[semantic.ProfileID]semantic.ProfileKey{}
	for _, profile := range result.Profiles() {
		keysByProfile[profile.ID()] = profile.Key()
	}
	verdicts := map[semantic.ProfileKey]semantic.ReadinessVerdict{}
	for _, assessment := range result.Assessments() {
		if assessment.CheckpointArtifactID() != artifact.ID() {
			continue
		}
		profileKey, ok := keysByProfile[assessment.ProfileID()]
		if !ok {
			t.Fatalf("assessment for %q references an unretained profile", key)
		}
		verdicts[profileKey] = assessment.Verdict()
	}
	if len(verdicts) != 2 {
		t.Fatalf("checkpoint %q has %d assessments, want 2", key, len(verdicts))
	}
	if verdicts[teamhos.ProfileCM] != cm || verdicts[teamhos.ProfileOptimizer] != optimizer {
		t.Fatalf("checkpoint %q verdicts cm=%q optimizer=%q, want %q/%q",
			key, verdicts[teamhos.ProfileCM], verdicts[teamhos.ProfileOptimizer], cm, optimizer)
	}
}

func findEndProjection(t *testing.T, observer *sequenceObserver, phase app.Phase) app.MetricObservation {
	t.Helper()
	for _, observation := range observer.ends {
		if observation.Phase() == phase {
			return observation.MetricProjection()
		}
	}
	t.Fatalf("no EndPhase observation for phase %v", phase)
	return app.MetricObservation{}
}

func resultProjection(result app.SpineResult) []byte {
	var buffer bytes.Buffer
	buffer.WriteString(result.Status().String())
	if status, ok := result.ExecutionStatus(); ok {
		buffer.WriteString(status.String())
	}
	if plan, ok := result.Plan(); ok {
		buffer.Write(plan.CanonicalBytes())
	}
	for _, profile := range result.Profiles() {
		buffer.Write(profile.CanonicalBytes())
	}
	if failure, ok := result.CompilationFailure(); ok {
		buffer.Write(failure.CanonicalBytes())
	}
	if failure, ok := result.SemanticFailure(); ok {
		buffer.Write(failure.CanonicalBytes())
	}
	if state, ok := result.State(); ok {
		buffer.Write(state.CanonicalBytes())
	}
	for _, entry := range result.Journal().Entries() {
		buffer.Write(entry.CanonicalBytes())
	}
	for _, checkpoint := range result.Checkpoints() {
		buffer.Write(checkpoint.CanonicalBytes())
	}
	for _, assessment := range result.Assessments() {
		buffer.Write(assessment.CanonicalBytes())
	}
	return buffer.Bytes()
}
