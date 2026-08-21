package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// IntegrityCode is the closed semantic-artifact integrity vocabulary.
type IntegrityCode string

const (
	ArtifactDigestMismatch     IntegrityCode = "ARTIFACT_DIGEST_MISMATCH"
	ArtifactLinkInconsistent   IntegrityCode = "ARTIFACT_LINK_INCONSISTENT"
	AssessmentIdentityConflict IntegrityCode = "ASSESSMENT_IDENTITY_CONFLICT"
	ReplayDivergence           IntegrityCode = "REPLAY_DIVERGENCE"
)

// ArtifactKind distinguishes implicated semantic artifacts.
type ArtifactKind uint8

const (
	ArtifactPlan ArtifactKind = iota + 1
	ArtifactCompiledProfile
	ArtifactState
	ArtifactWorld
	ArtifactPatch
	ArtifactJournalEntry
	ArtifactJournalPrefix
	ArtifactInvariantResultSet
	ArtifactCheckpoint
	ArtifactReadinessAssessment
)

type ArtifactRef struct {
	kind   ArtifactKind
	digest Digest
}

func (r ArtifactRef) Kind() ArtifactKind { return r.kind }
func (r ArtifactRef) Digest() Digest     { return r.digest }

func (k ArtifactKind) String() string {
	switch k {
	case ArtifactPlan:
		return "plan"
	case ArtifactCompiledProfile:
		return "compiled_profile"
	case ArtifactState:
		return "state"
	case ArtifactWorld:
		return "world"
	case ArtifactPatch:
		return "patch"
	case ArtifactJournalEntry:
		return "journal_entry"
	case ArtifactJournalPrefix:
		return "journal_prefix"
	case ArtifactInvariantResultSet:
		return "invariant_result_set"
	case ArtifactCheckpoint:
		return "checkpoint_artifact"
	case ArtifactReadinessAssessment:
		return "readiness_assessment"
	default:
		return ""
	}
}

type protectedCodeKind uint8

const (
	protectedOperationCode protectedCodeKind = iota + 1
	protectedInvariantCode
)

// ProtectedInvariantFailureReport is the rule- or operation-tagged immutable
// rejection artifact.
type ProtectedInvariantFailureReport struct {
	semanticRunID SemanticRunID
	rule          RuleID
	codeKind      protectedCodeKind
	operationCode OperationInvariantCode
	invariantCode InvariantCode
	predecessor   StateDigest
	results       []InvariantResult
	entities      []EntityRef
	facts         []FactRef
	invariantRefs []InvariantEvidenceRef
	patchDigest   *PatchDigest
	canonical     []byte
	digest        FailureReportDigest
}

func (f ProtectedInvariantFailureReport) Kind() FailureKind { return ProtectedInvariantFailed }
func (f ProtectedInvariantFailureReport) Code() string {
	if f.codeKind == protectedOperationCode {
		return string(f.operationCode)
	}
	return string(f.invariantCode)
}
func (f ProtectedInvariantFailureReport) OperationInvariantCode() OperationInvariantCode {
	return f.operationCode
}
func (f ProtectedInvariantFailureReport) InvariantCode() InvariantCode        { return f.invariantCode }
func (f ProtectedInvariantFailureReport) RuleID() RuleID                      { return f.rule }
func (f ProtectedInvariantFailureReport) PredecessorStateDigest() StateDigest { return f.predecessor }
func (f ProtectedInvariantFailureReport) InvariantResults() []InvariantResult {
	return cloneInvariantResults(f.results)
}
func (f ProtectedInvariantFailureReport) Entities() []EntityRef { return slices.Clone(f.entities) }
func (f ProtectedInvariantFailureReport) FactRefs() []FactRef   { return slices.Clone(f.facts) }
func (f ProtectedInvariantFailureReport) InvariantEvidenceRefs() []InvariantEvidenceRef {
	return slices.Clone(f.invariantRefs)
}
func (f ProtectedInvariantFailureReport) ProposedPatchDigest() (PatchDigest, bool) {
	if f.patchDigest == nil {
		return "", false
	}
	return *f.patchDigest, true
}
func (f ProtectedInvariantFailureReport) CanonicalBytes() []byte      { return bytes.Clone(f.canonical) }
func (f ProtectedInvariantFailureReport) Digest() FailureReportDigest { return f.digest }

// ArtifactIntegrityFailureReport is the distinct tagged integrity variant.
type ArtifactIntegrityFailureReport struct {
	semanticRunID  SemanticRunID
	code           IntegrityCode
	artifactKind   ArtifactKind
	artifact       ArtifactRef
	lastState      *StateDigest
	lastCheckpoint *CheckpointArtifactID
	expected       *Digest
	observed       *Digest
	references     []ArtifactRef
	canonical      []byte
	digest         FailureReportDigest
}

