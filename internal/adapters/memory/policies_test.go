package memory_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesThePolicyStoreContract(t *testing.T) {
	storagecontract.RunPolicyStoreContract(t, func(*testing.T) ports.PolicyStore {
		return memory.NewStore()
	})
}
