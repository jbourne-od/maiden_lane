package memory

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

var _ ports.CorpusStore = (*Store)(nil)

// corpusKey scopes a corpus by tenant and identity, so an unscoped lookup is not
// expressible.
type corpusKey struct {
	tenant   ports.TenantID
	corpusID semantic.CorpusID
}

// PutCorpus stores a corpus for its tenant, idempotently.
func (s *Store) PutCorpus(ctx context.Context, record ports.CorpusRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.TenantID == "" || record.CorpusID == "" {
		return ErrIncompleteRecord
	}
	if record.Corpus.ID() != record.CorpusID {
		// A record whose identity is not its corpus's would be returned under a name
		// the kernel never produced for those cases.
		return ErrIncompleteRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := corpusKey{tenant: record.TenantID, corpusID: record.CorpusID}
	// Corpus identity is content derived, so an existing entry under this key already
	// holds the same cases. Keeping the first write makes repeated submission idempotent
	// without comparing whole artifacts.
	if _, present := s.corpora[key]; present {
		return nil
	}
	s.corpora[key] = record
	return nil
}

// GetCorpus reports the corpus for this tenant, or absence.
func (s *Store) GetCorpus(
	ctx context.Context, tenant ports.TenantID, corpusID semantic.CorpusID,
) (ports.CorpusRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.CorpusRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, present := s.corpora[corpusKey{tenant: tenant, corpusID: corpusID}]
	return record, present, nil
}
