package app

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CorpusRunStores is what running one side of a comparison over a corpus needs.
//
// There is deliberately no store for the side run itself. Everything about it is
// derivable: a corpus, a plan, a world, an executor, and a provenance policy determine
// every case's ExecutionID through BindRun, so a side run has no state to keep and
// therefore no identity of its own that could drift from what it describes. Progress is a
// question answered by re-deriving the identities and looking them up, not by reading a
// record somebody remembered to update.
type CorpusRunStores struct {
	Corpora    ports.CorpusStore
	Plans      ports.PlanStore
	Executions ports.ExecutionStore
}

// CorpusRunRequest names one side of a comparison: a plan run over a corpus in a world.
//
// The completeness profile is deliberately absent. The spine assesses every sealed
// checkpoint under every profile the plan compiled, so a profile is a read-side selector
// used when comparability picks which assessment to read — not an input to running. The
// programme plan said "one plan and profile" for this slice, which was imprecise.
type CorpusRunRequest struct {
	TenantID ports.TenantID
	CorpusID semantic.CorpusID
	PlanID   semantic.PlanID

	// World is pinned for the whole side. With the corpus it determines every case's
	// InputID, which is what makes two sides naming the same corpus and world provably
	// running over the same inputs.
	World semantic.World

	// ExecutorIdentity and Policy affect ExecutionID and nothing semantic. Two sides may
	// legitimately differ here while comparing the same semantic runs, which is the point
	// of the identity model separating them.
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

// CaseRun is one corpus case's execution for this side.
type CaseRun struct {
	caseDigest  semantic.StateDigest
	runID       semantic.SemanticRunID
	executionID semantic.ExecutionID
	status      ports.ExecutionStatus
	enqueued    bool
}

// CaseDigest identifies which corpus case this is.
func (c CaseRun) CaseDigest() semantic.StateDigest { return c.caseDigest }

// SemanticRunID and ExecutionID are the derived identities for this case on this side.
func (c CaseRun) SemanticRunID() semantic.SemanticRunID { return c.runID }
func (c CaseRun) ExecutionID() semantic.ExecutionID     { return c.executionID }

// Status is the case's lifecycle status.
func (c CaseRun) Status() ports.ExecutionStatus { return c.status }

// Enqueued reports whether THIS call created the execution, as opposed to finding one
// already present.
//
// It is the observable form of the cost model: a case already executed is not executed
// again, because its identity is derived from the semantic request and a repeat is
// necessarily the same execution. A caller can therefore see what a run actually cost
// rather than inferring it.
func (c CaseRun) Enqueued() bool { return c.enqueued }

// Terminal reports whether this case has finished, successfully or with a deterministic
// semantic rejection. Both are finished: a computation that ran and refused produced a
// real answer.
func (c CaseRun) Terminal() bool {
	return c.status == ports.ExecutionSucceeded || c.status == ports.ExecutionFailed
}

// CorpusRun is one side's state over every case of a corpus.
type CorpusRun struct {
	corpusID semantic.CorpusID
	planID   semantic.PlanID
	cases    []CaseRun
}

// CorpusID and PlanID identify the side.
func (r CorpusRun) CorpusID() semantic.CorpusID { return r.corpusID }
func (r CorpusRun) PlanID() semantic.PlanID     { return r.planID }

// Cases returns every case in the corpus's canonical order.
func (r CorpusRun) Cases() []CaseRun { return append([]CaseRun(nil), r.cases...) }

// Complete reports whether every case has finished.
//
// This is the precondition comparability will require of both sides. It is deliberately
// all-or-nothing: a comparison over a corpus where some cases have not run is a
// comparison over a different, smaller corpus, and reporting it as partially complete
// would invite exactly that substitution.
func (r CorpusRun) Complete() bool {
	if len(r.cases) == 0 {
		// A run over no cases is not complete, it is unstarted. An empty corpus cannot
		// be constructed, so reaching this means the run was never resolved.
		return false
	}
	for _, run := range r.cases {
		if !run.Terminal() {
			return false
		}
	}
	return true
}

// Counts summarizes the side by lifecycle status.
type Counts struct {
	Total     int
	Pending   int
	Running   int
	Succeeded int
	Failed    int

	// Enqueued is how many executions the call that produced this run created. Zero on a
	// progress read, and zero on a repeat run of a corpus already executed.
	Enqueued int
}

// Counts summarizes this side.
func (r CorpusRun) Counts() Counts {
	counts := Counts{Total: len(r.cases)}
	for _, run := range r.cases {
		switch run.status {
		case ports.ExecutionPending:
			counts.Pending++
		case ports.ExecutionRunning:
			counts.Running++
		case ports.ExecutionSucceeded:
			counts.Succeeded++
		case ports.ExecutionFailed:
			counts.Failed++
		}
		if run.enqueued {
			counts.Enqueued++
		}
	}
	return counts
}

// RunCorpus enqueues an execution for every case of a corpus under one plan, and reports
// the side's state.
//
// Enqueueing is idempotent per case because ExecutionID is derived from the semantic
// request: a case already executed is found rather than repeated, and the returned run
// says which cases this call actually created. That is the whole of the cost model for
// this operation — n lookups and however many genuinely new executions. Authenticating
// the results costs a re-execution each, and that is comparability's bill to pay, not
// this one's.
//
// There is no cap on corpus size and no batching. A cap would silently run a different,
// smaller corpus than the caller named, and the identity of a corpus is precisely the set
// of cases it holds.
func RunCorpus(
	ctx context.Context, stores CorpusRunStores, request CorpusRunRequest,
) (CorpusRun, error) {
	return resolveCorpusRun(ctx, stores, request, true)
}

// CorpusProgress reports a side's state without enqueueing anything.
//
// Separate from RunCorpus rather than a flag on it, because "tell me where this stands"
// and "start this" are different intents and a boolean parameter at a call site does not
// say which one is meant.
func CorpusProgress(
	ctx context.Context, stores CorpusRunStores, request CorpusRunRequest,
) (CorpusRun, error) {
	return resolveCorpusRun(ctx, stores, request, false)
}

func resolveCorpusRun(
	ctx context.Context, stores CorpusRunStores, request CorpusRunRequest, enqueue bool,
) (CorpusRun, error) {
	if err := ctx.Err(); err != nil {
		return CorpusRun{}, err
	}
	if request.TenantID == "" || request.CorpusID == "" || request.PlanID == "" {
		return CorpusRun{}, InvalidInputError{Code: InputCorpusRunIncomplete}
	}

	record, found, err := stores.Corpora.GetCorpus(ctx, request.TenantID, request.CorpusID)
	if err != nil {
		return CorpusRun{}, fmt.Errorf("app: corpus could not be read: %w", err)
	}
	if !found {
		return CorpusRun{}, InvalidInputError{Code: InputCorpusAbsent}
	}

	planRecord, found, err := stores.Plans.GetPlan(ctx, request.TenantID, request.PlanID)
	if err != nil {
		return CorpusRun{}, fmt.Errorf("app: plan could not be read: %w", err)
	}
	if !found {
		return CorpusRun{}, InvalidInputError{Code: InputCorpusRunPlanAbsent}
	}
	plan, present := planRecord.Compilation.Plan()
	if !present {
		return CorpusRun{}, IntegrityError{Code: IntegrityPlanAbsent}
	}

	// FAIL FAST ON THE SCHEMA. BindRun refuses a state whose schema digest is not the
	// plan's, so a corpus under a different schema would fail identically for every case
	// — and without this check the failure would arrive n times, after n durable
	// executions had been created for work that cannot succeed.
	if record.Corpus.SchemaDigest() != plan.SchemaDigest() {
		return CorpusRun{}, InvalidInputError{Code: InputCorpusSchemaMismatch}
	}

	run := CorpusRun{
		corpusID: request.CorpusID,
		planID:   request.PlanID,
		cases:    make([]CaseRun, 0, record.Corpus.Len()),
	}
	for index := 0; index < record.Corpus.Len(); index++ {
		state, ok := record.Corpus.Case(index)
		if !ok {
			return CorpusRun{}, IntegrityError{
				Code: IntegrityResultDiverged, Detail: "corpus case count"}
		}

		// The identities are derived per case rather than allocated, which is what makes
		// this idempotent and what makes two sides over the same corpus and world
		// provably the same inputs.
		binding, err := semantic.BindRun(semantic.RunBindingRequest{
			Plan:             plan,
			InitialState:     state,
			World:            request.World,
			ExecutorIdentity: request.ExecutorIdentity,
			Policy:           request.Policy,
		})
		if err != nil {
			// The schema was already checked, so a binding failure here is about the
			// world, executor, or policy — the parts of the request that are the same for
			// every case, so it will not become a per-case story.
			return CorpusRun{}, InvalidInputError{Code: InputRunBindingIncomplete}
		}

		execution := ports.ExecutionRequest{
			TenantID:    request.TenantID,
			ExecutionID: binding.ExecutionID(),
			RunID:       binding.SemanticRunID(),
			PlanID:      request.PlanID,
			Input: ports.ExecutionInput{
				InitialState:     state,
				World:            request.World,
				ExecutorIdentity: request.ExecutorIdentity,
				Policy:           request.Policy,
			},
		}

		created := false
		if enqueue {
			created, err = stores.Executions.Enqueue(ctx, execution)
			if err != nil {
				return CorpusRun{}, fmt.Errorf("app: corpus case could not be enqueued: %w", err)
			}
		}

		status := ports.ExecutionPending
		if !created {
			// Either this is a progress read, or the execution was already present. Its
			// status has to be read rather than assumed: a case enqueued by an earlier
			// run may have finished since.
			stored, found, err := stores.Executions.Get(ctx, request.TenantID, binding.ExecutionID())
			if err != nil {
				return CorpusRun{}, fmt.Errorf("app: corpus case could not be read: %w", err)
			}
			if !found {
				// Not yet enqueued at all, which is the ordinary state of a progress read
				// on a side nobody has run.
				status = ""
			} else {
				status = stored.Status
			}
		}

		run.cases = append(run.cases, CaseRun{
			caseDigest:  state.Digest(),
			runID:       binding.SemanticRunID(),
			executionID: binding.ExecutionID(),
			status:      status,
			enqueued:    created,
		})
	}
	return run, nil
}
