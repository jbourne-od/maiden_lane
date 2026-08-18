package storagecontract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunPublicationStoreContract asserts every behaviour a ports.PublicationStore
// must exhibit.
//
// The weight is on the compare-and-swap, because that is the one property with no
// later repair. HLD §14.1 requires a conflicting publication to fail rather than
// silently overwrite a newer result, and a store that gets everything else right
// while admitting two publishers to one version has published something nobody
// authorized to a destination that will act on it.
func RunPublicationStoreContract(t *testing.T, newStore func(*testing.T) ports.PublicationStore) {
	t.Helper()

	t.Run("records a first publication at version one", func(t *testing.T) {
		store := newStore(t)
		publication := PublicationFixture(t, "acme", "cust", "cm", 1)

		if err := store.Publish(t.Context(), publication); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		current, found, err := store.CurrentPublication(t.Context(), "acme", "cust", "cm")
		if err != nil || !found {
			t.Fatalf("CurrentPublication: found=%t err=%v", found, err)
		}
		if current != publication {
			t.Fatalf("current publication = %+v, want %+v", current, publication)
		}
	})

	t.Run("reports a target never published to as absent rather than failing", func(t *testing.T) {
		store := newStore(t)
		// This is the ordinary initial state of every target. An error would make an
		// unused destination indistinguishable from a broken store, and the gate
		// already reports "nothing may publish here yet" as a refusal.
		_, found, err := store.CurrentPublication(t.Context(), "acme", "cust", "cm")
		if err != nil {
			t.Fatalf("CurrentPublication on an unpublished target: %v", err)
		}
		if found {
			t.Fatal("a target with no publication reported one")
		}
	})

	t.Run("advances the current publication to the newest version", func(t *testing.T) {
		store := newStore(t)
		publishRange(t, store, "acme", "cust", "cm", 3)

		current, found, err := store.CurrentPublication(t.Context(), "acme", "cust", "cm")
		if err != nil || !found {
			t.Fatalf("CurrentPublication: found=%t err=%v", found, err)
		}
		if current.Version != 3 {
			t.Fatalf("current version = %d, want 3", current.Version)
		}
	})

	t.Run("keeps every superseded publication readable", func(t *testing.T) {
		store := newStore(t)
		publishRange(t, store, "acme", "cust", "cm", 3)

		// A superseded publication is what makes "what was published when this
		// decision was taken?" answerable. Retaining only the current one would
		// answer what is published now and destroy the history it replaced --
		// which is also how PublicationStatus stays derivable rather than stored.
		for version := ports.PublicationVersion(1); version <= 3; version++ {
			recorded, found, err := store.PublicationAtVersion(
				t.Context(), "acme", "cust", "cm", version)
			if err != nil || !found {
				t.Fatalf("PublicationAtVersion(%d): found=%t err=%v", version, found, err)
			}
			if want := PublicationFixture(t, "acme", "cust", "cm", version); recorded != want {
				t.Fatalf("v%d = %+v, want %+v", version, recorded, want)
			}
		}
	})

	t.Run("preserves every pinned identity exactly", func(t *testing.T) {
		store := newStore(t)
		publication := PublicationFixture(t, "acme", "cust", "cm", 1)
		if err := store.Publish(t.Context(), publication); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		recorded, _, err := store.CurrentPublication(t.Context(), "acme", "cust", "cm")
		if err != nil {
			t.Fatalf("CurrentPublication: %v", err)
		}

		// Field by field rather than by struct comparison, so a store that drops or
		// swaps one identity says which. Every one of these is load-bearing: the
		// record is auditable only because the decision can be re-derived from
		// exactly these, and a swapped pair would re-derive a different decision
		// while looking complete.
		for _, check := range []struct {
			name        string
			got, wanted any
		}{
			{"policy version", recorded.PolicyVersion, publication.PolicyVersion},
			{"profile", recorded.ProfileID, publication.ProfileID},
			{"assessment", recorded.AssessmentID, publication.AssessmentID},
			{"checkpoint artifact", recorded.CheckpointArtifactID, publication.CheckpointArtifactID},
			{"semantic run", recorded.SemanticRunID, publication.SemanticRunID},
			{"execution", recorded.ExecutionID, publication.ExecutionID},
		} {
			if check.got != check.wanted {
				t.Errorf("%s = %v, want %v", check.name, check.got, check.wanted)
			}
		}
	})

	t.Run("refuses to rewrite a recorded publication", func(t *testing.T) {
		store := newStore(t)
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		// Same version, different checkpoint. This is the assertion protecting every
		// audit record: without it, what a target was published with at version 1
		// could be changed after the fact.
		rewritten := PublicationFixture(t, "acme", "cust", "cm", 1)
		rewritten.CheckpointArtifactID = semantic.CheckpointArtifactID(digestLiteral("other"))
		if err := store.Publish(t.Context(), rewritten); !errors.Is(err, ports.ErrPublicationConflict) {
			t.Fatalf("Publish over a recorded version = %v, want ErrPublicationConflict", err)
		}

		current, _, err := store.CurrentPublication(t.Context(), "acme", "cust", "cm")
		if err != nil {
			t.Fatalf("CurrentPublication: %v", err)
		}
		original := PublicationFixture(t, "acme", "cust", "cm", 1)
		if current.CheckpointArtifactID != original.CheckpointArtifactID {
			t.Fatal("a refused publication still altered the recorded one")
		}
	})

	t.Run("accepts an identical repeat so a retry is safe", func(t *testing.T) {
		store := newStore(t)
		publication := PublicationFixture(t, "acme", "cust", "cm", 1)
		if err := store.Publish(t.Context(), publication); err != nil {
			t.Fatalf("first Publish: %v", err)
		}
		// A publisher that did not learn whether its write landed must be able to
		// repeat it. Identical content is not a rewrite, and must not become a
		// second publication either.
		if err := store.Publish(t.Context(), publication); err != nil {
			t.Fatalf("identical Publish: %v", err)
		}
		if _, found, err := store.PublicationAtVersion(
			t.Context(), "acme", "cust", "cm", 2); err != nil || found {
			t.Fatalf("a retry created a second publication: found=%t err=%v", found, err)
		}
	})

	t.Run("refuses a version that skips history", func(t *testing.T) {
		store := newStore(t)
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		// Version 3 after version 1 leaves a hole nothing can fill, so the target's
		// history would no longer be a sequence of what was published.
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 3)); !errors.Is(err, ports.ErrPublicationConflict) {
			t.Fatalf("Publish skipping a version = %v, want ErrPublicationConflict", err)
		}
	})

	t.Run("refuses a first publication that does not start at one", func(t *testing.T) {
		store := newStore(t)
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 2)); !errors.Is(err, ports.ErrPublicationConflict) {
			t.Fatalf("Publish of v2 with no v1 = %v, want ErrPublicationConflict", err)
		}
	})

	t.Run("refuses version zero", func(t *testing.T) {
		store := newStore(t)
		// Zero means never published. Admitting it as a version would make "never
		// published" and "published at version zero" two states to distinguish.
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 0)); err == nil {
			t.Fatal("Publish accepted version zero")
		}
	})

	t.Run("refuses a publication missing any pinned identity", func(t *testing.T) {
		store := newStore(t)
		// A record missing one of these is not a weaker record: the decision behind
		// it cannot be re-derived, so it is an unverifiable claim that something was
		// authorized. Storing one would put a caller in the position of treating it
		// as an authorization anyway.
		for _, test := range []struct {
			name  string
			blank func(*ports.Publication)
		}{
			{"policy version", func(p *ports.Publication) { p.PolicyVersion = 0 }},
			{"profile", func(p *ports.Publication) { p.ProfileID = "" }},
			{"assessment", func(p *ports.Publication) { p.AssessmentID = "" }},
			{"checkpoint", func(p *ports.Publication) { p.CheckpointArtifactID = "" }},
			{"semantic run", func(p *ports.Publication) { p.SemanticRunID = "" }},
			{"execution", func(p *ports.Publication) { p.ExecutionID = "" }},
			{"tenant", func(p *ports.Publication) { p.TenantID = "" }},
			{"customer", func(p *ports.Publication) { p.CustomerID = "" }},
			{"target", func(p *ports.Publication) { p.Target = "" }},
		} {
			t.Run(test.name, func(t *testing.T) {
				publication := PublicationFixture(t, "acme", "cust", "cm", 1)
				test.blank(&publication)
				if err := store.Publish(t.Context(), publication); err == nil {
					t.Fatalf("Publish accepted a publication with no %s", test.name)
				}
			})
		}
	})

	t.Run("isolates tenants, customers, and targets", func(t *testing.T) {
		store := newStore(t)
		if err := store.Publish(t.Context(),
			PublicationFixture(t, "acme", "cust", "cm", 1)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		// Each key part must independently scope the publication. A store keying on
		// only two would report one customer's published artifact as another's, and
		// a consumer reading its own target would act on it.
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
				_, found, err := store.CurrentPublication(
					t.Context(), other.tenant, other.customer, other.target)
				if err != nil {
					t.Fatalf("CurrentPublication: %v", err)
				}
				if found {
					t.Fatal("a publication leaked across the key")
				}
			})
		}
	})

	t.Run("admits exactly one publisher to a version under concurrency", func(t *testing.T) {
		store := newStore(t)
		// This is the assertion the whole slice exists for. Two publishers racing on
		// one target is the situation §14.1's compare-and-swap addresses: exactly one
		// must win, and the loser must learn it lost rather than overwrite a result
		// it never saw.
		const publishers = 6
		var (
			start     sync.WaitGroup
			finished  sync.WaitGroup
			mu        sync.Mutex
			succeeded int
			conflicts int
			other     []error
		)
		start.Add(1)
		for i := 0; i < publishers; i++ {
			finished.Add(1)
			go func(i int) {
				defer finished.Done()
				publication := PublicationFixture(t, "acme", "cust", "cm", 1)
				// Distinct content, so an accepted identical retry cannot be mistaken
				// for two publishers both winning.
				publication.CheckpointArtifactID =
					semantic.CheckpointArtifactID(digestLiteral(string(rune('a' + i))))
				start.Wait()
				err := store.Publish(context.Background(), publication)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ports.ErrPublicationConflict):
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
			t.Fatalf("publishers that succeeded = %d, want exactly 1", succeeded)
		}
		if conflicts != publishers-1 {
			t.Fatalf("publishers that saw a conflict = %d, want %d", conflicts, publishers-1)
		}

		// Exactly one publication exists, and it is the winner's.
		if _, found, err := store.PublicationAtVersion(
			t.Context(), "acme", "cust", "cm", 2); err != nil || found {
			t.Fatalf("a losing publisher still appended: found=%t err=%v", found, err)
		}
	})

	t.Run("stops on a cancelled context", func(t *testing.T) {
		store := newStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := store.Publish(ctx, PublicationFixture(t, "acme", "cust", "cm", 1)); err == nil {
			t.Fatal("Publish succeeded on a cancelled context")
		}
		if _, _, err := store.CurrentPublication(ctx, "acme", "cust", "cm"); err == nil {
			t.Fatal("CurrentPublication succeeded on a cancelled context")
		}
		if _, _, err := store.PublicationAtVersion(ctx, "acme", "cust", "cm", 1); err == nil {
			t.Fatal("PublicationAtVersion succeeded on a cancelled context")
		}
	})
}

