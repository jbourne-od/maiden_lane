package app

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ComparisonSide is what a comparison's evidence needs beyond the comparison itself.
//
// Only the executor identity and the provenance policy. The plans come from the
// comparison's own correspondence, which pins both — reading them from there rather than
// accepting them removes the possibility of assembling evidence from a plan the
// correspondence was never authored for, which is a mistake no later check would catch
// because the artifacts would all be individually valid.
//
// Executor and provenance are supplied because they are not semantic and the comparison
// deliberately does not pin them: two sides may legitimately run on different backends
// while comparing the same semantic runs.
type ComparisonSide struct {
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

// AssembleComparisonRequest names one comparison to gather evidence for.
type AssembleComparisonRequest struct {
	TenantID   ports.TenantID
	Comparison semantic.Comparison

	// World is the pinned world the comparison names, supplied as a value because the
	// comparison pins only its identity and a WorldID is one-way: binding a case needs
	// the references themselves. It is verified against Comparison.World() rather than
	// trusted, which is the same shape as the invariant witness — supply the value, check
	// it against the commitment, and a caller cannot substitute a different world for the
	// one the comparison was identified under.
	World semantic.World

	Baseline  ComparisonSide
	Candidate ComparisonSide
}

// ComparisonStores is what assembling comparison evidence reads. It writes nothing.
type ComparisonStores struct {
	Corpora    ports.CorpusStore
	Plans      ports.PlanStore
	Executions ports.ExecutionStore
}

// MissingReason says why one case contributed no evidence.
//
// The reasons are distinct because they call for different action: two of them are
// answered by running something, and two mean the execution ran and did not produce what
// this comparison needs.
type MissingReason uint8

const (
	// MissingUnknown is the zero value and should never be reported. It exists so a
	// reason nobody set does not silently read as a real one.
	MissingUnknown MissingReason = iota
	// MissingNotExecuted means no execution exists for this case on this side.
	MissingNotExecuted
	// MissingNotAnswered means the execution exists but has not produced a result:
	// still running, or terminally failed without ever being attempted.
	MissingNotAnswered
	// MissingCheckpointNotSealed means the execution answered but sealed no checkpoint
	// for the declaration this side compares. A deterministic refusal before that
	// boundary produces exactly this, and it is a real answer about the case rather than
	// a gap in the evidence.
	MissingCheckpointNotSealed
	// MissingNotAssessed means the checkpoint sealed but carries no assessment under the
	// comparison's profile.
	MissingNotAssessed
)

func (r MissingReason) String() string {
	switch r {
	case MissingUnknown:
		return "unknown"
	case MissingNotExecuted:
		return "not_executed"
	case MissingNotAnswered:
		return "not_answered"
	case MissingCheckpointNotSealed:
		return "checkpoint_not_sealed"
	case MissingNotAssessed:
		return "not_assessed"
	default:
		return "unknown"
	}
}

// MissingCase names one case that contributed no evidence, and why.
type MissingCase struct {
	Baseline    bool
	CaseDigest  semantic.StateDigest
	ExecutionID semantic.ExecutionID
	Reason      MissingReason
}

// ComparisonAssembly is the result of gathering evidence for a comparison.
//
// It reports either complete evidence or exactly which cases are missing and why. There
// is no partial evidence: comparability is a statement about a whole corpus, and handing
// back what happened to be available would let a caller evaluate a comparison over a
// smaller corpus than the one it names.
type ComparisonAssembly struct {
	complete bool
	evidence promotion.ComparisonEvidence
	missing  []MissingCase
}

// Evidence returns the assembled evidence, present only when every case on both sides
// contributed.
func (a ComparisonAssembly) Evidence() (*promotion.ComparisonEvidence, bool) {
	if !a.complete {
		return nil, false
	}
	evidence := a.evidence
	return &evidence, true
}

// Missing returns every case that contributed nothing, with its reason. Empty when the
// evidence is complete.
func (a ComparisonAssembly) Missing() []MissingCase {
	return append([]MissingCase(nil), a.missing...)
}

// AssembleComparison gathers the evidence answering one comparison, by re-deriving both
// sides' executions and rehydrating them.
//
// THIS IS THE EXPENSIVE OPERATION IN THE PROGRAMME, and the cost is exactly what the
// programme plan states: rehydration authenticates by re-executing, so assembling
// evidence for a corpus of n cases costs 2n deterministic re-executions. That is not a
// missing optimization. Kernel values cannot be rebuilt from bytes, and recomputing an
// identity from stored components would establish only that a stored tuple agrees with
// itself — which a wrong-but-self-consistent record already does. Authorization consumes
// artifacts this process produced, or it consumes nothing.
//
// A caller wanting to know whether the work is done should ask CorpusProgress, which
// costs lookups. A side reporting complete is a reason to attempt this, never a
// substitute for it: progress reads a store's own account of its executions, and no
// projection may carry authorization weight.
func AssembleComparison(
	ctx context.Context, stores ComparisonStores, request AssembleComparisonRequest,
) (ComparisonAssembly, error) {
	if err := ctx.Err(); err != nil {
		return ComparisonAssembly{}, err
	}
	if request.TenantID == "" || request.Comparison.ID() == "" {
		return ComparisonAssembly{}, InvalidInputError{Code: InputComparisonIncomplete}
	}
	if request.World.ID() != request.Comparison.World() {
		// The supplied world is not the one this comparison was identified under, so
		// binding the cases with it would derive executions the comparison does not name.
		return ComparisonAssembly{}, InvalidInputError{Code: InputComparisonWorldMismatch}
	}

	corpusRecord, found, err := stores.Corpora.GetCorpus(
		ctx, request.TenantID, request.Comparison.Corpus())
	if err != nil {
		return ComparisonAssembly{}, fmt.Errorf("app: corpus could not be read: %w", err)
	}
	if !found {
		return ComparisonAssembly{}, InvalidInputError{Code: InputCorpusAbsent}
	}
	// The stored corpus must be the one the comparison names. GetCorpus already requires
	// a row to reproduce its own identity, so this cannot disagree — checked anyway,
	// because a comparison evaluated over a different corpus than it names would be
	// answering a question nobody asked.
	if corpusRecord.Corpus.ID() != request.Comparison.Corpus() {
		return ComparisonAssembly{}, IntegrityError{
			Code: IntegrityResultDiverged, Detail: "corpus identity"}
	}

	assembly := ComparisonAssembly{
		evidence: promotion.ComparisonEvidence{Comparison: request.Comparison},
	}
	policy := request.Comparison.Policy()

	for _, side := range []struct {
		baseline   bool
		plan       semantic.PlanID
		checkpoint semantic.CheckpointID
		settings   ComparisonSide
	}{
		{true, policy.BaselinePlan(), request.Comparison.Baseline(), request.Baseline},
		{false, policy.CandidatePlan(), request.Comparison.Candidate(), request.Candidate},
	} {
		cases, missing, err := assembleSide(ctx, stores, request, side.plan, side.checkpoint,
			side.settings, corpusRecord.Corpus, side.baseline)
		if err != nil {
			return ComparisonAssembly{}, err
		}
		assembly.missing = append(assembly.missing, missing...)
		if side.baseline {
			assembly.evidence.Baseline = cases
		} else {
			assembly.evidence.Candidate = cases
		}
	}

	assembly.complete = len(assembly.missing) == 0
	if !assembly.complete {
		// No partial evidence. Comparability is a statement about a whole corpus, and
		// returning what happened to be available would let a caller evaluate a
		// comparison over a smaller corpus than the one it names.
		assembly.evidence = promotion.ComparisonEvidence{}
	}
	return assembly, nil
}

// assembleSide gathers one side's evidence, case by case in the corpus's canonical order.
//
// The order is load-bearing rather than incidental: comparability establishes coverage by
// re-deriving the corpus identity from the case digests, and a corpus's canonical order is
// part of what that identity commits to. Iterating the corpus is what produces that order,
// which is why the corpus is walked rather than the store queried.
func assembleSide(
	ctx context.Context, stores ComparisonStores, request AssembleComparisonRequest,
	planID semantic.PlanID, checkpoint semantic.CheckpointID, settings ComparisonSide,
	corpus semantic.Corpus, baseline bool,
) ([]promotion.ComparedCase, []MissingCase, error) {
	planRecord, found, err := stores.Plans.GetPlan(ctx, request.TenantID, planID)
	if err != nil {
		return nil, nil, fmt.Errorf("app: plan could not be read: %w", err)
	}
	if !found {
		return nil, nil, InvalidInputError{Code: InputCorpusRunPlanAbsent}
	}
	plan, present := planRecord.Compilation.Plan()
	if !present {
		return nil, nil, IntegrityError{Code: IntegrityPlanAbsent}
	}
	if corpus.SchemaDigest() != plan.SchemaDigest() {
		// No case could execute under this plan, so there is nothing to gather and the
		// comparison names a side that cannot be run.
		return nil, nil, InvalidInputError{Code: InputCorpusSchemaMismatch}
	}

	cases := make([]promotion.ComparedCase, 0, corpus.Len())
	missing := make([]MissingCase, 0)

	for index := 0; index < corpus.Len(); index++ {
		state, ok := corpus.Case(index)
		if !ok {
			return nil, nil, IntegrityError{
				Code: IntegrityResultDiverged, Detail: "corpus case count"}
		}
		binding, err := semantic.BindRun(semantic.RunBindingRequest{
			Plan: plan, InitialState: state, World: request.World,
			ExecutorIdentity: settings.ExecutorIdentity, Policy: settings.Policy,
		})
		if err != nil {
			return nil, nil, InvalidInputError{Code: InputRunBindingIncomplete}
		}

		compared, reason, err := assembleCase(ctx, stores, request.TenantID,
			binding.ExecutionID(), checkpoint, request.Comparison.Profile())
		if err != nil {
			return nil, nil, err
		}
		if reason != MissingUnknown {
			missing = append(missing, MissingCase{
				Baseline: baseline, CaseDigest: state.Digest(),
				ExecutionID: binding.ExecutionID(), Reason: reason,
			})
			continue
		}
		cases = append(cases, compared)
	}
	return cases, missing, nil
}

// assembleCase rehydrates one execution and extracts the artifacts this comparison needs.
//
// The artifacts come from the fresh spine result, never from the stored projection. That
// is what makes them authenticated: this process's kernel just produced them, and
// rehydration required everything the store recorded to match.
func assembleCase(
	ctx context.Context, stores ComparisonStores, tenant ports.TenantID,
	executionID semantic.ExecutionID, checkpoint semantic.CheckpointID,
	profile semantic.ProfileID,
) (promotion.ComparedCase, MissingReason, error) {
	rehydrated, err := Rehydrate(ctx,
		RehydrationStores{Plans: stores.Plans, Executions: stores.Executions},
		tenant, executionID)
	if err != nil {
		// An integrity failure is not a missing case. The store holds something it cannot
		// reproduce, which is a fault to surface rather than a gap to report.
		return promotion.ComparedCase{}, MissingUnknown, err
	}
	switch rehydrated.Outcome() {
	case RehydrationAbsent:
		return promotion.ComparedCase{}, MissingNotExecuted, nil
	case RehydrationPending, RehydrationUnattempted:
		return promotion.ComparedCase{}, MissingNotAnswered, nil
	}

	result, ok := rehydrated.Result()
	if !ok {
		return promotion.ComparedCase{}, MissingNotAnswered, nil
	}

	var artifact semantic.CheckpointArtifact
	for _, sealed := range result.Checkpoints() {
		if sealed.CheckpointID() == checkpoint {
			artifact = sealed
			break
		}
	}
	if artifact.ID() == "" {
		// The execution answered and sealed nothing for this declaration, which a
		// deterministic refusal before that boundary produces. That is a real answer
		// about the case, and it means the comparison cannot be evidenced.
		return promotion.ComparedCase{}, MissingCheckpointNotSealed, nil
	}

	for _, assessment := range result.Assessments() {
		if assessment.CheckpointArtifactID() == artifact.ID() &&
			assessment.ProfileID() == profile {
			return promotion.ComparedCase{Checkpoint: artifact, Assessment: assessment},
				MissingUnknown, nil
		}
	}
	return promotion.ComparedCase{}, MissingNotAssessed, nil
}
