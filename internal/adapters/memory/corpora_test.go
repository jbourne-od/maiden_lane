package memory_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesTheCorpusStoreContract(t *testing.T) {
	storagecontract.RunCorpusStoreContract(t, func(*testing.T) ports.CorpusStore {
		return memory.NewStore()
	})
}