func (f ArtifactIntegrityFailureReport) Kind() FailureKind           { return ArtifactIntegrityFailed }
func (f ArtifactIntegrityFailureReport) Code() IntegrityCode         { return f.code }
func (f ArtifactIntegrityFailureReport) ArtifactKind() ArtifactKind  { return f.artifactKind }
func (f ArtifactIntegrityFailureReport) Artifact() ArtifactRef       { return f.artifact }
func (f ArtifactIntegrityFailureReport) References() []ArtifactRef   { return slices.Clone(f.references) }
func (f ArtifactIntegrityFailureReport) CanonicalBytes() []byte      { return bytes.Clone(f.canonical) }
func (f ArtifactIntegrityFailureReport) Digest() FailureReportDigest { return f.digest }
func (f ArtifactIntegrityFailureReport) LastVerifiedStateDigest() (StateDigest, bool) {
	if f.lastState == nil {
		return "", false
	}
	return *f.lastState, true
}
func (f ArtifactIntegrityFailureReport) LastVerifiedCheckpointArtifactID() (CheckpointArtifactID, bool) {
	if f.lastCheckpoint == nil {
		return "", false
	}
	return *f.lastCheckpoint, true
}
func (f ArtifactIntegrityFailureReport) ExpectedDigest() (Digest, bool) {
	if f.expected == nil {
		return "", false
	}
	return *f.expected, true
}
func (f ArtifactIntegrityFailureReport) ObservedDigest() (Digest, bool) {
	if f.observed == nil {
		return "", false
	}
	return *f.observed, true
}

func newArtifactIntegrityFailure(run SemanticRunID, code IntegrityCode, artifact ArtifactRef, lastState *StateDigest, lastCheckpoint *CheckpointArtifactID, expected, observed *Digest, references []ArtifactRef) (ArtifactIntegrityFailureReport, error) {
	if !validIntegrityCode(code) || !validArtifactRef(artifact) {
		return ArtifactIntegrityFailureReport{}, fmt.Errorf("invalid artifact integrity failure")
	}
	normalized := slices.Clone(references)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].kind != normalized[j].kind {
			return normalized[i].kind < normalized[j].kind
		}
		return normalized[i].digest < normalized[j].digest
	})
	for _, reference := range normalized {
		if !validArtifactRef(reference) {
			return ArtifactIntegrityFailureReport{}, fmt.Errorf("invalid artifact integrity reference")
		}
	}
	report := ArtifactIntegrityFailureReport{semanticRunID: run, code: code, artifactKind: artifact.kind, artifact: artifact, references: normalized}
	if lastState != nil {
		value := *lastState
		report.lastState = &value
	}
	if lastCheckpoint != nil {
		value := *lastCheckpoint
		report.lastCheckpoint = &value
	}
	if expected != nil {
		value := *expected
		report.expected = &value
	}
	if observed != nil {
		value := *observed
		report.observed = &value
	}
	canonical, err := encodeArtifactIntegrityFailure(report)
	if err != nil {
		return ArtifactIntegrityFailureReport{}, err
	}
	report.canonical, report.digest = canonical, FailureReportDigest(canonicalDigest(canonical))
	return report, nil
}

func validArtifactRef(reference ArtifactRef) bool {
	if reference.kind < ArtifactPlan || reference.kind > ArtifactReadinessAssessment || reference.kind.String() == "" {
		return false
	}
	_, err := decodeDigest(string(reference.digest))
	return err == nil
}

func validIntegrityCode(code IntegrityCode) bool {
	switch code {
	case ArtifactDigestMismatch, ArtifactLinkInconsistent, AssessmentIdentityConflict, ReplayDivergence:
		return true
	default:
		return false
	}
}

// FailureReport is the closed tagged execution-failure union.
type FailureReport struct {
	protected *ProtectedInvariantFailureReport
	integrity *ArtifactIntegrityFailureReport
}

func (f FailureReport) Kind() FailureKind {
	if f.protected != nil {
		return ProtectedInvariantFailed
	}
	if f.integrity != nil {
		return ArtifactIntegrityFailed
	}
	return ""
}
func (f FailureReport) Code() string {
	if f.protected != nil {
		return f.protected.Code()
	}
	if f.integrity != nil {
		return string(f.integrity.code)
	}
	return ""
}
func (f FailureReport) InvariantCode() InvariantCode {
	if f.protected == nil {
		return ""
	}
	return f.protected.invariantCode
}
func (f FailureReport) OperationInvariantCode() OperationInvariantCode {
	if f.protected == nil {
		return ""
	}
	return f.protected.operationCode
}
func (f FailureReport) ProposedPatchDigest() (PatchDigest, bool) {
	if f.protected == nil {
		return "", false
	}
	return f.protected.ProposedPatchDigest()
}
func (f FailureReport) CanonicalBytes() []byte {
	if f.protected != nil {
		return f.protected.CanonicalBytes()
	}
	if f.integrity != nil {
		return f.integrity.CanonicalBytes()
	}
	return nil
}
func (f FailureReport) Digest() FailureReportDigest {
	if f.protected != nil {
		return f.protected.digest
	}
	if f.integrity != nil {
		return f.integrity.digest
	}
	return ""
}
func (f FailureReport) InvariantResults() []InvariantResult {
	if f.protected == nil {
		return nil
	}
	return f.protected.InvariantResults()
}

func (f FailureReport) ProtectedInvariant() (ProtectedInvariantFailureReport, bool) {
	if f.protected == nil {
		return ProtectedInvariantFailureReport{}, false
	}
	return *f.protected, true
}

func (f FailureReport) Entities() []EntityRef {
	if f.protected == nil {
		return nil
	}
	return f.protected.Entities()
}

func (f FailureReport) FactRefs() []FactRef {
	if f.protected == nil {
		return nil
	}
	return f.protected.FactRefs()
}

func (f FailureReport) ArtifactIntegrity() (ArtifactIntegrityFailureReport, bool) {
	if f.integrity == nil {
		return ArtifactIntegrityFailureReport{}, false
	}
	return *f.integrity, true
}
