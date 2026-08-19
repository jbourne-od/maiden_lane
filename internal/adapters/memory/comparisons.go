package memory

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

var _ ports.ComparisonStore = (*Store)(nil)

// comparisonKey scopes a comparison by tenant and identity, so an unscoped lookup is not
// expressible.
type comparisonKey struct {
	tenant       ports.TenantID
	comparisonID semantic.ComparisonID
}

// PutComparison stores a comparison for its tenant, idempotently.
func (s *Store) PutComparison(
	ctx context.Context, tenant ports.TenantID, comparison semantic.Comparison,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant == "" || comparison.ID() == "" {
		// The zero Comparison has no identity. Every exported struct here has a
		// constructible zero value, so absence is checked rather than assumed away.
		return ErrIncompleteRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := comparisonKey{tenant: tenant, comparisonID: comparison.ID()}
	// Comparison identity is content derived, so an existing entry under this key
	// already holds the same question. Keeping the first write makes repeated submission
	// idempotent without comparing whole artifacts.
	if _, present := s.comparisons[key]; present {
		return nil
	}
	s.comparisons[key] = ports.ProjectComparison(tenant, comparison)
	return nil
}

// GetComparison reports the stored projection for this tenant, or absence.
func (s *Store) GetComparison(
	ctx context.Context, tenant ports.TenantID, comparisonID semantic.ComparisonID,
) (ports.ComparisonRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ComparisonRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, present := s.comparisons[comparisonKey{tenant: tenant, comparisonID: comparisonID}]
	if !present {
		return ports.ComparisonRecord{}, false, nil
	}
	// Cloned, or a caller writing to the returned correspondences would write into the
	// store's own copy. An in-memory adapter is the only one where that is possible, and
	// the contract must not hold differently for it.
	return record.Clone(), true, nil
}
