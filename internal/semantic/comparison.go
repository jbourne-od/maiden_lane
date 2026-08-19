package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// CheckpointPair is one authored statement that two checkpoint declarations mean the
// same thing in their respective plans.
//
// Keys rather than identities, because this is the authoring form: somebody writing a
// correspondence knows that `team_hos_aggregated.v1` in the old plan is
// `team_hos_reconciled.v2` in the new one. The identities are derived from the plans by
// NewComparisonPolicy, so a pair naming a checkpoint that does not exist is refused
// rather than recorded.
type CheckpointPair struct {
	Baseline  CheckpointKey
	Candidate CheckpointKey
}

// CheckpointCorrespondence is one derived, plan-bound correspondence.
type CheckpointCorrespondence struct {
	baseline  CheckpointID
	candidate CheckpointID
}

// Baseline and Candidate return the two corresponding checkpoint identities.
func (c CheckpointCorrespondence) Baseline() CheckpointID  { return c.baseline }
func (c CheckpointCorrespondence) Candidate() CheckpointID { return c.candidate }

// ComparisonPolicy is the explicit, immutable statement of which checkpoint
// declarations of two plans correspond.
//
// HLD §14.2 requires exactly this and forbids the obvious shortcut: "Plans under
// comparison may have different PlanID values, but the comparison contract must
// explicitly map semantically corresponding checkpoint declarations and fail closed when
// no correspondence exists."
//
// Matching by name is what that forbids, and the reason is worth stating because the
// shortcut looks harmless. Two plans may legitimately name the same semantics
// differently, which name matching would miss; far worse, they may name DIFFERENT
// semantics the same, which name matching would silently accept. A comparison built on
// that would be right most of the time, and the times it was wrong would be
// indistinguishable from the times it was right.
//
// Correspondence is derived against the plans rather than asserted about them. A pair
// naming a checkpoint neither plan declares cannot become a policy, so a correspondence
// cannot outlive the declarations it describes.
type ComparisonPolicy struct {
	baselinePlan    PlanID
	candidatePlan   PlanID
	correspondences []CheckpointCorrespondence
	canonical       []byte
	id              ComparisonPolicyID
}

// NewComparisonPolicy validates an authored correspondence against the two plans it
// describes, and identifies it.
//
// The mapping must be one-to-one in both directions. Two baseline checkpoints
// corresponding to one candidate — or one baseline to two candidates — makes
// "corresponding" ambiguous, and a comparison cannot fail closed on an ambiguity it
// silently resolved by picking the first match.
func NewComparisonPolicy(baseline, candidate Plan, pairs []CheckpointPair) (ComparisonPolicy, error) {
	if baseline.ID() == "" || candidate.ID() == "" {
		return ComparisonPolicy{}, fmt.Errorf("comparison policy requires two compiled plans")
	}
	if len(pairs) == 0 {
		// A contract mapping nothing corresponds nothing. Admitting it would let a
		// comparison be authored under a policy that can never supply the
		// correspondence it will be asked for, which is a refusal deferred rather than
		// avoided.
		return ComparisonPolicy{}, fmt.Errorf("comparison policy maps no checkpoints")
	}

	declared := func(plan Plan, key CheckpointKey) bool {
		for _, checkpoint := range plan.Checkpoints() {
			if checkpoint.Key == key {
				return true
			}
		}
		return false
	}

	correspondences := make([]CheckpointCorrespondence, 0, len(pairs))
	for _, pair := range pairs {
		if !declared(baseline, pair.Baseline) {
			return ComparisonPolicy{}, fmt.Errorf(
				"comparison policy maps a checkpoint the baseline plan does not declare")
		}
		if !declared(candidate, pair.Candidate) {
			return ComparisonPolicy{}, fmt.Errorf(
				"comparison policy maps a checkpoint the candidate plan does not declare")
		}
		baselineID, err := checkpointIdentity(baseline.ID(), pair.Baseline)
		if err != nil {
			return ComparisonPolicy{}, err
		}
		candidateID, err := checkpointIdentity(candidate.ID(), pair.Candidate)
		if err != nil {
			return ComparisonPolicy{}, err
		}
		correspondences = append(correspondences,
			CheckpointCorrespondence{baseline: baselineID, candidate: candidateID})
	}

	// Canonical order, so an authored correspondence has one identity regardless of the
	// order somebody wrote the rows in.
	sort.Slice(correspondences, func(i, j int) bool {
		if correspondences[i].baseline != correspondences[j].baseline {
			return correspondences[i].baseline < correspondences[j].baseline
		}
		return correspondences[i].candidate < correspondences[j].candidate
	})

	seenBaseline := make(map[CheckpointID]bool, len(correspondences))
	seenCandidate := make(map[CheckpointID]bool, len(correspondences))
	for _, correspondence := range correspondences {
		if seenBaseline[correspondence.baseline] {
			return ComparisonPolicy{}, fmt.Errorf(
				"comparison policy maps one baseline checkpoint to more than one candidate")
		}
		if seenCandidate[correspondence.candidate] {
			return ComparisonPolicy{}, fmt.Errorf(
				"comparison policy maps more than one baseline checkpoint to one candidate")
		}
		seenBaseline[correspondence.baseline] = true
		seenCandidate[correspondence.candidate] = true
	}

	canonical, err := comparisonPolicyCanonicalBytes(baseline.ID(), candidate.ID(), correspondences)
	if err != nil {
		return ComparisonPolicy{}, fmt.Errorf("canonicalize comparison policy: %w", err)
	}

	return ComparisonPolicy{
		baselinePlan:    baseline.ID(),
		candidatePlan:   candidate.ID(),
		correspondences: correspondences,
		canonical:       canonical,
		id:              ComparisonPolicyID(canonicalDigest(canonical)),
	}, nil
}

