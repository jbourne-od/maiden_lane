package semantic

import (
	"bytes"
	"fmt"
	"slices"
)

// SealRequest supplies the exact accepted prefix and protected evidence for
// one compiled checkpoint declaration.
type SealRequest struct {
	Binding          RunBinding
	Checkpoint       CheckpointKey
	State            State
	Journal          Journal
	InvariantResults []InvariantResult
	KnownArtifacts   []CheckpointArtifact
}

// CheckpointArtifact is one immutable, replay-linked sealed semantic prefix.
// Its claim identity and complete manifest digest remain distinct values.
type CheckpointArtifact struct {
	checkpoint            CheckpointDeclaration
	checkpointID          CheckpointID
	planID                PlanID
	semanticRunID         SemanticRunID
	inputID               InputID
	initialStateDigest    StateDigest
	worldID               WorldID
	policyID              ProvenancePolicyID
	stateDigest           StateDigest
	journalPrefixDigest   JournalPrefixDigest
	invariantResultDigest InvariantResultDigest
	id                    CheckpointArtifactID
	canonical             []byte
	digest                CheckpointArtifactDigest

	// invariantResultCanonical is the exact byte sequence invariantResultDigest
	// was derived from: the witness beside the commitment.
	//
	// A digest is a commitment, not a witness. InvariantResultDigest can tell a
	// later reader whether some supplied bytes are the committed ones; it cannot
	// tell that reader what those bytes said. Retaining them here gives the
	// witness the owner that commits to it, so nothing downstream has to remember
	// which bytes from which transition belong to which subsequently sealed
	// checkpoint.
	//
	// It participates in NO identity. CheckpointArtifactID is derived from
	// invariantResultDigest, and the manifest encoder does not read this field, so
	// retaining it leaves every identity and digest byte-identical. Two tests
	// assert exactly that.
	invariantResultCanonical []byte
}

func (c CheckpointArtifact) Checkpoint() CheckpointDeclaration { return c.checkpoint }
func (c CheckpointArtifact) CheckpointID() CheckpointID        { return c.checkpointID }
func (c CheckpointArtifact) PlanID() PlanID                    { return c.planID }
func (c CheckpointArtifact) SemanticRunID() SemanticRunID      { return c.semanticRunID }
func (c CheckpointArtifact) InputID() InputID                  { return c.inputID }
func (c CheckpointArtifact) InitialStateDigest() StateDigest   { return c.initialStateDigest }
func (c CheckpointArtifact) WorldID() WorldID                  { return c.worldID }
func (c CheckpointArtifact) ProvenancePolicyID() ProvenancePolicyID {
	return c.policyID
}
func (c CheckpointArtifact) StateDigest() StateDigest { return c.stateDigest }
func (c CheckpointArtifact) JournalPrefixDigest() JournalPrefixDigest {
	return c.journalPrefixDigest
}

// InvariantResultCanonicalBytes returns the exact canonical invariant-result
// bytes this artifact's InvariantResultDigest was derived from.
//
// It is a witness for verification, not a value to interpret. There is
// deliberately no way to turn these bytes back into []InvariantResult: the kernel
// has no semantic decoders, so canonical encoding stays one-way and no second
// source of semantic meaning can appear. A caller establishes that these are the
// committed bytes with VerifyInvariantResultDigest and reasons from the fact of
// sealing, rather than by reading them.
func (c CheckpointArtifact) InvariantResultCanonicalBytes() []byte {
	return bytes.Clone(c.invariantResultCanonical)
}

