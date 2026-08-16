// Package memory implements the application's storage ports in process.
//
// DURABILITY: this adapter keeps everything in a Go map. Records are lost when
// the process exits, and two replicas share nothing. That is a real limitation,
// stated rather than hidden: it is honest for a single-process deployment and
// for tests, and it is why the interfaces live in internal/ports so a durable
// PostgreSQL or S3 adapter can replace this one without touching internal/app
// or internal/semantic.
//
// The adapter stores the kernel's immutable values directly. It performs no
// serialization, so no storage encoding can become a second source of semantic
// meaning. A durable adapter must preserve that property by persisting the
// kernel's canonical bytes verbatim rather than inventing a schema for them.
package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ErrIncompleteRecord reports a record missing its tenant or artifact
// identity. It is a programming error at the call site, not hostile input:
// handlers establish both before storing anything.
var ErrIncompleteRecord = errors.New("memory: record is missing its tenant or artifact identity")

// planKey scopes a stored plan by tenant. Tenancy is part of the key rather
// than a filter applied afterwards, so an unscoped read is not expressible.
type planKey struct {
	tenant ports.TenantID
	planID semantic.PlanID
}

// Store is a concurrency-safe in-process implementation of the storage ports.
type Store struct {
	mu    sync.RWMutex
	plans map[planKey]ports.PlanRecord
}

// NewStore returns an empty store ready for concurrent use.
func NewStore() *Store {
	return &Store{plans: map[planKey]ports.PlanRecord{}}
}

var _ ports.PlanStore = (*Store)(nil)

// PutPlan stores a plan for its tenant, idempotently.
func (s *Store) PutPlan(ctx context.Context, record ports.PlanRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.TenantID == "" || record.PlanID == "" {
		return ErrIncompleteRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := planKey{tenant: record.TenantID, planID: record.PlanID}
	// Plan identity is content derived, so an existing entry under the same
	// key already holds the same plan. Keeping the first write makes repeated
	// submission idempotent without comparing whole artifacts.
	if _, present := s.plans[key]; present {
		return nil
	}
	s.plans[key] = record
	return nil
}

// GetPlan reports the plan for this tenant, or absence.
func (s *Store) GetPlan(ctx context.Context, tenant ports.TenantID, planID semantic.PlanID) (ports.PlanRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.PlanRecord{}, false, err
	}
	if tenant == "" || planID == "" {
		return ports.PlanRecord{}, false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	record, found := s.plans[planKey{tenant: tenant, planID: planID}]
	if !found {
		return ports.PlanRecord{}, false, nil
	}
	// The record is returned by value. Its Schema and Compilation are kernel
	// values whose getters clone, so a caller cannot reach the stored copy
	// through them; there is no additional defensive copy to make here.
	return record, true, nil
}
