package app

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

type recordedCall struct {
	ctx         context.Context
	observation PhaseObservation
}

type recordingObserver struct {
	calls []recordedCall
}

func (o *recordingObserver) BeginPhase(ctx context.Context, observation PhaseObservation) {
	o.calls = append(o.calls, recordedCall{ctx: ctx, observation: observation})
}

func (o *recordingObserver) EndPhase(ctx context.Context, observation PhaseObservation) {
	o.calls = append(o.calls, recordedCall{ctx: ctx, observation: observation})
}

type panicObserver struct{}

func (panicObserver) BeginPhase(context.Context, PhaseObservation) { panic("begin") }
func (panicObserver) EndPhase(context.Context, PhaseObservation)   { panic("end") }

// expectedObservation is one step of a closed Begin/End sequence.
type expectedObservation struct {
	event      ObservationEvent
	phase      Phase
	result     PhaseResult
	transition TransitionKind
	checkpoint CheckpointKind
	profile    ProfileKind
	code       ObservationCode
}

func fixtureRequest(t *testing.T, variant teamhos.Variant) Request {
	t.Helper()
	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	return Request{
		Compilation:      inputs.Compilation,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	}
}

func assertSequence(t *testing.T, observer *recordingObserver, expected []expectedObservation) {
	t.Helper()
	if len(observer.calls) != len(expected) {
		t.Fatalf("observed %d calls, want %d", len(observer.calls), len(expected))
	}
	for i, want := range expected {
		got := observer.calls[i].observation
		if got.Event() != want.event || got.Phase() != want.phase {
			t.Fatalf("call %d: event/phase = %v/%v, want %v/%v", i, got.Event(), got.Phase(), want.event, want.phase)
		}
		result, hasResult := got.Result()
		if want.event == ObservationBegin && hasResult {
			t.Fatalf("call %d: begin observation carries a result", i)
		}
		if want.event == ObservationEnd && (!hasResult || result != want.result) {
			t.Fatalf("call %d: result = %v (present=%v), want %v", i, result, hasResult, want.result)
		}
		transition, _ := got.Transition()
		checkpoint, _ := got.Checkpoint()
		profile, _ := got.Profile()
		if transition != want.transition || checkpoint != want.checkpoint || profile != want.profile {
			t.Fatalf("call %d: kinds = %v/%v/%v, want %v/%v/%v",
				i, transition, checkpoint, profile, want.transition, want.checkpoint, want.profile)
		}
		code, _ := got.Code()
		if code != want.code {
			t.Fatalf("call %d: code = %v, want %v", i, code, want.code)
		}
	}
}

func passingSequence() []expectedObservation {
	return []expectedObservation{
		{event: ObservationBegin, phase: PhaseExecuteSpine},
		{event: ObservationBegin, phase: PhaseCompile},
		{event: ObservationEnd, phase: PhaseCompile, result: ResultSuccess},
		{event: ObservationBegin, phase: PhaseExecuteTransition, transition: TransitionFormTeam},
		{event: ObservationEnd, phase: PhaseExecuteTransition, result: ResultSuccess, transition: TransitionFormTeam},
		{event: ObservationBegin, phase: PhaseSealCheckpoint, checkpoint: CheckpointTeamFormed},
		{event: ObservationEnd, phase: PhaseSealCheckpoint, result: ResultSuccess, checkpoint: CheckpointTeamFormed},
		{event: ObservationBegin, phase: PhaseAssessReadiness, checkpoint: CheckpointTeamFormed, profile: ProfileCM},
		{event: ObservationEnd, phase: PhaseAssessReadiness, result: ResultReady, checkpoint: CheckpointTeamFormed, profile: ProfileCM},
		{event: ObservationBegin, phase: PhaseAssessReadiness, checkpoint: CheckpointTeamFormed, profile: ProfileOptimizer},
		{event: ObservationEnd, phase: PhaseAssessReadiness, result: ResultNeedsInput, checkpoint: CheckpointTeamFormed, profile: ProfileOptimizer},
		{event: ObservationBegin, phase: PhaseExecuteTransition, transition: TransitionAggregateTeamHOS},
		{event: ObservationEnd, phase: PhaseExecuteTransition, result: ResultSuccess, transition: TransitionAggregateTeamHOS},
		{event: ObservationBegin, phase: PhaseSealCheckpoint, checkpoint: CheckpointTeamHOSAggregated},
		{event: ObservationEnd, phase: PhaseSealCheckpoint, result: ResultSuccess, checkpoint: CheckpointTeamHOSAggregated},
		{event: ObservationBegin, phase: PhaseAssessReadiness, checkpoint: CheckpointTeamHOSAggregated, profile: ProfileCM},
		{event: ObservationEnd, phase: PhaseAssessReadiness, result: ResultReady, checkpoint: CheckpointTeamHOSAggregated, profile: ProfileCM},
		{event: ObservationBegin, phase: PhaseAssessReadiness, checkpoint: CheckpointTeamHOSAggregated, profile: ProfileOptimizer},
		{event: ObservationEnd, phase: PhaseAssessReadiness, result: ResultReady, checkpoint: CheckpointTeamHOSAggregated, profile: ProfileOptimizer},
		{event: ObservationEnd, phase: PhaseExecuteSpine, result: ResultSuccess},
	}
}

