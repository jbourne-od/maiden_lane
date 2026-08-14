package semantic

import (
	"bytes"
	"slices"
)

// FactRef safely identifies a semantic fact without retaining its value or a
// source-system key.
type FactRef struct {
	entity EntityRef
	field  FieldName
}

func (r FactRef) Entity() EntityRef { return r.entity }
func (r FactRef) Field() FieldName  { return r.field }

// InvariantEvidenceRef names an evaluated closed invariant declaration.
type InvariantEvidenceRef struct {
	declarationKey string
}

func (r InvariantEvidenceRef) DeclarationKey() string { return r.declarationKey }

// InvariantResult is one immutable protected-obligation result. Journaled
// transitions contain only passing results; failure reports may contain the
// deterministic prefix through the first failed result.
type InvariantResult struct {
	declarationKey string
	scope          InvariantScope
	boundary       RuleID
	passed         bool
	code           InvariantCode
	entities       []EntityRef
	facts          []FactRef
}

func (r InvariantResult) DeclarationKey() string { return r.declarationKey }
func (r InvariantResult) Scope() InvariantScope  { return r.scope }
func (r InvariantResult) Boundary() RuleID       { return r.boundary }
func (r InvariantResult) Passed() bool           { return r.passed }
func (r InvariantResult) Code() InvariantCode    { return r.code }
func (r InvariantResult) Entities() []EntityRef  { return slices.Clone(r.entities) }
func (r InvariantResult) FactRefs() []FactRef    { return slices.Clone(r.facts) }

// JournalEntry is one accepted semantic transition. It contains no execution,
// backend, attempt, clock, host, or storage metadata.
type JournalEntry struct {
	rule             RuleID
	predecessor      StateDigest
	result           StateDigest
	patch            Patch
	evidence         []FactRef
	invariantResults []InvariantResult
	canonical        []byte
	digest           JournalEntryDigest
}

func (e JournalEntry) RuleID() RuleID                      { return e.rule }
func (e JournalEntry) PredecessorStateDigest() StateDigest { return e.predecessor }
func (e JournalEntry) ResultStateDigest() StateDigest      { return e.result }
func (e JournalEntry) Patch() Patch                        { return clonePatch(e.patch) }
func (e JournalEntry) Evidence() []FactRef                 { return slices.Clone(e.evidence) }
func (e JournalEntry) InvariantResults() []InvariantResult {
	return cloneInvariantResults(e.invariantResults)
}
func (e JournalEntry) CanonicalBytes() []byte     { return bytes.Clone(e.canonical) }
func (e JournalEntry) Digest() JournalEntryDigest { return e.digest }

// Journal is an immutable accepted-only sequence.
type Journal struct{ entries []JournalEntry }

func NewJournal() Journal { return Journal{} }

// AppendAccepted returns a new journal containing one already verified entry.
func (j Journal) AppendAccepted(entry JournalEntry) Journal {
	entries := cloneJournalEntries(j.entries)
	if !verifiedJournalEntry(entry) {
		return Journal{entries: entries}
	}
	if len(entries) > 0 && entry.predecessor != entries[len(entries)-1].result {
		return Journal{entries: entries}
	}
	entries = append(entries, cloneJournalEntry(entry))
	return Journal{entries: entries}
}

func (j Journal) Entries() []JournalEntry { return cloneJournalEntries(j.entries) }

// PrefixDigest identifies accepted history for one semantic run and policy.
func (j Journal) PrefixDigest(binding RunBinding) JournalPrefixDigest {
	if err := verifyBinding(binding); err != nil {
		return ""
	}
	if err := verifyJournalEntries(binding, j); err != nil {
		return ""
	}
	canonical, err := encodeJournalPrefix(binding.semanticRunID, binding.policyID, j.entries)
	if err != nil {
		return ""
	}
	return JournalPrefixDigest(canonicalDigest(canonical))
}

func verifiedJournalEntry(entry JournalEntry) bool {
	if !validSemanticName(string(entry.rule)) || entry.predecessor == "" || entry.result == "" || entry.digest == "" || len(entry.canonical) == 0 {
		return false
	}
	if len(entry.invariantResults) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(entry.invariantResults))
	for _, result := range entry.invariantResults {
		if !result.passed || result.declarationKey == "" {
			return false
		}
		if _, duplicate := seen[result.declarationKey]; duplicate {
			return false
		}
		seen[result.declarationKey] = struct{}{}
	}
	patchBytes, err := encodePatch(entry.patch.schemaDigest, entry.patch.operations)
	if err != nil || !bytes.Equal(patchBytes, entry.patch.canonical) || PatchDigest(canonicalDigest(patchBytes)) != entry.patch.digest {
		return false
	}
	canonical, err := encodeJournalEntry(entry)
	return err == nil && bytes.Equal(canonical, entry.canonical) && JournalEntryDigest(canonicalDigest(canonical)) == entry.digest
}

func cloneJournalEntry(input JournalEntry) JournalEntry {
	return JournalEntry{rule: input.rule, predecessor: input.predecessor, result: input.result,
		patch: clonePatch(input.patch), evidence: slices.Clone(input.evidence),
		invariantResults: cloneInvariantResults(input.invariantResults), canonical: bytes.Clone(input.canonical), digest: input.digest}
}

func cloneJournalEntries(input []JournalEntry) []JournalEntry {
	result := make([]JournalEntry, len(input))
	for i := range input {
		result[i] = cloneJournalEntry(input[i])
	}
	return result
}

func cloneInvariantResults(input []InvariantResult) []InvariantResult {
	result := make([]InvariantResult, len(input))
	for i, item := range input {
		result[i] = item
		result[i].entities = slices.Clone(item.entities)
		result[i].facts = slices.Clone(item.facts)
	}
	return result
}

func clonePatch(input Patch) Patch {
	return Patch{schemaDigest: input.schemaDigest, operations: cloneOperations(input.operations), canonical: bytes.Clone(input.canonical), digest: input.digest}
}
