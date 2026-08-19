package ports

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CorpusRecord is one replay corpus retained for comparison.
//
// It carries the kernel value rather than a serialized form, like every other record
// here. A semantic.Corpus is immutable and its accessors return deep copies, which is
// what lets an adapter store and return one by ordinary assignment.
//
// There is no version and no tenant-scoped mutation, because a corpus cannot be edited:
// its identity IS its contents, so changing a case produces a different corpus. Storage
// is therefore idempotent on CorpusID in exactly the way plan storage is, and for exactly
// the same reason — the same identity always denotes the same artifact.
type CorpusRecord struct {
	TenantID TenantID
	CorpusID semantic.CorpusID

	// Corpus is the kernel value. A durable adapter cannot serialize it directly, for
	// the same reason it cannot serialize a Compilation: the kernel's canonical encoders
	// are one-way and there is no decoder. It therefore stores the cases in its own
	// encoding, rebuilds them through the kernel's constructors on read, and requires
	// the re-derived CorpusID to equal the one it stored. Storage consequently cannot
	// return a corpus under an identity it did not actually produce.
	Corpus semantic.Corpus
}

// CorpusStore persists replay corpora.
//
// Every method is tenant scoped by signature, and there is deliberately no unscoped
// lookup, so a handler cannot forget to filter because no such call exists.
type CorpusStore interface {
	// PutCorpus stores a corpus for its tenant.
	//
	// Corpus identity is content derived, so storing the same cases twice is idempotent
	// rather than a conflict: the same identity always denotes the same corpus. It
	// returns an error only for an incomplete record or a cancelled context.
	//
	// There is no update and no delete. A corpus that should have contained different
	// cases is a different corpus, and the one already stored must remain readable
	// because comparisons made against it pin its identity — deleting it would leave
	// those comparisons naming a set of cases nothing can reconstruct.
	PutCorpus(context.Context, CorpusRecord) error

	// GetCorpus reports the corpus for this tenant. A corpus belonging to another
	// tenant is reported as absent, never as an error: distinguishing the two would
	// leak its existence to a caller with no right to know.
	GetCorpus(context.Context, TenantID, semantic.CorpusID) (CorpusRecord, bool, error)
}
