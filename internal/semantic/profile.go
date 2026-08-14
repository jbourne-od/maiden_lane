package semantic

// Readiness assessment: one immutable, typed answer to the question "does
// this sealed checkpoint satisfy this compiled consumer profile?".
//
// Completeness is consumer-relative and never forks semantics (Inviolate 19):
// a profile assesses state but cannot transform it, waive a protected
// invariant, or select an alternate pipeline. Assess therefore mutates
// nothing — no state change, no journal append — and `needs_input` is a
// lawful verdict, not an invariant failure (Inviolate 4). Scope and
// aggregation are explicit compiled-profile content; the assessor never
// infers scope from a caller-supplied entity, and a non-ready in-scope
// entity always appears in the retained per-entity results.

import (
	"bytes"
	"fmt"
	"slices"
)

// ReadinessVerdict is the closed consumer-readiness vocabulary. The string
// values are the ratified canonical tokens used in the v1 canonical encoding.
type ReadinessVerdict string

const (
	Ready      ReadinessVerdict = "ready"
	NeedsInput ReadinessVerdict = "needs_input"
)

func validReadinessVerdict(verdict ReadinessVerdict) bool {
	return verdict == Ready || verdict == NeedsInput
}

// RequirementResultKind is the closed satisfied/missing result for one
// normalized requirement atom evaluated against one selected entity.
type RequirementResultKind uint8

const (
	RequirementSatisfied RequirementResultKind = iota + 1
	RequirementMissing
)

func validRequirementResultKind(kind RequirementResultKind) bool {
	return kind == RequirementSatisfied || kind == RequirementMissing
}

// RequirementResult is one immutable per-requirement evaluation. Satisfied
// results carry the present fact's safe reference; missing results carry no
// fact reference because the required fact does not exist in the state.
type RequirementResult struct {
	code   RequirementCode
	result RequirementResultKind
	facts  []FactRef
}

// Code returns the stable closed readiness requirement code.
func (r RequirementResult) Code() RequirementCode { return r.code }

// Result returns the closed satisfied/missing tag.
func (r RequirementResult) Result() RequirementResultKind { return r.result }

// Satisfied reports whether the requirement atom held for the entity.
func (r RequirementResult) Satisfied() bool { return r.result == RequirementSatisfied }

// FactRefs returns a defensive copy of the sorted safe fact evidence.
func (r RequirementResult) FactRefs() []FactRef { return slices.Clone(r.facts) }

// EntityAssessment retains every normalized requirement result for one
// selected entity, satisfied and missing alike, so aggregation can never
// silently omit a failing in-scope entity.
type EntityAssessment struct {
	entity  EntityRef
	results []RequirementResult
}

// Entity returns the selected entity's stable typed reference.
func (a EntityAssessment) Entity() EntityRef { return a.entity }

// Results returns defensive copies in canonical requirement order.
func (a EntityAssessment) Results() []RequirementResult {
	return cloneRequirementResults(a.results)
}

// Assessment is one immutable readiness artifact over a sealed checkpoint and
// a compiled profile. AssessmentID identifies the deterministic semantic
// question (checkpoint x profile); AssessmentDigest content-addresses the
// complete canonical answer. They remain distinct values.
type Assessment struct {
	checkpointArtifactID CheckpointArtifactID
	profileID            ProfileID
	verdict              ReadinessVerdict
	entities             []EntityAssessment
	id                   AssessmentID
	canonical            []byte
	digest               AssessmentDigest
}

// CheckpointArtifactID returns the assessed sealed checkpoint claim identity.
func (a Assessment) CheckpointArtifactID() CheckpointArtifactID { return a.checkpointArtifactID }

// ProfileID returns the compiled profile identity that posed the question.
func (a Assessment) ProfileID() ProfileID { return a.profileID }

// Verdict returns the aggregate closed readiness verdict.
func (a Assessment) Verdict() ReadinessVerdict { return a.verdict }

// EntityResults returns deep copies of every selected entity's results in
// canonical entity order.
func (a Assessment) EntityResults() []EntityAssessment {
	return cloneEntityAssessments(a.entities)
}

// ID returns the semantic question identity H(CheckpointArtifactID, ProfileID).
func (a Assessment) ID() AssessmentID { return a.id }

// CanonicalBytes returns a defensive copy of the v1 assessment bytes.
func (a Assessment) CanonicalBytes() []byte { return bytes.Clone(a.canonical) }

// Digest returns the content identity of the complete canonical assessment.
func (a Assessment) Digest() AssessmentDigest { return a.digest }