// Production break caught: a drifting phase order, missing end event, or
// leaked identity would break the closed observation contract Task 9 maps
// exhaustively into spans and metrics.
func TestRunObserverSequencePassing(t *testing.T) {
	observer := &recordingObserver{}
	result, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), observer, productionOperations())
	if err != nil {
		t.Fatalf("runWithOperations: %v", err)
	}
	if result.Status() != SpineSucceeded {
		t.Fatalf("status = %v", result.Status())
	}
	assertSequence(t, observer, passingSequence())

	spineBegin := observer.calls[0].observation
	if _, ok := spineBegin.PlanID(); ok {
		t.Fatal("spine begin observation carries a plan ID before compilation")
	}
	compileEnd := observer.calls[2].observation
	if _, ok := compileEnd.PlanID(); !ok {
		t.Fatal("compile end observation lacks the plan ID")
	}
	if _, ok := compileEnd.SemanticRunID(); ok {
		t.Fatal("compile end observation carries a run ID before binding")
	}
	t1Begin := observer.calls[3].observation
	if _, ok := t1Begin.SemanticRunID(); !ok {
		t.Fatal("execution observation lacks the semantic run ID")
	}
	if _, ok := t1Begin.ExecutionID(); !ok {
		t.Fatal("execution observation lacks the execution ID")
	}
	t1End := observer.calls[4].observation.MetricProjection()
	if t1End.AcceptedInserts != 1 || t1End.AcceptedRelates != 2 || t1End.AcceptedUpdates != 0 {
		t.Fatalf("T1 accepted operations = %d/%d/%d, want 1/2/0", t1End.AcceptedInserts, t1End.AcceptedRelates, t1End.AcceptedUpdates)
	}
	t2End := observer.calls[12].observation.MetricProjection()
	if t2End.AcceptedInserts != 0 || t2End.AcceptedRelates != 0 || t2End.AcceptedUpdates != 1 {
		t.Fatalf("T2 accepted operations = %d/%d/%d, want 0/0/1", t2End.AcceptedInserts, t2End.AcceptedRelates, t2End.AcceptedUpdates)
	}
}

// Production break caught: the rejected variant must end T2 with the exact
// protected classification and code, keep the C1 observations identical to
// the accepted prefix, and never observe a C2 phase.
func TestRunObserverSequenceRejected(t *testing.T) {
	observer := &recordingObserver{}
	result, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.AnchorMismatch), observer, productionOperations())
	if err != nil {
		t.Fatalf("runWithOperations: %v", err)
	}
	if result.Status() != SpineFailed {
		t.Fatalf("status = %v", result.Status())
	}
	expected := append(passingSequence()[:12:12],
		expectedObservation{event: ObservationEnd, phase: PhaseExecuteTransition,
			result: ResultProtectedInvariantFailed, transition: TransitionAggregateTeamHOS, code: CodeHOSAnchorMismatch},
		expectedObservation{event: ObservationEnd, phase: PhaseExecuteSpine,
			result: ResultProtectedInvariantFailed, code: CodeHOSAnchorMismatch},
	)
	assertSequence(t, observer, expected)
	projection := observer.calls[12].observation.MetricProjection()
	if projection.InvariantFailures != 1 {
		t.Fatalf("invariant failures = %d, want 1", projection.InvariantFailures)
	}
	if projection.RejectedInserts != 0 || projection.RejectedRelates != 0 || projection.RejectedUpdates != 0 {
		t.Fatal("pre-patch rejection projected rejected operations")
	}
}

