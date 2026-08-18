package ports

import (
	"context"
	"errors"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ErrPublicationConflict reports a publication that would overwrite or skip a
// newer result.
//
// HLD §14.1 requires exactly this: "a conflicting publication fails rather than
// silently overwriting a newer result". It covers both directions, as
// ErrPolicyConflict does, because in both the caller acted on a belief about the
// target's current state that is no longer true, and the fix is to read it again
// rather than to correct the argument.
var ErrPublicationConflict = errors.New("ports: publication conflicts with the target's recorded history")

// PublicationVersion orders the publications of one target.
//
// It is the compare-and-swap token: a publication at version N is accepted only
// when N-1 is current. Versions begin at 1, and zero means the target has never
// been published to.
//
// Like PolicyVersion this is a version rather than a derived identity, and for the
// same reason: HLD §6 enumerates every derived identity in this system and a
// publication is not among them. A publication is not a semantic artifact. It is
// the record of a decision about where an artifact may go, and what makes it
// trustworthy is the identities it pins rather than an identity of its own.
type PublicationVersion uint64

// Publication is one immutable record of a sealed checkpoint being published to a
// target.
//
// Every field after the key is an identity that the authorization rested on, which
// is what HLD §14.1 asks for: the record "pins that policy version, profile,
// assessment, checkpoint, semantic run, and execution that authorized it".
//
// It deliberately does NOT store the gate's verdict. The verdict is reproducible
// from the pinned identities, because gate evaluation is a pure function of the
// artifacts they name; storing it would add a projection able to disagree with the
// artifacts it summarizes, and a disagreement there is unresolvable — there would
// be no way to tell whether the artifacts changed meaning or the summary was wrong
// when written. Pinning the inputs and re-deriving the answer has no such failure.
//
// It also does not pin a PlanID. That is not an omission: SemanticRunID is derived
// from the plan together with the input, world, and provenance policy, so the plan
// is already committed to by the run this record names. A separate column would be
// a second statement of the same fact, able to drift from it.
type Publication struct {
	TenantID   TenantID
	CustomerID CustomerID
	Target     TargetKey
	Version    PublicationVersion

	// PolicyVersion is the target policy that authorized this publication.
	// Policies are immutable per version, so this stays resolvable to the exact
	// rule that was applied even after the target's policy advances.
	PolicyVersion PolicyVersion

	// ProfileID is the compiled completeness profile the checkpoint was assessed
	// ready under. It is pinned separately from the policy rather than read back
	// through it, because a reader asking "what was this judged complete for?"
	// should not have to resolve a policy version to find out.
	ProfileID semantic.ProfileID

	// AssessmentID is the readiness answer relied on, and CheckpointArtifactID is
	// the sealed prefix published. Both are pinned because an assessment is bound
	// to one checkpoint artifact, so naming only one would leave the other to be
	// guessed at.
	AssessmentID         semantic.AssessmentID
	CheckpointArtifactID semantic.CheckpointArtifactID

	// SemanticRunID and ExecutionID identify what produced the checkpoint. The run
	// is the meaning; the execution is that meaning carried out on one backend.
	// Both are pinned because "the same result from a different executor" is a
	// question this system is built to answer, and it cannot be answered from
	// either alone.
	SemanticRunID semantic.SemanticRunID
	ExecutionID   semantic.ExecutionID
}

// PublicationStore persists publication pointers. It is append-only by contract:
// a version that exists is never rewritten.
//
// HLD §14 ratifies PublicationStatus as unpublished, published, or superseded.
// None of those is stored here, because all three are derivable from the history
// this store keeps: a target with no record is unpublished, its highest version is
// published, and every lower version is superseded. A stored status would be a
// fourth thing able to disagree with the first three, and the disagreement would
// have no resolution. A status vocabulary can be exposed by whatever eventually
// renders publications, from the same reads defined here.
type PublicationStore interface {
	// Publish appends one publication for a target.
	//
	// The supplied Version must be exactly one greater than the target's current
	// version, or 1 when it has never been published to. That is the
	// compare-and-swap §14.1 requires, and it is what makes a conflicting
	// publication fail instead of overwriting a newer result: a publisher holding a
	// stale view of the target cannot express its write.
	//
	// Re-submitting a version that already exists with identical content succeeds
	// and changes nothing, so a publisher that did not learn whether its write
	// landed can safely repeat it. Different content under an existing version is
	// ErrPublicationConflict, because that is an attempt to rewrite what a target
	// was published with.
	Publish(context.Context, Publication) error

	// CurrentPublication returns the highest version recorded for a target: what
	// is published there now.
	//
	// A target that has never been published to is not an error. It is the
	// ordinary initial state of every target, and reporting it as a fault would
	// make an unused destination look like a broken store.
	CurrentPublication(context.Context, TenantID, CustomerID, TargetKey) (Publication, bool, error)

	// PublicationAtVersion returns one specific version, which is how a
	// superseded publication stays explainable. Retaining only the current one
	// would answer "what is published?" while making "what was published when
	// this was decided?" unanswerable.
	PublicationAtVersion(context.Context, TenantID, CustomerID, TargetKey, PublicationVersion) (Publication, bool, error)
}
