package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// TransitionOutcome always retains the authoritative predecessor on rejection
// and appends accepted history only after all checks pass.
type TransitionOutcome struct {
	state   State
	patch   *Patch
	journal Journal
	results []InvariantResult
	failure *FailureReport
}

func (o TransitionOutcome) State() State { return o.state }
func (o TransitionOutcome) Patch() Patch {
	if o.patch == nil {
		return Patch{}
	}
	return clonePatch(*o.patch)
}
func (o TransitionOutcome) HasPatch() bool { return o.patch != nil }
func (o TransitionOutcome) Journal() Journal {
	return Journal{entries: cloneJournalEntries(o.journal.entries)}
}
func (o TransitionOutcome) InvariantResults() []InvariantResult {
	return cloneInvariantResults(o.results)
}
func (o TransitionOutcome) InvariantResultDigest() InvariantResultDigest {
	canonical, err := encodeInvariantResults(o.results)
	if err != nil {
		return ""
	}
	return InvariantResultDigest(canonicalDigest(canonical))
}
func (o TransitionOutcome) Failure() (FailureReport, bool) {
	if o.failure == nil {
		return FailureReport{}, false
	}
	return *o.failure, true
}

// ExecuteTransition interprets only a compiled closed operator selected from
// the bound plan.
func ExecuteTransition(binding RunBinding, rule RuleID, state State, journal Journal) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	if err := verifyBinding(binding); err != nil {
		return base, err
	}
	verifiedState, verifiedJournal, issue := replayVerifiedJournal(binding, journal)
	if issue != nil {
		return journalIntegrityTransitionOutcome(binding, verifiedState, verifiedJournal, *issue)
	}
	if err := verifyState(state); err != nil {
		return integrityTransitionOutcome(binding, verifiedState, verifiedJournal, ArtifactDigestMismatch, ArtifactState, digestOrFallback(string(state.digest), string(canonicalDigest(state.canonical))), Digest(verifiedState.Digest()))
	}
	if state.Schema().Digest() != binding.plan.SchemaDigest() {
		return integrityTransitionOutcome(binding, verifiedState, verifiedJournal, ArtifactLinkInconsistent, ArtifactState, Digest(state.digest), Digest(verifiedState.Digest()))
	}
	if state.Digest() != verifiedState.Digest() || !bytes.Equal(state.CanonicalBytes(), verifiedState.CanonicalBytes()) {
		return integrityTransitionOutcome(binding, verifiedState, verifiedJournal, ArtifactLinkInconsistent, ArtifactState, Digest(state.digest), Digest(verifiedState.Digest()))
	}
	if len(journal.entries) >= len(binding.plan.transformations) || binding.plan.transformations[len(journal.entries)].declaration.ID != rule {
		return base, fmt.Errorf("execute transition: rule %q is not the next compiled transformation", rule)
	}
	transformation := cloneCompiledTransformation(binding.plan.transformations[len(journal.entries)])
	switch transformation.Operator() {
	case OperatorSelectAndAssign:
		return executeSelectAndAssign(binding, transformation, state, journal)
	case OperatorInsertEntity:
		return executeInsertEntity(binding, transformation, state, journal)
	case OperatorDeleteEntity:
		return executeDeleteEntity(binding, transformation, state, journal)
	case OperatorRelateEntities:
		return executeRelateEntities(binding, transformation, state, journal)
	case OperatorUnrelateEntities:
		return executeUnrelateEntities(binding, transformation, state, journal)
	case OperatorMergeEntities:
		return executeMergeEntities(binding, transformation, state, journal)
	case OperatorSplitEntity:
		return executeSplitEntity(binding, transformation, state, journal)
	default:
		return base, fmt.Errorf("execute transition: unsupported compiled operator %d", transformation.Operator())
	}
}

