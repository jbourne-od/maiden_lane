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
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

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

	executions     map[executionKey]*executionEntry
	executionOrder []executionKey

	// claimCursor is the index before which every execution is terminal, so
	// claiming does not rescan finished history on every poll.
	claimCursor int

	// Policies are append-only. policyVersions holds each target's current
	// version so appending does not have to scan for it, and so "no policy" is a
	// missing entry rather than an absence inferred from a scan finding nothing.
	policies       map[policyKey]ports.TargetPolicy
	policyVersions map[targetKey]ports.PolicyVersion

	// Publications are append-only for the same reason, and hold their current
	// version for the same two: appending does not scan for it, and a target that
	// has never been published to is a missing entry rather than an absence
	// inferred from a scan. The highest version is what is published; every lower
	// one is superseded and stays readable, which is what makes an old decision
	// explainable.
	publications        map[publicationKey]ports.Publication
	publicationVersions map[targetKey]ports.PublicationVersion
}

// NewStore returns an empty store ready for concurrent use.
func NewStore() *Store {
	return &Store{
		plans:          map[planKey]ports.PlanRecord{},
		executions:     map[executionKey]*executionEntry{},
		policies:       map[policyKey]ports.TargetPolicy{},
		policyVersions: map[targetKey]ports.PolicyVersion{},

		publications:        map[publicationKey]ports.Publication{},
		publicationVersions: map[targetKey]ports.PublicationVersion{},
	}
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

// executionKey scopes a stored execution by tenant, for the same reason plans
// are keyed that way: an unscoped read is not expressible.
type executionKey struct {
	tenant      ports.TenantID
	executionID semantic.ExecutionID
}

// executionEntry is one queued or completed execution.
//
// leaseExpiry is operational state. It observes wall-clock time, which is why it
// lives here and never reaches the semantic kernel: nothing about when an
// execution ran may influence what it computed.
type executionEntry struct {
	request       ports.ExecutionRequest
	status        ports.ExecutionStatus
	result        *ports.ExecutionResult
	failureReason string
	leaseExpiry   time.Time
}

var _ ports.ExecutionStore = (*Store)(nil)

// ErrNotQueued reports an execution that is absent, or already terminal and
// therefore no longer accepting an outcome. It is distinct from
// ErrIncompleteRecord, which means the caller's argument was malformed: a caller
// needs to tell "you gave me nonsense" from "that is already decided".
var ErrNotQueued = errors.New("memory: execution is absent or already terminal")

// ErrUnusableInput reports a request whose pinned input cannot be executed.
// Accepting one would promise work the store cannot deliver: every later claim
// and read would fail on the same input.
var ErrUnusableInput = errors.New("memory: execution request has no usable pinned input")

// terminal reports whether a status is a final outcome.
//
// Completing with a non-terminal status is refused rather than stored, because
// such a row still matches the claim predicate while carrying a result: the
// execution would be re-run forever and a read would report the
// pending-with-result shape ExecutionRecord documents as impossible.
func terminal(status ports.ExecutionStatus) bool {
	return status == ports.ExecutionSucceeded || status == ports.ExecutionFailed
}

// usableInput reports whether a pinned input can actually be executed. A zero
// state has no lineage and no digest, so nothing downstream can bind it.
func usableInput(input ports.ExecutionInput) bool {
	return input.InitialState.Digest() != "" && input.World.ID() != "" &&
		input.ExecutorIdentity != (semantic.ExecutorIdentity{}) && input.Policy != 0
}

// Enqueue stores a pending execution, idempotently on its derived identity.
func (s *Store) Enqueue(ctx context.Context, request ports.ExecutionRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if request.TenantID == "" || request.ExecutionID == "" {
		return false, ErrIncompleteRecord
	}
	if !usableInput(request.Input) {
		return false, ErrUnusableInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := executionKey{tenant: request.TenantID, executionID: request.ExecutionID}
	if _, present := s.executions[key]; present {
		// ExecutionID is derived from the semantic request, so an existing entry
		// under this key is necessarily the same execution.
		return false, nil
	}
	s.executions[key] = &executionEntry{request: request, status: ports.ExecutionPending}
	// Insertion order is kept explicitly rather than relying on map iteration,
	// so claiming is predictable instead of arbitrary.
	s.executionOrder = append(s.executionOrder, key)
	return true, nil
}

// Claim leases the oldest available execution.
//
// Available means pending, or running with an expired lease. Reclaiming is safe
// because execution is deterministic: a second attempt reproduces byte-identical
// artifacts, so at-least-once delivery cannot produce a divergent result.
func (s *Store) Claim(ctx context.Context, lease time.Duration) (ports.ExecutionRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ExecutionRequest{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()

	// The cursor matters because of this build's default configuration: an
	// in-process worker polls continuously, so rescanning from index zero would
	// make every poll cost proportional to all history ever enqueued, under the
	// write lock. Terminal entries at the head can never become claimable
	// again, so the cursor advances past them permanently.
	for s.claimCursor < len(s.executionOrder) {
		entry, present := s.executions[s.executionOrder[s.claimCursor]]
		if present && !terminal(entry.status) {
			break
		}
		s.claimCursor++
	}

	for _, key := range s.executionOrder[s.claimCursor:] {
		entry, present := s.executions[key]
		if !present || terminal(entry.status) {
			continue
		}
		claimable := entry.status == ports.ExecutionPending ||
			(entry.status == ports.ExecutionRunning && now.After(entry.leaseExpiry))
		if !claimable {
			continue
		}
		entry.status = ports.ExecutionRunning
		entry.leaseExpiry = now.Add(lease)
		return entry.request, true, nil
	}
	return ports.ExecutionRequest{}, false, nil
}

// Complete stores the result and takes the execution out of the queue.
func (s *Store) Complete(ctx context.Context, result ports.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.TenantID == "" || result.ExecutionID == "" {
		return ErrIncompleteRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !terminal(result.Status) {
		return fmt.Errorf("%w: %q is not a terminal status", ErrIncompleteRecord, result.Status)
	}

	entry, present := s.executions[executionKey{tenant: result.TenantID, executionID: result.ExecutionID}]
	if !present {
		return ErrNotQueued
	}
	if terminal(entry.status) {
		// Already decided. A late Complete from an abandoned attempt must not
		// resurrect a failed execution or overwrite another attempt's outcome.
		return ErrNotQueued
	}
	stored := cloneExecutionResult(result)
	entry.result = &stored
	entry.status = result.Status
	entry.failureReason = ""
	entry.leaseExpiry = time.Time{}
	return nil
}

// Fail records that an execution could not be attempted.
//
// This is not a semantic rejection. A deterministic refusal is a completed
// execution whose result carries a typed failure, because the computation
// produced a real answer.
func (s *Store) Fail(ctx context.Context, tenant ports.TenantID, executionID semantic.ExecutionID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, present := s.executions[executionKey{tenant: tenant, executionID: executionID}]
	if !present {
		return ErrNotQueued
	}
	if terminal(entry.status) {
		// The lease-expiry argument that makes at-least-once safe covers a
		// duplicate Complete, because a second attempt reproduces the same
		// artifacts. It does not cover a late Fail: that would replace a real
		// outcome with an operational one and destroy the result.
		return ErrNotQueued
	}
	entry.status = ports.ExecutionFailed
	entry.failureReason = reason
	entry.result = nil
	entry.leaseExpiry = time.Time{}
	return nil
}

// Get reports the execution for this tenant, or absence.
func (s *Store) Get(ctx context.Context, tenant ports.TenantID, executionID semantic.ExecutionID) (ports.ExecutionRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ExecutionRecord{}, false, err
	}
	if tenant == "" || executionID == "" {
		return ports.ExecutionRecord{}, false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, present := s.executions[executionKey{tenant: tenant, executionID: executionID}]
	if !present {
		return ports.ExecutionRecord{}, false, nil
	}

	record := ports.ExecutionRecord{
		Request:       entry.request,
		Status:        entry.status,
		FailureReason: entry.failureReason,
	}
	if entry.result != nil {
		// Copied on the way out as well as in. The sealed bytes are the
		// artifact; a caller that could mutate them would corrupt every later
		// reader's view of something that is supposed to be immutable.
		stored := cloneExecutionResult(*entry.result)
		record.Result = &stored
	}
	return record, true, nil
}

// cloneExecutionResult deep-copies the parts of a result whose interior a
// caller can reach: the byte slices holding sealed artifacts, and the closed
// code slices beside them.
func cloneExecutionResult(result ports.ExecutionResult) ports.ExecutionResult {
	cloned := result
	cloned.AcceptedRules = slices.Clone(result.AcceptedRules)

	cloned.Checkpoints = make([]ports.SealedCheckpoint, len(result.Checkpoints))
	for i, checkpoint := range result.Checkpoints {
		cloned.Checkpoints[i] = checkpoint
		cloned.Checkpoints[i].CanonicalBytes = bytes.Clone(checkpoint.CanonicalBytes)
		cloned.Checkpoints[i].InvariantResultCanonicalBytes =
			bytes.Clone(checkpoint.InvariantResultCanonicalBytes)
	}

	cloned.Assessments = make([]ports.StoredAssessment, len(result.Assessments))
	for i, assessment := range result.Assessments {
		cloned.Assessments[i] = assessment
		cloned.Assessments[i].CanonicalBytes = bytes.Clone(assessment.CanonicalBytes)
		cloned.Assessments[i].MissingRequirements = slices.Clone(assessment.MissingRequirements)
	}

	if result.Failure != nil {
		failure := *result.Failure
		cloned.Failure = &failure
	}
	return cloned
}
