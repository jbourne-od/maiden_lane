package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CreateExecution runs a compiled plan over pinned inputs and returns the
// complete result.
//
// INTERIM DEVIATION: the High-Level Design specifies 202 Accepted with a later
// read. That requires a worker mode and durable storage, neither of which
// exists, so this executes synchronously and returns 200. The response body is
// the projection the future asynchronous read will return, so clients written
// now keep working when the asynchronous shape lands.
//
// The result matrix is the important contract here. A deterministic semantic
// rejection is a 200 carrying a typed failure, because the run produced a real
// answer: a failed protected invariant means the computation correctly refused
// to commit, which is a finding, not a server error. Only the application's
// inability to reach an answer becomes a problem document.
func (s *server) CreateExecution(w http.ResponseWriter, r *http.Request, params openapiv1.CreateExecutionParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}

	var body openapiv1.CreateExecutionRequest
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeProblem(w, err)
		return
	}

	record, found, err := s.deps.Plans.GetPlan(r.Context(), tenant, semantic.PlanID(body.PlanID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		// A plan belonging to another tenant is indistinguishable from one that
		// does not exist.
		writeProblem(w, problemNotFound, nil)
		return
	}

	state, err := stateFromWire(record.Schema, body.InitialState)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}
	world, err := worldFromWire(body.World)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}
	executor, err := executorIdentityFromWire(body.ExecutorIdentity)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}
	policy, err := provenancePolicyFromWire(body.ProvenancePolicy)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	request, ok := compileRequestFor(record)
	if !ok {
		// A stored record whose compilation carries no plan is an internal
		// contradiction: nothing accepts a plan into storage without one.
		writeProblem(w, problemInternalError, nil)
		return
	}

	result, err := s.deps.Runner.Run(r.Context(), app.Request{
		Compilation:      request,
		InitialState:     state,
		World:            world,
		ExecutorIdentity: executor,
		Policy:           policy,
	}, s.deps.Observer)
	if err != nil {
		writeMachineryProblem(w, err)
		return
	}

	// Compilation is deterministic, so re-running it inside the use case must
	// reproduce the plan that was stored.
	//
	// The absent case matters as much as the mismatched one. If the retained
	// input ever stopped compiling, app.Run would legitimately report
	// invalid_plan with no plan and a nil error, and returning that verbatim
	// would tell a client that a plan they had already created and had accepted
	// is invalid. Once /v1/plans has established a PlanID, an execution that
	// cannot reproduce it is an integrity failure on this side of the boundary,
	// never a verdict about the caller's request.
	plan, present := result.Plan()
	if !present || plan.ID() != record.PlanID {
		writeProblem(w, problemInternalError, nil)
		return
	}

	writeJSON(w, http.StatusOK, executionToWire(record.PlanID, result))
}

// writeMachineryProblem maps the application's typed inability onto the closed
// problem vocabulary. It never renders a cause: the classification travels,
// the cause stays in the process.
func writeMachineryProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, problemDependencyUnavailable, nil)
	case errors.As(err, &app.InfrastructureUnavailableError{}):
		writeProblem(w, problemDependencyUnavailable, nil)
	case errors.As(err, &app.InvalidInputError{}):
		writeProblem(w, problemInvalidSemanticInput, nil)
	default:
		writeProblem(w, problemInternalError, nil)
	}
}

// executionToWire projects the spine result. Identities are copied from the
// kernel's artifacts; nothing here is recomputed.
func executionToWire(planID semantic.PlanID, result app.SpineResult) openapiv1.Execution {
	projected := openapiv1.Execution{
		PlanID:      openapiv1.Digest(planID),
		SpineStatus: spineStatusToWire(result.Status()),
		Checkpoints: checkpointsToWire(result.Checkpoints()),
		Assessments: assessmentsToWire(result.Assessments(), result.Profiles()),
	}
	if runID, present := result.SemanticRunID(); present {
		digest := openapiv1.Digest(runID)
		projected.SemanticRunID = &digest
	}
	if executionID, present := result.ExecutionID(); present {
		digest := openapiv1.Digest(executionID)
		projected.ExecutionID = &digest
	}
	if inputID, present := result.InputID(); present {
		digest := openapiv1.Digest(inputID)
		projected.InputID = &digest
	}
	if worldID, present := result.WorldID(); present {
		digest := openapiv1.Digest(worldID)
		projected.WorldID = &digest
	}
	if prefix, present := result.JournalPrefixDigest(); present {
		digest := openapiv1.Digest(prefix)
		projected.JournalPrefixDigest = &digest
	}
	if status, present := result.ExecutionStatus(); present {
		executionStatus := executionStatusToWire(status)
		projected.ExecutionStatus = &executionStatus
	}
	if state, present := result.State(); present {
		digest := openapiv1.Digest(state.Digest())
		projected.FinalStateDigest = &digest
	}
	if rules := acceptedRules(result); len(rules) > 0 {
		projected.AcceptedRules = &rules
	}
	if failure, present := result.SemanticFailure(); present {
		semanticFailure := semanticFailureToWire(failure)
		projected.Failure = &semanticFailure
	}
	return projected
}

