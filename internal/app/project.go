package app

import (
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Project turns a spine result into the stored projection.
//
// The sealed artifacts' canonical bytes travel with their identities, because
// sealing produces an artifact and keeping only its digest would keep the receipt
// while discarding the goods.

// It lives here rather than in the worker that writes it because rehydration has to
// compare a re-derived result against a stored one, and two implementations of the
// projection could disagree. A disagreement there is the worst kind: it would surface
// either as a false integrity failure on a faithful store, or as a real divergence
// nobody noticed. One implementation makes the comparison exact by construction.
func Project(request ports.ExecutionRequest, result SpineResult) (ports.ExecutionResult, error) {
	// The claim must not be projected onto artifacts that contradict it.
	//
	// This is the write-path half of the same check Rehydrate performs, and it is
	// currently unreachable: the worker verifies a queued request's identity before
	// executing it, and the worker is the only caller of Complete. It is checked anyway,
	// on the same grounds as promotion's coherence check -- "unreachable" is a claim about
	// today's code, and the guarantee otherwise rests on call order inside one function
	// rather than on structure.
	//
	// The comparison is conditional on the result having established an identity, because
	// it legitimately may not have: an invalid plan produces a terminal result with no
	// binding, and that result still has to be stored under the request's identity.
	if run, ok := result.SemanticRunID(); ok && run != request.RunID {
		return ports.ExecutionResult{}, IntegrityError{
			Code: IntegrityResultDiverged, Detail: "request.semanticRunID"}
	}
	if execution, ok := result.ExecutionID(); ok && execution != request.ExecutionID {
		return ports.ExecutionResult{}, IntegrityError{
			Code: IntegrityResultDiverged, Detail: "request.executionID"}
	}

	projected := ports.ExecutionResult{
		TenantID:    request.TenantID,
		ExecutionID: request.ExecutionID,
		Status:      ports.ExecutionSucceeded,
		SpineStatus: result.Status().String(),
	}
	if result.Status() != SpineSucceeded {
		// A deterministic refusal is still a completed execution; the lifecycle
		// status records that the computation did not succeed, while the result
		// records what it decided.
		projected.Status = ports.ExecutionFailed
	}
	if state, ok := result.State(); ok {
		projected.FinalStateDigest = state.Digest()
	}
	if prefix, ok := result.JournalPrefixDigest(); ok {
		projected.JournalPrefixDigest = prefix
	}
	if inputID, ok := result.InputID(); ok {
		projected.InputID = inputID
	}
	if worldID, ok := result.WorldID(); ok {
		projected.WorldID = worldID
	}
	for _, entry := range result.Journal().Entries() {
		projected.AcceptedRules = append(projected.AcceptedRules, entry.RuleID())
	}

	for _, artifact := range result.Checkpoints() {
		projected.Checkpoints = append(projected.Checkpoints, ports.SealedCheckpoint{
			CheckpointKey:        artifact.Checkpoint().Key,
			CheckpointID:         artifact.CheckpointID(),
			CheckpointArtifactID: artifact.ID(),
			Digest:               artifact.Digest(),
			StateDigest:          artifact.StateDigest(),
			CanonicalBytes:       artifact.CanonicalBytes(),
			// The commitment and the witness travel together with the artifact
			// that binds them, so no later reader has to work out which evidence
			// belongs to which seal, and the witness is never stored somewhere
			// the digest that validates it cannot be found.
			InvariantResultDigest:         artifact.InvariantResultDigest(),
			InvariantResultCanonicalBytes: artifact.InvariantResultCanonicalBytes(),
		})
	}

	keys := make(map[semantic.ProfileID]semantic.ProfileKey, len(result.Profiles()))
	for _, profile := range result.Profiles() {
		keys[profile.ID()] = profile.Key()
	}
	for _, assessment := range result.Assessments() {
		projected.Assessments = append(projected.Assessments, ports.StoredAssessment{
			AssessmentID:         assessment.ID(),
			Digest:               assessment.Digest(),
			CheckpointArtifactID: assessment.CheckpointArtifactID(),
			ProfileID:            assessment.ProfileID(),
			ProfileKey:           keys[assessment.ProfileID()],
			Verdict:              assessment.Verdict(),
			MissingRequirements:  missingRequirements(assessment),
			CanonicalBytes:       assessment.CanonicalBytes(),
		})
	}

	if failure, ok := result.SemanticFailure(); ok {
		projected.Failure = &ports.StoredFailure{Kind: failure.Kind(), Code: failureCode(failure)}
	}
	return projected, nil
}

// missingRequirements collects the unsatisfied requirement codes, deduplicated
// across selected entities: the same requirement failing for several entities is
// one reason the checkpoint is not ready, not several.
func missingRequirements(assessment semantic.Assessment) []semantic.RequirementCode {
	seen := map[semantic.RequirementCode]bool{}
	codes := make([]semantic.RequirementCode, 0)
	for _, entity := range assessment.EntityResults() {
		for _, requirement := range entity.Results() {
			if requirement.Satisfied() || seen[requirement.Code()] {
				continue
			}
			seen[requirement.Code()] = true
			codes = append(codes, requirement.Code())
		}
	}
	slices.Sort(codes)
	return codes
}

func failureCode(failure semantic.FailureReport) string {
	if report, ok := failure.ArtifactIntegrity(); ok {
		return string(report.Code())
	}
	if operation := failure.OperationInvariantCode(); operation != "" {
		return string(operation)
	}
	return string(failure.InvariantCode())
}