// VerifyInvariantResultDigest reports whether canonical is the exact byte
// sequence expected was derived from.
//
// This is the whole of what a later reader needs, and deliberately no more. It
// establishes that a retained witness is the one a checkpoint committed to,
// without exposing how the kernel hashes anything and without any path back to
// []InvariantResult. No constructor, no decoder, and neither encodeInvariantResults
// nor canonicalDigest becomes reachable: canonical encoding remains one-way, so
// the kernel stays the single source of semantic meaning.
//
// A caller establishing this, over an artifact that genuinely sealed, may conclude
// that every applicable protected invariant passed — not because the bytes were
// read, but because the kernel refuses to seal a checkpoint whose exact
// all-passing protected result set does not match its accepted journal. That is a
// theorem the kernel enforces, not a property inferred from storage.
func VerifyInvariantResultDigest(canonical []byte, expected InvariantResultDigest) bool {
	if len(canonical) == 0 {
		// An absent witness verifies nothing. Returning true for the digest of
		// empty input would let missing evidence satisfy the check.
		return false
	}
	return InvariantResultDigest(canonicalDigest(canonical)) == expected
}

func (c CheckpointArtifact) InvariantResultDigest() InvariantResultDigest {
	return c.invariantResultDigest
}
func (c CheckpointArtifact) ID() CheckpointArtifactID         { return c.id }
func (c CheckpointArtifact) CanonicalBytes() []byte           { return bytes.Clone(c.canonical) }
func (c CheckpointArtifact) Digest() CheckpointArtifactDigest { return c.digest }

// SealOutcome contains either one verified immutable artifact or one typed
// integrity failure. A refused boundary never contains a partial artifact.
type SealOutcome struct {
	artifact *CheckpointArtifact
	failure  *FailureReport
}

func (o SealOutcome) Sealed() bool { return o.artifact != nil && o.failure == nil }
func (o SealOutcome) Artifact() (CheckpointArtifact, bool) {
	if o.artifact == nil {
		return CheckpointArtifact{}, false
	}
	return cloneCheckpointArtifact(*o.artifact), true
}
func (o SealOutcome) Failure() (FailureReport, bool) {
	if o.failure == nil {
		return FailureReport{}, false
	}
	return *o.failure, true
}

