package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

var _ ports.ComparisonStore = (*Store)(nil)

// PutComparison stores a comparison for its tenant, idempotently on its content identity.
//
// ON CONFLICT DO NOTHING rather than an upsert, because a comparison cannot be edited:
// the identity is derived from the question, so a row already present under this
// identity already asks it. An upsert would express a change that cannot exist.
//
// The head row and its correspondences are written in one transaction. They are one
// artifact split across two tables purely because a correspondence set is a list, and a
// comparison that committed with some of its correspondences missing would rehydrate
// into a policy nobody authored: a smaller mapping is a valid policy, so nothing later
// would notice.
func (s *Store) PutComparison(
	ctx context.Context, tenant ports.TenantID, comparison semantic.Comparison,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant == "" || comparison.ID() == "" {
		return ErrIncompleteRecord
	}
	record := ports.ProjectComparison(tenant, comparison)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: comparison could not be written: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`INSERT INTO comparisons
		     (tenant_id, comparison_id, baseline_checkpoint_id, candidate_checkpoint_id,
		      profile_id, world_id, corpus_id, policy_id, baseline_plan_id, candidate_plan_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (tenant_id, comparison_id) DO NOTHING`,
		string(record.TenantID), string(record.ComparisonID),
		string(record.Baseline), string(record.Candidate),
		string(record.Profile), string(record.World), string(record.Corpus),
		string(record.PolicyID), string(record.BaselinePlan), string(record.CandidatePlan))
	if err != nil {
		return fmt.Errorf("postgres: comparison could not be written: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already stored. The correspondences are not rewritten, because the identity
		// already present derives from them: a row under this identity holds this
		// mapping.
		return nil
	}

	for position, correspondence := range record.Correspondences {
		if _, err := tx.Exec(ctx,
			`INSERT INTO comparison_correspondences
			     (tenant_id, comparison_id, position,
			      baseline_checkpoint_id, candidate_checkpoint_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			string(record.TenantID), string(record.ComparisonID), position,
			string(correspondence.Baseline), string(correspondence.Candidate)); err != nil {
			return fmt.Errorf("postgres: comparison correspondence could not be written: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: comparison could not be committed: %w", err)
	}
	return nil
}

// GetComparison reports the stored projection for this tenant, or absence.
//
// Unlike a corpus, nothing is rebuilt here. A comparison's policy is derived from two
// compiled plans, and a ComparisonStore is not guaranteed to have plans at all, so the
// authentication that other reads perform in the adapter happens instead in
// app.RehydrateComparison, which has both stores. What this returns is a projection and
// is documented as carrying no authority.
func (s *Store) GetComparison(
	ctx context.Context, tenant ports.TenantID, comparisonID semantic.ComparisonID,
) (ports.ComparisonRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ComparisonRecord{}, false, err
	}
	if tenant == "" || comparisonID == "" {
		return ports.ComparisonRecord{}, false, nil
	}

	record := ports.ComparisonRecord{TenantID: tenant, ComparisonID: comparisonID}
	err := s.pool.QueryRow(ctx,
		`SELECT baseline_checkpoint_id, candidate_checkpoint_id, profile_id, world_id,
		        corpus_id, policy_id, baseline_plan_id, candidate_plan_id
		 FROM comparisons WHERE tenant_id = $1 AND comparison_id = $2`,
		string(tenant), string(comparisonID)).Scan(
		&record.Baseline, &record.Candidate, &record.Profile, &record.World,
		&record.Corpus, &record.PolicyID, &record.BaselinePlan, &record.CandidatePlan)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ComparisonRecord{}, false, nil
	}
	if err != nil {
		return ports.ComparisonRecord{}, false,
			fmt.Errorf("postgres: comparison could not be read: %w", err)
	}

	// Ordered explicitly. Rows come back in no particular order otherwise, and the
	// record is documented as carrying the policy's canonical order so that two adapters
	// describe one comparison identically.
	rows, err := s.pool.Query(ctx,
		`SELECT baseline_checkpoint_id, candidate_checkpoint_id
		 FROM comparison_correspondences
		 WHERE tenant_id = $1 AND comparison_id = $2
		 ORDER BY position`,
		string(tenant), string(comparisonID))
	if err != nil {
		return ports.ComparisonRecord{}, false,
			fmt.Errorf("postgres: comparison correspondences could not be read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var correspondence ports.ComparisonCorrespondence
		if err := rows.Scan(&correspondence.Baseline, &correspondence.Candidate); err != nil {
			return ports.ComparisonRecord{}, false,
				fmt.Errorf("postgres: comparison correspondence could not be read: %w", err)
		}
		record.Correspondences = append(record.Correspondences, correspondence)
	}
	if err := rows.Err(); err != nil {
		return ports.ComparisonRecord{}, false,
			fmt.Errorf("postgres: comparison correspondences could not be read: %w", err)
	}
	if len(record.Correspondences) == 0 {
		// A comparison with no correspondences cannot exist: the kernel refuses to build
		// a policy without at least one, and the write is transactional. Reaching here
		// means the rows were removed underneath a head row that still names them, so
		// the mapping this comparison asserts is no longer recoverable.
		return ports.ComparisonRecord{}, false, fmt.Errorf(
			"%w: comparison has no stored correspondences", ErrIntegrity)
	}
	return record, true, nil
}
