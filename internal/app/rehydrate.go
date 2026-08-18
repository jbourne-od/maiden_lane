package app

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// IntegrityCode is the closed classification for a store that returned something
// this build cannot reproduce.
//
// It is distinct from InvalidInputCode and InfrastructureUnavailableError because it
// is neither: the request was well formed and every dependency answered. What failed
// is the agreement between what a store recorded and what the kernel derives from the
// inputs that store also holds.
type IntegrityCode string

const (
	// IntegrityPlanAbsent means a stored execution names a plan the store does not
	// have. Execution identity is derived from the plan, so the plan cannot be
	// recovered from the execution and nothing can be re-derived without it.
	IntegrityPlanAbsent IntegrityCode = "REFERENCED_PLAN_ABSENT"

	// IntegrityResultDiverged means re-executing the stored inputs produced a result
	// that does not match the stored one.
	IntegrityResultDiverged IntegrityCode = "STORED_RESULT_DIVERGED"
)

// IntegrityError reports that a store's contents and the kernel disagree.
//
// Detail names the field that diverged and carries no value from either side. That is
// the same rule InvalidInputError follows and it matters more here, not less: this
// error is produced while handling content that is already suspect, so putting any of
// it in a message would make a corrupt store a channel for whatever it contains.
type IntegrityError struct {
	Code   IntegrityCode
	Detail string
}

func (e IntegrityError) Error() string {
	if e.Detail == "" {
		return "app: stored artifacts are not reproducible: " + string(e.Code)
	}
	return "app: stored artifacts are not reproducible: " + string(e.Code) + ": " + e.Detail
}

// RehydrationStores is what rehydration reads, and nothing else. It writes nothing.
type RehydrationStores struct {
	Plans      ports.PlanStore
	Executions ports.ExecutionStore
}

// RehydrationOutcome reports what rehydration found.
//
// RehydrationAbsent is the zero value, so a result nobody set carries no artifacts,
// for the same reason promotion.NotEvaluated is zero.
type RehydrationOutcome uint8

const (
	// RehydrationAbsent means the store has no such execution for this tenant.
	RehydrationAbsent RehydrationOutcome = iota
	// RehydrationPending means the execution exists but has not finished, so there is
	// no result to reproduce yet.
	RehydrationPending
	// RehydrationVerified means the execution was re-derived and every stored
	// identity, digest, and canonical byte matched.
	RehydrationVerified
)

func (o RehydrationOutcome) String() string {
	switch o {
	case RehydrationAbsent:
		return "absent"
	case RehydrationPending:
		return "pending"
	case RehydrationVerified:
		return "verified"
	default:
		return "unknown"
	}
}

// Rehydrated holds authenticated kernel artifacts for a stored execution.
type Rehydrated struct {
	outcome RehydrationOutcome
	status  ports.ExecutionStatus
	result  SpineResult
}

// Outcome reports what rehydration found.
func (r Rehydrated) Outcome() RehydrationOutcome { return r.outcome }

// Status is the stored lifecycle status, present whenever the execution exists. It is
// how a caller distinguishes pending from running without a second read.
func (r Rehydrated) Status() ports.ExecutionStatus { return r.status }

// Result returns the re-derived spine result, present only once verified. It is the
// live product of this process's own kernel calls, so its artifacts are authenticated
// by construction rather than by anything storage promised.
func (r Rehydrated) Result() (SpineResult, bool) {
	if r.outcome != RehydrationVerified {
		return SpineResult{}, false
	}
	return r.result, true
}

