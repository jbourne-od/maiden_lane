package ports

import (
	"context"
	"errors"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ErrPolicyConflict reports a write that would rewrite or skip policy history.
//
// It covers both directions: content differing from a version already recorded,
// and a version that is not the immediate successor of the current one. Both are
// conflicts rather than validation failures, because in both cases the caller
// acted on a belief about the target's current state that is no longer true, and
// the fix is to read it again rather than to correct the argument.
var ErrPolicyConflict = errors.New("ports: target policy version conflicts with recorded history")

// CustomerID and TargetKey complete the key a publication pointer is held under.
//
// HLD §14.1 keys publication by tenant, customer, and target. Both are distinct
// types for the same reason TenantID is: an identifier that can be passed where a
// different identifier is expected is an identifier that will be, and the compiler
// is a cheaper place to find that out than a cross-tenant read.
//
// Neither is a semantic identity. They are operational names for a destination,
// they participate in no artifact identity, and nothing derived from them ever
// reaches the kernel.
type (
	CustomerID string
	TargetKey  string
)

// PolicyVersion orders the policies of one target.
//
// It is a version rather than a derived identity, and that is deliberate rather
// than a shortcut. The ratified identity model in HLD §6 enumerates every derived
// identity in this system — InputID, PlanID, SemanticRunID, ExecutionID, ProfileID,
// CheckpointArtifactID, AssessmentID — and a target policy is not among them.
// §14.1 correspondingly says a publication record pins the policy *version* that
// authorized it. A policy is control-plane configuration that names which semantic
// contract a destination demands; it is not itself a semantic artifact, so the
// kernel neither derives nor validates it.
//
// Versions begin at 1. Zero is not a policy.
type PolicyVersion uint64

// TargetPolicy is an immutable, versioned statement of what a target requires
// before a sealed checkpoint may publish to it.
//
// Immutability is what makes an old publication explainable. A publication record
// pins the version that authorized it, so a policy whose content could change
// would leave an audit record naming a rule that no longer exists and no way to
// discover what it had said.
//
// It binds exactly one thing today, which is what §14.1 asks of it: the profile a
// checkpoint must be assessed ready under. The remaining gate clauses — comparison
// over a replay corpus, protected metric regression, backend certification — will
// need fields here, and each will arrive with a migration when the concept it
// refers to exists. Adding them now would be inventing a shape for a decision
// nobody has made.
type TargetPolicy struct {
	TenantID   TenantID
	CustomerID CustomerID
	Target     TargetKey
	Version    PolicyVersion

	// RequiredProfileID is the compiled completeness profile under which a
	// checkpoint must hold a `ready` assessment. It is a ProfileID rather than a
	// profile key because a key is a name an author chose and a ProfileID is the
	// identity of a specific compiled contract: binding the key would let the
	// requirement change underneath a publication that had already been
	// authorized.
	RequiredProfileID semantic.ProfileID
}

// PolicyStore persists target policies. It is append-only by contract: a version
// that exists is never rewritten.
type PolicyStore interface {
	// PutPolicy appends one version of a target's policy.
	//
	// The supplied Version must be exactly one greater than the target's current
	// version, or 1 when the target has none. That makes writing a policy a
	// compare-and-swap rather than a blind write: two operators editing the same
	// target concurrently cannot silently lose one another's change, which for an
	// audit-bearing rule matters more than the convenience of letting the store
	// pick the next number.
	//
	// Re-submitting a version that already exists with byte-identical content
	// succeeds and changes nothing, so a retried write is safe. Submitting
	// different content under an existing version is ErrPolicyConflict, because
	// that is an attempt to rewrite history.
	PutPolicy(context.Context, TargetPolicy) error

	// ActivePolicy returns the highest version recorded for a target.
	//
	// A target with no policy is not an error. It means nothing may publish there
	// yet, which the gate reports as a refusal rather than a fault.
	ActivePolicy(context.Context, TenantID, CustomerID, TargetKey) (TargetPolicy, bool, error)

	// PolicyAtVersion returns one specific version, which is how a publication
	// record's pinned version is resolved back to the rule that authorized it.
	PolicyAtVersion(context.Context, TenantID, CustomerID, TargetKey, PolicyVersion) (TargetPolicy, bool, error)
}
