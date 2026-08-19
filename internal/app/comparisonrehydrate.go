package app

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

const (
	// IntegrityComparisonPlanAbsent means a stored comparison names a plan the store
	// does not have. A comparison policy is derived from two compiled plans, so without
	// them nothing can be re-derived and the question cannot be recovered at all.
	IntegrityComparisonPlanAbsent IntegrityCode = "COMPARISON_PLAN_ABSENT"

	// IntegrityComparisonCheckpointAbsent means a stored correspondence names a
	// checkpoint identity that its plan does not declare.
	//
	// This is what catches a row that has been edited to correspond checkpoints the
	// plans never contained. Such a row would otherwise rebuild into a policy that
	// looks entirely well formed while asserting a correspondence between declarations
	// nothing can realize.
	IntegrityComparisonCheckpointAbsent IntegrityCode = "COMPARISON_CHECKPOINT_ABSENT"

	// IntegrityComparisonDiverged means the stored components no longer derive the
	// identity the comparison is filed under.
	IntegrityComparisonDiverged IntegrityCode = "STORED_COMPARISON_DIVERGED"
)

// ComparisonRehydrationStores is what comparison rehydration reads, and nothing else.
// It writes nothing.
type ComparisonRehydrationStores struct {
	Plans       ports.PlanStore
	Comparisons ports.ComparisonStore
}

// RehydrateComparison recovers an authenticated comparison from what a store holds.
//
// A semantic.Comparison cannot be read. Its policy is derived from two compiled plans
// and the kernel's encoders are one way, so what storage returns is a projection and
// this is what turns one back into a kernel value: both plans are loaded and recompiled,
// each stored correspondence is matched back to the checkpoint declarations that produce
// it, the policy and the comparison are re-derived through their ordinary constructors,
// and the resulting identity is required to equal the one the row is filed under.
//
// That final equality is the whole guarantee. Every intermediate value here comes from
// storage and none of it is trusted; what makes the returned comparison authentic is
// that the kernel produced it from components which, taken together, reproduce the name
// it was stored under. A row edited in any of those components derives a different
// identity and is refused rather than returned.
//
// Absence is reported as absence, not as an error: a comparison nobody created is an
// ordinary answer to a lookup.
func RehydrateComparison(
	ctx context.Context, stores ComparisonRehydrationStores,
	tenant ports.TenantID, comparisonID semantic.ComparisonID,
) (semantic.Comparison, bool, error) {
	record, found, err := stores.Comparisons.GetComparison(ctx, tenant, comparisonID)
	if err != nil || !found {
		return semantic.Comparison{}, false, err
	}

	baselinePlan, err := compiledPlanFor(ctx, stores.Plans, tenant, record.BaselinePlan)
	if err != nil {
		return semantic.Comparison{}, false, err
	}
	candidatePlan, err := compiledPlanFor(ctx, stores.Plans, tenant, record.CandidatePlan)
	if err != nil {
		return semantic.Comparison{}, false, err
	}

	pairs, err := authoredPairs(baselinePlan, candidatePlan, record.Correspondences)
	if err != nil {
		return semantic.Comparison{}, false, err
	}

	policy, err := semantic.NewComparisonPolicy(baselinePlan, candidatePlan, pairs)
	if err != nil {
		// The stored correspondences do not form a policy the kernel will build. A
		// one-to-many mapping edited into the row lands here rather than being resolved
		// by picking a first match.
		return semantic.Comparison{}, false, IntegrityError{
			Code: IntegrityComparisonDiverged, Detail: "correspondences do not form a policy",
		}
	}

	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  record.Baseline,
		Candidate: record.Candidate,
		Profile:   record.Profile,
		World:     record.World,
		Corpus:    record.Corpus,
		Policy:    policy,
	})
	if err != nil {
		return semantic.Comparison{}, false, IntegrityError{
			Code: IntegrityComparisonDiverged, Detail: "components do not form a comparison",
		}
	}
	if comparison.ID() != comparisonID {
		return semantic.Comparison{}, false, IntegrityError{
			Code: IntegrityComparisonDiverged, Detail: "re-derived identity differs",
		}
	}
	// The projected policy identity is checked against the rebuilt policy rather than
	// trusted. It is committed by the comparison identity already, so a disagreement
	// cannot survive the check above -- but a column that could disagree with the
	// correspondences beside it is a second description of them, and this says plainly
	// that it is not one.
	if policy.ID() != record.PolicyID {
		return semantic.Comparison{}, false, IntegrityError{
			Code: IntegrityComparisonDiverged, Detail: "policy identity differs",
		}
	}
	return comparison, true, nil
}

// compiledPlanFor loads one plan and returns the compiled value inside it.
func compiledPlanFor(
	ctx context.Context, plans ports.PlanStore, tenant ports.TenantID, planID semantic.PlanID,
) (semantic.Plan, error) {
	record, found, err := plans.GetPlan(ctx, tenant, planID)
	if err != nil {
		return semantic.Plan{}, fmt.Errorf("read plan: %w", err)
	}
	if !found {
		return semantic.Plan{}, IntegrityError{Code: IntegrityComparisonPlanAbsent}
	}
	plan, ok := record.Compilation.Plan()
	if !ok {
		return semantic.Plan{}, IntegrityError{
			Code: IntegrityComparisonPlanAbsent, Detail: "stored plan carries no compiled plan",
		}
	}
	return plan, nil
}

// authoredPairs recovers the checkpoint keys the stored correspondences were written in.
//
// A CheckpointID is a digest of its plan and key, so it cannot be inverted. What can be
// done is to derive the identity of every checkpoint each plan declares and find which
// one matches, which is a search over declarations rather than an inversion: a
// correspondence naming anything the plans do not declare has no match and is refused.
func authoredPairs(
	baseline, candidate semantic.Plan, stored []ports.ComparisonCorrespondence,
) ([]semantic.CheckpointPair, error) {
	baselineKeys := declaredKeysByIdentity(baseline)
	candidateKeys := declaredKeysByIdentity(candidate)

	pairs := make([]semantic.CheckpointPair, 0, len(stored))
	for _, correspondence := range stored {
		baselineKey, declared := baselineKeys[correspondence.Baseline]
		if !declared {
			return nil, IntegrityError{
				Code: IntegrityComparisonCheckpointAbsent, Detail: "baseline side",
			}
		}
		candidateKey, declared := candidateKeys[correspondence.Candidate]
		if !declared {
			return nil, IntegrityError{
				Code: IntegrityComparisonCheckpointAbsent, Detail: "candidate side",
			}
		}
		pairs = append(pairs, semantic.CheckpointPair{
			Baseline: baselineKey, Candidate: candidateKey,
		})
	}
	return pairs, nil
}

// declaredKeysByIdentity indexes a plan's checkpoint declarations by their derived
// identity.
func declaredKeysByIdentity(plan semantic.Plan) map[semantic.CheckpointID]semantic.CheckpointKey {
	declarations := plan.Checkpoints()
	byIdentity := make(map[semantic.CheckpointID]semantic.CheckpointKey, len(declarations))
	for _, declaration := range declarations {
		identity, declared := plan.CheckpointID(declaration.Key)
		if !declared {
			// Unreachable: the key came from this plan's own declarations.
			continue
		}
		byIdentity[identity] = declaration.Key
	}
	return byIdentity
}
