package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

var _ ports.CorpusStore = (*Store)(nil)

// PutCorpus stores a corpus for its tenant, idempotently on its content identity.
//
// ON CONFLICT DO NOTHING rather than an upsert, because a corpus cannot be edited: the
// identity is derived from the cases, so a row already present under this identity
// already holds these cases. An upsert would express a change that cannot exist.
func (s *Store) PutCorpus(ctx context.Context, record ports.CorpusRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.TenantID == "" || record.CorpusID == "" {
		return ErrIncompleteRecord
	}
	if record.Corpus.ID() != record.CorpusID {
		// Storing a corpus under an identity that is not its own would make every later
		// read return cases under a name the kernel never assigned them.
		return ErrIncompleteRecord
	}

	document, err := encodeCorpus(record.Corpus)
	if err != nil {
		return fmt.Errorf("postgres: corpus could not be encoded: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO corpora
		     (tenant_id, corpus_id, schema_digest, case_count, document, format)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (tenant_id, corpus_id) DO NOTHING`,
		string(record.TenantID), string(record.CorpusID),
		string(record.Corpus.SchemaDigest()), record.Corpus.Len(), document, storedFormat); err != nil {
		return fmt.Errorf("postgres: corpus could not be written: %w", err)
	}
	return nil
}

// GetCorpus reports the corpus for this tenant, rebuilding it through the kernel.
//
// The re-derived identity must equal the one the row is stored under. That is the whole
// guarantee: a corpus cannot be serialized, so what comes back is rebuilt from stored
// parts, and requiring the rebuilt identity to match is what stops a mangled row being
// returned as a corpus the kernel never produced. It is the same rule plan storage
// follows when it recompiles on read.
func (s *Store) GetCorpus(
	ctx context.Context, tenant ports.TenantID, corpusID semantic.CorpusID,
) (ports.CorpusRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.CorpusRecord{}, false, err
	}

	var (
		schemaDigest string
		caseCount    int
		document     []byte
		format       int
	)
	err := s.pool.QueryRow(ctx,
		`SELECT schema_digest, case_count, document, format FROM corpora
		 WHERE tenant_id = $1 AND corpus_id = $2`,
		string(tenant), string(corpusID)).Scan(&schemaDigest, &caseCount, &document, &format)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.CorpusRecord{}, false, nil
	}
	if err != nil {
		return ports.CorpusRecord{}, false, fmt.Errorf("postgres: corpus could not be read: %w", err)
	}
	if format != storedFormat {
		return ports.CorpusRecord{}, false, fmt.Errorf(
			"%w: corpus row uses storage format %d, this build understands %d",
			ErrIntegrity, format, storedFormat)
	}

	corpus, err := decodeCorpus(document)
	if err != nil {
		return ports.CorpusRecord{}, false, err
	}
	if corpus.ID() != corpusID {
		// The stored cases no longer produce the identity they are filed under. Refused
		// rather than returned, because a caller given this would hold a corpus whose
		// name does not describe its contents — and comparisons pin that name.
		return ports.CorpusRecord{}, false, fmt.Errorf(
			"%w: stored corpus does not reproduce its identity", ErrIntegrity)
	}
	// The projected columns are checked against the rebuilt corpus rather than trusted.
	// They exist so a reader can size work without decoding, and a column that disagreed
	// with the cases would be a second description of them able to mislead.
	if semantic.SchemaDigest(schemaDigest) != corpus.SchemaDigest() || caseCount != corpus.Len() {
		return ports.CorpusRecord{}, false, fmt.Errorf(
			"%w: corpus row columns disagree with its cases", ErrIntegrity)
	}

	return ports.CorpusRecord{TenantID: tenant, CorpusID: corpusID, Corpus: corpus}, true, nil
}
