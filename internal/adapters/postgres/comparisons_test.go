package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesTheComparisonStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunComparisonStoreContract(t, func(t *testing.T) ports.ComparisonStore {
		return freshComparisonStore(t, url)
	}, comparisonPlans)
}

// freshComparisonStore returns a store over empty comparison tables. The contract
// requires each subtest to start empty, and truncating is how that holds for a database
// that outlives the process.
func freshComparisonStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	// Cascades into comparison_correspondences, which is the only reason one statement
	// is enough.
	execute(t, url, `TRUNCATE comparisons CASCADE`, nil)
	return store
}

// comparisonPlans supplies the ratified two-plan pair the contract compares over. The
// contract itself must stay domain free, so the declarations come from here.
func comparisonPlans(t *testing.T) (semantic.Plan, semantic.Plan) {
	t.Helper()
	baseline, candidate, err := teamhos.ComparisonPlans()
	if err != nil {
		t.Fatalf("teamhos.ComparisonPlans: %v", err)
	}
	return baseline, candidate
}

// A comparison is one artifact split across two tables, so a head row whose
// correspondences are gone is not a comparison with an empty mapping — it is a row
// asserting a correspondence that is no longer recoverable.
//
// The dangerous reading is the other one. An empty mapping looks like a perfectly
// ordinary value, and a store returning it would hand rehydration a record that rebuilds
// into a policy nobody authored, or into no policy at all, in a package with no way to
// tell the difference between "this comparison corresponds nothing" and "the rows are
// missing".
func TestComparisonWithoutCorrespondencesFailsClosed(t *testing.T) {
	url := requireDatabase(t)
	store := freshComparisonStore(t, url)
	baseline, candidate, err := teamhos.ComparisonPlans()
	if err != nil {
		t.Fatalf("teamhos.ComparisonPlans: %v", err)
	}
	comparison := comparisonOverPlans(t, baseline, candidate)

	if err := store.PutComparison(context.Background(), "acme", comparison); err != nil {
		t.Fatalf("PutComparison: %v", err)
	}
	// Baseline read, so a later failure is attributable to the corruption.
	if _, found, err := store.GetComparison(
		context.Background(), "acme", comparison.ID()); err != nil || !found {
		t.Fatalf("the comparison did not read back before corruption: found=%t err=%v", found, err)
	}

	execute(t, url, `DELETE FROM comparison_correspondences`, nil)

	record, found, err := store.GetComparison(context.Background(), "acme", comparison.ID())
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("a comparison with no correspondences was returned: "+
			"found=%t err=%v record=%+v", found, err, record)
	}
	if found {
		t.Fatal("a refused read still reported the comparison as found")
	}
}

// comparisonOverPlans builds the same comparison the contract does, for tests that need
// one outside the contract's own scope.
func comparisonOverPlans(t *testing.T, baseline, candidate semantic.Plan) semantic.Comparison {
	t.Helper()
	pairs := make([]semantic.CheckpointPair, len(baseline.Checkpoints()))
	baselineCheckpoints, candidateCheckpoints := baseline.Checkpoints(), candidate.Checkpoints()
	for i := range baselineCheckpoints {
		pairs[i] = semantic.CheckpointPair{
			Baseline: baselineCheckpoints[i].Key, Candidate: candidateCheckpoints[i].Key,
		}
	}
	policy, err := semantic.NewComparisonPolicy(baseline, candidate, pairs)
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	last := len(pairs) - 1
	baselineID, _ := baseline.CheckpointID(pairs[last].Baseline)
	candidateID, _ := candidate.CheckpointID(pairs[last].Candidate)
	world, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline: baselineID, Candidate: candidateID,
		Profile: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		World:   world.ID(), Corpus: storagecontract.CorpusFixture(t, 2).ID(), Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	return comparison
}