// Production break caught: misclassifying machinery inability, dropping the
// independently verified frontier, or inventing a semantic failure for a Go
// error would collapse the two disjoint failure channels of the contract.
func TestRunMachineryInjectionMatrix(t *testing.T) {
	dependencyFailure := errors.New("dependency closed the connection")
	internalFailure := errors.New("impossible internal contradiction")

	reference, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), nil, productionOperations())
	if err != nil {
		t.Fatalf("reference run: %v", err)
	}
	t1StateDigest := reference.Journal().Entries()[0].ResultStateDigest()
	s0Digest := fixtureRequest(t, teamhos.Passing).InitialState.Digest()

	causes := []struct {
		name   string
		err    error
		result PhaseResult
		verify func(t *testing.T, err error)
	}{
		{name: "cancellation", err: context.Canceled, result: ResultCancelled, verify: func(t *testing.T, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("errors.Is(context.Canceled) failed for %v", err)
			}
		}},
		{name: "deadline", err: context.DeadlineExceeded, result: ResultCancelled, verify: func(t *testing.T, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("errors.Is(context.DeadlineExceeded) failed for %v", err)
			}
		}},
		{name: "infrastructure", err: InfrastructureUnavailableError{Code: InfrastructureDependencyUnavailable, Cause: dependencyFailure},
			result: ResultInfrastructureUnavailable, verify: func(t *testing.T, err error) {
				var infrastructure InfrastructureUnavailableError
				if !errors.As(err, &infrastructure) || infrastructure.Code != InfrastructureDependencyUnavailable {
					t.Fatalf("errors.As(InfrastructureUnavailableError) failed for %v", err)
				}
				if !errors.Is(err, dependencyFailure) {
					t.Fatal("infrastructure cause chain broken")
				}
				if strings.Contains(infrastructure.Error(), "connection") {
					t.Fatal("infrastructure error text leaks dependency text")
				}
			}},
		{name: "internal", err: internalFailure, result: ResultInternalError, verify: func(t *testing.T, err error) {
			if !errors.Is(err, internalFailure) {
				t.Fatalf("errors.Is(internal sentinel) failed for %v", err)
			}
		}},
	}

	boundaries := []struct {
		name         string
		inject       func(ops *operations, cause error)
		phase        Phase
		hasExecution bool
		verify       func(t *testing.T, result SpineResult)
	}{
		{name: "compile", phase: PhaseCompile,
			inject: func(ops *operations, cause error) {
				ops.compile = func(semantic.CompileRequest) (semantic.Compilation, error) {
					return semantic.Compilation{}, cause
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				if result.Status() != SpineStatus(0) {
					t.Fatalf("pre-work machinery failure returned a populated result: %v", result.Status())
				}
				if _, ok := result.Plan(); ok {
					t.Fatal("pre-work machinery failure retained a plan")
				}
			}},
		{name: "bind", phase: Phase(0),
			inject: func(ops *operations, cause error) {
				ops.bind = func(semantic.RunBindingRequest) (semantic.RunBinding, error) {
					return semantic.RunBinding{}, cause
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				if result.Status() != SpineFailed {
					t.Fatalf("status = %v, want SpineFailed", result.Status())
				}
				if _, ok := result.Plan(); !ok {
					t.Fatal("post-compile machinery failure lost the compiled plan")
				}
				if _, ok := result.ExecutionStatus(); ok {
					t.Fatal("pre-bind machinery failure claims an execution status")
				}
				if _, ok := result.State(); ok {
					t.Fatal("pre-bind machinery failure retained a state")
				}
			}},
		{name: "execute T1", hasExecution: true, phase: PhaseExecuteTransition,
			inject: func(ops *operations, cause error) {
				ops.execute = func(semantic.RunBinding, semantic.RuleID, semantic.State, semantic.Journal) (semantic.TransitionOutcome, error) {
					return semantic.TransitionOutcome{}, cause
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				status, ok := result.ExecutionStatus()
				if !ok || status != ExecutionFailed {
					t.Fatalf("execution status = %v ok=%v", status, ok)
				}
				state, ok := result.State()
				if !ok || len(result.Journal().Entries()) != 0 {
					t.Fatal("T1 machinery failure lost the bound initial state frontier")
				}
				if state.Digest() == t1StateDigest || state.Digest() != s0Digest {
					t.Fatal("T1 machinery failure did not stop at the verified S0 frontier")
				}
				if len(result.Checkpoints()) != 0 || len(result.Assessments()) != 0 || len(result.Profiles()) != 0 {
					t.Fatal("T1 machinery failure retained unreached artifacts")
				}
			}},
		{name: "seal C1", hasExecution: true, phase: PhaseSealCheckpoint,
			inject: func(ops *operations, cause error) {
				ops.seal = func(semantic.SealRequest) (semantic.SealOutcome, error) {
					return semantic.SealOutcome{}, cause
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				state, ok := result.State()
				if !ok || state.Digest() != t1StateDigest || len(result.Journal().Entries()) != 1 {
					t.Fatal("C1 seal machinery failure lost the verified T1 prefix")
				}
				if len(result.Checkpoints()) != 0 || len(result.Assessments()) != 0 || len(result.Profiles()) != 0 {
					t.Fatal("unverified C1 or dependents retained after its own seal machinery failure")
				}
			}},
		{name: "assess C1 cm", hasExecution: true, phase: PhaseAssessReadiness,
			inject: func(ops *operations, cause error) {
				ops.assess = func(semantic.AssessmentRequest) (semantic.AssessmentOutcome, error) {
					return semantic.AssessmentOutcome{}, cause
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				if len(result.Checkpoints()) != 1 {
					t.Fatal("verified C1 lost after its assessment machinery failure")
				}
				if len(result.Assessments()) != 0 || len(result.Profiles()) != 0 {
					t.Fatal("unverified assessments retained after machinery failure")
				}
			}},
		{name: "execute T2", hasExecution: true, phase: PhaseExecuteTransition,
			inject: func(ops *operations, cause error) {
				production := productionOperations().execute
				ops.execute = func(binding semantic.RunBinding, rule semantic.RuleID, state semantic.State, journal semantic.Journal) (semantic.TransitionOutcome, error) {
					if rule == teamhos.RuleAggregateTeamHOS {
						return semantic.TransitionOutcome{}, cause
					}
					return production(binding, rule, state, journal)
				}
			},
			verify: func(t *testing.T, result SpineResult) {
				state, ok := result.State()
				if !ok || state.Digest() != t1StateDigest {
					t.Fatal("post-C1 machinery failure lost the verified C1 state")
				}
				if len(result.Journal().Entries()) != 1 || len(result.Checkpoints()) != 1 {
					t.Fatal("post-C1 machinery failure lost the verified C1 prefix")
				}
				if len(result.Assessments()) != 2 || len(result.Profiles()) != 2 {
					t.Fatal("post-C1 machinery failure lost the completed C1 assessments")
				}
			}},
	}

	for _, boundary := range boundaries {
		for _, cause := range causes {
			t.Run(boundary.name+"/"+cause.name, func(t *testing.T) {
				ops := productionOperations()
				boundary.inject(&ops, cause.err)
				observer := &recordingObserver{}
				result, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), observer, ops)
				if err == nil {
					t.Fatal("machinery failure returned nil Go error")
				}
				cause.verify(t, err)
				if _, ok := result.SemanticFailure(); ok {
					t.Fatal("machinery failure invented a semantic failure report")
				}
				if _, ok := result.CompilationFailure(); ok {
					t.Fatal("machinery failure invented a compilation failure")
				}
				status, ok := result.ExecutionStatus()
				if ok != boundary.hasExecution {
					t.Fatalf("execution status present=%v, want %v", ok, boundary.hasExecution)
				}
				if ok && status != ExecutionFailed {
					t.Fatalf("execution status = %v, want failed", status)
				}
				boundary.verify(t, result)
				last := observer.calls[len(observer.calls)-1].observation
				lastResult, _ := last.Result()
				if last.Phase() != PhaseExecuteSpine || lastResult != cause.result {
					t.Fatalf("spine end = %v/%v, want execute_spine/%v", last.Phase(), lastResult, cause.result)
				}
				if boundary.phase != Phase(0) {
					phaseEnd := observer.calls[len(observer.calls)-2].observation
					phaseResult, _ := phaseEnd.Result()
					if phaseEnd.Phase() != boundary.phase || phaseResult != cause.result {
						t.Fatalf("phase end = %v/%v, want %v/%v", phaseEnd.Phase(), phaseResult, boundary.phase, cause.result)
					}
				}
			})
		}
	}
}

// Production break caught: a machinery failure before any work must return
// the zero result without observing phantom phases.
func TestRunCancelledBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	observer := &recordingObserver{}
	result, err := runWithOperations(ctx, fixtureRequest(t, teamhos.Passing), observer, productionOperations())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if result.Status() != SpineStatus(0) {
		t.Fatalf("cancelled-before-work result populated: %v", result.Status())
	}
	assertSequence(t, observer, []expectedObservation{
		{event: ObservationBegin, phase: PhaseExecuteSpine},
		{event: ObservationEnd, phase: PhaseExecuteSpine, result: ResultCancelled},
	})
}

// Production break caught: a typed invalid-input cause is only lawful at the
// initial canonical boundary; anywhere later it is an internal contradiction.
func TestRunInvalidInputClassification(t *testing.T) {
	ops := productionOperations()
	ops.compile = func(semantic.CompileRequest) (semantic.Compilation, error) {
		return semantic.Compilation{}, InvalidInputError{Code: InputCompilationRequestIncomplete}
	}
	observer := &recordingObserver{}
	result, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), observer, ops)
	var invalid InvalidInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want InvalidInputError", err)
	}
	if result.Status() != SpineStatus(0) {
		t.Fatal("invalid input at compile returned a populated result")
	}
	compileEnd := observer.calls[2].observation
	if compileResult, _ := compileEnd.Result(); compileResult != ResultInvalidInput {
		t.Fatalf("compile end result = %v, want invalid_input", compileResult)
	}

	ops = productionOperations()
	ops.seal = func(semantic.SealRequest) (semantic.SealOutcome, error) {
		return semantic.SealOutcome{}, InvalidInputError{Code: InputRunBindingIncomplete}
	}
	observer = &recordingObserver{}
	if _, err = runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), observer, ops); err == nil {
		t.Fatal("seal invalid-input injection returned nil error")
	}
	sealEnd := observer.calls[len(observer.calls)-2].observation
	if sealResult, _ := sealEnd.Result(); sealEnd.Phase() != PhaseSealCheckpoint || sealResult != ResultInternalError {
		t.Fatalf("post-execution invalid input classified as %v, want internal_error", sealResult)
	}
}

