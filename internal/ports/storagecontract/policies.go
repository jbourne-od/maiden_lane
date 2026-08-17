package storagecontract

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunPolicyStoreContract asserts every behaviour a ports.PolicyStore must
// exhibit.
//
// The weight here is on append-only history rather than on round-tripping. A
// store that reads and writes correctly but lets one version be rewritten breaks
// something no later code can repair: a publication record pins the version that
// authorized it, so a mutated version leaves an audit trail naming a rule that
// never existed.
func RunPolicyStoreContract(t *testing.T, newStore func(*testing.T) ports.PolicyStore) {
	t.Helper()

	t.Run("records a first policy at version one", func(t *testing.T) {
		store := newStore(t)
		policy := PolicyFixture(t, "acme", "cust", "cm", 1)

		if err := store.PutPolicy(t.Context(), policy); err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}
		active, found, err := store.ActivePolicy(t.Context(), "acme", "cust", "cm")
		if err != nil || !found {
			t.Fatalf("ActivePolicy: found=%t err=%v", found, err)
		}
		if active.Version != 1 {
			t.Fatalf("version = %d, want 1", active.Version)
		}
		if active.RequiredProfileID != policy.RequiredProfileID {
			t.Fatalf("requiredProfileID = %q, want %q",
				active.RequiredProfileID, policy.RequiredProfileID)
		}
	})

	t.Run("reports a target with no policy as absent rather than failing", func(t *testing.T) {
		store := newStore(t)
		// Nothing may publish to a target with no policy, and the gate reports
		// that as a refusal. An error here would make an ordinary unconfigured
		// target look like a broken store.
		_, found, err := store.ActivePolicy(t.Context(), "acme", "cust", "cm")
		if err != nil {
			t.Fatalf("ActivePolicy on an unconfigured target: %v", err)
		}
		if found {
			t.Fatal("an unconfigured target reported a policy")
		}
	})

	t.Run("advances the active policy to the newest version", func(t *testing.T) {
		store := newStore(t)
		for version := ports.PolicyVersion(1); version <= 3; version++ {
			if err := store.PutPolicy(t.Context(),
				PolicyFixture(t, "acme", "cust", "cm", version)); err != nil {
				t.Fatalf("PutPolicy v%d: %v", version, err)
			}
		}
		active, found, err := store.ActivePolicy(t.Context(), "acme", "cust", "cm")
		if err != nil || !found {
			t.Fatalf("ActivePolicy: found=%t err=%v", found, err)
		}
		if active.Version != 3 {
			t.Fatalf("active version = %d, want 3", active.Version)
		}
	})

	t.Run("keeps every superseded version readable", func(t *testing.T) {
		store := newStore(t)
		for version := ports.PolicyVersion(1); version <= 3; version++ {
			if err := store.PutPolicy(t.Context(),
				PolicyFixture(t, "acme", "cust", "cm", version)); err != nil {
				t.Fatalf("PutPolicy v%d: %v", version, err)
			}
		}
		// A publication pins the version that authorized it, so a superseded
		// version must stay resolvable forever. Retaining only the newest would
		// make historical publications unexplainable.
		for version := ports.PolicyVersion(1); version <= 3; version++ {
			recorded, found, err := store.PolicyAtVersion(t.Context(), "acme", "cust", "cm", version)
			if err != nil || !found {
				t.Fatalf("PolicyAtVersion(%d): found=%t err=%v", version, found, err)
			}
			want := PolicyFixture(t, "acme", "cust", "cm", version)
			if recorded.RequiredProfileID != want.RequiredProfileID {
				t.Fatalf("v%d profile = %q, want %q",
					version, recorded.RequiredProfileID, want.RequiredProfileID)
			}
		}
	})

	t.Run("refuses to rewrite a recorded version", func(t *testing.T) {
		store := newStore(t)
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}

		// Same version, different content. This is the assertion that protects
		// every audit record pinning version 1.
		rewritten := PolicyFixture(t, "acme", "cust", "cm", 1)
		rewritten.RequiredProfileID = profileIdentity(t, "a different profile")
		if err := store.PutPolicy(t.Context(), rewritten); !errors.Is(err, ports.ErrPolicyConflict) {
			t.Fatalf("PutPolicy over a recorded version = %v, want ErrPolicyConflict", err)
		}

		active, _, err := store.ActivePolicy(t.Context(), "acme", "cust", "cm")
		if err != nil {
			t.Fatalf("ActivePolicy: %v", err)
		}
		original := PolicyFixture(t, "acme", "cust", "cm", 1)
		if active.RequiredProfileID != original.RequiredProfileID {
			t.Fatal("a refused write still altered the recorded policy")
		}
	})

	t.Run("accepts an identical rewrite so a retry is safe", func(t *testing.T) {
		store := newStore(t)
		policy := PolicyFixture(t, "acme", "cust", "cm", 1)
		if err := store.PutPolicy(t.Context(), policy); err != nil {
			t.Fatalf("first PutPolicy: %v", err)
		}
		// A caller that does not know whether its write landed must be able to
		// repeat it. Identical content is not a rewrite of history.
		if err := store.PutPolicy(t.Context(), policy); err != nil {
			t.Fatalf("identical PutPolicy: %v", err)
		}
		if _, found, err := store.PolicyAtVersion(t.Context(), "acme", "cust", "cm", 2); err != nil || found {
			t.Fatalf("a retry created a second version: found=%t err=%v", found, err)
		}
	})

	t.Run("refuses a version that skips history", func(t *testing.T) {
		store := newStore(t)
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}
		// Version 3 after version 1 would leave a hole. Nothing could then say
		// what version 2 required, and a publication pinning it would be
		// unexplainable.
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 3)); !errors.Is(err, ports.ErrPolicyConflict) {
			t.Fatalf("PutPolicy skipping a version = %v, want ErrPolicyConflict", err)
		}
	})

	t.Run("refuses a first policy that does not start at one", func(t *testing.T) {
		store := newStore(t)
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 2)); !errors.Is(err, ports.ErrPolicyConflict) {
			t.Fatalf("PutPolicy of v2 with no v1 = %v, want ErrPolicyConflict", err)
		}
	})

	t.Run("refuses version zero", func(t *testing.T) {
		store := newStore(t)
		// Zero is not a policy. Admitting it would make "no policy" and "policy
		// version zero" two states a reader has to distinguish.
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 0)); err == nil {
			t.Fatal("PutPolicy accepted version zero")
		}
	})

	t.Run("isolates tenants, customers, and targets", func(t *testing.T) {
		store := newStore(t)
		if err := store.PutPolicy(t.Context(),
			PolicyFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}
		// Each of the three key parts must independently scope the policy. A
		// store that keys on only two would let one customer's target policy
		// authorize another's publication.
		for _, other := range []struct {
			name     string
			tenant   ports.TenantID
			customer ports.CustomerID
			target   ports.TargetKey
		}{
			{"another tenant", "other", "cust", "cm"},
			{"another customer", "acme", "other", "cm"},
			{"another target", "acme", "cust", "optimizer"},
		} {
			t.Run(other.name, func(t *testing.T) {
				_, found, err := store.ActivePolicy(t.Context(),
					other.tenant, other.customer, other.target)
				if err != nil {
					t.Fatalf("ActivePolicy: %v", err)
				}
				if found {
					t.Fatal("a policy leaked across the key")
				}
			})
		}
	})

	t.Run("admits exactly one writer to a version under concurrency", func(t *testing.T) {
		store := newStore(t)
		// Two operators editing one target concurrently is the case a
		// compare-and-swap exists for. Exactly one must win, and the loser must
		// learn it lost rather than silently overwrite.
		const writers = 6
		var (
			start     sync.WaitGroup
			finished  sync.WaitGroup
			mu        sync.Mutex
			succeeded int
			conflicts int
			other     []error
		)
		start.Add(1)
		for i := 0; i < writers; i++ {
			finished.Add(1)
			go func(i int) {
				defer finished.Done()
				policy := PolicyFixture(t, "acme", "cust", "cm", 1)
				// Distinct content, so an identical-retry acceptance cannot be
				// mistaken for two writers both winning.
				policy.RequiredProfileID = profileIdentity(t, string(rune('a'+i)))
				start.Wait()
				err := store.PutPolicy(context.Background(), policy)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ports.ErrPolicyConflict):
					conflicts++
				default:
					other = append(other, err)
				}
			}(i)
		}
		start.Done()
		finished.Wait()

		if len(other) > 0 {
			t.Fatalf("unexpected errors: %v", other)
		}
		if succeeded != 1 {
			t.Fatalf("writers that succeeded = %d, want exactly 1", succeeded)
		}
		if conflicts != writers-1 {
			t.Fatalf("writers that saw a conflict = %d, want %d", conflicts, writers-1)
		}
	})

	t.Run("stops on a cancelled context", func(t *testing.T) {
		store := newStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := store.PutPolicy(ctx, PolicyFixture(t, "acme", "cust", "cm", 1)); err == nil {
			t.Fatal("PutPolicy succeeded on a cancelled context")
		}
		if _, _, err := store.ActivePolicy(ctx, "acme", "cust", "cm"); err == nil {
			t.Fatal("ActivePolicy succeeded on a cancelled context")
		}
	})
}

