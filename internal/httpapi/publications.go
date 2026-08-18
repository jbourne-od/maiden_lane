package httpapi

import (
	"errors"
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CreatePublication evaluates the promotion gate and publishes if it authorizes.
//
// A refusal is a 200, not a problem. The gate produces a per-clause result and
// returning it is the point: an operator told only "refused" cannot act, while a
// clause list says which requirement was not met and whether the answer was fail or
// not_evaluated. This is the same rule a needs_input readiness verdict follows,
// because in both cases the computation produced a real answer.
//
// The request names identities and the service re-derives the evidence. That is not
// a convenience: authorization rests on artifacts the kernel produced, which cannot
// be transmitted or reconstructed from bytes, so a client that could supply evidence
// would be supplying something nothing had verified.
func (s *server) CreatePublication(w http.ResponseWriter, r *http.Request, params openapiv1.CreatePublicationParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Policies == nil || s.deps.Publications == nil {
		// Configured without a control plane. Reported as unavailable rather than
		// as a refusal, because a refusal would claim the gate was evaluated.
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}

	var body openapiv1.CreatePublicationRequest
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeProblem(w, err)
		return
	}
	if body.CustomerID == "" || body.Target == "" || body.ExecutionID == "" ||
		body.CheckpointArtifactID == "" || body.AssessmentID == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}
	if body.ExpectedCurrentVersion < 0 {
		// The schema forbids it, so this is the router's own guard against a
		// document that reached the handler without validation.
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	// Re-derive the execution. This is where the stored form becomes authenticated
	// kernel values, and where a store whose contents cannot be reproduced is caught.
	rehydrated, err := app.Rehydrate(r.Context(),
		app.RehydrationStores{Plans: s.deps.Plans, Executions: s.deps.Executions},
		tenant, semantic.ExecutionID(body.ExecutionID))
	if err != nil {
		writeRehydrationProblem(w, err)
		return
	}
	switch rehydrated.Outcome() {
	case app.RehydrationAbsent:
		writeProblem(w, problemNotFound, nil)
		return
	case app.RehydrationPending, app.RehydrationUnattempted:
		// An execution with no result has no sealed checkpoint to publish. Reported
		// as absent rather than as a conflict: the checkpoint the caller named does
		// not exist, which is what a 404 says.
		writeProblem(w, problemNotFound, nil)
		return
	}

	publishable, found := s.resolvePublishable(rehydrated, body)
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	outcome, err := app.Publish(r.Context(),
		app.PublicationStores{Policies: s.deps.Policies, Publications: s.deps.Publications},
		app.PublishRequest{
			TenantID:               tenant,
			CustomerID:             ports.CustomerID(body.CustomerID),
			Target:                 ports.TargetKey(body.Target),
			ExpectedCurrentVersion: ports.PublicationVersion(body.ExpectedCurrentVersion),
			Receipt:                publishable.Receipt,
			Candidate:              publishable.Candidate,
		})
	if err != nil {
		writePublishProblem(w, err)
		return
	}

	status := http.StatusOK
	if outcome.Result() == app.PublicationRecorded {
		status = http.StatusCreated
	}
	writeJSON(w, status, decisionToWire(outcome))
}

// resolvePublishable finds the named checkpoint and assessment among the re-derived
// artifacts.
//
// The caller's identities are resolved against what re-execution produced, never
// trusted as descriptions of it. A checkpoint identity the execution did not produce,
// or an assessment not taken against that checkpoint, is simply not found — there is
// nothing to publish, which is what absence means here.
func (s *server) resolvePublishable(
	rehydrated app.Rehydrated, body openapiv1.CreatePublicationRequest,
) (app.PublishableCheckpoint, bool) {
	result, ok := rehydrated.Result()
	if !ok {
		return app.PublishableCheckpoint{}, false
	}

	artifact := semantic.CheckpointArtifactID(body.CheckpointArtifactID)
	var checkpoint semantic.CheckpointKey
	for _, sealed := range result.Checkpoints() {
		if sealed.ID() == artifact {
			checkpoint = sealed.Checkpoint().Key
			break
		}
	}
	if checkpoint == "" {
		return app.PublishableCheckpoint{}, false
	}

	// The assessment is resolved to its profile, and the profile is what selects the
	// evidence. Naming the assessment rather than the profile is deliberate on the
	// wire: a caller says which answer it relied on, and the service establishes
	// that the answer was really taken against this checkpoint.
	var profile semantic.ProfileID
	for _, assessment := range result.Assessments() {
		if assessment.ID() == semantic.AssessmentID(body.AssessmentID) &&
			assessment.CheckpointArtifactID() == artifact {
			profile = assessment.ProfileID()
			break
		}
	}
	if profile == "" {
		return app.PublishableCheckpoint{}, false
	}

	return rehydrated.Publishable(checkpoint, profile)
}