// AssessmentRequest supplies the exact sealed checkpoint, its state, and the
// compiled profile for one readiness question. KnownAssessments is the
// caller's in-memory verified frontier used only to refuse one AssessmentID
// resolving to two different answers; there is no global registry.
type AssessmentRequest struct {
	Checkpoint       CheckpointArtifact
	State            State
	Profile          CompiledProfile
	KnownAssessments []Assessment
}

// AssessmentOutcome contains either one immutable assessment or one typed
// integrity failure. A refused boundary never exposes a partial assessment.
type AssessmentOutcome struct {
	assessment *Assessment
	failure    *FailureReport
}

// Assessed reports whether the request produced an immutable assessment.
func (o AssessmentOutcome) Assessed() bool { return o.assessment != nil && o.failure == nil }

// Assessment returns a defensive copy of the produced assessment, if any.
func (o AssessmentOutcome) Assessment() (Assessment, bool) {
	if o.assessment == nil {
		return Assessment{}, false
	}
	return cloneAssessment(*o.assessment), true
}

// Failure returns the typed integrity failure, if the request was refused.
func (o AssessmentOutcome) Failure() (FailureReport, bool) {
	if o.failure == nil {
		return FailureReport{}, false
	}
	return *o.failure, true
}

// Assess verifies every checkpoint/state/profile content identity and link
// before evaluating readiness. Malformed inputs that never established a
// semantic run are Go errors; deterministic content or link defects on
// established artifacts are typed ARTIFACT_INTEGRITY_FAILED outcomes, so the
// evidence of a corruption is never discarded through the error channel.
// Neither refusal path produces an AssessmentID.
func Assess(request AssessmentRequest) (AssessmentOutcome, error) {
	// The known set is cloned before any comparison so caller-held slices
	// cannot alias internal values or change conflict evaluation later.
	known := cloneAssessments(request.KnownAssessments)

	run := request.Checkpoint.semanticRunID
	if _, err := decodeDigest(string(run)); err != nil || len(request.Checkpoint.canonical) == 0 {
		return AssessmentOutcome{}, fmt.Errorf("assess checkpoint is not an established sealed artifact")
	}
	if err := validateAssessableProfile(request.Profile); err != nil {
		return AssessmentOutcome{}, fmt.Errorf("assess profile: %w", err)
	}

	// Checkpoint artifact content identity: rebuild both the claim tuple and
	// the complete manifest and compare, exactly like verifyPlan/verifyState.
	claim, err := encodeCheckpointArtifactID(request.Checkpoint.semanticRunID, request.Checkpoint.checkpointID,
		request.Checkpoint.stateDigest, request.Checkpoint.journalPrefixDigest,
		request.Checkpoint.invariantResultDigest, request.Checkpoint.policyID)
	if err != nil {
		return AssessmentOutcome{}, fmt.Errorf("assess checkpoint claim: %w", err)
	}
	manifest, err := encodeCheckpointArtifact(request.Checkpoint)
	if err != nil {
		return AssessmentOutcome{}, fmt.Errorf("assess checkpoint manifest: %w", err)
	}
	if !bytes.Equal(manifest, request.Checkpoint.canonical) ||
		CheckpointArtifactDigest(canonicalDigest(request.Checkpoint.canonical)) != request.Checkpoint.digest {
		content := canonicalDigest(request.Checkpoint.canonical)
		expected, observed := checkpointDigestEvidence(Digest(request.Checkpoint.digest), request.Checkpoint.canonical, manifest)
		return assessIntegrityFailure(run, ArtifactDigestMismatch, ArtifactCheckpoint, content, nil, nil, expected, observed)
	}
	if CheckpointArtifactID(canonicalDigest(claim)) != request.Checkpoint.id {
		content := canonicalDigest(request.Checkpoint.canonical)
		expected := canonicalDigest(claim)
		observed := optionalValidDigest(Digest(request.Checkpoint.id))
		return assessIntegrityFailure(run, ArtifactDigestMismatch, ArtifactCheckpoint, content, nil, nil, &expected, observed)
	}
	// The checkpoint is now the verified frontier for later failure evidence.
	verifiedCheckpoint := request.Checkpoint.id

	if err := verifyState(request.State); err != nil {
		rebuilt, rebuildErr := encodeState(request.State)
		if rebuildErr != nil {
			return AssessmentOutcome{}, fmt.Errorf("assess state: %w", rebuildErr)
		}
		content := canonicalDigest(request.State.canonical)
		expected, observed := checkpointDigestEvidence(Digest(request.State.digest), request.State.canonical, rebuilt)
		return assessIntegrityFailure(run, ArtifactDigestMismatch, ArtifactState, content, nil, &verifiedCheckpoint, expected, observed)
	}
	if err := verifyCompiledProfileIdentity(request.Profile); err != nil {
		rebuilt, rebuildErr := encodeCompiledProfile(request.Profile)
		if rebuildErr != nil {
			return AssessmentOutcome{}, fmt.Errorf("assess profile: %w", rebuildErr)
		}
		content := canonicalDigest(request.Profile.canonical)
		expected, observed := checkpointDigestEvidence(Digest(request.Profile.id), request.Profile.canonical, rebuilt)
		return assessIntegrityFailure(run, ArtifactDigestMismatch, ArtifactCompiledProfile, content, nil, &verifiedCheckpoint, expected, observed)
	}

	// Link: the supplied state must be the exact state the checkpoint sealed.
	// The sealed claim is authoritative, so it provides the expected digest.
	if request.State.Digest() != request.Checkpoint.stateDigest {
		expected, observed := Digest(request.Checkpoint.stateDigest), Digest(request.State.digest)
		return assessIntegrityFailure(run, ArtifactLinkInconsistent, ArtifactState, observed, nil, &verifiedCheckpoint, &expected, &observed)
	}
	// Link: the profile must pin the schema the assessed state actually uses;
	// the checkpoint/state pair is already verified, so the profile is the
	// implicated artifact when the schema links disagree.
	if request.State.Schema().Digest() != request.Profile.schemaDigest {
		content := canonicalDigest(request.Profile.canonical)
		expected, observed := Digest(request.State.Schema().Digest()), Digest(request.Profile.schemaDigest)
		return assessIntegrityFailure(run, ArtifactLinkInconsistent, ArtifactCompiledProfile, content, nil, &verifiedCheckpoint, &expected, &observed)
	}

	verdict, entities := evaluateProfileOverState(request.State, request.Profile)
	assessment := Assessment{
		checkpointArtifactID: request.Checkpoint.id,
		profileID:            request.Profile.id,
		verdict:              verdict,
		entities:             entities,
	}
	idBytes, err := encodeAssessmentID(assessment.checkpointArtifactID, assessment.profileID)
	if err != nil {
		return AssessmentOutcome{}, fmt.Errorf("assess identity: %w", err)
	}
	assessment.id = AssessmentID(canonicalDigest(idBytes))
	canonical, err := encodeAssessment(assessment)
	if err != nil {
		return AssessmentOutcome{}, fmt.Errorf("canonicalize assessment: %w", err)
	}
	assessment.canonical = canonical
	assessment.digest = AssessmentDigest(canonicalDigest(canonical))

	if conflict := conflictingKnownAssessment(known, assessment); conflict != nil {
		lastState := request.State.Digest()
		return assessmentConflictFailure(run, lastState, verifiedCheckpoint, *conflict, assessment)
	}
	result := cloneAssessment(assessment)
	return AssessmentOutcome{assessment: &result}, nil
}

