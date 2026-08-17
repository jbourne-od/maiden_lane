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

// Enqueue stores a pending execution, idempotently on its derived identity.
func (s *Store) Enqueue(ctx context.Context, request ports.ExecutionRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if request.TenantID == "" || request.ExecutionID == "" {
		return false, ErrIncompleteRecord
	}

	encoded, err := encodeExecutionRequest(request)
	if err != nil {
		return false, err
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO executions
			(tenant_id, execution_id, run_id, plan_id, status, request, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, execution_id) DO NOTHING`,
		string(request.TenantID), string(request.ExecutionID),
		string(request.RunID), string(request.PlanID),
		string(ports.ExecutionPending), encoded, contentHash(encoded))
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
		RETURNING tenant_id, execution_id, run_id, plan_id, request, request_hash`,
		lease.String()).Scan(&tenant, &executionID, &runID, &planID, &encoded, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ExecutionRequest{}, false, nil
	}
	if err != nil {
		return ports.ExecutionRequest{}, false, fmt.Errorf("postgres: execution could not be claimed: %w", err)
	}
	if contentHash(encoded) != hash {
		return ports.ExecutionRequest{}, false, fmt.Errorf(
			"%w: stored execution request does not match its content hash", ErrIntegrity)
	}

	request, err := decodeExecutionRequest(ports.TenantID(tenant),
		semantic.ExecutionID(executionID), semantic.SemanticRunID(runID),
		semantic.PlanID(planID), encoded)
	if err != nil {
		return ports.ExecutionRequest{}, false, err
	}
	return request, true, nil
}

// Complete stores the result and takes the execution out of the queue.
func (s *Store) Complete(ctx context.Context, result ports.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.TenantID == "" || result.ExecutionID == "" {
		return ErrIncompleteRecord
	}

	encoded, err := json.Marshal(storedResult(result))
	if err != nil {
		return fmt.Errorf("postgres: result could not be encoded: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE executions
		SET status = $3, result = $4, result_hash = $5, lease_expires_at = NULL
		WHERE tenant_id = $1 AND execution_id = $2`,
		string(result.TenantID), string(result.ExecutionID),
		string(result.Status), encoded, contentHash(encoded))
	if err != nil {
		return fmt.Errorf("postgres: result could not be stored: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIncompleteRecord
	}
	return nil
}

// Fail records that an execution could not be attempted.
func (s *Store) Fail(ctx context.Context, tenant ports.TenantID, executionID semantic.ExecutionID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE executions
		SET status = $3, failure_reason = $4, lease_expires_at = NULL
		WHERE tenant_id = $1 AND execution_id = $2`,
		string(tenant), string(executionID), string(ports.ExecutionFailed), reason)
	if err != nil {
		return fmt.Errorf("postgres: execution could not be failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIncompleteRecord
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
		requestBytes, resultBytes            []byte
		requestHash                          string
		resultHash                           *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT run_id, plan_id, status, failure_reason, request, request_hash, result, result_hash
		FROM executions WHERE tenant_id = $1 AND execution_id = $2`,
		string(tenant), string(executionID)).Scan(&runID, &planID, &status, &failureReason,
		&requestBytes, &requestHash, &resultBytes, &resultHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ExecutionRecord{}, false, nil
	}
	if err != nil {
		return ports.ExecutionRecord{}, false, fmt.Errorf("postgres: execution could not be read: %w", err)
	}
	if contentHash(requestBytes) != requestHash {
		return ports.ExecutionRecord{}, false, fmt.Errorf(
			"%w: stored execution request does not match its content hash", ErrIntegrity)
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
