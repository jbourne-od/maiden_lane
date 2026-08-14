package semantic

import (
	"bytes"
	"fmt"
)

// verifySealBinding treats malformed values as machinery errors only until a
// semantic run is established. Thereafter deterministic content/link defects
// become typed integrity outcomes so sealing cannot silently discard evidence.
func verifySealBinding(binding RunBinding, journal Journal) (*SealOutcome, error) {
	if _, err := decodeDigest(string(binding.semanticRunID)); err != nil || !validExecutorIdentity(binding.executor) || binding.policy != ChangesProvenance {
		return nil, fmt.Errorf("seal binding is not an established run")
	}
	if err := verifyPlan(binding.plan); err != nil {
		rebuilt, rebuildErr := encodePlan(binding.plan.schemaDigest, binding.plan.rulesetDigest, binding.plan.compilerVersion, binding.plan.transformations, binding.plan.checkpoints)
		if rebuildErr != nil {
			return nil, fmt.Errorf("seal binding plan: %w", rebuildErr)
		}
		content := canonicalDigest(binding.plan.canonical)
		expected, observed := checkpointDigestEvidence(Digest(binding.plan.id), binding.plan.canonical, rebuilt)
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactDigestMismatch, ArtifactPlan, content, nil, expected, observed)
		return &outcome, failureErr
	}
	if err := verifyState(binding.initialState); err != nil {
		rebuilt, rebuildErr := encodeState(binding.initialState)
		if rebuildErr != nil {
			return nil, fmt.Errorf("seal binding initial state: %w", rebuildErr)
		}
		content := canonicalDigest(binding.initialState.canonical)
		expected, observed := checkpointDigestEvidence(Digest(binding.initialState.digest), binding.initialState.canonical, rebuilt)
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactDigestMismatch, ArtifactState, content, nil, expected, observed)
		return &outcome, failureErr
	}
	if err := verifyWorld(binding.world); err != nil {
		rebuilt, rebuildErr := NewWorld(binding.world.references)
		if rebuildErr != nil {
			return nil, fmt.Errorf("seal binding world: %w", rebuildErr)
		}
		content := canonicalDigest(binding.world.canonical)
		expected, observed := checkpointDigestEvidence(Digest(binding.world.id), binding.world.canonical, rebuilt.canonical)
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactDigestMismatch, ArtifactWorld, content, nil, expected, observed)
		return &outcome, failureErr
	}
	if binding.initialState.Schema().Digest() != binding.plan.SchemaDigest() {
		observed, expected := Digest(binding.initialState.Schema().Digest()), Digest(binding.plan.SchemaDigest())
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactState, observed, nil, &expected, &observed)
		return &outcome, failureErr
	}
	if binding.initialState.Digest() != binding.initialStateDigest {
		expected := Digest(binding.initialState.Digest())
		observed := optionalValidDigest(Digest(binding.initialStateDigest))
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactState, expected, nil, &expected, observed)
		return &outcome, failureErr
	}
	if binding.world.ID() != binding.worldID {
		expected := Digest(binding.world.ID())
		observed := optionalValidDigest(Digest(binding.worldID))
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactWorld, expected, nil, &expected, observed)
		return &outcome, failureErr
	}
	policyBytes, err := encodeProvenancePolicy(binding.policy)
	if err != nil {
		return nil, fmt.Errorf("seal binding policy: %w", err)
	}
	policyID := ProvenancePolicyID(canonicalDigest(policyBytes))
	inputBytes, err := encodeInputIdentity(binding.initialStateDigest, binding.worldID)
	if err != nil {
		return nil, fmt.Errorf("seal binding input: %w", err)
	}
	inputID := InputID(canonicalDigest(inputBytes))
	if binding.inputID != inputID {
		expected := Digest(inputID)
		observed := optionalValidDigest(Digest(binding.inputID))
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactState, Digest(binding.initialStateDigest), nil, &expected, observed)
		return &outcome, failureErr
	}
	runBytes, err := encodeSemanticRunIdentity(inputID, binding.plan.ID())
	if err != nil {
		return nil, fmt.Errorf("seal binding run: %w", err)
	}
	if runID := SemanticRunID(canonicalDigest(runBytes)); binding.semanticRunID != runID {
		observed, expected := Digest(binding.semanticRunID), Digest(runID)
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactPlan, Digest(binding.plan.ID()), nil, &expected, &observed)
		return &outcome, failureErr
	}
	if binding.policyID != policyID {
		expected := Digest(policyID)
		observed := optionalValidDigest(Digest(binding.policyID))
		prefixPolicy := policyID
		if observed != nil {
			prefixPolicy = ProvenancePolicyID(*observed)
		}
		prefixBytes, prefixErr := encodeJournalPrefix(binding.semanticRunID, prefixPolicy, journal.entries)
		if prefixErr != nil {
			return nil, fmt.Errorf("seal binding journal prefix: %w", prefixErr)
		}
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactJournalPrefix, canonicalDigest(prefixBytes), nil, &expected, observed)
		return &outcome, failureErr
	}
	executionBytes, err := encodeExecutionIdentity(binding.semanticRunID, binding.executor, policyID)
	if err != nil {
		return nil, fmt.Errorf("seal binding execution: %w", err)
	}
	if binding.executionID != ExecutionID(canonicalDigest(executionBytes)) {
		expected := canonicalDigest(executionBytes)
		observed := optionalValidDigest(Digest(binding.executionID))
		outcome, failureErr := sealIntegrityFailure(binding, ArtifactLinkInconsistent, ArtifactPlan, Digest(binding.plan.ID()), nil, &expected, observed)
		return &outcome, failureErr
	}
	return nil, nil
}

func checkpointDigestEvidence(cached Digest, supplied, rebuilt []byte) (*Digest, *Digest) {
	expected := canonicalDigest(rebuilt)
	if !bytes.Equal(supplied, rebuilt) {
		observed := canonicalDigest(supplied)
		return &expected, &observed
	}
	return &expected, optionalValidDigest(cached)
}

func optionalValidDigest(value Digest) *Digest {
	if _, err := decodeDigest(string(value)); err != nil {
		return nil
	}
	copy := value
	return &copy
}
