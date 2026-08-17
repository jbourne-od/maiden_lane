package memory_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

// The behavioural assertions live in storagecontract, not here, so that every
// adapter is held to one definition of what a PlanStore does. An assertion that
// only ever ran against this adapter would describe this implementation rather
// than the port.
func TestStoreSatisfiesThePlanStoreContract(t *testing.T) {
	storagecontract.RunPlanStoreContract(t, func(*testing.T) ports.PlanStore {
		return memory.NewStore()
	})
}

// Production break caught: a fresh store that already contained records would
// let one process's plans appear in another's, and would make every isolation
// assertion in the contract suite meaningless.
func TestNewStoreIsEmpty(t *testing.T) {
	store := memory.NewStore()
	record := storagecontract.PlanRecordFixture(t, "acme", "memory.v1")

	if _, found, err := store.GetPlan(t.Context(), record.TenantID, record.PlanID); err != nil || found {
		t.Fatalf("a new store already held a record: found=%t err=%v", found, err)
	}
}

// Production break caught: two stores must not share state. The contract suite
// builds a store per subtest and relies on that; a package-level map behind
// NewStore would make the suite pass while the adapter was broken.
func TestStoresDoNotShareState(t *testing.T) {
	first, second := memory.NewStore(), memory.NewStore()
	record := storagecontract.PlanRecordFixture(t, "acme", "memory.v1")

	if err := first.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	if _, found, err := second.GetPlan(t.Context(), record.TenantID, record.PlanID); err != nil || found {
		t.Fatalf("a second store observed the first store's record: found=%t err=%v", found, err)
	}
}

// Documented limitation, asserted so it cannot be mistaken for durability: this
// adapter holds records in process memory. Nothing here survives a restart, and
// two replicas share nothing. The durable adapter exists precisely because of
// this, and the port is what lets it replace this one.
func TestStoreIsExplicitlyNotDurable(t *testing.T) {
	record := storagecontract.PlanRecordFixture(t, "acme", "memory.v1")

	original := memory.NewStore()
	if err := original.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}

	// A new store stands in for a restarted process: same code, no shared state.
	restarted := memory.NewStore()
	if _, found, _ := restarted.GetPlan(t.Context(), record.TenantID, record.PlanID); found {
		t.Fatal("this adapter appears to persist across instances; update its documentation if that changed")
	}
}
