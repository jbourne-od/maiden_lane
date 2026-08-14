package semantic

import (
	"bytes"
	"fmt"
)

type journalVerificationIssue struct {
	code     IntegrityCode
	artifact ArtifactRef
	expected *Digest
	observed *Digest
}

func journalIntegrityTransitionOutcome(binding RunBinding, state State, journal Journal, issue journalVerificationIssue) (TransitionOutcome, error) {
	last := state.Digest()
	references := []ArtifactRef{{kind: ArtifactState, digest: Digest(last)}}
	if issue.artifact != (ArtifactRef{}) {
		references = append(references, issue.artifact)
	}
	report, err := newArtifactIntegrityFailure(binding.semanticRunID, issue.code, issue.artifact, &last, nil, issue.expected, issue.observed, references)
	if err != nil {
		return TransitionOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return TransitionOutcome{state: state, journal: journal, results: journalInvariantResults(journal), failure: &failure}, nil
}

func verifyJournalEntries(binding RunBinding, journal Journal) error {
	_, _, issue := replayVerifiedJournal(binding, journal)
	if issue != nil {
		return fmt.Errorf("journal verification: %s", issue.code)
	}
	return nil
}

func replayVerifiedJournal(binding RunBinding, journal Journal) (State, Journal, *journalVerificationIssue) {
	state := binding.initialState
	verified := NewJournal()
	for index, entry := range journal.entries {
		artifact := ArtifactRef{kind: ArtifactJournalEntry, digest: canonicalDigest(entry.canonical)}
		if issue := verifyJournalEntryIdentity(entry); issue != nil {
			return state, verified, issue
		}
		if index >= len(binding.plan.transformations) || entry.rule != binding.plan.transformations[index].declaration.ID {
			return state, verified, &journalVerificationIssue{code: ArtifactLinkInconsistent, artifact: artifact}
		}
		if entry.predecessor != state.Digest() {
			return state, verified, &journalVerificationIssue{code: ArtifactLinkInconsistent, artifact: artifact}
		}
		if entry.patch.schemaDigest != binding.plan.schemaDigest {
			return state, verified, &journalVerificationIssue{code: ArtifactLinkInconsistent, artifact: artifact}
		}
		if !exactPassingInvariantResults(binding.plan.transformations[index].invariants, entry.invariantResults) {
			return state, verified, &journalVerificationIssue{code: ArtifactLinkInconsistent, artifact: artifact}
		}
		applied, err := ApplyPatch(state, entry.patch)
		if err != nil || applied.Failure() != nil {
			return state, verified, &journalVerificationIssue{code: ReplayDivergence, artifact: artifact}
		}
		if applied.State().Digest() != entry.result {
			expected, observed := Digest(applied.State().Digest()), Digest(entry.result)
			return state, verified, &journalVerificationIssue{code: ReplayDivergence, artifact: artifact, expected: &expected, observed: &observed}
		}
		state = applied.State()
		verified = verified.AppendAccepted(entry)
	}
	return state, verified, nil
}

func verifyJournalEntryIdentity(entry JournalEntry) *journalVerificationIssue {
	actualContent := Digest(canonicalDigest(entry.canonical))
	artifact := ArtifactRef{kind: ArtifactJournalEntry, digest: actualContent}
	patchBytes, err := encodePatch(entry.patch.schemaDigest, entry.patch.operations)
	patchArtifact := ArtifactRef{kind: ArtifactPatch, digest: canonicalDigest(entry.patch.canonical)}
	if err != nil {
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: patchArtifact}
	}
	expectedPatch := Digest(canonicalDigest(patchBytes))
	if !bytes.Equal(patchBytes, entry.patch.canonical) {
		observedPatch := patchArtifact.digest
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: patchArtifact, expected: &expectedPatch, observed: &observedPatch}
	}
	if PatchDigest(expectedPatch) != entry.patch.digest {
		observedPatch := Digest(entry.patch.digest)
		if _, err := decodeDigest(string(observedPatch)); err != nil {
			return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: patchArtifact, expected: &expectedPatch}
		}
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: patchArtifact, expected: &expectedPatch, observed: &observedPatch}
	}
	canonical, err := encodeJournalEntry(entry)
	if err != nil {
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: artifact}
	}
	expected := Digest(canonicalDigest(canonical))
	if !bytes.Equal(canonical, entry.canonical) {
		observed := actualContent
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: artifact, expected: &expected, observed: &observed}
	}
	if JournalEntryDigest(expected) != entry.digest {
		observed := Digest(entry.digest)
		if _, err := decodeDigest(string(observed)); err != nil {
			return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: artifact, expected: &expected}
		}
		return &journalVerificationIssue{code: ArtifactDigestMismatch, artifact: artifact, expected: &expected, observed: &observed}
	}
	return nil
}

func exactPassingInvariantResults(declarations []InvariantDeclaration, results []InvariantResult) bool {
	if len(declarations) != len(results) {
		return false
	}
	byKey := make(map[string]InvariantResult, len(results))
	for _, result := range results {
		if !result.passed {
			return false
		}
		if _, duplicate := byKey[result.declarationKey]; duplicate {
			return false
		}
		byKey[result.declarationKey] = result
	}
	for _, declaration := range declarations {
		result, ok := byKey[declaration.key]
		if !ok || result.code != declaration.code || result.scope != declaration.scope || result.boundary != declaration.appliesAfter {
			return false
		}
	}
	return true
}