// Production break caught: an actual seal refusal is a deterministic typed
// semantic result; retrying it, erroring it, or retaining the refused
// artifact would each violate the ratified contract.
func TestRunSealRefusalIsTypedAndProjectsRejectedCheckpoint(t *testing.T) {
	ops := productionOperations()
	ops.seal = func(request semantic.SealRequest) (semantic.SealOutcome, error) {
		request.InvariantResults = request.InvariantResults[:len(request.InvariantResults)-1]
		return semantic.Seal(request)
	}
	observer := &recordingObserver{}
	result, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), observer, ops)
	if err != nil {
		t.Fatalf("typed seal refusal returned Go error: %v", err)
	}
	if result.Status() != SpineFailed {
		t.Fatalf("status = %v", result.Status())
	}
	failure, ok := result.SemanticFailure()
	if !ok || failure.Kind() != semantic.ArtifactIntegrityFailed {
		t.Fatalf("failure kind = %q ok=%v", failure.Kind(), ok)
	}
	if len(result.Checkpoints()) != 0 || len(result.Assessments()) != 0 || len(result.Profiles()) != 0 {
		t.Fatal("refused checkpoint or dependents retained")
	}
	if len(result.Journal().Entries()) != 1 {
		t.Fatal("verified T1 prefix lost on seal refusal")
	}
	expected := append(passingSequence()[:6:6],
		expectedObservation{event: ObservationEnd, phase: PhaseSealCheckpoint,
			result: ResultArtifactIntegrityFailed, checkpoint: CheckpointTeamFormed, code: CodeArtifactLinkInconsistent},
		expectedObservation{event: ObservationEnd, phase: PhaseExecuteSpine,
			result: ResultArtifactIntegrityFailed, code: CodeArtifactLinkInconsistent},
	)
	assertSequence(t, observer, expected)
}

