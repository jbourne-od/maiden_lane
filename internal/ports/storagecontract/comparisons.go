package storagecontract

import (
	"context"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunComparisonStoreContract asserts every behaviour a ports.ComparisonStore must
// exhibit.
//
// The weight is on what the projection preserves rather than on round-tripping fields,
// because a comparison cannot be returned as a kernel value at all: its policy is derived
// from two compiled plans and the kernel's encoders are one way. What a store returns is
// a description, and app.RehydrateComparison turns it back into a comparison only if the
// components reproduce the identity. Every field a store drops or reorders therefore
// surfaces as an integrity failure much later, in another package, so it is pinned here.
func RunComparisonStoreContract(
	t *testing.T,
	newStore func(*testing.T) ports.ComparisonStore,
	newPlans func(*testing.T) (baseline, candidate semantic.Plan),
) {
	t.Helper()
	comparisonFor := func(t *testing.T) semantic.Comparison {
		t.Helper()
		baseline, candidate := newPlans(t)
		return comparisonOver(t, baseline, candidate)
	}

	t.Run("returns every component of a stored comparison", func(t *testing.T) {
		store := newStore(t)
		comparison := comparisonFor(t)
		expected := ports.ProjectComparison("acme", comparison)

		if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
			t.Fatalf("PutComparison: %v", err)
		}
		got, found, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil || !found {
			t.Fatalf("GetComparison: found=%t err=%v", found, err)
		}

		// Each of these is a component of ComparisonID, so a store dropping any one of
		// them returns a row that cannot rebuild the question it is filed under.
		for _, field := range []struct {
			name      string
			got, want string
		}{
			{"comparisonID", string(got.ComparisonID), string(comparison.ID())},
			{"baseline", string(got.Baseline), string(comparison.Baseline())},
			{"candidate", string(got.Candidate), string(comparison.Candidate())},
			{"profile", string(got.Profile), string(comparison.Profile())},
			{"world", string(got.World), string(comparison.World())},
			{"corpus", string(got.Corpus), string(comparison.Corpus())},
			{"policyID", string(got.PolicyID), string(comparison.Policy().ID())},
			{"baselinePlan", string(got.BaselinePlan), string(comparison.Policy().BaselinePlan())},
			{"candidatePlan", string(got.CandidatePlan), string(comparison.Policy().CandidatePlan())},
		} {
			if field.got != field.want {
				t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
			}
		}
		if got.TenantID != "acme" {
			t.Errorf("tenant = %q, want acme", got.TenantID)
		}
		assertCorrespondencesEqual(t, got.Correspondences, expected.Correspondences)
	})

	t.Run("preserves correspondence order", func(t *testing.T) {
		// Order is NOT what makes a rebuild work: NewComparisonPolicy sorts whatever it
		// is given, so a reordered row set still rebuilds the same policy. What order
		// pins is that two adapters project the same comparison into the same record, so
		// a caller may compare records field by field without one backend spuriously
		// disagreeing with another. In SQL it also pins something concrete, because rows
		// come back in no particular order unless a query says otherwise.
		store := newStore(t)
		comparison := comparisonFor(t)
		if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
			t.Fatalf("PutComparison: %v", err)
		}
		got, _, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil {
			t.Fatalf("GetComparison: %v", err)
		}

		derived := comparison.Policy().Correspondences()
		if len(got.Correspondences) != len(derived) {
			t.Fatalf("correspondences = %d, want %d", len(got.Correspondences), len(derived))
		}
		for i, correspondence := range derived {
			if got.Correspondences[i].Baseline != correspondence.Baseline() ||
				got.Correspondences[i].Candidate != correspondence.Candidate() {
				t.Fatalf("correspondence %d differs from the canonical order", i)
			}
		}
	})

	t.Run("distinguishes the two sides", func(t *testing.T) {
		// A store that filled both sides from one column would round-trip a comparison
		// that looks well formed and asks a different question. The fixture is
		// deliberately asymmetric so this is observable at all.
		store := newStore(t)
		comparison := comparisonFor(t)
		if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
			t.Fatalf("PutComparison: %v", err)
		}
		got, _, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil {
			t.Fatalf("GetComparison: %v", err)
		}

		if got.Baseline == got.Candidate {
			t.Fatal("both checkpoint sides came back identical")
		}
		if got.BaselinePlan == got.CandidatePlan {
			t.Fatal("both plan sides came back identical")
		}
		for i, correspondence := range got.Correspondences {
			if correspondence.Baseline == correspondence.Candidate {
				t.Fatalf("correspondence %d has identical sides", i)
			}
		}
	})

	t.Run("storing the same question twice is idempotent", func(t *testing.T) {
		// Comparison identity is content derived, so the second write is the same
		// question and not a conflict.
		store := newStore(t)
		comparison := comparisonFor(t)
		for attempt := range 2 {
			if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
				t.Fatalf("PutComparison attempt %d: %v", attempt+1, err)
			}
		}
		got, found, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil || !found {
			t.Fatalf("GetComparison: found=%t err=%v", found, err)
		}
		if got.ComparisonID != comparison.ID() {
			t.Fatalf("comparison ID = %s, want %s", got.ComparisonID, comparison.ID())
		}
	})

	t.Run("reports an unknown comparison as absent rather than failing", func(t *testing.T) {
		store := newStore(t)
		got, found, err := store.GetComparison(t.Context(), "acme", "sha256:"+
			"0000000000000000000000000000000000000000000000000000000000000000")
		if err != nil {
			t.Fatalf("GetComparison: %v", err)
		}
		if found {
			t.Fatalf("a comparison nobody stored was found: %+v", got)
		}
	})

	t.Run("isolates tenants", func(t *testing.T) {
		// A comparison names checkpoints, plans and a corpus belonging to one tenant.
		// Returning it to another would disclose their existence to a caller with no
		// right to know, so absence is the answer rather than an error.
		store := newStore(t)
		comparison := comparisonFor(t)
		if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
			t.Fatalf("PutComparison: %v", err)
		}

		if _, found, err := store.GetComparison(t.Context(), "other", comparison.ID()); err != nil || found {
			t.Fatalf("another tenant's comparison was visible: found=%t err=%v", found, err)
		}
		// And the same question stored by two tenants is two records, not one shared
		// one, because content identity is not a capability.
		if err := store.PutComparison(t.Context(), "other", comparison); err != nil {
			t.Fatalf("PutComparison for the second tenant: %v", err)
		}
		for _, tenant := range []ports.TenantID{"acme", "other"} {
			got, found, err := store.GetComparison(t.Context(), tenant, comparison.ID())
			if err != nil || !found {
				t.Fatalf("GetComparison for %s: found=%t err=%v", tenant, found, err)
			}
			if got.TenantID != tenant {
				t.Fatalf("tenant = %q, want %q", got.TenantID, tenant)
			}
		}
	})

	t.Run("refuses an incomplete write", func(t *testing.T) {
		// Every exported struct here has a constructible zero value, so a comparison
		// nobody built is a thing a caller can hand over. Storing one would file a row
		// under the empty identity that no rehydration could ever reproduce.
		store := newStore(t)
		comparison := comparisonFor(t)

		if err := store.PutComparison(t.Context(), "acme", semantic.Comparison{}); err == nil {
			t.Error("PutComparison accepted a zero comparison")
		}
		if err := store.PutComparison(t.Context(), "", comparison); err == nil {
			t.Error("PutComparison accepted an empty tenant")
		}
		if _, found, err := store.GetComparison(t.Context(), "acme", ""); err != nil || found {
			t.Fatalf("the empty identity resolved to something: found=%t err=%v", found, err)
		}
	})

	t.Run("stored records are unreachable for mutation", func(t *testing.T) {
		// An in-memory adapter can hand back the slice it is holding. Every other record
		// here returns copies and a caller has no way to know this one did not.
		store := newStore(t)
		comparison := comparisonFor(t)
		if err := store.PutComparison(t.Context(), "acme", comparison); err != nil {
			t.Fatalf("PutComparison: %v", err)
		}
		first, _, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil {
			t.Fatalf("GetComparison: %v", err)
		}
		for i := range first.Correspondences {
			first.Correspondences[i] = ports.ComparisonCorrespondence{}
		}

		second, _, err := store.GetComparison(t.Context(), "acme", comparison.ID())
		if err != nil {
			t.Fatalf("GetComparison: %v", err)
		}
		assertCorrespondencesEqual(t, second.Correspondences,
			ports.ProjectComparison("acme", comparison).Correspondences)
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		store := newStore(t)
		comparison := comparisonFor(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if err := store.PutComparison(ctx, "acme", comparison); err == nil {
			t.Error("PutComparison ignored a cancelled context")
		}
		if _, _, err := store.GetComparison(ctx, "acme", comparison.ID()); err == nil {
			t.Error("GetComparison ignored a cancelled context")
		}
	})
}