func integrityTransitionOutcome(binding RunBinding, state State, journal Journal, code IntegrityCode, kind ArtifactKind, observed, expected Digest) (TransitionOutcome, error) {
	artifact := ArtifactRef{kind: kind, digest: observed}
	last := state.Digest()
	report, err := newArtifactIntegrityFailure(binding.semanticRunID, code, artifact, &last, nil, &expected, &observed, []ArtifactRef{{kind: ArtifactState, digest: Digest(last)}})
	if err != nil {
		return TransitionOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return TransitionOutcome{state: state, journal: journal, results: journalInvariantResults(journal), failure: &failure}, nil
}

func digestOrFallback(value, fallback string) Digest {
	if _, err := decodeDigest(value); err == nil {
		return Digest(value)
	}
	return Digest(fallback)
}

func passingResults(declarations []InvariantDeclaration, entities []EntityRef, facts []FactRef) []InvariantResult {
	results := make([]InvariantResult, len(declarations))
	for i, declaration := range declarations {
		results[i] = invariantResult(declaration, true, entities, evidenceForInvariant(declaration, facts))
	}
	return results
}

func passingResultsBeforeCandidate(declarations []InvariantDeclaration, entities []EntityRef, facts []FactRef) []InvariantResult {
	results := make([]InvariantResult, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration.scope == InvariantCandidatePostcondition {
			continue
		}
		results = append(results, invariantResult(declaration, true, entities, evidenceForInvariant(declaration, facts)))
	}
	return results
}

func journalInvariantResults(journal Journal) []InvariantResult {
	results := make([]InvariantResult, 0)
	for _, entry := range journal.entries {
		results = append(results, cloneInvariantResults(entry.invariantResults)...)
	}
	return results
}

func outcomeInvariantResults(journal Journal, current []InvariantResult) []InvariantResult {
	return append(journalInvariantResults(journal), cloneInvariantResults(current)...)
}

func invariantResult(declaration InvariantDeclaration, passed bool, entities []EntityRef, facts []FactRef) InvariantResult {
	entityCopy := slices.Clone(entities)
	sort.Slice(entityCopy, func(i, j int) bool { return compareEntityRefs(entityCopy[i], entityCopy[j]) < 0 })
	factCopy := canonicalFactRefs(facts)
	return InvariantResult{declarationKey: declaration.key, scope: declaration.scope, boundary: declaration.appliesAfter,
		passed: passed, code: declaration.code, entities: entityCopy, facts: factCopy}
}

func rejectInvariantAtKey(binding RunBinding, rule RuleID, state State, journal Journal, declarations []InvariantDeclaration, code InvariantCode, failureKey string, entities []EntityRef, facts []FactRef, patch *Patch) (TransitionOutcome, error) {
	results := evaluatedFailureResults(declarations, failureKey, entities, facts)
	return rejectInvariantEvaluated(binding, rule, state, journal, code, results, entities, facts, patch)
}

func rejectInvariantEvaluated(binding RunBinding, rule RuleID, state State, journal Journal, code InvariantCode, results []InvariantResult, entities []EntityRef, facts []FactRef, patch *Patch) (TransitionOutcome, error) {
	report, err := protectedFailure(binding, rule, state.Digest(), protectedInvariantCode, "", code, results, entities, facts, patch)
	if err != nil {
		return TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}, err
	}
	failure := FailureReport{protected: &report}
	return TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}, results: outcomeInvariantResults(journal, results), failure: &failure}, nil
}

func rejectOperation(binding RunBinding, rule RuleID, state State, journal Journal, declarations []InvariantDeclaration, code OperationInvariantCode, entities []EntityRef, facts []FactRef, patch Patch) (TransitionOutcome, error) {
	results := passingResultsBeforeCandidate(declarations, entities, facts)
	report, err := protectedFailure(binding, rule, state.Digest(), protectedOperationCode, code, "", results, entities, facts, &patch)
	if err != nil {
		return TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}, err
	}
	failure := FailureReport{protected: &report}
	return TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}, results: outcomeInvariantResults(journal, results), failure: &failure}, nil
}