// evaluateProfileOverState selects every entity of the compiled scope kind in
// the state's canonical (kind, EntityID) order and evaluates every normalized
// requirement atom for every selected entity, retaining satisfied and missing
// results alike. Aggregation is universal: the verdict is Ready only when
// every selected entity satisfies every atom.
//
// An empty explicit selection is vacuously Ready: with universal ("all
// selected") aggregation there is no selected entity whose requirement can be
// missing. This documented behavior cannot mask the fixture's missing team
// because the protected T1 plan boundary requires a formed team before C1
// seals.
func evaluateProfileOverState(state State, profile CompiledProfile) (ReadinessVerdict, []EntityAssessment) {
	scopeKind := profile.declaration.Scope.EntityKind
	verdict := Ready
	entities := make([]EntityAssessment, 0)
	for _, entity := range state.entities {
		if entity.ref.Kind != scopeKind {
			continue
		}
		results := make([]RequirementResult, 0, len(profile.declaration.Requirements))
		for _, requirement := range profile.declaration.Requirements {
			// Requirement order is the compiled profile's normalized order;
			// no re-sorting happens here so evaluation order equals the
			// canonical encoded order.
			_, fieldName := splitFieldPath(requirement.Field)
			result := RequirementResult{code: requirement.Code, result: RequirementSatisfied}
			if _, present := entity.fields[fieldName]; present {
				// A single field-presence atom yields exactly one fact
				// reference, so the evidence list is trivially sorted.
				result.facts = []FactRef{{entity: entity.ref, field: fieldName}}
			} else {
				result.result = RequirementMissing
				verdict = NeedsInput
			}
			results = append(results, result)
		}
		entities = append(entities, EntityAssessment{entity: entity.ref, results: results})
	}
	return verdict, entities
}