// GetPublication reports what is published to a target.
func (s *server) GetPublication(
	w http.ResponseWriter, r *http.Request,
	customerID string, target string, params openapiv1.GetPublicationParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Publications == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if customerID == "" || target == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	current, published, err := s.deps.Publications.CurrentPublication(
		r.Context(), tenant, ports.CustomerID(customerID), ports.TargetKey(target))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}

	if params.Version == nil {
		if !published {
			// The ordinary initial state of every target, not an absence. Reporting
			// 404 would make an unused destination indistinguishable from one the
			// caller has no right to see.
			writeJSON(w, http.StatusOK, openapiv1.PublicationState{
				CustomerID: customerID, Target: target,
				Status: openapiv1.PublicationStatusUnpublished,
			})
			return
		}
		writeJSON(w, http.StatusOK, publicationStateToWire(
			customerID, target, current, openapiv1.PublicationStatusPublished))
		return
	}

	requested := ports.PublicationVersion(*params.Version)
	recorded, found, err := s.deps.Publications.PublicationAtVersion(
		r.Context(), tenant, ports.CustomerID(customerID), ports.TargetKey(target), requested)
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		// A specific version that does not exist IS an absence, unlike a target that
		// has never been published to.
		writeProblem(w, problemNotFound, nil)
		return
	}
	// Status is derived rather than stored: the highest version is published and every
	// lower one is superseded. A stored status would be a value able to disagree with
	// the history it summarizes.
	status := openapiv1.PublicationStatusSuperseded
	if published && recorded.Version == current.Version {
		status = openapiv1.PublicationStatusPublished
	}
	writeJSON(w, http.StatusOK, publicationStateToWire(customerID, target, recorded, status))
}

// writeRehydrationProblem maps a rehydration failure onto a problem.
//
// An integrity failure is a 500 because it is this service's fault to own: the
// request was well formed and every dependency answered, and what disagreed was the
// store and the kernel. Nothing from the failure's detail reaches the response, since
// it is produced while handling material that is already suspect.
func writeRehydrationProblem(w http.ResponseWriter, err error) {
	var integrity app.IntegrityError
	if errors.As(err, &integrity) {
		writeProblem(w, problemStoredArtifactsUnverifiable, nil)
		return
	}
	writeStorageProblem(w, err)
}

// writePublishProblem maps a publication failure onto a problem.
func writePublishProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPublicationConflict) {
		writeProblem(w, problemPublicationConflict, nil)
		return
	}
	var invalid app.InvalidInputError
	if errors.As(err, &invalid) {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}
	var integrity app.IntegrityError
	if errors.As(err, &integrity) {
		writeProblem(w, problemStoredArtifactsUnverifiable, nil)
		return
	}
	writeStorageProblem(w, err)
}

// ── wire translation ────────────────────────────────────────────────────────

// gateClauses maps the promotion package's closed clause vocabulary onto the wire's.
//
// It is an explicit table rather than a string conversion because the two vocabularies
// are independently ratified: the wire enum is part of a published contract and the
// domain constants are not, so letting one render the other would make a rename in
// either a silent breaking change in the other. A clause with no entry produces no
// result, which a test detects as a short clause list.
var gateClauses = map[promotion.Clause]openapiv1.GateClauseResultClause{
	promotion.ClauseStaticValidation:     openapiv1.GateClauseResultClauseStaticValidation,
	promotion.ClauseSealedWithProvenance: openapiv1.GateClauseResultClauseSealedWithProvenance,
	promotion.ClauseProtectedInvariants:  openapiv1.GateClauseResultClauseProtectedInvariants,
	promotion.ClauseReadyAssessment:      openapiv1.GateClauseResultClauseReadyAssessment,
	promotion.ClausePinnedIdentities:     openapiv1.GateClauseResultClausePinnedIdentities,
	promotion.ClauseComparisonCorpus:     openapiv1.GateClauseResultClauseComparisonCorpus,
	promotion.ClauseNoMetricRegression:   openapiv1.GateClauseResultClauseNoMetricRegression,
	promotion.ClauseDigestConsistency:    openapiv1.GateClauseResultClauseDigestConsistency,
	promotion.ClauseCertifiedBackend:     openapiv1.GateClauseResultClauseCertifiedBackend,
}