// ID returns the content identity of the canonical correspondence.
func (p ComparisonPolicy) ID() ComparisonPolicyID { return p.id }

// BaselinePlan and CandidatePlan return the two plans this correspondence describes.
//
// They are carried explicitly even though each CheckpointID commits to its plan, because
// a CheckpointID is a digest: it proves which plan a checkpoint belongs to without
// saying which plan that is. A caller needs to know before it can decide whether this
// policy describes the comparison it is holding.
func (p ComparisonPolicy) BaselinePlan() PlanID  { return p.baselinePlan }
func (p ComparisonPolicy) CandidatePlan() PlanID { return p.candidatePlan }

// Correspondences returns the canonically ordered correspondence set.
func (p ComparisonPolicy) Correspondences() []CheckpointCorrespondence {
	return slices.Clone(p.correspondences)
}

// CanonicalBytes returns a copy of the v1 policy bytes.
func (p ComparisonPolicy) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// CandidateFor reports which candidate checkpoint corresponds to a baseline one, or that
// no correspondence was declared.
//
// The absent case is the one §14.2 exists to produce: a checkpoint with no counterpart is
// not comparable, and reporting that as a refusal rather than as an empty result is the
// difference between "these do not correspond" and "these correspond to nothing", which
// are opposite claims.
func (p ComparisonPolicy) CandidateFor(baseline CheckpointID) (CheckpointID, bool) {
	for _, correspondence := range p.correspondences {
		if correspondence.baseline == baseline {
			return correspondence.candidate, true
		}
	}
	return "", false
}

// Corresponds reports whether the policy declares these two checkpoints to correspond.
//
// This is the question a comparison actually asks, and it is deliberately not answered by
// two lookups at the call site: checking only that the baseline maps to something would
// accept a comparison whose candidate side is a checkpoint the contract never mentioned.
func (p ComparisonPolicy) Corresponds(baseline, candidate CheckpointID) bool {
	mapped, found := p.CandidateFor(baseline)
	return found && mapped == candidate
}

// ComparisonRequest names one promotion comparison, per HLD §14.2's
// Compare(C_baseline, C_candidate, ProfileID, WorldID, CorpusID) plus the comparison
// policy that section requires to participate in the identity.
type ComparisonRequest struct {
	// Baseline and Candidate are checkpoint DECLARATIONS, not realized artifacts.
	//
	// This is the crux of what a corpus comparison is. A comparison over n cases has no
	// single artifact per side; it has n realizations of each. CheckpointID identifies a
	// declaration within its plan, while CheckpointArtifactID identifies one checkpoint
	// of one run, so the thing being compared is the semantics evaluated across the
	// corpus — exactly §14.2's phrase "corresponding checkpoint semantics".
	Baseline  CheckpointID
	Candidate CheckpointID

	Profile ProfileID
	World   WorldID
	Corpus  CorpusID
	Policy  ComparisonPolicy
}

// Comparison identifies one promotion comparison. It is the QUESTION, not its answer and
// not the evidence that answered it.
//
// The per-case ExecutionIDs and CheckpointArtifactIDs that eventually answer a comparison
// are deliberately absent from its identity. Folding them in for auditability would
// collapse question and evidence — the same category error as storing a gate verdict
// beside the artifacts it summarizes — and would make a comparison's identity change
// every time it was re-evidenced, so the same question asked twice would be two
// questions.
type Comparison struct {
	request   ComparisonRequest
	canonical []byte
	id        ComparisonID
}