// validateAssessableProfile rejects, as Go errors, profile values that could
// not have come from a successful compilation: this package's compiler is the
// only producer of CompiledProfile, so an unsupported scope, aggregation, or
// atom shape means the value is malformed rather than a corrupt established
// artifact.
func validateAssessableProfile(profile CompiledProfile) error {
	if len(profile.canonical) == 0 {
		return fmt.Errorf("compiled profile is not initialized")
	}
	declaration := profile.declaration
	if declaration.Scope.Kind != AllEntitiesOfKind || !validSemanticName(string(declaration.Scope.EntityKind)) {
		return fmt.Errorf("unsupported profile scope")
	}
	if declaration.Aggregation != AllSelected {
		return fmt.Errorf("unsupported profile aggregation %d", declaration.Aggregation)
	}
	for _, requirement := range declaration.Requirements {
		if requirement.Kind != FieldPresent || !validRequirementCode(requirement.Code) {
			return fmt.Errorf("unsupported requirement atom for code %q", requirement.Code)
		}
		kind, name := splitFieldPath(requirement.Field)
		if kind != declaration.Scope.EntityKind || !validSemanticName(string(name)) {
			return fmt.Errorf("requirement field %q is outside the profile scope", requirement.Field)
		}
	}
	return nil
}

// verifyCompiledProfileIdentity rebuilds the canonical compiled-profile bytes
// and compares content and cached identity, like verifyPlan/verifyState.
func verifyCompiledProfileIdentity(profile CompiledProfile) error {
	canonical, err := encodeCompiledProfile(profile)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, profile.canonical) || ProfileID(canonicalDigest(canonical)) != profile.id {
		return fmt.Errorf("compiled profile canonical identity mismatch")
	}
	return nil
}

// conflictingKnownAssessment reports a defensively copied known assessment
// whose identity equals the candidate's but whose content digest differs. One
// AssessmentID resolving to two different digests is an integrity failure,
// never a second valid answer.
func conflictingKnownAssessment(known []Assessment, candidate Assessment) *Assessment {
	for _, assessment := range known {
		if assessment.id == candidate.id && assessment.digest != candidate.digest {
			conflict := cloneAssessment(assessment)
			return &conflict
		}
	}
	return nil
}

func assessmentConflictFailure(run SemanticRunID, lastState StateDigest, lastCheckpoint CheckpointArtifactID, known, candidate Assessment) (AssessmentOutcome, error) {
	expected, observed := Digest(known.digest), Digest(candidate.digest)
	references := []ArtifactRef{
		{kind: ArtifactReadinessAssessment, digest: expected},
		{kind: ArtifactReadinessAssessment, digest: observed},
	}
	report, err := newArtifactIntegrityFailure(run, AssessmentIdentityConflict,
		ArtifactRef{kind: ArtifactReadinessAssessment, digest: observed}, &lastState, &lastCheckpoint,
		&expected, &observed, references)
	if err != nil {
		return AssessmentOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return AssessmentOutcome{failure: &failure}, nil
}

func assessIntegrityFailure(run SemanticRunID, code IntegrityCode, kind ArtifactKind, content Digest, lastState *StateDigest, lastCheckpoint *CheckpointArtifactID, expected, observed *Digest) (AssessmentOutcome, error) {
	artifact := ArtifactRef{kind: kind, digest: content}
	report, err := newArtifactIntegrityFailure(run, code, artifact, lastState, lastCheckpoint, expected, observed, []ArtifactRef{artifact})
	if err != nil {
		return AssessmentOutcome{}, err
	}
	failure := FailureReport{integrity: &report}
	return AssessmentOutcome{failure: &failure}, nil
}

func cloneRequirementResults(input []RequirementResult) []RequirementResult {
	result := make([]RequirementResult, len(input))
	for i, item := range input {
		result[i] = item
		result[i].facts = slices.Clone(item.facts)
	}
	return result
}

func cloneEntityAssessments(input []EntityAssessment) []EntityAssessment {
	result := make([]EntityAssessment, len(input))
	for i, item := range input {
		result[i] = EntityAssessment{entity: item.entity, results: cloneRequirementResults(item.results)}
	}
	return result
}

func cloneAssessment(input Assessment) Assessment {
	input.entities = cloneEntityAssessments(input.entities)
	input.canonical = bytes.Clone(input.canonical)
	return input
}

func cloneAssessments(input []Assessment) []Assessment {
	result := make([]Assessment, len(input))
	for i, assessment := range input {
		result[i] = cloneAssessment(assessment)
	}
	return result
}