func evaluatedFailureResults(declarations []InvariantDeclaration, failureKey string, entities []EntityRef, facts []FactRef) []InvariantResult {
	results := make([]InvariantResult, 0, len(declarations))
	for _, declaration := range declarations {
		passed := declaration.key != failureKey
		results = append(results, invariantResult(declaration, passed, entities, evidenceForInvariant(declaration, facts)))
		if !passed {
			break
		}
	}
	return results
}

func evidenceForInvariant(declaration InvariantDeclaration, facts []FactRef) []FactRef {
	if len(declaration.reads) == 0 {
		return nil
	}
	wanted := make(map[FieldName]struct{}, len(declaration.reads))
	for _, path := range declaration.reads {
		_, name := splitFieldPath(path)
		wanted[name] = struct{}{}
	}
	result := make([]FactRef, 0, len(facts))
	for _, fact := range facts {
		if _, ok := wanted[fact.field]; ok {
			result = append(result, fact)
		}
	}
	return canonicalFactRefs(result)
}

func protectedFailure(binding RunBinding, rule RuleID, predecessor StateDigest, codeKind protectedCodeKind, operation OperationInvariantCode, invariant InvariantCode, results []InvariantResult, entities []EntityRef, facts []FactRef, patch *Patch) (ProtectedInvariantFailureReport, error) {
	report := ProtectedInvariantFailureReport{semanticRunID: binding.semanticRunID, rule: rule, codeKind: codeKind,
		operationCode: operation, invariantCode: invariant, predecessor: predecessor, results: cloneInvariantResults(results),
		entities: canonicalEntityRefs(entities), facts: canonicalFactRefs(facts)}
	for _, result := range results {
		report.invariantRefs = append(report.invariantRefs, InvariantEvidenceRef{declarationKey: result.declarationKey})
	}
	report.invariantRefs = canonicalInvariantEvidenceRefs(report.invariantRefs)
	if patch != nil {
		digest := patch.Digest()
		report.patchDigest = &digest
	}
	canonical, err := encodeProtectedFailure(report)
	if err != nil {
		return ProtectedInvariantFailureReport{}, fmt.Errorf("canonicalize protected failure: %w", err)
	}
	report.canonical, report.digest = canonical, FailureReportDigest(canonicalDigest(canonical))
	return report, nil
}

func canonicalInvariantEvidenceRefs(input []InvariantEvidenceRef) []InvariantEvidenceRef {
	result := slices.Clone(input)
	sort.Slice(result, func(i, j int) bool { return result[i].declarationKey < result[j].declarationKey })
	return slices.CompactFunc(result, func(a, b InvariantEvidenceRef) bool { return a.declarationKey == b.declarationKey })
}

func newJournalEntry(rule RuleID, predecessor, result State, patch Patch, evidence []FactRef, results []InvariantResult) (JournalEntry, error) {
	entry := JournalEntry{rule: rule, predecessor: predecessor.Digest(), result: result.Digest(), patch: clonePatch(patch),
		evidence: canonicalFactRefs(evidence), invariantResults: cloneInvariantResults(results)}
	canonical, err := encodeJournalEntry(entry)
	if err != nil {
		return JournalEntry{}, fmt.Errorf("canonicalize journal entry: %w", err)
	}
	entry.canonical, entry.digest = canonical, JournalEntryDigest(canonicalDigest(canonical))
	return entry, nil
}

func canonicalEntityRefs(input []EntityRef) []EntityRef {
	result := slices.Clone(input)
	sort.Slice(result, func(i, j int) bool { return compareEntityRefs(result[i], result[j]) < 0 })
	return slices.Compact(result)
}

func canonicalFactRefs(input []FactRef) []FactRef {
	result := slices.Clone(input)
	sort.Slice(result, func(i, j int) bool {
		if comparison := compareEntityRefs(result[i].entity, result[j].entity); comparison != 0 {
			return comparison < 0
		}
		return result[i].field < result[j].field
	})
	return slices.Compact(result)
}
