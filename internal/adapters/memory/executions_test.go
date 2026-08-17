package memory_test

import (
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

// The queue behaviours live in the shared contract, so the durable adapter is
// held to exactly the same definition of what an ExecutionStore does.
func TestStoreSatisfiesTheExecutionStoreContract(t *testing.T) {
	storagecontract.RunExecutionStoreContract(t, func(*testing.T) ports.ExecutionStore {
		return memory.NewStore()
	})
}

// Documented limitation, asserted so it cannot be mistaken for durability. With
// this adapter the queue lives in the serving process, so a separate worker
// process cannot see it: an enqueued execution would never run. That is why
// serve runs an in-process worker when storage is in memory.
func TestQueueIsNotVisibleToAnotherProcess(t *testing.T) {
	request := storagecontract.ExecutionRequestFixture(t, "acme", "exec-a")

	serving := memory.NewStore()
	if _, err := serving.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// A second store stands in for a separate worker process.
	worker := memory.NewStore()
	if _, found, err := worker.Claim(t.Context(), time.Minute); err != nil || found {
		t.Fatalf("a separate store observed the queue: found=%t err=%v", found, err)
	}
}
