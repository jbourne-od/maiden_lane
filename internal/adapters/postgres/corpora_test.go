package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

func TestStoreSatisfiesTheCorpusStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunCorpusStoreContract(t, func(t *testing.T) ports.CorpusStore {
		return freshCorpusStore(t, url)
	})
}

// freshCorpusStore returns a store over an empty corpora table. The contract requires each
// subtest to start empty, and truncating is how that holds for a database that outlives
// the process.
func freshCorpusStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	execute(t, url, `TRUNCATE corpora`, nil)
	return store
}

// THE PROPERTY THIS SLICE RESTS ON: a corpus cannot be serialized, so what comes back is
// rebuilt from stored parts. Requiring the rebuilt identity to equal the one the row is
// filed under is what stops a mangled row being returned as a corpus the kernel never
// produced — and comparisons pin that identity, so a corpus whose name does not describe
// its contents would make every comparison against it describe a set of cases nothing can
// reconstruct.
//
// The dangerous case is the last one: a document that is syntactically fine, decodes
// cleanly, and rebuilds into a perfectly valid corpus of DIFFERENT cases. Nothing about
// the row looks wrong.
func TestCorruptedCorpusRowsFailClosed(t *testing.T) {
	url := requireDatabase(t)

	for _, test := range []struct {
		name     string
		corrupt  string
		argument func(t *testing.T) []byte
	}{
		{
			// A document that is valid JSON and describes no corpus. There is
			// deliberately no "syntactically unreadable document" case: the column is
			// jsonb, so PostgreSQL rejects malformed JSON on write and that corruption
			// is unrepresentable in this table. The plan and execution tables store
			// bytea and do test it, which is the right difference — the column type is
			// doing work here that application code has to do there.
			name:    "a document describing no corpus",
			corrupt: `UPDATE corpora SET document = '{"cases":[]}'::jsonb`,
		},
		{
			name:    "a case removed",
			corrupt: `UPDATE corpora SET document = jsonb_set(document, '{cases}', (document->'cases') - 0)`,
		},
		{
			name: "a different but perfectly valid corpus",
			// The one that matters. It decodes, it rebuilds, every case is a real state,
			// and it is not the corpus this row is filed under.
			corrupt:  `UPDATE corpora SET document = $1`,
			argument: func(t *testing.T) []byte { return encodeCorpusFor(t, 5) },
		},
		{
			name: "schema digest altered",
			corrupt: `UPDATE corpora SET schema_digest = 'sha256:` +
				`1111111111111111111111111111111111111111111111111111111111111111'`,
		},
		{
			name:    "case count altered",
			corrupt: `UPDATE corpora SET case_count = 99`,
		},
		{
			name:    "storage format from a build this one does not understand",
			corrupt: `UPDATE corpora SET format = format + 1`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := freshCorpusStore(t, url)
			record := storagecontract.CorpusRecordFixture(t, "acme", 3)
			if err := store.PutCorpus(context.Background(), record); err != nil {
				t.Fatalf("PutCorpus: %v", err)
			}

			var argument []byte
			if test.argument != nil {
				argument = test.argument(t)
			}
			execute(t, url, test.corrupt, argument)

			_, found, err := store.GetCorpus(context.Background(), "acme", record.CorpusID)
			if err == nil {
				t.Fatalf("a corrupted corpus row was returned (found=%t) instead of refused", found)
			}
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("error = %v, want ErrIntegrity", err)
			}
			if found {
				t.Fatal("a refused read still reported the corpus as present")
			}
		})
	}
}

// encodeCorpusFor produces the stored document for a corpus of a different size, so a row
// can be replaced with one that is entirely valid and entirely wrong.
func encodeCorpusFor(t *testing.T, cases int) []byte {
	t.Helper()
	document, err := encodeCorpus(storagecontract.CorpusFixture(t, cases))
	if err != nil {
		t.Fatalf("encodeCorpus: %v", err)
	}
	return document
}
