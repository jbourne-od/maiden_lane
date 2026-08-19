package promotion

import (
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ComparedCase is one corpus case's evidence from one side of a comparison.
//
// The artifacts are authenticated by their types, as everywhere else in this package:
// only Seal and Assess produce them, so holding one is holding something the kernel
// verified. What this package establishes is that the right ones were supplied.
type ComparedCase struct {
	// Checkpoint is the sealed checkpoint this side produced for this case.
	Checkpoint semantic.CheckpointArtifact

	// Assessment is the readiness answer taken against that checkpoint under the
	// comparison's profile.
	Assessment semantic.Assessment
}

// ComparisonEvidence is what one promotion comparison was answered with.
//
// It is the evidence, and the Comparison it answers is the question — the distinction
// this programme has been careful to keep. The per-case artifacts are here and are
// deliberately absent from ComparisonID, so re-evidencing a comparison later cannot turn
// it into a different comparison.
//
// The cases are ordered, and the order is load-bearing rather than cosmetic: coverage is
// established by re-deriving the corpus identity from the case digests, and a corpus's
// canonical order is part of what its identity commits to.
type ComparisonEvidence struct {
	Comparison semantic.Comparison
	Baseline   []ComparedCase
	Candidate  []ComparedCase
}

// comparisonCorpus establishes HLD §14.1's "baseline and candidate checkpoint executions
// over the same replay corpus, corresponding checkpoint semantics, and completeness
// profile".
//
// It is a comparability precondition, not a judgment. Nothing here asks whether the
// candidate is better, or even whether the two sides differ: that is the metric
// regression clause, which needs a concept this build does not have. What this
// establishes is that a comparison between these two things is MEANINGFUL — and a
// comparison whose comparability cannot be established is worthless whatever its numbers
// eventually say.
//
// Every check is on the authenticated artifacts rather than on anything describing them.
// In particular the world is checked per artifact, not taken from the request that
// produced the evidence: a side run reporting the right world is a projection, and no
// projection may carry authorization weight.
func comparisonCorpus(candidate Candidate, evidence *ComparisonEvidence) ClauseResult {
	if !candidate.sealed() || evidence == nil {
		return Unestablished(ClauseComparisonCorpus)
	}
	comparison := evidence.Comparison
	if comparison.ID() == "" {
		return Unestablished(ClauseComparisonCorpus)
	}

	// The checkpoint being promoted must BE the candidate side of the comparison.
	// Without this, evidence from a comparison of two entirely different checkpoint
	// declarations would satisfy the clause for this one.
	if candidate.Checkpoint.CheckpointID() != comparison.Candidate() {
		return Failed(ClauseComparisonCorpus)
	}

	// Both sides must cover the corpus the comparison names. Content-addressing makes
	// this provable rather than asserted: the case digests a side actually produced
	// cannot add up to a corpus it did not run.
	for _, side := range []struct {
		cases      []ComparedCase
		checkpoint semantic.CheckpointID
	}{
		{evidence.Baseline, comparison.Baseline()},
		{evidence.Candidate, comparison.Candidate()},
	} {
		if len(side.cases) == 0 {
			// A side with no evidence has not been established, which is different from
			// a side whose evidence is wrong.
			return Unestablished(ClauseComparisonCorpus)
		}
		if result, ok := sideCovers(side.cases, side.checkpoint, comparison); !ok {
			return result
		}
	}
	return Passed(ClauseComparisonCorpus)
}

// sideCovers establishes that one side's evidence answers the comparison's question.
//
// It returns the clause result to report when it does not, because the two ways a side
// can be wrong call for different answers: missing evidence is unevaluated, while evidence
// that contradicts the comparison is a definite adverse finding about what was supplied.
func sideCovers(
	cases []ComparedCase, checkpoint semantic.CheckpointID, comparison semantic.Comparison,
) (ClauseResult, bool) {
	digests := make([]semantic.StateDigest, 0, len(cases))
	for _, compared := range cases {
		artifact, assessment := compared.Checkpoint, compared.Assessment
		if artifact.ID() == "" || assessment.ID() == "" {
			return Unestablished(ClauseComparisonCorpus), false
		}

		// The artifact must be a realization of the checkpoint declaration this side of
		// the comparison names. A side that ran the right corpus under the wrong
		// checkpoint has answered a different question.
		if artifact.CheckpointID() != checkpoint {
			return Failed(ClauseComparisonCorpus), false
		}

		// The world is checked on the ARTIFACT, which is the whole point of checking it
		// here at all. §14.2 pins WorldID into the comparison question, and a side run
		// reporting the right world is a projection: only the sealed artifact carries the
		// world the execution actually pinned.
		if artifact.WorldID() != comparison.World() {
			return Failed(ClauseComparisonCorpus), false
		}

		// The assessment must be bound to this artifact and taken under the comparison's
		// profile. §14.2 is explicit that comparing an optimizer-ready baseline to a
		// merely CM-ready candidate cannot support promotion, and one ProfileID in the
		// comparison identity is what prevents it — this is where that is enforced
		// against the evidence rather than left as a property of the identity.
		if assessment.CheckpointArtifactID() != artifact.ID() {
			return Failed(ClauseComparisonCorpus), false
		}
		if assessment.ProfileID() != comparison.Profile() {
			return Failed(ClauseComparisonCorpus), false
		}

		// InitialStateDigest, not StateDigest. A checkpoint's StateDigest is the state at
		// the checkpoint BOUNDARY — after the transitions before it committed — while a
		// corpus is a set of INITIAL states. The two coincide only for a checkpoint that
		// follows no committed transition, so using StateDigest would make coverage
		// verify for a first checkpoint and fail for every later one, which is the worst
		// kind of wrong: correct in the simplest test and silently broken in use.
		digests = append(digests, artifact.InitialStateDigest())
	}

	// Coverage last, because it is the expensive claim and the checks above establish
	// that these digests come from artifacts that belong to this comparison at all.
	if !semantic.VerifyCorpusIdentity(digests, comparison.Corpus()) {
		return Failed(ClauseComparisonCorpus), false
	}
	return ClauseResult{}, true
}