// Production break caught: a suffix integrity failure must preserve the
// independently verified C1 checkpoint and its completed CM assessment while
// refusing everything that depends on the failed assessment.
func TestRunSuffixAssessIntegrityFailurePreservesC1(t *testing.T) {
	request := fixtureRequest(t, teamhos.Passing)
	calls := 0
	ops := productionOperations()
	ops.assess = func(assessRequest semantic.AssessmentRequest) (semantic.AssessmentOutcome, error) {
		calls++
		if calls == 2 {
			assessRequest.State = request.InitialState
		}
		return semantic.Assess(assessRequest)
	}
	result, err := runWithOperations(t.Context(), request, nil, ops)
	if err != nil {
		t.Fatalf("typed integrity failure returned Go error: %v", err)
	}
	failure, ok := result.SemanticFailure()
	if !ok || failure.Kind() != semantic.ArtifactIntegrityFailed {
		t.Fatalf("failure kind = %q ok=%v", failure.Kind(), ok)
	}
	report, ok := failure.ArtifactIntegrity()
	if !ok {
		t.Fatal("integrity failure lacks its typed report")
	}
	checkpoints := result.Checkpoints()
	if len(checkpoints) != 1 || checkpoints[0].Checkpoint().Key != teamhos.CheckpointTeamFormed {
		t.Fatal("independently verified C1 lost after suffix integrity failure")
	}
	if verified, ok := report.LastVerifiedCheckpointArtifactID(); !ok || verified != checkpoints[0].ID() {
		t.Fatal("integrity report does not verify the retained C1")
	}
	assessments := result.Assessments()
	if len(assessments) != 1 || assessments[0].Verdict() != semantic.Ready {
		t.Fatalf("retained assessments = %d, want only the completed CM assessment", len(assessments))
	}
	profiles := result.Profiles()
	if len(profiles) != 1 || profiles[0].Key() != teamhos.ProfileCM {
		t.Fatal("retained profiles are not exactly those referenced by retained assessments")
	}
}