var gateVerdicts = map[promotion.Verdict]openapiv1.GateVerdict{
	promotion.NotEvaluated: openapiv1.GateVerdictNotEvaluated,
	promotion.Pass:         openapiv1.GateVerdictPass,
	promotion.Fail:         openapiv1.GateVerdictFail,
}

var gateReasons = map[promotion.Unevaluated]openapiv1.GateUnevaluatedReason{
	promotion.UnevaluatedNotApplicable: openapiv1.GateUnevaluatedReasonNotApplicable,
	promotion.UnsupportedByBuild:       openapiv1.GateUnevaluatedReasonUnsupportedByBuild,
	promotion.InformationAbsent:        openapiv1.GateUnevaluatedReasonInformationAbsent,
}

var publicationOutcomes = map[app.PublicationResult]openapiv1.PublicationOutcome{
	app.PublicationRefused:   openapiv1.PublicationOutcomeRefused,
	app.PublicationRecorded:  openapiv1.PublicationOutcomeRecorded,
	app.PublicationUnchanged: openapiv1.PublicationOutcomeUnchanged,
}

// decisionToWire projects the gate's decision, every clause included.
//
// Authorized is taken from the decision rather than inferred from the outcome, so the
// two cannot disagree on the wire even though both are present.
func decisionToWire(outcome app.PublicationOutcome) openapiv1.PublicationDecision {
	decision := outcome.Decision()
	clauses := make([]openapiv1.GateClauseResult, 0, len(decision.Clauses()))
	for _, result := range decision.Clauses() {
		clause, known := gateClauses[result.Clause()]
		if !known {
			// A domain clause with no wire name. Omitted rather than guessed at, which
			// makes the clause list short and fails the contract's minItems: better a
			// detectable violation than a fabricated vocabulary word.
			continue
		}
		clauses = append(clauses, openapiv1.GateClauseResult{
			Clause:            clause,
			Verdict:           gateVerdicts[result.Verdict()],
			UnevaluatedReason: gateReasons[result.Unevaluated()],
		})
	}

	projected := openapiv1.PublicationDecision{
		Outcome:       publicationOutcomes[outcome.Result()],
		Authorized:    decision.Authorized(),
		PolicyVersion: int64(decision.PolicyVersion()),
		Clauses:       clauses,
	}
	if publication, ok := outcome.Publication(); ok {
		// A published record is always the current one at this instant, which is what
		// the request just established.
		wire := publicationToWire(publication, openapiv1.PublicationStatusPublished)
		projected.Publication = &wire
	}
	return projected
}

func publicationStateToWire(
	customerID, target string, publication ports.Publication, status openapiv1.PublicationStatus,
) openapiv1.PublicationState {
	wire := publicationToWire(publication, status)
	return openapiv1.PublicationState{
		CustomerID: customerID, Target: target, Status: status, Publication: &wire,
	}
}

func publicationToWire(
	publication ports.Publication, status openapiv1.PublicationStatus,
) openapiv1.Publication {
	return openapiv1.Publication{
		Version:              int64(publication.Version),
		Status:               status,
		PolicyVersion:        int64(publication.PolicyVersion),
		ProfileID:            openapiv1.Digest(publication.ProfileID),
		AssessmentID:         openapiv1.Digest(publication.AssessmentID),
		CheckpointArtifactID: openapiv1.Digest(publication.CheckpointArtifactID),
		SemanticRunID:        openapiv1.Digest(publication.SemanticRunID),
		ExecutionID:          openapiv1.Digest(publication.ExecutionID),
	}
}
