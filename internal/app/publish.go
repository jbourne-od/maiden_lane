package app

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
)

// PublicationStores is what publishing needs from storage, and nothing else.
//
// The policy store is read to learn what the target requires; the publication store
// is read to learn what is there now and written to advance it. Neither is used to
// establish anything about the candidate: HLD §14.1 says publication "never reruns
// transformations or readiness evaluation", and the gate reaches no store at all.
type PublicationStores struct {
	Policies     ports.PolicyStore
	Publications ports.PublicationStore
}

// PublishRequest is one request to publish a sealed checkpoint to a target.
type PublishRequest struct {
	TenantID   ports.TenantID
	CustomerID ports.CustomerID
	Target     ports.TargetKey

	// ExpectedCurrentVersion is the target's publication version the caller formed
	// this request against. Zero means the caller believes the target has never
	// been published to.
	//
	// HLD §16 requires it: publication "requires the expected current target
	// version". It is the caller's compare-and-swap token, and it must come from
	// the caller rather than be read here, which is the whole point. Reading the
	// current version inside this function and writing one past it performs a
	// flawless swap on the wrong proposition: it protects only the window between
	// that read and the write, and not the window that actually matters, which
	// opens when the caller decides and closes when it publishes. If A publishes
	// v7 after B formed its decision against v6, B would observe v7 and cheerfully
	// write v8 over a result it never saw.
	//
	// Naming the version moves the staleness question to the only place with the
	// context to answer it. A publisher that named v6 and finds v7 there learns
	// its decision is stale and can re-derive it.
	ExpectedCurrentVersion ports.PublicationVersion

	// Receipt is evidence that an execution actually produced the checkpoint being
	// published. See CheckpointReceipt: neither the artifact nor a RunBinding can
	// establish that relation, so it is minted by the SpineResult that holds both
	// halves as facts.
	Receipt CheckpointReceipt

	// Candidate is the evidence the gate judges. Its kernel values are
	// authenticated by their types; see promotion.Candidate.
	Candidate promotion.Candidate
}

// PublicationResult reports what publishing did.
//
// PublicationRefused is the zero value, for the same reason promotion.NotEvaluated
// is: a result nobody set must not read as an authorization.
type PublicationResult uint8

const (
	// PublicationRefused means the gate did not authorize publication. The
	// decision says which clauses refused and why.
	PublicationRefused PublicationResult = iota
	// PublicationRecorded means the pointer advanced to a new version.
	PublicationRecorded
	// PublicationUnchanged means the target already published exactly this, so
	// nothing was appended.
	PublicationUnchanged
)

func (r PublicationResult) String() string {
	switch r {
	case PublicationRefused:
		return "refused"
	case PublicationRecorded:
		return "recorded"
	case PublicationUnchanged:
		return "unchanged"
	default:
		return "unknown"
	}
}

// PublicationOutcome carries the gate's decision and, when it authorized, the
// publication that resulted. It never carries both a refusal and a publication.
type PublicationOutcome struct {
	result      PublicationResult
	decision    promotion.Decision
	publication *ports.Publication
}

// Result reports what publishing did.
func (o PublicationOutcome) Result() PublicationResult { return o.result }

// Authorized reports whether the gate authorized publication. It is answered from
// the decision rather than from the result, so the two cannot disagree.
func (o PublicationOutcome) Authorized() bool { return o.decision.Authorized() }

// Decision is the gate's complete per-clause answer. It is present whether or not
// anything was published, because "why was this refused?" is the question an
// operator actually has.
func (o PublicationOutcome) Decision() promotion.Decision { return o.decision }

// Publication returns what is published at the target, if this request authorized
// it. A refusal returns no publication rather than a zero-valued one.
func (o PublicationOutcome) Publication() (ports.Publication, bool) {
	if o.publication == nil {
		return ports.Publication{}, false
	}
	return *o.publication, true
}