// PolicyFixture builds a policy whose required profile varies with its version,
// so a test can tell one version's content from another's.
func PolicyFixture(
	t *testing.T, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PolicyVersion,
) ports.TargetPolicy {
	t.Helper()
	return ports.TargetPolicy{
		TenantID:          tenant,
		CustomerID:        customer,
		Target:            target,
		Version:           version,
		RequiredProfileID: profileIdentity(t, string(target)+":"+versionLabel(version)),
	}
}

func versionLabel(version ports.PolicyVersion) string {
	if version == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for value := version; value > 0; value /= 10 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
	}
	return string(digits)
}

// profileIdentity compiles a real profile through the kernel so the stored
// ProfileID is one the kernel actually produced. A fabricated digest would let a
// store pass while failing for identities of the shape it will really be given.
//
// Profiles compile only as part of a whole request, so this builds a minimal one.
// The requirement code comes from the kernel's closed readiness vocabulary; an
// invented code does not compile, which is the compiler doing its job.
func profileIdentity(t *testing.T, key string) semantic.ProfileID {
	t.Helper()

	schema, err := semantic.NewSchema([]semantic.EntityDeclaration{{
		Kind: "team",
		Fields: []semantic.FieldDeclaration{
			{Name: "assignment_key", Kind: semantic.ValueString},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema:                   schema.Declaration(),
		CompilerSemanticsVersion: "maiden-lane.compiler-semantics.v1",
		Profiles: []semantic.ProfileDeclaration{{
			Key:         semantic.ProfileKey(key),
			Scope:       semantic.ProfileScope{Kind: semantic.AllEntitiesOfKind, EntityKind: "team"},
			Aggregation: semantic.AllSelected,
			Requirements: []semantic.RequirementAtom{{
				Code:  semantic.TeamAssignmentKeyRequired,
				Kind:  semantic.FieldPresent,
				Field: "team.assignment_key",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compile(%q): %v", key, err)
	}
	if _, ok := compilation.Plan(); !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("policy fixture profile did not compile: %v", failure.Diagnostics())
	}
	profiles := compilation.Profiles()
	if len(profiles) != 1 {
		t.Fatalf("compiled profiles = %d, want 1", len(profiles))
	}
	return profiles[0].ID()
}