func publishRange(
	t *testing.T, store ports.PublicationStore,
	tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
	through ports.PublicationVersion,
) {
	t.Helper()
	for version := ports.PublicationVersion(1); version <= through; version++ {
		if err := store.Publish(t.Context(),
			PublicationFixture(t, tenant, customer, target, version)); err != nil {
			t.Fatalf("Publish v%d: %v", version, err)
		}
	}
}

// PublicationFixture builds a publication whose pinned identities vary with its
// version, so a test can tell one version's content from another's and a store that
// returns the wrong version is visible rather than merely unlucky.
//
// The ProfileID is compiled through the kernel while the remaining identities are
// digest-shaped literals. That asymmetry is deliberate: the profile is the one
// identity a store is also given by the policy path, so it is worth exercising in
// the shape the kernel really produces, and the rest are opaque to storage by
// design -- an adapter that behaved differently for a kernel-produced digest than
// for a well-formed literal would be reading them.
func PublicationFixture(
	t *testing.T, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PublicationVersion,
) ports.Publication {
	t.Helper()
	label := string(target) + ":" + publicationVersionLabel(version)
	return ports.Publication{
		TenantID:   tenant,
		CustomerID: customer,
		Target:     target,
		Version:    version,

		// The policy version deliberately differs from the publication version, so a
		// store that confused the two would fail rather than coincidentally pass.
		PolicyVersion: ports.PolicyVersion(version) + 10,

		ProfileID:            profileIdentity(t, label),
		AssessmentID:         semantic.AssessmentID(digestLiteral("assessment:" + label)),
		CheckpointArtifactID: semantic.CheckpointArtifactID(digestLiteral("checkpoint:" + label)),
		SemanticRunID:        semantic.SemanticRunID(digestLiteral("run:" + label)),
		ExecutionID:          semantic.ExecutionID(digestLiteral("execution:" + label)),
	}
}

func publicationVersionLabel(version ports.PublicationVersion) string {
	return versionLabel(ports.PolicyVersion(version))
}

// digestLiteral builds a well-formed, distinct digest string for a label.
//
// It hashes rather than pads, so every value is the right shape and length and no
// two labels collide. A store must treat these identically to kernel-produced
// digests: they are opaque strings to storage, and an adapter that behaved
// differently for one than the other would be interpreting them.
func digestLiteral(label string) string {
	sum := sha256.Sum256([]byte(label))
	return "sha256:" + hex.EncodeToString(sum[:])
}