// Publish evaluates the promotion gate for a candidate and, only if every clause
// passes, advances the target's publication pointer.
//
// A gate refusal is an outcome, not an error: the request was well formed and the
// system produced a real answer about it, which is the same distinction execution
// draws between a semantic rejection and machinery failure. An error means the
// answer could not be reached -- a malformed request, an unreadable store, or a
// pointer that moved under the caller.
//
// Nothing publishes in this build. Three of HLD §14.1's nine clauses have no
// implementation and four more are not yet wired, so every clause list contains a
// NotEvaluated and every decision refuses. That is deliberate and is asserted by
// this package's tests: the gate ships refusing, and each clause becomes real on
// its own evidence rather than publication arriving first and the checks catching up.
func Publish(
	ctx context.Context, stores PublicationStores, request PublishRequest,
) (PublicationOutcome, error) {
	if err := ctx.Err(); err != nil {
		return PublicationOutcome{}, err
	}
	if err := validatePublishRequest(request); err != nil {
		return PublicationOutcome{}, err
	}

	// An absent policy needs no special case. The gate is handed the zero value,
	// which it reports as every clause unevaluated for want of a policy -- the
	// truthful answer, and better than a bespoke error, because an unconfigured
	// target is an ordinary state rather than a fault.
	policy, _, err := stores.Policies.ActivePolicy(
		ctx, request.TenantID, request.CustomerID, request.Target)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("app: target policy could not be read: %w", err)
	}

	decision := promotion.Evaluate(policy, request.Candidate)
	if !decision.Authorized() {
		return PublicationOutcome{result: PublicationRefused, decision: decision}, nil
	}

	return advancePointer(ctx, stores.Publications, request, policy, decision)
}

// advancePointer performs the compare-and-swap once the gate has authorized.
//
// It is a separate function because the gate refuses every candidate in this build,
// which leaves everything past authorization unreachable through Publish. Splitting
// it here makes all four of its outcomes directly testable without introducing a
// seam through which authorization itself could be supplied: it is unexported, Publish
// is its only caller, and it does not consult the decision it is handed. The
// authorization fact stays something only Publish can establish.
func advancePointer(
	ctx context.Context, store ports.PublicationStore,
	request PublishRequest, policy ports.TargetPolicy, decision promotion.Decision,
) (PublicationOutcome, error) {
	current, published, err := store.CurrentPublication(
		ctx, request.TenantID, request.CustomerID, request.Target)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("app: current publication could not be read: %w", err)
	}

	next := publicationFor(request, policy, request.ExpectedCurrentVersion+1)

	// The already-published check comes BEFORE the expected-version check, and the
	// order is load-bearing.
	//
	// Execution delivery is at-least-once by design: a lease can expire and a second
	// attempt reproduces byte-identical artifacts. Such a retry re-derives the same
	// decision from the same expected version, and by then its own earlier
	// publication is current. Checking the version first would refuse it as stale,
	// turning an idempotent retry into a conflict a caller cannot resolve -- its view
	// is not wrong, it is already satisfied. Checking here reports the truth: the
	// target already publishes exactly this.
	//
	// Without it, an expired lease would leave a target's history showing the same
	// checkpoint published twice, and nothing later could tell that from two real
	// decisions.
	if published && samePublication(current, next) {
		recorded := current
		return PublicationOutcome{
			result: PublicationUnchanged, decision: decision, publication: &recorded,
		}, nil
	}

	// The caller's token does not match, so its decision was formed against a target
	// state that no longer holds. This is the case a version read inside this function
	// cannot catch: it would observe whatever is current and write one past it,
	// performing a flawless swap on the wrong proposition.
	//
	// Refused here for a legible error. The store's compare-and-swap remains the
	// enforcement point and would refuse this write even without this check.
	if current.Version != request.ExpectedCurrentVersion {
		return PublicationOutcome{}, fmt.Errorf(
			"%w: the request expected version %d but the target is at %d",
			ports.ErrPublicationConflict, request.ExpectedCurrentVersion, current.Version)
	}

	if err := store.Publish(ctx, next); err != nil {
		// A conflict is not a gate refusal. The pointer moved between the read above
		// and this write, so the caller's view is stale and the fix is to read again.
		// This is the silent overwrite §14.1 requires to fail.
		return PublicationOutcome{}, fmt.Errorf("app: publication could not be recorded: %w", err)
	}
	return PublicationOutcome{
		result: PublicationRecorded, decision: decision, publication: &next,
	}, nil
}