// assertCorrespondencesEqual compares two correspondence sets position by position.
func assertCorrespondencesEqual(t *testing.T, got, want []ports.ComparisonCorrespondence) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("correspondences = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("correspondence %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// comparisonOver builds one comparison from the two plans a caller supplied.
//
// The plans come from outside because this package must not depend on any domain's
// declarations: an architectural guard keeps the ratified team-HOS fixture out of the
// production path, and a private copy of those declarations here would be a second
// ratified fixture able to drift from the real one.
//
// The correspondence is derived from what the plans DECLARE rather than from any known
// key, which is what keeps this domain free. Checkpoints are paired in the plans'
// canonical declaration order, so the caller's only obligation is to supply two plans
// declaring the same number of them.
func comparisonOver(t *testing.T, baseline, candidate semantic.Plan) semantic.Comparison {
	t.Helper()

	if baseline.ID() == candidate.ID() {
		// A symmetric fixture cannot distinguish a store that fills both sides from one
		// column, which is the bug most likely to be written here.
		t.Fatal("the contract was given one plan twice, so neither side is observable")
	}
	baselineCheckpoints := baseline.Checkpoints()
	candidateCheckpoints := candidate.Checkpoints()
	if len(baselineCheckpoints) != len(candidateCheckpoints) {
		t.Fatalf("the two plans declare %d and %d checkpoints; a correspondence must be "+
			"one-to-one", len(baselineCheckpoints), len(candidateCheckpoints))
	}
	if len(baselineCheckpoints) < 2 {
		// One correspondence cannot show that order is preserved or that the two sides
		// are distinguished.
		t.Fatalf("the contract needs at least two checkpoints per plan, got %d",
			len(baselineCheckpoints))
	}

	pairs := make([]semantic.CheckpointPair, len(baselineCheckpoints))
	for i := range baselineCheckpoints {
		pairs[i] = semantic.CheckpointPair{
			Baseline: baselineCheckpoints[i].Key, Candidate: candidateCheckpoints[i].Key,
		}
	}
	policy, err := semantic.NewComparisonPolicy(baseline, candidate, pairs)
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}

	// The last declared checkpoint on each side is the one compared, because a plan's
	// checkpoints are ordered by prefix and the final one is the furthest the semantics
	// reach.
	last := len(pairs) - 1
	baselineID, declared := baseline.CheckpointID(pairs[last].Baseline)
	if !declared {
		t.Fatal("the baseline plan does not declare its own checkpoint")
	}
	candidateID, declared := candidate.CheckpointID(pairs[last].Candidate)
	if !declared {
		t.Fatal("the candidate plan does not declare its own checkpoint")
	}
	if baselineID == candidateID {
		t.Fatal("the two compared checkpoints are the same, so neither side is observable")
	}

	world, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  baselineID,
		Candidate: candidateID,
		Profile:   semantic.ProfileID("sha256:" + comparisonFixtureProfileDigest),
		World:     world.ID(),
		Corpus:    CorpusFixture(t, 2).ID(),
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	return comparison
}

// comparisonFixtureProfileDigest is a literal profile identity.
//
// A comparison pins a ProfileID and nothing here compiles profiles, so a literal is
// honest: storage neither derives it nor checks it, and rehydration re-derives the
// comparison identity from whatever is stored. Using a real compiled profile would
// suggest this layer validates one.
const comparisonFixtureProfileDigest = "1111111111111111111111111111111111111111111111111111111111111111"
