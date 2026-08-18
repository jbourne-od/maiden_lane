package memory_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesThePublicationStoreContract(t *testing.T) {
	storagecontract.RunPublicationStoreContract(t, func(*testing.T) ports.PublicationStore {
		return memory.NewStore()
	})
}