// Seal verifies the complete accepted prefix before constructing any
// checkpoint identity or manifest.
func Seal(request SealRequest) (SealOutcome, error) {
	if failure, err := verifySealBinding(request.Binding, request.Journal); err != nil {
		return SealOutcome{}, err
	} else if failure != nil {
		return *failure, nil
	}
	declaration, prefixLength, ok := checkpointBoundary(request.Binding.plan, request.Checkpoint)
	if !ok {
		return sealIntegrityFailure(request.Binding, ArtifactLinkInconsistent, ArtifactPlan,
			Digest(request.Binding.plan.id), nil, nil, nil)
	}

	verifiedState, verifiedJournal, issue := replayVerifiedJournal(request.Binding, request.Journal)
	if issue != nil {
		return sealJournalIntegrityFailure(request.Binding, verifiedState, verifiedJournal, *issue)
	}
	if len(verifiedJournal.entries) != prefixLength {
		prefixDigest := digestJournalPrefixOrFallback(request.Binding, verifiedJournal)
		return sealIntegrityFailure(request.Binding, ArtifactLinkInconsistent, ArtifactJournalPrefix,
			prefixDigest, &verifiedState, nil, nil)
	}
	if err := verifyState(request.State); err != nil {
		rebuilt, rebuildErr := encodeState(request.State)
		if rebuildErr != nil {
			return SealOutcome{}, fmt.Errorf("seal state: %w", rebuildErr)
		}
		content := canonicalDigest(request.State.canonical)
		expected, observed := checkpointDigestEvidence(Digest(request.State.digest), request.State.canonical, rebuilt)
		return sealIntegrityFailure(request.Binding, ArtifactDigestMismatch, ArtifactState,
			content, &verifiedState, expected, observed)
	}
	if request.State.Schema().Digest() != request.Binding.plan.SchemaDigest() ||
		request.State.Digest() != verifiedState.Digest() ||
		!bytes.Equal(request.State.CanonicalBytes(), verifiedState.CanonicalBytes()) {
		observed, expected := Digest(request.State.Digest()), Digest(verifiedState.Digest())
		return sealIntegrityFailure(request.Binding, ReplayDivergence, ArtifactState,
			observed, &verifiedState, &expected, &observed)
	}

	expectedResults := journalInvariantResults(verifiedJournal)
	if !exactCheckpointInvariantResults(request.Binding.plan, prefixLength, request.InvariantResults) {
		digest := digestInvariantResultsOrFallback(request.InvariantResults)
		return sealIntegrityFailure(request.Binding, ArtifactLinkInconsistent, ArtifactInvariantResultSet,
			digest, &verifiedState, nil, nil)
	}
	providedBytes, err := encodeInvariantResults(request.InvariantResults)
	if err != nil {
		digest := digestInvariantResultsOrFallback(request.InvariantResults)
		return sealIntegrityFailure(request.Binding, ArtifactLinkInconsistent, ArtifactInvariantResultSet,
			digest, &verifiedState, nil, nil)
	}
	expectedBytes, err := encodeInvariantResults(expectedResults)
	if err != nil {
		return SealOutcome{}, fmt.Errorf("seal accepted invariant results: %w", err)
	}
	if !bytes.Equal(providedBytes, expectedBytes) {
		observed, expected := canonicalDigest(providedBytes), canonicalDigest(expectedBytes)
		return sealIntegrityFailure(request.Binding, ArtifactLinkInconsistent, ArtifactInvariantResultSet,
			observed, &verifiedState, &expected, &observed)
	}

	journalBytes, err := encodeJournalPrefix(request.Binding.semanticRunID, request.Binding.policyID, verifiedJournal.entries)
	if err != nil {
		return SealOutcome{}, fmt.Errorf("seal journal prefix: %w", err)
	}
	journalDigest := JournalPrefixDigest(canonicalDigest(journalBytes))
	invariantDigest := InvariantResultDigest(canonicalDigest(providedBytes))
	checkpointBytes, err := encodeCheckpointID(request.Binding.plan.id, declaration.Key)
	if err != nil {
		return SealOutcome{}, fmt.Errorf("seal checkpoint ID: %w", err)
	}
	checkpointID := CheckpointID(canonicalDigest(checkpointBytes))
	claimBytes, err := encodeCheckpointArtifactID(request.Binding.semanticRunID, checkpointID, verifiedState.Digest(), journalDigest, invariantDigest, request.Binding.policyID)
	if err != nil {
		return SealOutcome{}, fmt.Errorf("seal checkpoint claim: %w", err)
	}
	artifact := CheckpointArtifact{
		checkpoint: declaration, checkpointID: checkpointID, planID: request.Binding.plan.id,
		semanticRunID: request.Binding.semanticRunID, inputID: request.Binding.inputID,
		initialStateDigest: request.Binding.initialStateDigest, worldID: request.Binding.worldID,
		policyID: request.Binding.policyID, stateDigest: verifiedState.Digest(),
		journalPrefixDigest: journalDigest, invariantResultDigest: invariantDigest,
		id: CheckpointArtifactID(canonicalDigest(claimBytes)),
		// Exactly the bytes invariantDigest was taken from, a few lines above.
		invariantResultCanonical: providedBytes,
	}
	manifest, err := encodeCheckpointArtifact(artifact)
	if err != nil {
		return SealOutcome{}, fmt.Errorf("seal checkpoint manifest: %w", err)
	}
	artifact.canonical = manifest
	artifact.digest = CheckpointArtifactDigest(canonicalDigest(manifest))
	if conflict := conflictingKnownCheckpoint(request.KnownArtifacts, artifact); conflict != nil {
		return sealCheckpointConflict(request.Binding, verifiedState, *conflict, artifact)
	}
	result := cloneCheckpointArtifact(artifact)
	return SealOutcome{artifact: &result}, nil
}

