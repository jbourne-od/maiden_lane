package promotion

import (
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// noMetricRegression establishes HLD §14.1's "no regression against comparison policy
// metrics".
//
// It evaluates the candidate against the baseline across the replay corpus cases.
// For promotion to proceed, the candidate must satisfy:
//  1. Target Profile Alignment: The comparison and all case assessments must be evaluated
//     under the target's required completeness profile.
//  2. Exact Case Alignment: Candidate and baseline cases are matched by initial state digest.
//  3. Zero Invariant Failures: Candidate cases must hold verified invariant result digests.
//  4. Per-Case Readiness Non-Regression: For every corpus case where the baseline achieved
//     `Ready` under the required profile, the candidate must also achieve `Ready`.
//  5. Aggregate Readiness Non-Regression: Total ready cases in candidate >= total ready cases
//     in baseline.
//  6. Complete Corpus Coverage: Baseline and candidate cases must be non-empty and equal in count.
//
// Missing comparison evidence reports Unestablished.
// Any metric regression, profile mismatch, or corpus case mismatch reports Failed.
func noMetricRegression(
	policy ports.TargetPolicy, candidate Candidate, evidence *ComparisonEvidence,
) ClauseResult {
	if !candidate.sealed() || evidence == nil {
		return Unestablished(ClauseNoMetricRegression)
	}

	comparison := evidence.Comparison
	if comparison.ID() == "" || policy.RequiredProfileID == "" {
		return Unestablished(ClauseNoMetricRegression)
	}

	// The comparison must be identified under the profile the target requires.
	if comparison.Profile() != policy.RequiredProfileID {
		return Unestablished(ClauseNoMetricRegression)
	}

	if len(evidence.Baseline) == 0 || len(evidence.Candidate) == 0 {
		return Unestablished(ClauseNoMetricRegression)
	}

	if len(evidence.Baseline) != len(evidence.Candidate) {
		return Failed(ClauseNoMetricRegression)
	}

	// Index baseline cases by InitialStateDigest to guarantee aligned case-by-case comparison.
	baselineByInitialState := make(map[semantic.StateDigest]ComparedCase, len(evidence.Baseline))
	baselineReadyCount := 0

	for _, bCase := range evidence.Baseline {
		if bCase.Checkpoint.ID() == "" || bCase.Assessment.ID() == "" {
			return Unestablished(ClauseNoMetricRegression)
		}
		if bCase.Assessment.ProfileID() != policy.RequiredProfileID {
			return Failed(ClauseNoMetricRegression)
		}
		if bCase.Checkpoint.InvariantResultDigest() == "" {
			return Unestablished(ClauseNoMetricRegression)
		}
		if bCase.Assessment.Verdict() == semantic.Ready {
			baselineReadyCount++
		}
		baselineByInitialState[bCase.Checkpoint.InitialStateDigest()] = bCase
	}

	candidateReadyCount := 0

	for _, cCase := range evidence.Candidate {
		if cCase.Checkpoint.ID() == "" || cCase.Assessment.ID() == "" {
			return Unestablished(ClauseNoMetricRegression)
		}
		if cCase.Assessment.ProfileID() != policy.RequiredProfileID {
			return Failed(ClauseNoMetricRegression)
		}
		if cCase.Checkpoint.InvariantResultDigest() == "" {
			return Unestablished(ClauseNoMetricRegression)
		}

		bCase, existsInBaseline := baselineByInitialState[cCase.Checkpoint.InitialStateDigest()]
		if !existsInBaseline {
			// Candidate case does not match any baseline case in the corpus
			return Failed(ClauseNoMetricRegression)
		}

		bReady := bCase.Assessment.Verdict() == semantic.Ready
		cReady := cCase.Assessment.Verdict() == semantic.Ready

		if cReady {
			candidateReadyCount++
		}

		// Per-case non-regression: a case that was ready in baseline cannot regress to needs_input in candidate.
		if bReady && !cReady {
			return Failed(ClauseNoMetricRegression)
		}
	}

	if candidateReadyCount < baselineReadyCount {
		return Failed(ClauseNoMetricRegression)
	}

	return Passed(ClauseNoMetricRegression)
}