// Rehydrate re-derives a stored execution's artifacts by executing its stored inputs
// again, and requires everything the store recorded to match.
//
// This is the answer to a problem no verifier over stored fields can solve. Kernel
// values cannot be reconstructed from bytes -- canonical encoding is one-way and there
// are no decoders -- so a checkpoint read back from storage is not a
// semantic.CheckpointArtifact and cannot be handed to the gate. Recomputing an identity
// from stored components would not fix that: it would establish that a stored tuple is
// internally consistent, which is exactly what a wrong-but-self-consistent record
// already is. An adapter's guarantee is only that it returns the bytes it was given.
//
// Re-execution answers both halves at once. The artifacts are authenticated because
// this process's kernel just produced them, and the store is proven faithful because
// the products match what it recorded. It is affordable because execution is
// deterministic by construction, which is the property the whole system is built on;
// and the pattern is already ratified here rather than new, since the PostgreSQL plan
// adapter recompiles on read and requires the resulting PlanID to equal the one it
// stored. This extends that from plans to executions.
//
// Divergence is an error rather than an outcome. An absent or unfinished execution is an
// ordinary state, but a store whose contents the kernel cannot reproduce is a fault, and
// the caller must not be handed artifacts alongside a flag saying they might be wrong.
func Rehydrate(
	ctx context.Context, stores RehydrationStores,
	tenant ports.TenantID, executionID semantic.ExecutionID,
) (Rehydrated, error) {
	if err := ctx.Err(); err != nil {
		return Rehydrated{}, err
	}

	record, found, err := stores.Executions.Get(ctx, tenant, executionID)
	if err != nil {
		return Rehydrated{}, fmt.Errorf("app: execution could not be read: %w", err)
	}
	if !found {
		return Rehydrated{}, nil
	}
	if record.Result == nil {
		return Rehydrated{outcome: RehydrationPending, status: record.Status}, nil
	}

	plan, found, err := stores.Plans.GetPlan(ctx, tenant, record.Request.PlanID)
	if err != nil {
		return Rehydrated{}, fmt.Errorf("app: plan could not be read: %w", err)
	}
	if !found {
		// Not an absence to report as one. The execution exists and names this plan,
		// so the plan being gone means the store cannot answer for what it holds.
		return Rehydrated{}, IntegrityError{Code: IntegrityPlanAbsent}
	}

	// The inputs come entirely from the store, which is what makes the comparison
	// meaningful: nothing the caller supplies can steer the re-derivation toward
	// agreeing.
	result, err := Run(ctx, Request{
		Compilation:      plan.Input.Request(),
		InitialState:     record.Request.Input.InitialState,
		World:            record.Request.Input.World,
		ExecutorIdentity: record.Request.Input.ExecutorIdentity,
		Policy:           record.Request.Input.Policy,
	}, nil)
	if err != nil {
		// Machinery inability, not divergence. Re-execution could not reach an answer,
		// so nothing has been learned about whether the store is faithful.
		return Rehydrated{}, fmt.Errorf("app: stored execution could not be re-derived: %w", err)
	}

	if field := divergence(Project(record.Request, result), *record.Result); field != "" {
		return Rehydrated{}, IntegrityError{Code: IntegrityResultDiverged, Detail: field}
	}
	return Rehydrated{outcome: RehydrationVerified, status: record.Status, result: result}, nil
}

// PublishableCheckpoint is one rehydrated checkpoint with everything publication needs:
// the gate's evidence and the receipt binding it to the execution that produced it.
type PublishableCheckpoint struct {
	Candidate promotion.Candidate
	Receipt   CheckpointReceipt
}

// Publishable assembles the evidence for one checkpoint of a verified rehydration,
// under one completeness profile.
//
// The profile is a parameter because the readiness clause asks about the profile the
// target requires, and a checkpoint carries an assessment per compiled profile. Picking
// one here rather than letting the caller choose from a list is deliberate: a caller
// selecting an assessment could select the one that answers most favourably.
func (r Rehydrated) Publishable(
	checkpoint semantic.CheckpointKey, profile semantic.ProfileID,
) (PublishableCheckpoint, bool) {
	result, ok := r.Result()
	if !ok {
		return PublishableCheckpoint{}, false
	}
	plan, ok := result.Plan()
	if !ok {
		return PublishableCheckpoint{}, false
	}

	var artifact semantic.CheckpointArtifact
	for _, candidate := range result.Checkpoints() {
		if candidate.Checkpoint().Key == checkpoint {
			artifact = candidate
			break
		}
	}
	if artifact.ID() == "" {
		return PublishableCheckpoint{}, false
	}

	var assessment semantic.Assessment
	for _, candidate := range result.Assessments() {
		if candidate.CheckpointArtifactID() == artifact.ID() && candidate.ProfileID() == profile {
			assessment = candidate
			break
		}
	}
	if assessment.ID() == "" {
		return PublishableCheckpoint{}, false
	}

	receipt, ok := result.ReceiptFor(artifact)
	if !ok {
		return PublishableCheckpoint{}, false
	}

	return PublishableCheckpoint{
		Candidate: promotion.Candidate{
			Checkpoint:               artifact,
			Plan:                     plan,
			Assessment:               assessment,
			RetainedInvariantWitness: artifact.InvariantResultCanonicalBytes(),
			ExecutionID:              receipt.ExecutionID(),
		},
		Receipt: receipt,
	}, true
}

