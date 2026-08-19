package ports

import (
	"context"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ComparisonCorrespondence is one stored checkpoint correspondence.
//
// It is a plain pair rather than semantic.CheckpointCorrespondence because that type has
// no exported constructor: a durable adapter reading two digests out of a row could not
// produce one, and giving it a constructor would hand every caller a way to assert a
// correspondence the kernel never derived from real plans.
type ComparisonCorrespondence struct {
	Baseline  semantic.CheckpointID
	Candidate semantic.CheckpointID
}

// ComparisonRecord is a PROJECTION of one stored comparison question. It is not a
// comparison, and it carries no authority.
//
// A semantic.Comparison cannot be returned from storage. Its policy is derived from two
// compiled plans, and the kernel's encoders are one way, so nothing can be rebuilt from
// bytes alone. What a store can return is this description of what was asked, which
// app.RehydrateComparison turns back into an authenticated comparison by loading both
// plans, re-deriving the policy and the comparison, and requiring the identity to match.
// A caller that reads this record and acts on it has trusted a projection, which is the
// one thing this system never does.
//
// Every field here is a COMPONENT of ComparisonID, through the comparison tuple or
// through its policy. Nothing is stored beside the identity that the identity does not
// already commit to, so there is no value a mangled row could carry without the
// re-derived identity ceasing to match.
//
// The one thing that is presentation rather than content is the ORDER of Correspondences,
// which is the policy's canonical order. Rehydration does not depend on it, because the
// kernel sorts whatever it is given; it is fixed so that two adapters project one
// comparison into one record and a caller may compare records directly.
type ComparisonRecord struct {
	TenantID     TenantID
	ComparisonID semantic.ComparisonID

	// Baseline and Candidate are checkpoint DECLARATIONS, not realized artifacts. A
	// comparison over n cases has n realizations of each side.
	Baseline  semantic.CheckpointID
	Candidate semantic.CheckpointID

	Profile semantic.ProfileID
	World   semantic.WorldID
	Corpus  semantic.CorpusID

	// PolicyID, the two plans, and the correspondences are the policy's own components.
	// They are stored rather than the policy value for the same reason as above.
	PolicyID        semantic.ComparisonPolicyID
	BaselinePlan    semantic.PlanID
	CandidatePlan   semantic.PlanID
	Correspondences []ComparisonCorrespondence
}

// ComparisonStore persists comparison questions.
//
// Every method is tenant scoped by signature, and there is deliberately no unscoped
// lookup, so a handler cannot forget to filter because no such call exists.
//
// The signatures are deliberately asymmetric. A write takes an authenticated kernel
// value, so a record describing a comparison the kernel never built cannot be stored;
// a read returns a projection, because that is all storage can honestly produce. Taking
// a record on the way in would let a caller assemble one whose parts disagree, and the
// disagreement would surface only later, at rehydration, as somebody else's integrity
// error.
type ComparisonStore interface {
	// PutComparison stores a comparison for its tenant.
	//
	// Comparison identity is content derived, so storing the same question twice is
	// idempotent rather than a conflict, and never a version conflict a caller must
	// resolve. Beyond an incomplete comparison and a cancelled context, an
	// implementation may of course fail for its own reasons: a durable one can lose its
	// connection or its disk, and a caller must handle that rather than read this as a
	// promise that only its own input can be at fault.
	//
	// There is no update and no delete. A comparison that should have asked something
	// else is a different question, and the one already stored must remain readable
	// because a publication may pin it.
	PutComparison(context.Context, TenantID, semantic.Comparison) error

	// GetComparison reports the stored projection for this tenant. A comparison
	// belonging to another tenant is reported as absent, never as an error:
	// distinguishing the two would leak its existence to a caller with no right to know.
	GetComparison(context.Context, TenantID, semantic.ComparisonID) (ComparisonRecord, bool, error)
}

// ProjectComparison describes an authenticated comparison in the terms a store can hold.
//
// It lives here rather than in each adapter so both produce the same row from the same
// value: a projection that differed between adapters would make a comparison rehydrate
// in one deployment and fail integrity in another, which is the class of divergence the
// shared contract exists to prevent.
func ProjectComparison(tenant TenantID, comparison semantic.Comparison) ComparisonRecord {
	policy := comparison.Policy()
	derived := policy.Correspondences()
	correspondences := make([]ComparisonCorrespondence, len(derived))
	for i, correspondence := range derived {
		correspondences[i] = ComparisonCorrespondence{
			Baseline:  correspondence.Baseline(),
			Candidate: correspondence.Candidate(),
		}
	}
	return ComparisonRecord{
		TenantID:        tenant,
		ComparisonID:    comparison.ID(),
		Baseline:        comparison.Baseline(),
		Candidate:       comparison.Candidate(),
		Profile:         comparison.Profile(),
		World:           comparison.World(),
		Corpus:          comparison.Corpus(),
		PolicyID:        policy.ID(),
		BaselinePlan:    policy.BaselinePlan(),
		CandidatePlan:   policy.CandidatePlan(),
		Correspondences: correspondences,
	}
}

// Clone returns a record that shares nothing with this one.
//
// Copying the struct copies the slice header, so two callers would write into one
// backing array. Every other record here returns copies and a caller has no way to know
// this one did not.
func (r ComparisonRecord) Clone() ComparisonRecord {
	r.Correspondences = slices.Clone(r.Correspondences)
	return r
}