// Production break caught: observer dispatch that let a panic escape, or that
// let a panicking observer perturb the result or error, would make telemetry
// able to fail the semantic spine.
func TestObserverPanicCannotChangeOutcome(t *testing.T) {
	quiet, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), DiscardObserver(), productionOperations())
	if err != nil {
		t.Fatalf("discard run: %v", err)
	}
	loud, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), panicObserver{}, productionOperations())
	if err != nil {
		t.Fatalf("panicking observer produced a Go error: %v", err)
	}
	if !bytes.Equal(inPackageProjection(quiet), inPackageProjection(loud)) {
		t.Fatal("panicking observer changed the spine result")
	}

	internalFailure := errors.New("internal contradiction")
	ops := productionOperations()
	ops.seal = func(semantic.SealRequest) (semantic.SealOutcome, error) {
		return semantic.SealOutcome{}, internalFailure
	}
	quietResult, quietErr := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), DiscardObserver(), ops)
	loudResult, loudErr := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), panicObserver{}, ops)
	if !errors.Is(quietErr, internalFailure) || !errors.Is(loudErr, internalFailure) || quietErr.Error() != loudErr.Error() {
		t.Fatalf("observer changed the machinery error: %v vs %v", quietErr, loudErr)
	}
	if !bytes.Equal(inPackageProjection(quietResult), inPackageProjection(loudResult)) {
		t.Fatal("panicking observer changed the machinery-failure frontier")
	}
}

// Production break caught: nil and DiscardObserver must be the same no-op.
func TestDiscardAndNilObserverIdentical(t *testing.T) {
	withNil, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), nil, productionOperations())
	if err != nil {
		t.Fatalf("nil observer run: %v", err)
	}
	withDiscard, err := runWithOperations(t.Context(), fixtureRequest(t, teamhos.Passing), DiscardObserver(), productionOperations())
	if err != nil {
		t.Fatalf("discard observer run: %v", err)
	}
	if !bytes.Equal(inPackageProjection(withNil), inPackageProjection(withDiscard)) {
		t.Fatal("nil and DiscardObserver produced different results")
	}
}

type callerContextKey string

