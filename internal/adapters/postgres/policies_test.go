package postgres

import (
	"context"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesThePolicyStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunPolicyStoreContract(t, func(t *testing.T) ports.PolicyStore {
		return freshPolicyStore(t, url)
	})
}

// freshPolicyStore returns a store over an empty target_policies table. The
// contract requires each subtest to start empty, and truncating is how that holds
// for a database that outlives the process.
func freshPolicyStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	execute(t, url, `TRUNCATE target_policies`, nil)
	return store
}