// NewComparison validates and identifies one comparison.
//
// Every input is required. §14.2 names five and adds the comparison policy, and each is
// load-bearing: a comparison missing its profile compares readiness against nothing, one
// missing its world or corpus compares over an unstated set of inputs, and one missing
// its policy asserts a correspondence nobody declared.
func NewComparison(request ComparisonRequest) (Comparison, error) {
	switch {
	case request.Baseline == "":
		return Comparison{}, fmt.Errorf("comparison has no baseline checkpoint")
	case request.Candidate == "":
		return Comparison{}, fmt.Errorf("comparison has no candidate checkpoint")
	case request.Profile == "":
		return Comparison{}, fmt.Errorf("comparison has no completeness profile")
	case request.World == "":
		return Comparison{}, fmt.Errorf("comparison has no pinned world")
	case request.Corpus == "":
		return Comparison{}, fmt.Errorf("comparison has no replay corpus")
	case request.Policy.ID() == "":
		return Comparison{}, fmt.Errorf("comparison has no comparison policy")
	}

	// FAIL CLOSED. §14.2 requires refusal when no correspondence exists, and this is
	// where it happens: a comparison whose two sides the policy does not declare to
	// correspond cannot be identified at all, so no downstream code can be handed one and
	// left to notice. Checking at evaluation instead would make the refusal a behaviour
	// somebody has to remember rather than a state that cannot be built.
	if !request.Policy.Corresponds(request.Baseline, request.Candidate) {
		return Comparison{}, fmt.Errorf(
			"comparison policy declares no correspondence between these checkpoints")
	}

	canonical, err := comparisonCanonicalBytes(
		request.Baseline, request.Candidate, request.Profile,
		request.World, request.Corpus, request.Policy.ID())
	if err != nil {
		return Comparison{}, fmt.Errorf("canonicalize comparison: %w", err)
	}

	return Comparison{
		request:   request,
		canonical: canonical,
		id:        ComparisonID(canonicalDigest(canonical)),
	}, nil
}

// ID returns the content identity of the comparison question.
func (c Comparison) ID() ComparisonID { return c.id }

// Baseline and Candidate return the corresponding checkpoint declarations compared.
func (c Comparison) Baseline() CheckpointID  { return c.request.Baseline }
func (c Comparison) Candidate() CheckpointID { return c.request.Candidate }

// Profile, World, and Corpus return the pinned inputs the comparison is meaningful under.
func (c Comparison) Profile() ProfileID { return c.request.Profile }
func (c Comparison) World() WorldID     { return c.request.World }
func (c Comparison) Corpus() CorpusID   { return c.request.Corpus }

// Policy returns the correspondence the comparison was identified under.
func (c Comparison) Policy() ComparisonPolicy { return c.request.Policy }

// CanonicalBytes returns a copy of the v1 comparison bytes.
func (c Comparison) CanonicalBytes() []byte { return bytes.Clone(c.canonical) }

// checkpointIdentity derives a checkpoint declaration's identity within its plan.
func checkpointIdentity(plan PlanID, key CheckpointKey) (CheckpointID, error) {
	encoded, err := encodeCheckpointID(plan, key)
	if err != nil {
		return "", fmt.Errorf("checkpoint identity: %w", err)
	}
	return CheckpointID(canonicalDigest(encoded)), nil
}

// comparisonPolicyCanonicalBytes encodes the v1 comparison-policy tuple.
//
// It is a separate function from the constructor so a golden vector can pin the exact
// bytes. That matters more here than a behavioural test can express: each
// correspondence's CheckpointID already commits to its plan, so a black-box test cannot
// prove the plan identities are literally present, and the one-to-one invariant means a
// valid fixture cannot vary one side of a correspondence independently of the other. The
// state space genuinely cannot distinguish some omissions from this tuple, so the
// representation is pinned directly instead.
func comparisonPolicyCanonicalBytes(
	baselinePlan, candidatePlan PlanID, correspondences []CheckpointCorrespondence,
) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(comparisonPolicyDomainTag)
	encoder.digest(string(baselinePlan))
	encoder.digest(string(candidatePlan))
	encoder.uint64(uint64(len(correspondences)))
	for _, correspondence := range correspondences {
		encoder.digest(string(correspondence.baseline))
		encoder.digest(string(correspondence.candidate))
	}
	return encoder.bytes()
}

// comparisonCanonicalBytes encodes the v1 comparison tuple: HLD §14.2's five inputs plus
// the comparison policy that section requires to participate.
//
// Separate from the constructor for the same reason as above, and here the need is
// sharper. A one-to-one correspondence makes Candidate functionally determined by
// Baseline for a fixed policy, so no valid comparison can vary one without the other, and
// a behavioural test can only establish that at least one of the pair participates —
// never that each does. A golden vector establishes it directly.
func comparisonCanonicalBytes(
	baseline, candidate CheckpointID, profile ProfileID,
	world WorldID, corpus CorpusID, policy ComparisonPolicyID,
) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(comparisonIDDomainTag)
	encoder.digest(string(baseline))
	encoder.digest(string(candidate))
	encoder.digest(string(profile))
	encoder.digest(string(world))
	encoder.digest(string(corpus))
	encoder.digest(string(policy))
	return encoder.bytes()
}