// divergence reports the first field at which a re-derived result differs from a
// stored one, or the empty string when they agree.
//
// It names the field rather than returning a boolean because an integrity failure has
// to be diagnosable: "the store is wrong" sends someone to read everything, while "the
// second checkpoint's canonical bytes" sends them to one row.
//
// EXHAUSTIVENESS IS THE HAZARD HERE. A field this comparison forgets is a field in
// which storage may diverge undetected, and nothing about the code would look wrong.
// A reflective test walks every field of every struct below and requires each to be
// caught, so adding a field without adding it here fails that test rather than
// quietly widening the hole.
func divergence(derived, stored ports.ExecutionResult) string {
	switch {
	case derived.TenantID != stored.TenantID:
		return "tenantID"
	case derived.ExecutionID != stored.ExecutionID:
		return "executionID"
	case derived.Status != stored.Status:
		return "status"
	case derived.SpineStatus != stored.SpineStatus:
		return "spineStatus"
	case derived.FinalStateDigest != stored.FinalStateDigest:
		return "finalStateDigest"
	case derived.JournalPrefixDigest != stored.JournalPrefixDigest:
		return "journalPrefixDigest"
	case derived.InputID != stored.InputID:
		return "inputID"
	case derived.WorldID != stored.WorldID:
		return "worldID"
	case !slices.Equal(derived.AcceptedRules, stored.AcceptedRules):
		return "acceptedRules"
	}
	if field := failureDivergence(derived.Failure, stored.Failure); field != "" {
		return field
	}
	if len(derived.Checkpoints) != len(stored.Checkpoints) {
		return "checkpoints.length"
	}
	for i := range derived.Checkpoints {
		if field := checkpointDivergence(derived.Checkpoints[i], stored.Checkpoints[i]); field != "" {
			// The index is a position in a list this build produced, not content from
			// the store, so it is safe to name and is the whole value of the message.
			return fmt.Sprintf("checkpoints[%d].%s", i, field)
		}
	}
	if len(derived.Assessments) != len(stored.Assessments) {
		return "assessments.length"
	}
	for i := range derived.Assessments {
		if field := assessmentDivergence(derived.Assessments[i], stored.Assessments[i]); field != "" {
			return fmt.Sprintf("assessments[%d].%s", i, field)
		}
	}
	return ""
}

// checkpointDivergence compares one sealed checkpoint, canonical bytes included.
//
// The byte comparison is the strongest check available and the reason this is worth
// doing at all: identities agreeing while the artifact they name differs is precisely
// the wrong-but-self-consistent record no digest recomputation would catch.
func checkpointDivergence(derived, stored ports.SealedCheckpoint) string {
	switch {
	case derived.CheckpointKey != stored.CheckpointKey:
		return "checkpointKey"
	case derived.CheckpointID != stored.CheckpointID:
		return "checkpointID"
	case derived.CheckpointArtifactID != stored.CheckpointArtifactID:
		return "checkpointArtifactID"
	case derived.Digest != stored.Digest:
		return "digest"
	case derived.StateDigest != stored.StateDigest:
		return "stateDigest"
	case !bytes.Equal(derived.CanonicalBytes, stored.CanonicalBytes):
		return "canonicalBytes"
	case derived.InvariantResultDigest != stored.InvariantResultDigest:
		return "invariantResultDigest"
	case !bytes.Equal(derived.InvariantResultCanonicalBytes, stored.InvariantResultCanonicalBytes):
		return "invariantResultCanonicalBytes"
	}
	return ""
}

func assessmentDivergence(derived, stored ports.StoredAssessment) string {
	switch {
	case derived.AssessmentID != stored.AssessmentID:
		return "assessmentID"
	case derived.Digest != stored.Digest:
		return "digest"
	case derived.CheckpointArtifactID != stored.CheckpointArtifactID:
		return "checkpointArtifactID"
	case derived.ProfileID != stored.ProfileID:
		return "profileID"
	case derived.ProfileKey != stored.ProfileKey:
		return "profileKey"
	case derived.Verdict != stored.Verdict:
		return "verdict"
	case !slices.Equal(derived.MissingRequirements, stored.MissingRequirements):
		return "missingRequirements"
	case !bytes.Equal(derived.CanonicalBytes, stored.CanonicalBytes):
		return "canonicalBytes"
	}
	return ""
}

// failureDivergence compares the optional semantic failure.
//
// Presence is compared before content, because one side having a failure and the other
// not is the most consequential disagreement of all: it is the difference between a run
// that refused and a run that committed.
func failureDivergence(derived, stored *ports.StoredFailure) string {
	switch {
	case derived == nil && stored == nil:
		return ""
	case derived == nil || stored == nil:
		return "failure.presence"
	case derived.Kind != stored.Kind:
		return "failure.kind"
	case derived.Code != stored.Code:
		return "failure.code"
	}
	return ""
}