// publicationFor builds the record from the authenticated evidence.
//
// The profile pinned is the assessment's, not the policy's required profile. They
// are the same thing once the readiness clause is wired, and pinning the
// assessment's is still correct: it records the profile the checkpoint was actually
// assessed under, which is a fact, where the policy's is a requirement. The
// requirement stays recoverable through the pinned policy version, so nothing is
// lost, and if the two ever disagreed the record would say what was true rather
// than what was asked for.
func publicationFor(
	request PublishRequest, policy ports.TargetPolicy, version ports.PublicationVersion,
) ports.Publication {
	return ports.Publication{
		TenantID:             request.TenantID,
		CustomerID:           request.CustomerID,
		Target:               request.Target,
		Version:              version,
		PolicyVersion:        policy.Version,
		ProfileID:            request.Candidate.Assessment.ProfileID(),
		AssessmentID:         request.Candidate.Assessment.ID(),
		CheckpointArtifactID: request.Candidate.Checkpoint.ID(),
		SemanticRunID:        request.Receipt.SemanticRunID(),
		ExecutionID:          request.Receipt.ExecutionID(),
	}
}

// samePublication compares everything a publication pins, ignoring its version.
//
// The version is what distinguishes two records; the pinned identities are what
// distinguish two decisions. Comparing whole structs would make every repeat look
// new, which is the opposite of what the retry case needs.
func samePublication(current, next ports.Publication) bool {
	current.Version, next.Version = 0, 0
	return current == next
}

// validatePublishRequest refuses a request that could not produce an auditable
// record, before any store is touched.
//
// The run/checkpoint consistency check is the one worth reading twice. A binding
// whose semantic run is not the checkpoint's would produce a record naming an
// execution that did not produce the artifact it names -- a record that looks
// complete and audits to a contradiction. That is not a statement about the
// candidate, so it is an error rather than a gate refusal.
func validatePublishRequest(request PublishRequest) error {
	if request.TenantID == "" || request.CustomerID == "" || request.Target == "" {
		return InvalidInputError{Code: InputPublishRequestIncomplete}
	}

	// Kernel values are authenticated by their types, but every exported struct type
	// still has a constructible zero value, so absence is checked explicitly.
	checkpoint := request.Candidate.Checkpoint
	if checkpoint.ID() == "" || request.Candidate.Assessment.ID() == "" ||
		request.Receipt.ExecutionID() == "" {
		return InvalidInputError{Code: InputPublishRequestIncomplete}
	}

	// The receipt must be for THIS checkpoint. A receipt proves that its execution
	// produced the checkpoint it names, and nothing about any other: an execution can
	// retain several, so pairing one checkpoint's receipt with another's artifact
	// would attribute a production nobody witnessed.
	if request.Receipt.CheckpointArtifactID() != checkpoint.ID() {
		return InvalidInputError{Code: InputPublishReceiptMismatch}
	}

	// The runs must agree too. This is redundant given a correctly minted receipt,
	// since the minting result holds both facts, so it guards against a minting
	// defect rather than against a caller. It costs one comparison and the record it
	// protects is the audit trail.
	if request.Receipt.SemanticRunID() != checkpoint.SemanticRunID() {
		return InvalidInputError{Code: InputPublishReceiptMismatch}
	}
	return nil
}