// Production break caught: leaking the derived observation context into
// semantic machinery, sharing it across runs, or hiding the caller's values
// from observers would each break the private observation-context contract.
func TestObservationContextDerivation(t *testing.T) {
	base := context.WithValue(t.Context(), callerContextKey("caller"), "caller-value")
	if base.Value(observationContextKey{}) != nil {
		t.Fatal("caller context already carries the private observation marker")
	}
	first := &recordingObserver{}
	if _, err := runWithOperations(base, fixtureRequest(t, teamhos.Passing), first, productionOperations()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	second := &recordingObserver{}
	if _, err := runWithOperations(base, fixtureRequest(t, teamhos.Passing), second, productionOperations()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	firstToken := first.calls[0].ctx.Value(observationContextKey{})
	if firstToken == nil {
		t.Fatal("observer context lacks the private observation marker")
	}
	for _, call := range first.calls {
		if call.ctx == base {
			t.Fatal("observer received the caller's undecorated context")
		}
		if call.ctx.Value(callerContextKey("caller")) != "caller-value" {
			t.Fatal("observer context dropped the caller's own values")
		}
		if call.ctx.Value(observationContextKey{}) != firstToken {
			t.Fatal("one run used more than one observation context")
		}
	}
	if second.calls[0].ctx.Value(observationContextKey{}) == firstToken {
		t.Fatal("two runs shared one observation context token")
	}
	if base.Value(observationContextKey{}) != nil {
		t.Fatal("observation marker leaked into the caller's context")
	}
}

// Production break caught: the operations seam is the complete set of
// semantic entry points; none accepts a context, so the derived observation
// context is unrepresentable in any semantic call.
func TestSemanticOperationsCannotReceiveContext(t *testing.T) {
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	operationsType := reflect.TypeOf(operations{})
	for i := 0; i < operationsType.NumField(); i++ {
		field := operationsType.Field(i)
		if field.Type.Kind() != reflect.Func {
			t.Fatalf("operations field %s is not a function seam", field.Name)
		}
		for arg := 0; arg < field.Type.NumIn(); arg++ {
			if field.Type.In(arg).Implements(contextType) || field.Type.In(arg) == contextType {
				t.Fatalf("operations field %s accepts a context; observation context could leak", field.Name)
			}
		}
	}
}

// Production break caught: an ID, digest, or free-string field in the metric
// projection would leak identity into low-cardinality telemetry dimensions.
func TestMetricObservationFieldsAreBoundedEnumsAndCounts(t *testing.T) {
	appPackage := reflect.TypeOf(MetricObservation{}).PkgPath()
	metricType := reflect.TypeOf(MetricObservation{})
	for i := 0; i < metricType.NumField(); i++ {
		field := metricType.Field(i)
		switch field.Type.Kind() {
		case reflect.Uint8:
			if field.Type.PkgPath() != appPackage {
				t.Fatalf("field %s is not an app-owned closed enum", field.Name)
			}
		case reflect.Uint64:
			if field.Type != reflect.TypeOf(uint64(0)) {
				t.Fatalf("field %s is not a plain bounded count", field.Name)
			}
		default:
			t.Fatalf("field %s has kind %v; only bounded enums and counts are allowed", field.Name, field.Type.Kind())
		}
	}
}

// Production break caught: any string-typed accessor beyond the three
// observed trace IDs, or any accessor returning a semantic-kernel type, would
// widen the closed observation carrier into a generic metadata channel.
func TestPhaseObservationExposesOnlyObservedTraceIDs(t *testing.T) {
	allowedStringReturns := map[string]reflect.Type{
		"PlanID":        reflect.TypeOf(ObservedPlanID("")),
		"SemanticRunID": reflect.TypeOf(ObservedSemanticRunID("")),
		"ExecutionID":   reflect.TypeOf(ObservedExecutionID("")),
	}
	observationType := reflect.TypeOf(PhaseObservation{})
	appPackage := observationType.PkgPath()
	for i := 0; i < observationType.NumMethod(); i++ {
		method := observationType.Method(i)
		for out := 0; out < method.Func.Type().NumOut(); out++ {
			returned := method.Func.Type().Out(out)
			if returned.PkgPath() != "" && returned.PkgPath() != appPackage {
				t.Fatalf("method %s returns foreign type %v", method.Name, returned)
			}
			switch returned.Kind() {
			case reflect.String:
				if allowedStringReturns[method.Name] != returned {
					t.Fatalf("method %s returns string-kind %v; only the three observed IDs may", method.Name, returned)
				}
			case reflect.Map, reflect.Interface, reflect.Slice:
				t.Fatalf("method %s returns open-ended kind %v", method.Name, returned.Kind())
			}
		}
	}
	for i := 0; i < observationType.NumField(); i++ {
		if observationType.Field(i).IsExported() {
			t.Fatalf("PhaseObservation field %s is exported", observationType.Field(i).Name)
		}
	}
}

func inPackageProjection(result SpineResult) []byte {
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