func sealCheckpointConflict(binding RunBinding, state State, known, candidate CheckpointArtifact) (SealOutcome, error) {
	expected, observed := Digest(known.digest), Digest(candidate.digest)
	lastState, lastCheckpoint := state.Digest(), known.id
	references := []ArtifactRef{
		{kind: ArtifactCheckpoint, digest: expected},
		{kind: ArtifactCheckpoint, digest: observed},
	}
	report, err := newArtifactIntegrityFailure(binding.semanticRunID, ArtifactLinkInconsistent,
		ArtifactRef{kind: ArtifactCheckpoint, digest: observed}, &lastState, &lastCheckpoint,
		&expected, &observed, references)
	if err != nil {
		return SealOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return SealOutcome{failure: &failure}, nil
}

func checkpointBoundary(plan Plan, key CheckpointKey) (CheckpointDeclaration, int, bool) {
	for _, checkpoint := range plan.checkpoints {
		if checkpoint.Key != key {
			continue
		}
		for i, transformation := range plan.transformations {
			if transformation.declaration.ID == checkpoint.After {
				return checkpoint, i + 1, true
			}
		}
	}
	return CheckpointDeclaration{}, 0, false
}

func exactCheckpointInvariantResults(plan Plan, prefixLength int, results []InvariantResult) bool {
	declarations := make([]InvariantDeclaration, 0)
	for _, transformation := range plan.transformations[:prefixLength] {
		declarations = append(declarations, transformation.invariants...)
	}
	return exactPassingInvariantResults(declarations, results)
}

func conflictingKnownCheckpoint(known []CheckpointArtifact, candidate CheckpointArtifact) *CheckpointArtifact {
	for _, artifact := range slices.Clone(known) {
		if artifact.id == candidate.id && artifact.digest != candidate.digest {
			copy := cloneCheckpointArtifact(artifact)
			return &copy
		}
	}
	return nil
}

func cloneCheckpointArtifact(input CheckpointArtifact) CheckpointArtifact {
	input.canonical = bytes.Clone(input.canonical)
	// Struct assignment copies a slice header and shares its backing array, so
	// every retained []byte must be cloned here or two artifacts alias one buffer.
	input.invariantResultCanonical = bytes.Clone(input.invariantResultCanonical)
	return input
}

func digestJournalPrefixOrFallback(binding RunBinding, journal Journal) Digest {
	canonical, err := encodeJournalPrefix(binding.semanticRunID, binding.policyID, journal.entries)
	if err != nil {
		return canonicalDigest(nil)
	}
	return canonicalDigest(canonical)
}

func digestInvariantResultsOrFallback(results []InvariantResult) Digest {
	canonical, err := encodeInvariantResults(results)
	if err != nil {
		return canonicalDigest(nil)
	}
	return canonicalDigest(canonical)
}

func sealJournalIntegrityFailure(binding RunBinding, state State, journal Journal, issue journalVerificationIssue) (SealOutcome, error) {
	last := state.Digest()
	references := []ArtifactRef{{kind: ArtifactState, digest: Digest(last)}}
	if issue.artifact != (ArtifactRef{}) {
		references = append(references, issue.artifact)
	}
	report, err := newArtifactIntegrityFailure(binding.semanticRunID, issue.code, issue.artifact, &last, nil, issue.expected, issue.observed, references)
	if err != nil {
		return SealOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return SealOutcome{failure: &failure}, nil
}

func sealIntegrityFailure(binding RunBinding, code IntegrityCode, kind ArtifactKind, observed Digest, lastState *State, expected, explicitObserved *Digest) (SealOutcome, error) {
	artifact := ArtifactRef{kind: kind, digest: observed}
	var last *StateDigest
	if lastState != nil {
		value := lastState.Digest()
		last = &value
	}
	report, err := newArtifactIntegrityFailure(binding.semanticRunID, code, artifact, last, nil, expected, explicitObserved, []ArtifactRef{artifact})
	if err != nil {
		return SealOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return SealOutcome{failure: &failure}, nil
}
