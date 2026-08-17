package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// This file implements the execution queue.
//
// The integrity guarantee here is narrower than the one plans get, and the
// difference is worth stating. A stored plan is verified by recompiling it and
// comparing the identity that comes out, so storage cannot return a plan it did
// not actually reproduce. A sealed artifact cannot be re-derived that cheaply:
// reproducing it means re-executing. So what this adapter proves is the property
// storage actually owes — that it returns exactly the bytes it was given —
// using a hash it computes itself over the serialized blob.
//
// That hash is deliberately not a semantic digest. It is storage's own checksum
// over storage's own encoding, so this package never learns how the kernel
// derives an identity and cannot come to seem like a second source of one. A
// mismatch means the row changed after it was written, which is exactly the
// failure a checksum should catch.

var _ ports.ExecutionStore = (*Store)(nil)

// executionFormat is this codec's version. See schema.sql for why an execution
// needs one even though a plan's recompile-and-compare makes its own optional.
const executionFormat = 1

// ErrNotQueued reports an execution that is absent, or already terminal and so
// no longer accepting an outcome. Distinct from ErrIncompleteRecord, which means
// the caller's argument was malformed.
var ErrNotQueued = errors.New("postgres: execution is absent or already terminal")

// ErrUnusableInput reports a request whose pinned input cannot be executed.
var ErrUnusableInput = errors.New("postgres: execution request has no usable pinned input")

func terminalStatus(status ports.ExecutionStatus) bool {
	return status == ports.ExecutionSucceeded || status == ports.ExecutionFailed
}

func usableInput(input ports.ExecutionInput) bool {
	return input.InitialState.Digest() != "" && input.World.ID() != "" &&
		input.ExecutorIdentity != (semantic.ExecutorIdentity{}) && input.Policy != 0
}