func spineStatusToWire(status app.SpineStatus) openapiv1.ExecutionSpineStatus {
	switch status {
	case app.SpineSucceeded:
		return openapiv1.ExecutionSpineStatusSucceeded
	case app.SpineInvalidPlan:
		return openapiv1.ExecutionSpineStatusInvalidPlan
	default:
		return openapiv1.ExecutionSpineStatusFailed
	}
}

func executionStatusToWire(status app.ExecutionStatus) openapiv1.ExecutionExecutionStatus {
	switch status {
	case app.ExecutionSucceeded:
		return openapiv1.ExecutionExecutionStatusSucceeded
	case app.ExecutionFailed:
		return openapiv1.ExecutionExecutionStatusFailed
	case app.ExecutionRunning:
		return openapiv1.ExecutionExecutionStatusRunning
	default:
		return openapiv1.ExecutionExecutionStatusPending
	}
}

// acceptedRules projects committed transitions in accepted order. Rejections
// never appear, because they never entered the journal.
func acceptedRules(result app.SpineResult) []string {
	entries := result.Journal().Entries()
	rules := make([]string, 0, len(entries))
	for _, entry := range entries {
		rules = append(rules, string(entry.RuleID()))
	}
	return rules
}

func checkpointsToWire(artifacts []semantic.CheckpointArtifact) []openapiv1.Checkpoint {
	projected := make([]openapiv1.Checkpoint, 0, len(artifacts))
	for _, artifact := range artifacts {
		projected = append(projected, openapiv1.Checkpoint{
			CheckpointKey:        string(artifact.Checkpoint().Key),
			CheckpointID:         openapiv1.Digest(artifact.CheckpointID()),
			CheckpointArtifactID: openapiv1.Digest(artifact.ID()),
			Digest:               openapiv1.Digest(artifact.Digest()),
			StateDigest:          openapiv1.Digest(artifact.StateDigest()),
		})
	}
	return projected
}

// assessmentsToWire projects readiness answers. The profile key comes from the
// compiled profiles the result retained, because an assessment records the
// profile identity rather than its operational key.
func assessmentsToWire(assessments []semantic.Assessment, profiles []semantic.CompiledProfile) []openapiv1.Assessment {
	keys := make(map[semantic.ProfileID]string, len(profiles))
	for _, profile := range profiles {
		keys[profile.ID()] = string(profile.Key())
	}

	projected := make([]openapiv1.Assessment, 0, len(assessments))
	for _, assessment := range assessments {
		// Missing requirement codes are deduplicated across selected entities:
		// the same requirement failing for several entities is one reason the
		// checkpoint is not ready, not several.
		seen := map[string]bool{}
		missing := make([]string, 0)
		for _, entity := range assessment.EntityResults() {
			for _, requirement := range entity.Results() {
				code := string(requirement.Code())
				if requirement.Satisfied() || seen[code] {
					continue
				}
				seen[code] = true
				missing = append(missing, code)
			}
		}
		slices.Sort(missing)
		projected = append(projected, openapiv1.Assessment{
			AssessmentID:         openapiv1.Digest(assessment.ID()),
			Digest:               openapiv1.Digest(assessment.Digest()),
			CheckpointArtifactID: openapiv1.Digest(assessment.CheckpointArtifactID()),
			ProfileID:            openapiv1.Digest(assessment.ProfileID()),
			ProfileKey:           keys[assessment.ProfileID()],
			Verdict:              verdictToWire(assessment.Verdict()),
			MissingRequirements:  &missing,
		})
	}
	return projected
}

func verdictToWire(verdict semantic.ReadinessVerdict) openapiv1.ReadinessVerdict {
	if verdict == semantic.Ready {
		return openapiv1.ReadinessVerdictReady
	}
	return openapiv1.ReadinessVerdictNeedsInput
}

func semanticFailureToWire(failure semantic.FailureReport) openapiv1.SemanticFailure {
	projected := openapiv1.SemanticFailure{}
	if failure.Kind() == semantic.ArtifactIntegrityFailed {
		projected.Kind = openapiv1.SemanticFailureKindArtifactIntegrityFailed
		if report, present := failure.ArtifactIntegrity(); present {
			code := string(report.Code())
			projected.Code = &code
		}
		return projected
	}

	projected.Kind = openapiv1.SemanticFailureKindProtectedInvariantFailed
	if operation := failure.OperationInvariantCode(); operation != "" {
		code := string(operation)
		projected.Code = &code
	} else if invariant := failure.InvariantCode(); invariant != "" {
		code := string(invariant)
		projected.Code = &code
	}
	return projected
}
