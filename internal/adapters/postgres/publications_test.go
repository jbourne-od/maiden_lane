package postgres

import (
	"context"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesThePublicationStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunPublicationStoreContract(t, func(t *testing.T) ports.PublicationStore {
		return freshPublicationStore(t, url)
	})
}

// freshPublicationStore returns a store over an empty publications table. The
// contract requires each subtest to start empty, and truncating is how that holds
// for a database that outlives the process.
func freshPublicationStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	execute(t, url, `TRUNCATE publications`, nil)
	return store
}
