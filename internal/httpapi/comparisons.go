package httpapi

import (
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CreateComparison defines an explicit correspondence between checkpoint declarations
// of two compiled plans and stores the identified comparison contract.
//
// Correspondence is validated against the compiled plans and identified through the
// kernel's constructors. This handler translates, stores, and projects; it makes no
// independent decision about whether correspondences are valid.
func (s *server) CreateComparison(w http.ResponseWriter, r *http.Request, params openapiv1.CreateComparisonParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Comparisons == nil || s.deps.Plans == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}

	var body openapiv1.CreateComparisonRequest
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeProblem(w, err)
		return
	}

	if body.BaselinePlanID == "" || body.CandidatePlanID == "" ||
		body.BaselineCheckpoint == "" || body.CandidateCheckpoint == "" ||
		body.ProfileID == "" || body.WorldID == "" || body.CorpusID == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	baselineRecord, found, err := s.deps.Plans.GetPlan(r.Context(), tenant, semantic.PlanID(body.BaselinePlanID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	candidateRecord, found, err := s.deps.Plans.GetPlan(r.Context(), tenant, semantic.PlanID(body.CandidatePlanID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	baselinePlan, present := baselineRecord.Compilation.Plan()
	if !present {
		writeProblem(w, problemInternalError, nil)
		return
	}
	candidatePlan, present := candidateRecord.Compilation.Plan()
	if !present {
		writeProblem(w, problemInternalError, nil)
		return
	}

	baselineID, declared := baselinePlan.CheckpointID(semantic.CheckpointKey(body.BaselineCheckpoint))
	if !declared {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	candidateID, declared := candidatePlan.CheckpointID(semantic.CheckpointKey(body.CandidateCheckpoint))
	if !declared {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	pairs, err := checkpointPairsFromWire(body.Correspondences)
	if err != nil {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	policy, err := semantic.NewComparisonPolicy(baselinePlan, candidatePlan, pairs)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  baselineID,
		Candidate: candidateID,
		Profile:   semantic.ProfileID(body.ProfileID),
		World:     semantic.WorldID(body.WorldID),
		Corpus:    semantic.CorpusID(body.CorpusID),
		Policy:    policy,
	})
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	if err := s.deps.Comparisons.PutComparison(r.Context(), tenant, comparison); err != nil {
		writeStorageProblem(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, comparisonToWire(ports.ProjectComparison(tenant, comparison)))
}

// GetComparison retrieves the stored comparison question and its declared correspondences.
//
// This operation reports the comparison question, never a comparability verdict.
// Comparability is verified from fresh re-executions during promotion evaluation.
func (s *server) GetComparison(w http.ResponseWriter, r *http.Request, comparisonID openapiv1.Digest, params openapiv1.GetComparisonParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Comparisons == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}

	record, found, err := s.deps.Comparisons.GetComparison(r.Context(), tenant, semantic.ComparisonID(comparisonID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	writeJSON(w, http.StatusOK, comparisonToWire(record))
}
