package promotion

import "github.com/optimaldynamics/maiden-lane/internal/semantic"

// Recognized certified execution backends.
const (
	BackendGo         = "go"
	BackendSQL        = "sql"
	BackendDBT        = "dbt"
	BackendTranspiler = "transpiler"
)

var certifiedBackends = map[string]bool{
	BackendGo:         true,
	BackendSQL:        true,
	BackendDBT:        true,
	BackendTranspiler: true,
}

// certifiedBackend establishes HLD §14.1's "certified execution backend".
//
// It performs two distinct verifications:
//  1. Authenticates that Candidate.ExecutionID was cryptographically derived from
//     Candidate.Executor alongside the checkpoint's SemanticRunID and ProvenancePolicyID.
//  2. Verifies that the executor's backend token is recognized and certified.
//
// Missing executor or execution evidence reports Unestablished.
// A cryptographic mismatch, uncertified backend, or invalid digest reports Failed.
func certifiedBackend(candidate Candidate) ClauseResult {
	if !candidate.sealed() {
		return Unestablished(ClauseCertifiedBackend)
	}

	executor := candidate.Executor
	if executor.Backend() == "" || executor.Version() == "" || candidate.ExecutionID == "" {
		return Unestablished(ClauseCertifiedBackend)
	}

	// Verify cryptographic link between ExecutionID and Executor
	if !semantic.VerifyExecutionIdentity(
		candidate.ExecutionID,
		candidate.Checkpoint.SemanticRunID(),
		executor,
		candidate.Checkpoint.ProvenancePolicyID(),
	) {
		return Failed(ClauseCertifiedBackend)
	}

	// Verify backend certification
	if !certifiedBackends[executor.Backend()] {
		return Failed(ClauseCertifiedBackend)
	}

	return Passed(ClauseCertifiedBackend)
}

// IsCertifiedBackend reports whether a backend token is certified for execution.
func IsCertifiedBackend(backend string) bool {
	return certifiedBackends[backend]
}