// identityHash covers the request bytes together with the identity columns the
// row is stored under, so storage cannot alter an identity undetected.
//
// This is storage's own checksum over storage's own encoding, deliberately not a
// semantic digest: this package must never learn how the kernel derives
// identity. Re-deriving an ExecutionID needs the plan, which this store does not
// have; the worker does, and re-derives it there.
func identityHash(request ports.ExecutionRequest, encoded []byte) string {
	material := make([]byte, 0, len(encoded)+192)
	for _, part := range []string{
		string(request.TenantID), string(request.ExecutionID),
		string(request.RunID), string(request.PlanID),
	} {
		material = append(material, byte(len(part)))
		material = append(material, part...)
	}
	material = append(material, encoded...)
	return contentHash(material)
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

	encoded, err := encodeExecutionRequest(request)
	if err != nil {
		return false, err
	}
	// Round-trip before accepting. Storing a request that cannot be decoded
	// would return 202 for an execution that can never run and can never be
	// read, which is a promise the store cannot keep.
	if _, err := decodeExecutionRequest(request.TenantID, request.ExecutionID,
		request.RunID, request.PlanID, encoded); err != nil {
		return false, fmt.Errorf("%w: request does not survive its own encoding", ErrUnusableInput)
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO executions
			(tenant_id, execution_id, run_id, plan_id, status, format, request, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, execution_id) DO NOTHING`,
		string(request.TenantID), string(request.ExecutionID),
		string(request.RunID), string(request.PlanID),
		string(ports.ExecutionPending), executionFormat, encoded, identityHash(request, encoded))
	if err != nil {
		return false, fmt.Errorf("postgres: execution could not be enqueued: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Claim leases the oldest available execution.
//
// SKIP LOCKED is what makes several workers safe against one queue: a worker
// steps over rows another worker is already claiming instead of blocking on
// them, so the queue partitions rather than serializes.
func (s *Store) Claim(ctx context.Context, lease time.Duration) (ports.ExecutionRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ExecutionRequest{}, false, err
	}

	var (
		tenant, executionID, runID, planID, hash string
		format                                   int
		encoded                                  []byte
	)
	// The subquery selects and locks one candidate; the update claims it. Doing
	// both in one statement means no window exists in which a row is selected
	// but not yet claimed.
	err := s.pool.QueryRow(ctx, `
		UPDATE executions SET status = 'running', lease_expires_at = now() + $1::interval
		WHERE (tenant_id, execution_id) IN (
			SELECT tenant_id, execution_id FROM executions
			WHERE status = 'pending'
			   OR (status = 'running' AND lease_expires_at < now())
			ORDER BY enqueued_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING tenant_id, execution_id, run_id, plan_id, format, request, request_hash`,
		lease.String()).Scan(&tenant, &executionID, &runID, &planID, &format, &encoded, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ExecutionRequest{}, false, nil
	}
	if err != nil {
		return ports.ExecutionRequest{}, false, fmt.Errorf("postgres: execution could not be claimed: %w", err)
	}
	claimed := ports.ExecutionRequest{
		TenantID:    ports.TenantID(tenant),
		ExecutionID: semantic.ExecutionID(executionID),
		RunID:       semantic.SemanticRunID(runID),
		PlanID:      semantic.PlanID(planID),
	}

	// A row this build cannot trust is retired rather than left in place. The
	// claim has already committed, so returning only an error would leave the row
	// at the head of the queue with a lease that expires, making one poll fail
	// forever and never moving the execution to a terminal state.
	if format != executionFormat {
		return ports.ExecutionRequest{}, false, s.retire(ctx, claimed,
			"storage_format_unknown", fmt.Errorf(
				"%w: row uses storage format %d, this build understands %d",
				ErrIntegrity, format, executionFormat))
	}
	if identityHash(claimed, encoded) != hash {
		return ports.ExecutionRequest{}, false, s.retire(ctx, claimed, "storage_integrity_failed",
			fmt.Errorf("%w: stored execution does not match its content hash", ErrIntegrity))
	}

	request, err := decodeExecutionRequest(claimed.TenantID, claimed.ExecutionID,
		claimed.RunID, claimed.PlanID, encoded)
	if err != nil {
		return ports.ExecutionRequest{}, false, s.retire(ctx, claimed, "storage_integrity_failed", err)
	}
	return request, true, nil
}

// retire moves an untrustworthy row to a terminal state and returns the original
// cause. Without this a poisoned row would be re-selected every lease interval:
// ORDER BY enqueued_at makes it the first candidate again, so a worker treating a
// claim error as fatal would exit repeatedly and the queue would never drain past
// it. Failing to retire is reported alongside the cause rather than replacing it.
func (s *Store) retire(ctx context.Context, request ports.ExecutionRequest, reason string, cause error) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE executions
		SET status = $3, failure_reason = $4, lease_expires_at = NULL, result = NULL, result_hash = NULL
		WHERE tenant_id = $1 AND execution_id = $2`,
		string(request.TenantID), string(request.ExecutionID),
		string(ports.ExecutionFailed), reason); err != nil {
		return errors.Join(cause, fmt.Errorf("postgres: untrustworthy row could not be retired: %w", err))
	}
	return cause
}

// Complete stores the result and takes the execution out of the queue.
func (s *Store) Complete(ctx context.Context, result ports.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.TenantID == "" || result.ExecutionID == "" {
		return ErrIncompleteRecord
	}
	if !terminalStatus(result.Status) {
		return fmt.Errorf("%w: %q is not a terminal status", ErrIncompleteRecord, result.Status)
	}

	encoded, err := json.Marshal(storedResult(result))
	if err != nil {
		return fmt.Errorf("postgres: result could not be encoded: %w", err)
	}
	// The status guard makes the transition one-way. A late Complete from an
	// attempt whose lease expired must not resurrect a failed execution or
	// overwrite an outcome another attempt already recorded.
	tag, err := s.pool.Exec(ctx, `
		UPDATE executions
		SET status = $3, result = $4, result_hash = $5, failure_reason = '', lease_expires_at = NULL
		WHERE tenant_id = $1 AND execution_id = $2 AND status IN ('pending', 'running')`,
		string(result.TenantID), string(result.ExecutionID),
		string(result.Status), encoded, contentHash(encoded))
	if err != nil {
		return fmt.Errorf("postgres: result could not be stored: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotQueued
	}
	return nil
}

// Fail records that an execution could not be attempted.
func (s *Store) Fail(ctx context.Context, tenant ports.TenantID, executionID semantic.ExecutionID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Guarded for the reason the at-least-once argument does not cover: a second
	// Complete is harmless because it reproduces the same artifacts, but a late
	// Fail from an abandoned attempt would replace a real outcome with an
	// operational one and destroy the result.
	tag, err := s.pool.Exec(ctx, `
		UPDATE executions
		SET status = $3, failure_reason = $4, result = NULL, result_hash = NULL, lease_expires_at = NULL
		WHERE tenant_id = $1 AND execution_id = $2 AND status IN ('pending', 'running')`,
		string(tenant), string(executionID), string(ports.ExecutionFailed), reason)
	if err != nil {
		return fmt.Errorf("postgres: execution could not be failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotQueued
	}
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

	var (
		runID, planID, status, failureReason string
		format                               int
		requestBytes, resultBytes            []byte
		requestHash                          string
		resultHash                           *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT run_id, plan_id, status, failure_reason, format, request, request_hash, result, result_hash
		FROM executions WHERE tenant_id = $1 AND execution_id = $2`,
		string(tenant), string(executionID)).Scan(&runID, &planID, &status, &failureReason,
		&format, &requestBytes, &requestHash, &resultBytes, &resultHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ExecutionRecord{}, false, nil
	}
	if err != nil {
		return ports.ExecutionRecord{}, false, fmt.Errorf("postgres: execution could not be read: %w", err)
	}
	if format != executionFormat {
		return ports.ExecutionRecord{}, false, fmt.Errorf(
			"%w: row uses storage format %d, this build understands %d", ErrIntegrity, format, executionFormat)
	}
	identity := ports.ExecutionRequest{
		TenantID: tenant, ExecutionID: executionID,
		RunID: semantic.SemanticRunID(runID), PlanID: semantic.PlanID(planID),
	}
	if identityHash(identity, requestBytes) != requestHash {
		return ports.ExecutionRecord{}, false, fmt.Errorf(
			"%w: stored execution does not match its content hash", ErrIntegrity)
	}

	request, err := decodeExecutionRequest(tenant, executionID,
		semantic.SemanticRunID(runID), semantic.PlanID(planID), requestBytes)
	if err != nil {
		return ports.ExecutionRecord{}, false, err
	}
	record := ports.ExecutionRecord{
		Request:       request,
		Status:        ports.ExecutionStatus(status),
		FailureReason: failureReason,
	}

	if resultBytes != nil {
		if resultHash == nil || contentHash(resultBytes) != *resultHash {
			return ports.ExecutionRecord{}, false, fmt.Errorf(
				"%w: stored execution result does not match its content hash", ErrIntegrity)
		}
		var stored resultDocument
		if err := json.Unmarshal(resultBytes, &stored); err != nil {
			return ports.ExecutionRecord{}, false, fmt.Errorf(
				"%w: stored execution result could not be decoded", ErrIntegrity)
		}
		result := stored.toResult(tenant, executionID, ports.ExecutionStatus(status))
		record.Result = &result
	}
	return record, true, nil
}

// contentHash is storage's own checksum over storage's own encoding. It is
// deliberately not a semantic digest: this package never computes one, so it can
// never become a second source of semantic identity.
func contentHash(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
