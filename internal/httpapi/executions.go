package httpapi

import (
	"errors"
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CreateExecution accepts a plan for execution and returns its identities.
//
// The response is 202: the execution is queued and a worker runs it. There is no
// synchronous variant, so there is one execution path and one lifecycle to reason
// about, and worker availability never affects this response.
//
// Submission is idempotent without needing an idempotency key. The identities are
// derived from the semantic request by the kernel, so a resubmission is
// necessarily the same execution and finds it already present. Nothing here
// allocates an identity and a caller cannot supply one.
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
	if body.PlanID == "" {
		// A document with no plan reference is structurally incomplete, not a
		// lookup that missed.
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	record, found, err := s.deps.Plans.GetPlan(r.Context(), tenant, semantic.PlanID(body.PlanID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}
	plan, present := record.Compilation.Plan()
	if !present {
		writeProblem(w, problemInternalError, nil)
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

	// Binding derives the identities and, in doing so, verifies these inputs can
	// actually be executed against this plan. Doing it here means an unusable
	// request is refused now rather than queued and failed later, which matters
	// because a terminally failed execution cannot be resubmitted: its identity
	// is derived, so the same request would just find the failed row.
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: state, World: world,
		ExecutorIdentity: executor, Policy: policy,
	})
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	request := ports.ExecutionRequest{
		TenantID:    tenant,
		ExecutionID: binding.ExecutionID(),
		RunID:       binding.SemanticRunID(),
		PlanID:      plan.ID(),
		Input: ports.ExecutionInput{
			InitialState: state, World: world,
			ExecutorIdentity: executor, Policy: policy,
		},
	}
	if _, err := s.deps.Executions.Enqueue(r.Context(), request); err != nil {
		writeStorageProblem(w, err)
		return
	}

	// The same 202 whether this created the execution or found it. A caller
	// resubmitting learns the identities either way, and there is nothing
	// meaningfully different to tell them.
	writeJSON(w, http.StatusAccepted, openapiv1.ExecutionAccepted{
		ExecutionID:     openapiv1.Digest(request.ExecutionID),
		SemanticRunID:   openapiv1.Digest(request.RunID),
		PlanID:          openapiv1.Digest(request.PlanID),
		ExecutionStatus: openapiv1.ExecutionStatusPending,
	})
}

// GetExecution reports an execution's status and, once finished, its result.
func (s *server) GetExecution(w http.ResponseWriter, r *http.Request, executionID openapiv1.Digest, params openapiv1.GetExecutionParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}

	record, found, err := s.deps.Executions.Get(r.Context(), tenant, semantic.ExecutionID(executionID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		// Another tenant's execution is indistinguishable from one that does
		// not exist.
		writeProblem(w, problemNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, executionToWire(record))
}

// ReattemptExecution unblocks a failed execution for retry.
func (s *server) ReattemptExecution(
	w http.ResponseWriter, r *http.Request, executionID openapiv1.Digest, params openapiv1.ReattemptExecutionParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Executions == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if executionID == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	record, found, err := s.deps.Executions.Get(r.Context(), tenant, semantic.ExecutionID(executionID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}
	if record.Status != ports.ExecutionFailed || record.Result != nil {
		// A run that produced a deterministic semantic result cannot be reattempted,
		// and an execution that is not in a failed state cannot be retried.
		writeProblem(w, problemExecutionConflict, nil)
		return
	}

	if err := s.deps.Executions.Reattempt(r.Context(), tenant, semantic.ExecutionID(executionID)); err != nil {
		if errors.Is(err, ports.ErrExecutionNotReattemptable) {
			writeProblem(w, problemExecutionConflict, nil)
			return
		}
		writeStorageProblem(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, openapiv1.ExecutionAccepted{
		ExecutionID:     openapiv1.Digest(record.Request.ExecutionID),
		SemanticRunID:   openapiv1.Digest(record.Request.RunID),
		PlanID:          openapiv1.Digest(record.Request.PlanID),
		ExecutionStatus: openapiv1.ExecutionStatusPending,
	})
}

// GetExecutionCheckpoint retrieves detailed artifact records for one sealed checkpoint of a finished execution.
func (s *server) GetExecutionCheckpoint(
	w http.ResponseWriter, r *http.Request, executionID openapiv1.Digest, checkpointKey string, params openapiv1.GetExecutionCheckpointParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Executions == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if executionID == "" || checkpointKey == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	record, found, err := s.deps.Executions.Get(r.Context(), tenant, semantic.ExecutionID(executionID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found || record.Result == nil {
		writeProblem(w, problemNotFound, nil)
		return
	}

	var matchedCheckpoint *ports.SealedCheckpoint
	for _, cp := range record.Result.Checkpoints {
		if string(cp.CheckpointKey) == checkpointKey {
			matchedCheckpoint = &cp
			break
		}
	}
	if matchedCheckpoint == nil {
		writeProblem(w, problemNotFound, nil)
		return
	}

	matchedAssessments := make([]openapiv1.Assessment, 0)
	for _, a := range record.Result.Assessments {
		if a.CheckpointArtifactID == matchedCheckpoint.CheckpointArtifactID {
			missing := make([]string, 0, len(a.MissingRequirements))
			for _, code := range a.MissingRequirements {
				missing = append(missing, string(code))
			}
			matchedAssessments = append(matchedAssessments, openapiv1.Assessment{
				AssessmentID:         openapiv1.Digest(a.AssessmentID),
				Digest:               openapiv1.Digest(a.Digest),
				CheckpointArtifactID: openapiv1.Digest(a.CheckpointArtifactID),
				ProfileID:            openapiv1.Digest(a.ProfileID),
				ProfileKey:           string(a.ProfileKey),
				Verdict:              verdictToWire(a.Verdict),
				MissingRequirements:  &missing,
			})
		}
	}

	writeJSON(w, http.StatusOK, openapiv1.ExecutionCheckpointDetail{
		CheckpointKey:         string(matchedCheckpoint.CheckpointKey),
		CheckpointID:          openapiv1.Digest(matchedCheckpoint.CheckpointID),
		CheckpointArtifactID:  openapiv1.Digest(matchedCheckpoint.CheckpointArtifactID),
		Digest:                openapiv1.Digest(matchedCheckpoint.Digest),
		StateDigest:           openapiv1.Digest(matchedCheckpoint.StateDigest),
		InvariantResultDigest: openapiv1.Digest(matchedCheckpoint.InvariantResultDigest),
		Assessments:           matchedAssessments,
	})
}

// executionToWire projects a stored execution outward.
//
// The worker projects a spine result into the stored form, and this projects the
// stored form onto the wire: one projection each, so a field cannot be carried
// correctly in one place and wrongly in the other.
func executionToWire(record ports.ExecutionRecord) openapiv1.Execution {
	projected := openapiv1.Execution{
		ExecutionID:     openapiv1.Digest(record.Request.ExecutionID),
		SemanticRunID:   openapiv1.Digest(record.Request.RunID),
		PlanID:          openapiv1.Digest(record.Request.PlanID),
		ExecutionStatus: executionStatusToWire(record.Status),
	}
	if record.FailureReason != "" {
		reason := record.FailureReason
		projected.FailureReason = &reason
	}
	if record.Result != nil {
		result := resultToWire(*record.Result)
		projected.Result = &result
	}
	return projected
}

func executionStatusToWire(status ports.ExecutionStatus) openapiv1.ExecutionStatus {
	switch status {
	case ports.ExecutionSucceeded:
		return openapiv1.ExecutionStatusSucceeded
	case ports.ExecutionFailed:
		return openapiv1.ExecutionStatusFailed
	case ports.ExecutionRunning:
		return openapiv1.ExecutionStatusRunning
	default:
		return openapiv1.ExecutionStatusPending
	}
}

func resultToWire(result ports.ExecutionResult) openapiv1.ExecutionResult {
	projected := openapiv1.ExecutionResult{
		SpineStatus: openapiv1.ExecutionResultSpineStatus(result.SpineStatus),
		Checkpoints: make([]openapiv1.Checkpoint, 0, len(result.Checkpoints)),
		Assessments: make([]openapiv1.Assessment, 0, len(result.Assessments)),
	}
	if result.InputID != "" {
		digest := openapiv1.Digest(result.InputID)
		projected.InputID = &digest
	}
	if result.WorldID != "" {
		digest := openapiv1.Digest(result.WorldID)
		projected.WorldID = &digest
	}
	if result.FinalStateDigest != "" {
		digest := openapiv1.Digest(result.FinalStateDigest)
		projected.FinalStateDigest = &digest
	}
	if result.JournalPrefixDigest != "" {
		digest := openapiv1.Digest(result.JournalPrefixDigest)
		projected.JournalPrefixDigest = &digest
	}
	if len(result.AcceptedRules) > 0 {
		rules := make([]string, 0, len(result.AcceptedRules))
		for _, rule := range result.AcceptedRules {
			rules = append(rules, string(rule))
		}
		projected.AcceptedRules = &rules
	}

	for _, checkpoint := range result.Checkpoints {
		// The sealed bytes stay in storage. A client receives identities and
		// digests; serving artifact bodies is a later decision and must not leak
		// into this projection by accident.
		projected.Checkpoints = append(projected.Checkpoints, openapiv1.Checkpoint{
			CheckpointKey:        string(checkpoint.CheckpointKey),
			CheckpointID:         openapiv1.Digest(checkpoint.CheckpointID),
			CheckpointArtifactID: openapiv1.Digest(checkpoint.CheckpointArtifactID),
			Digest:               openapiv1.Digest(checkpoint.Digest),
			StateDigest:          openapiv1.Digest(checkpoint.StateDigest),
		})
	}
	for _, assessment := range result.Assessments {
		missing := make([]string, 0, len(assessment.MissingRequirements))
		for _, code := range assessment.MissingRequirements {
			missing = append(missing, string(code))
		}
		projected.Assessments = append(projected.Assessments, openapiv1.Assessment{
			AssessmentID:         openapiv1.Digest(assessment.AssessmentID),
			Digest:               openapiv1.Digest(assessment.Digest),
			CheckpointArtifactID: openapiv1.Digest(assessment.CheckpointArtifactID),
			ProfileID:            openapiv1.Digest(assessment.ProfileID),
			ProfileKey:           string(assessment.ProfileKey),
			Verdict:              verdictToWire(assessment.Verdict),
			MissingRequirements:  &missing,
		})
	}
	if result.Failure != nil {
		failure := openapiv1.SemanticFailure{Kind: failureKindToWire(result.Failure.Kind)}
		if result.Failure.Code != "" {
			code := result.Failure.Code
			failure.Code = &code
		}
		projected.Failure = &failure
	}
	return projected
}

func verdictToWire(verdict semantic.ReadinessVerdict) openapiv1.ReadinessVerdict {
	if verdict == semantic.Ready {
		return openapiv1.ReadinessVerdictReady
	}
	return openapiv1.ReadinessVerdictNeedsInput
}

func failureKindToWire(kind semantic.FailureKind) openapiv1.SemanticFailureKind {
	if kind == semantic.ArtifactIntegrityFailed {
		return openapiv1.SemanticFailureKindArtifactIntegrityFailed
	}
	return openapiv1.SemanticFailureKindProtectedInvariantFailed
}
