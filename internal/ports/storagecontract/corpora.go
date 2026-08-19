package storagecontract

import (
	"context"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunCorpusStoreContract asserts every behaviour a ports.CorpusStore must exhibit.
//
// The weight is on identity preservation rather than on round-tripping fields. A corpus
// cannot be serialized, so a durable adapter rebuilds it through the kernel's
// constructors — which means a store can return something well formed that is not what
// was put in. The identity is what detects that, and a comparison pins it, so a corpus
// returned under the wrong name would leave every comparison against it describing a set
// of cases nothing can reconstruct.
func RunCorpusStoreContract(t *testing.T, newStore func(*testing.T) ports.CorpusStore) {
	t.Helper()

	t.Run("returns a stored corpus under its own identity", func(t *testing.T) {
		store := newStore(t)
		record := CorpusRecordFixture(t, "acme", 3)

		if err := store.PutCorpus(t.Context(), record); err != nil {
			t.Fatalf("PutCorpus: %v", err)
		}
		got, found, err := store.GetCorpus(t.Context(), "acme", record.CorpusID)
		if err != nil || !found {
			t.Fatalf("GetCorpus: found=%t err=%v", found, err)
		}
		if got.CorpusID != record.CorpusID {
			t.Fatalf("corpus ID = %s, want %s", got.CorpusID, record.CorpusID)
		}
		// The rebuilt corpus must reproduce the identity, which is the assertion that
		// makes storage verifiable rather than merely lossless-looking.
		if got.Corpus.ID() != record.CorpusID {
			t.Fatalf("the returned corpus identifies as %s, want %s",
				got.Corpus.ID(), record.CorpusID)
		}
	})

	t.Run("preserves every case exactly", func(t *testing.T) {
		store := newStore(t)
		record := CorpusRecordFixture(t, "acme", 4)
		if err := store.PutCorpus(t.Context(), record); err != nil {
			t.Fatalf("PutCorpus: %v", err)
		}
		got, _, err := store.GetCorpus(t.Context(), "acme", record.CorpusID)
		if err != nil {
			t.Fatalf("GetCorpus: %v", err)
		}

		if got.Corpus.Len() != record.Corpus.Len() {
			t.Fatalf("cases = %d, want %d", got.Corpus.Len(), record.Corpus.Len())
		}
		if got.Corpus.SchemaDigest() != record.Corpus.SchemaDigest() {
			t.Fatalf("schema digest = %s, want %s",
				got.Corpus.SchemaDigest(), record.Corpus.SchemaDigest())
		}
		// Case by case, in canonical order, so a store that returned the right number of
		// the wrong cases is visible. The identity check above would catch it too; this
		// says which case moved.
		want := record.Corpus.Digests()
		for i, digest := range got.Corpus.Digests() {
			if digest != want[i] {
				t.Fatalf("case %d = %s, want %s", i, digest, want[i])
			}
		}
		// The states themselves must come back, not merely their digests: running a side
		// over a corpus needs the cases, and a corpus of digests could only be compared.
		for i := 0; i < got.Corpus.Len(); i++ {
			state, ok := got.Corpus.Case(i)
			if !ok {
				t.Fatalf("case %d is missing", i)
			}
			if state.Digest() != want[i] {
				t.Fatalf("case %d state digest = %s, want %s", i, state.Digest(), want[i])
			}
			if len(state.Entities()) == 0 {
				t.Fatalf("case %d came back with no entities", i)
			}
		}
	})

	t.Run("reports an unknown corpus as absent rather than failing", func(t *testing.T) {
		store := newStore(t)
		_, found, err := store.GetCorpus(t.Context(), "acme",
			semantic.CorpusID(digestLiteral("nothing")))
		if err != nil {
			t.Fatalf("GetCorpus on an unknown corpus: %v", err)
		}
		if found {
			t.Fatal("an unknown corpus was reported as present")
		}
	})

	t.Run("stores the same corpus twice idempotently", func(t *testing.T) {
		store := newStore(t)
		record := CorpusRecordFixture(t, "acme", 3)

		// Content-derived identity means a repeat is necessarily the same corpus, so it
		// must not be a conflict. A caller that does not know whether its write landed
		// has to be able to repeat it.
		if err := store.PutCorpus(t.Context(), record); err != nil {
			t.Fatalf("first PutCorpus: %v", err)
		}
		if err := store.PutCorpus(t.Context(), record); err != nil {
			t.Fatalf("second PutCorpus: %v", err)
		}
		got, found, err := store.GetCorpus(t.Context(), "acme", record.CorpusID)
		if err != nil || !found {
			t.Fatalf("GetCorpus: found=%t err=%v", found, err)
		}
		if got.Corpus.Len() != record.Corpus.Len() {
			t.Fatalf("a repeat changed the corpus: %d cases, want %d",
				got.Corpus.Len(), record.Corpus.Len())
		}
	})

	t.Run("keeps a different corpus separate rather than replacing one", func(t *testing.T) {
		store := newStore(t)
		smaller := CorpusRecordFixture(t, "acme", 2)
		larger := CorpusRecordFixture(t, "acme", 3)
		if smaller.CorpusID == larger.CorpusID {
			t.Fatal("the fixture produced one corpus twice")
		}

		for _, record := range []ports.CorpusRecord{smaller, larger} {
			if err := store.PutCorpus(t.Context(), record); err != nil {
				t.Fatalf("PutCorpus: %v", err)
			}
		}
		// Editing a corpus is impossible: different cases are a different corpus, and
		// the earlier one must stay readable because comparisons against it pin its
		// identity. Deleting or replacing it would leave those naming nothing.
		for _, record := range []ports.CorpusRecord{smaller, larger} {
			got, found, err := store.GetCorpus(t.Context(), "acme", record.CorpusID)
			if err != nil || !found {
				t.Fatalf("GetCorpus(%s): found=%t err=%v", record.CorpusID, found, err)
			}
			if got.Corpus.Len() != record.Corpus.Len() {
				t.Fatalf("corpus %s has %d cases, want %d",
					record.CorpusID, got.Corpus.Len(), record.Corpus.Len())
			}
		}
	})

	t.Run("isolates tenants", func(t *testing.T) {
		store := newStore(t)
		record := CorpusRecordFixture(t, "acme", 2)
		if err := store.PutCorpus(t.Context(), record); err != nil {
			t.Fatalf("PutCorpus: %v", err)
		}
		// Another tenant's corpus is absent, never an error: distinguishing the two would
		// leak its existence to a caller with no right to know it.
		_, found, err := store.GetCorpus(t.Context(), "globex", record.CorpusID)
		if err != nil {
			t.Fatalf("GetCorpus for another tenant: %v", err)
		}
		if found {
			t.Fatal("a corpus leaked across tenants")
		}
	})

	t.Run("refuses an incomplete record", func(t *testing.T) {
		store := newStore(t)
		complete := CorpusRecordFixture(t, "acme", 2)

		for _, test := range []struct {
			name   string
			mutate func(*ports.CorpusRecord)
		}{
			{"no tenant", func(r *ports.CorpusRecord) { r.TenantID = "" }},
			{"no identity", func(r *ports.CorpusRecord) { r.CorpusID = "" }},
			// The record's identity must be the corpus's own. Storing one under any
			// other name would make every later read return cases under a name the
			// kernel never assigned them.
			{"an identity that is not the corpus's", func(r *ports.CorpusRecord) {
				r.CorpusID = semantic.CorpusID(digestLiteral("elsewhere"))
			}},
			{"no corpus", func(r *ports.CorpusRecord) { r.Corpus = semantic.Corpus{} }},
		} {
			t.Run(test.name, func(t *testing.T) {
				record := complete
				test.mutate(&record)
				if err := store.PutCorpus(t.Context(), record); err == nil {
					t.Fatalf("PutCorpus accepted a record with %s", test.name)
				}
			})
		}
	})

	t.Run("stops on a cancelled context", func(t *testing.T) {
		store := newStore(t)
		record := CorpusRecordFixture(t, "acme", 2)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := store.PutCorpus(ctx, record); err == nil {
			t.Fatal("PutCorpus succeeded on a cancelled context")
		}
		if _, _, err := store.GetCorpus(ctx, "acme", record.CorpusID); err == nil {
			t.Fatal("GetCorpus succeeded on a cancelled context")
		}
	})
}

// CorpusRecordFixture builds a corpus of distinct real cases through the kernel, so a
// store is exercised with corpora the kernel actually produced rather than with
// fabricated identities.
func CorpusRecordFixture(t *testing.T, tenant ports.TenantID, cases int) ports.CorpusRecord {
	t.Helper()
	corpus := CorpusFixture(t, cases)
	return ports.CorpusRecord{TenantID: tenant, CorpusID: corpus.ID(), Corpus: corpus}
}

// CorpusFixture builds a corpus of distinct cases sharing one schema, which the kernel
// requires: a corpus whose cases differed in schema could never be replayed under any
// plan, so no store will ever be handed one.
func CorpusFixture(t *testing.T, cases int) semantic.Corpus {
	t.Helper()

	schema, err := semantic.NewSchema([]semantic.EntityDeclaration{
		{Kind: "driver", Fields: []semantic.FieldDeclaration{
			{Name: "assignment_key", Kind: semantic.ValueString},
			{Name: "hos_elapsed_hours", Kind: semantic.ValueInt64},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	lineage, err := semantic.NewInputLineageID("maiden-lane.sanitized-fixture", "corpus")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}

	states := make([]semantic.State, 0, cases)
	for i := 0; i < cases; i++ {
		key, err := semantic.NewStringValue("case-" + string(rune('a'+i)))
		if err != nil {
			t.Fatalf("NewStringValue: %v", err)
		}
		entity, err := semantic.NewEntity(semantic.EntityRef{
			Kind: "driver",
			ID:   semantic.SourceEntityID(lineage, "driver", "A"),
		}, map[semantic.FieldName]semantic.Value{
			"assignment_key":    key,
			"hos_elapsed_hours": semantic.NewInt64Value(int64(i)),
		})
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		state, err := semantic.NewState(schema, lineage, []semantic.Entity{entity}, nil)
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		states = append(states, state)
	}

	corpus, err := semantic.NewCorpus(states)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	return corpus
}
