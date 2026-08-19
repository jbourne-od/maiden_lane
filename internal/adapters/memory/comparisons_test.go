package memory_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesTheComparisonStoreContract(t *testing.T) {
	storagecontract.RunComparisonStoreContract(t, func(*testing.T) ports.ComparisonStore {
		return memory.NewStore()
	}, comparisonPlans)
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
