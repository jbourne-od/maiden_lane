package storagecontract

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunExecutionStoreContract asserts every behaviour a ports.ExecutionStore must
// exhibit, including its behaviour as a work queue.
//
// The queue assertions carry most of the risk in this contract. A store that
// merely looks right can still hand one execution to two workers, lose work
// when a worker dies, or return a leased execution to a second claimer, and
// none of those show up in a single-threaded happy path.
func RunExecutionStoreContract(t *testing.T, newStore func(*testing.T) ports.ExecutionStore) {
	t.Helper()

	runReattemptContract(t, newStore)
	t.Run("enqueues idempotently on the derived identity", func(t *testing.T) {
		// ExecutionID is derived from the semantic request, so a repeated
		// submission is necessarily the same execution. If it created a second
		// one, every caller would need a deduplication key to compensate for
		// something the identity function already guarantees.
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")

		created, err := store.Enqueue(t.Context(), request)
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if !created {
			t.Fatal("the first enqueue reported the execution as already present")
		}

		created, err = store.Enqueue(t.Context(), request)
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if created {
			t.Fatal("a repeated enqueue created a second execution")
		}

		// And there is exactly one to claim.
		if _, found, err := store.Claim(t.Context(), time.Minute); err != nil || !found {
			t.Fatalf("Claim: found=%t err=%v", found, err)
		}
		if _, found, err := store.Claim(t.Context(), time.Minute); err != nil || found {
			t.Fatalf("a second execution was claimable: found=%t err=%v", found, err)
		}
	})

	t.Run("does not hand a leased execution to a second claimer", func(t *testing.T) {
		store := newStore(t)
		mustEnqueue(t, store, ExecutionRequestFixture(t, "acme", "exec-a"))

		first, found, err := store.Claim(t.Context(), time.Minute)
		if err != nil || !found {
			t.Fatalf("Claim: found=%t err=%v", found, err)
		}
		second, found, err := store.Claim(t.Context(), time.Minute)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if found {
			t.Fatalf("a leased execution was claimed twice: %s and %s",
				first.ExecutionID, second.ExecutionID)
		}
	})

	t.Run("reclaims an execution whose lease expired", func(t *testing.T) {
		// A worker can die between claiming and completing. Without reclaim the
		// execution would be stranded forever, which is worse than running it
		// twice: running twice is deterministic and therefore harmless.
		store := newStore(t)
		mustEnqueue(t, store, ExecutionRequestFixture(t, "acme", "exec-a"))

		first, found, err := store.Claim(t.Context(), time.Millisecond)
		if err != nil || !found {
			t.Fatalf("Claim: found=%t err=%v", found, err)
		}
		waitPast(time.Millisecond)

		second, found, err := store.Claim(t.Context(), time.Minute)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if !found {
			t.Fatal("an expired lease did not make the execution claimable again")
		}
		if second.ExecutionID != first.ExecutionID {
			t.Fatalf("reclaimed %s, want %s", second.ExecutionID, first.ExecutionID)
		}
		// The reclaimed request must be identical, or a second attempt would
		// compute something other than what was submitted.
		if second.RunID != first.RunID || second.PlanID != first.PlanID {
			t.Fatal("the reclaimed request differs from the original")
		}
		if second.Input.InitialState.Digest() != first.Input.InitialState.Digest() {
			t.Fatal("the reclaimed input differs from the original")
		}
	})

	t.Run("returns a claimed input identical to the enqueued one", func(t *testing.T) {
		// Comparing two claims against each other is not enough: a lossy
		// encoding would corrupt both identically and they would still match.
		// The comparison has to be against the request as submitted, before it
		// ever reached storage.
		//
		// This is the execution equivalent of the plan store's round-trip
		// assertion, and it is where a storage encoding that quietly altered a
		// value would surface: every downstream identity derives from this
		// state digest, so a lossy round trip would execute something other
		// than what was accepted.
		store := newStore(t)
		submitted := ExecutionRequestFixture(t, "acme", "exec-roundtrip")
		mustEnqueue(t, store, submitted)

		claimed, found, err := store.Claim(t.Context(), time.Minute)
		if err != nil || !found {
			t.Fatalf("Claim: found=%t err=%v", found, err)
		}

		if claimed.Input.InitialState.Digest() != submitted.Input.InitialState.Digest() {
			t.Fatalf("claimed state digest = %s, want %s",
				claimed.Input.InitialState.Digest(), submitted.Input.InitialState.Digest())
		}
		if claimed.Input.World.ID() != submitted.Input.World.ID() {
			t.Fatalf("claimed world = %s, want %s", claimed.Input.World.ID(), submitted.Input.World.ID())
		}
		if claimed.Input.ExecutorIdentity != submitted.Input.ExecutorIdentity {
			t.Fatal("claimed executor identity differs from the submitted one")
		}
		if claimed.Input.Policy != submitted.Input.Policy {
			t.Fatal("claimed provenance policy differs from the submitted one")
		}
		if claimed.ExecutionID != submitted.ExecutionID || claimed.RunID != submitted.RunID {
			t.Fatal("claimed identities differ from the submitted ones")
		}
	})

	t.Run("never claims a completed execution", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, request)
		if _, found, err := store.Claim(t.Context(), time.Millisecond); err != nil || !found {
			t.Fatalf("Claim: found=%t err=%v", found, err)
		}
		if err := store.Complete(t.Context(), ExecutionResultFixture(request, ports.ExecutionSucceeded)); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		// Even after the lease would have expired, a completed execution is done.
		waitPast(time.Millisecond)
		if _, found, err := store.Claim(t.Context(), time.Minute); err != nil || found {
			t.Fatalf("a completed execution was claimed again: found=%t err=%v", found, err)
		}
	})

	t.Run("stores and returns the completed result", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, request)
		result := ExecutionResultFixture(request, ports.ExecutionSucceeded)
		if err := store.Complete(t.Context(), result); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionSucceeded {
			t.Fatalf("status = %s, want succeeded", got.Status)
		}
		if got.Result == nil {
			t.Fatal("a completed execution carries no result")
		}
		if got.Result.FinalStateDigest != result.FinalStateDigest {
			t.Fatalf("final state digest = %s, want %s",
				got.Result.FinalStateDigest, result.FinalStateDigest)
		}
		if len(got.Result.Checkpoints) != len(result.Checkpoints) {
			t.Fatalf("checkpoints = %d, want %d", len(got.Result.Checkpoints), len(result.Checkpoints))
		}
		// The sealed bytes must round-trip exactly: they are the artifact, and
		// an artifact that changed in storage is not the one that was sealed.
		for i, checkpoint := range got.Result.Checkpoints {
			want := result.Checkpoints[i]
			if string(checkpoint.CanonicalBytes) != string(want.CanonicalBytes) {
				t.Errorf("checkpoint %d bytes changed in storage", i)
			}
			if checkpoint.Digest != want.Digest {
				t.Errorf("checkpoint %d digest = %s, want %s", i, checkpoint.Digest, want.Digest)
			}
			// The invariant witness must round-trip exactly for the same reason,
			// and for a sharper one: it is the evidence a promotion gate verifies
			// against the artifact's committed digest. A single altered byte does
			// not degrade that check, it fails it, and the execution becomes
			// unpublishable with no way to tell storage corruption from a genuine
			// mismatch.
			if string(checkpoint.InvariantResultCanonicalBytes) !=
				string(want.InvariantResultCanonicalBytes) {
				t.Errorf("checkpoint %d invariant witness changed in storage", i)
			}
			if len(checkpoint.InvariantResultCanonicalBytes) == 0 {
				t.Errorf("checkpoint %d came back with no invariant witness", i)
			}
			// The commitment must survive with it. A witness whose digest was
			// lost is unverifiable, which is indistinguishable from having no
			// witness at all.
			if checkpoint.InvariantResultDigest != want.InvariantResultDigest {
				t.Errorf("checkpoint %d invariant digest = %s, want %s",
					i, checkpoint.InvariantResultDigest, want.InvariantResultDigest)
			}
		}
		if len(got.Result.Assessments) != len(result.Assessments) {
			t.Fatalf("assessments = %d, want %d", len(got.Result.Assessments), len(result.Assessments))
		}
	})

	t.Run("reports a pending execution without a result", func(t *testing.T) {
		// A caller must not be able to infer a result from a status. Returning a
		// zero-valued result for a pending execution would look like an
		// execution that produced nothing rather than one that has not run.
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, request)

		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionPending {
			t.Fatalf("status = %s, want pending", got.Status)
		}
		if got.Result != nil {
			t.Fatal("a pending execution carries a result")
		}
	})

	t.Run("records an operational failure with a bounded reason", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, request)

		if err := store.Fail(t.Context(), "acme", request.ExecutionID, "dependency_unavailable"); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionFailed {
			t.Fatalf("status = %s, want failed", got.Status)
		}
		if got.FailureReason != "dependency_unavailable" {
			t.Fatalf("reason = %q", got.FailureReason)
		}
		if got.Result != nil {
			t.Fatal("an unattempted execution carries a semantic result")
		}
	})

	t.Run("keeps terminal states terminal", func(t *testing.T) {
		// A late Fail from a worker whose lease expired must not overwrite a
		// success another worker already recorded. The determinism argument that
		// makes at-least-once safe covers a duplicate Complete, because the
		// second attempt reproduces the same artifacts. It does NOT cover a
		// duplicate Fail: that destroys a real outcome and replaces it with an
		// operational one, and nothing about determinism prevents it.
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-terminal")
		mustEnqueue(t, store, request)
		result := ExecutionResultFixture(request, ports.ExecutionSucceeded)
		if err := store.Complete(t.Context(), result); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		// A late failure report from an abandoned attempt.
		_ = store.Fail(t.Context(), "acme", request.ExecutionID, "dependency_unavailable")

		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionSucceeded {
			t.Fatalf("status = %s; a late Fail overwrote a recorded success", got.Status)
		}
		if got.Result == nil {
			t.Fatal("a late Fail destroyed the recorded result")
		}
		if got.FailureReason != "" {
			t.Fatalf("a succeeded execution carries a failure reason: %q", got.FailureReason)
		}
	})

	t.Run("does not resurrect a failed execution", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-failed")
		mustEnqueue(t, store, request)
		if err := store.Fail(t.Context(), "acme", request.ExecutionID, "dependency_unavailable"); err != nil {
			t.Fatalf("Fail: %v", err)
		}

		_ = store.Complete(t.Context(), ExecutionResultFixture(request, ports.ExecutionSucceeded))

		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionFailed {
			t.Fatalf("status = %s; a late Complete resurrected a failed execution", got.Status)
		}
		if got.Result != nil {
			t.Fatal("a failed execution carries a semantic result")
		}
	})

	t.Run("refuses to complete with a non-terminal status", func(t *testing.T) {
		// A completed row carrying a non-terminal status is the worst shape
		// available: it still matches the claim predicate but has no lease, so
		// the execution is re-claimed and re-run forever, while a read reports a
		// pending record carrying a result, which ExecutionRecord documents as
		// impossible.
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-nonterminal")
		mustEnqueue(t, store, request)

		for _, status := range []ports.ExecutionStatus{
			ports.ExecutionPending, ports.ExecutionRunning, ports.ExecutionStatus(""),
		} {
			if err := store.Complete(t.Context(), ExecutionResultFixture(request, status)); err == nil {
				t.Errorf("Complete accepted the non-terminal status %q", status)
			}
		}

		// And the execution is untouched: still claimable, still without a result.
		got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if got.Status != ports.ExecutionPending || got.Result != nil {
			t.Fatalf("a refused Complete altered the execution: status=%s result=%v", got.Status, got.Result != nil)
		}
	})

	t.Run("refuses a request whose pinned input is unusable", func(t *testing.T) {
		// Accepting one produces an execution that can never run and can never
		// be read: every later claim and read fails on the same unusable input.
		// A store that accepts it has promised to do work it cannot do.
		store := newStore(t)
		unusable := ExecutionRequestFixture(t, "acme", "exec-unusable")
		unusable.Input = ports.ExecutionInput{}

		if _, err := store.Enqueue(t.Context(), unusable); err == nil {
			t.Fatal("Enqueue accepted a request with no pinned input")
		}
		if _, found, err := store.Get(t.Context(), "acme", unusable.ExecutionID); err != nil || found {
			t.Fatalf("a refused request was stored: found=%t err=%v", found, err)
		}
	})

	t.Run("isolates tenants", func(t *testing.T) {
		store := newStore(t)
		mine := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, mine)

		foreign, foreignFound, err := store.Get(t.Context(), "globex", mine.ExecutionID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		absent, absentFound, err := store.Get(t.Context(), "globex", "sha256:"+
			"0000000000000000000000000000000000000000000000000000000000000000")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if foreignFound || absentFound {
			t.Fatal("a foreign or unknown execution resolved to a record")
		}
		// Indistinguishable, so possession of an identity reveals nothing.
		if foreign.Status != absent.Status || foreign.Request.ExecutionID != absent.Request.ExecutionID {
			t.Fatalf("a foreign execution is distinguishable from an absent one: %+v vs %+v", foreign, absent)
		}
	})

	t.Run("refuses incomplete requests", func(t *testing.T) {
		store := newStore(t)
		complete := ExecutionRequestFixture(t, "acme", "exec-a")

		missingTenant := complete
		missingTenant.TenantID = ""
		if _, err := store.Enqueue(t.Context(), missingTenant); err == nil {
			t.Error("Enqueue accepted a request with no tenant")
		}
		missingID := complete
		missingID.ExecutionID = ""
		if _, err := store.Enqueue(t.Context(), missingID); err == nil {
			t.Error("Enqueue accepted a request with no execution identity")
		}
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "exec-a")
		mustEnqueue(t, store, request)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := store.Enqueue(ctx, request); !errors.Is(err, context.Canceled) {
			t.Errorf("Enqueue = %v, want context.Canceled", err)
		}
		if _, _, err := store.Claim(ctx, time.Minute); !errors.Is(err, context.Canceled) {
			t.Errorf("Claim = %v, want context.Canceled", err)
		}
		if _, _, err := store.Get(ctx, "acme", request.ExecutionID); !errors.Is(err, context.Canceled) {
			t.Errorf("Get = %v, want context.Canceled", err)
		}
	})

	t.Run("hands each execution to exactly one concurrent claimer", func(t *testing.T) {
		// The assertion that matters most. Several workers polling one queue
		// must partition the work: nothing claimed twice, nothing left behind.
		const executions = 24
		store := newStore(t)
		for i := range executions {
			mustEnqueue(t, store, ExecutionRequestFixture(t, "acme", executionKey(i)))
		}

		var (
			mutex  sync.Mutex
			claims = map[semantic.ExecutionID]int{}
			group  sync.WaitGroup
		)
		// The loop is bounded rather than run until the queue drains. A store
		// that hands out work without recording the claim would otherwise
		// return found=true forever and hang the test, which in CI means a
		// timeout with no useful message instead of a failure that names the
		// defect. Exceeding the amount of work that exists IS the defect, so
		// the bound is the assertion.
		const maxClaims = executions * 2
		var attempts atomic.Int64
		for range 6 {
			group.Go(func() {
				for {
					if attempts.Add(1) > maxClaims {
						t.Errorf("claims exceeded %d for %d executions; the store is handing out work it already gave away",
							maxClaims, executions)
						return
					}
					request, found, err := store.Claim(context.Background(), time.Minute)
					if err != nil {
						t.Errorf("Claim: %v", err)
						return
					}
					if !found {
						return
					}
					mutex.Lock()
					claims[request.ExecutionID]++
					mutex.Unlock()
				}
			})
		}
		group.Wait()

		if len(claims) != executions {
			t.Fatalf("distinct executions claimed = %d, want %d", len(claims), executions)
		}
		for id, count := range claims {
			if count != 1 {
				t.Errorf("execution %s was claimed %d times", id, count)
			}
		}
	})
}

// waitPast sleeps just past a lease so an expiry is observable. Leases are
// operational state, so a test may legitimately observe the clock here; nothing
// semantic depends on it.
// Reattempt returns an execution that could not be attempted to the queue, and refuses
// anything that produced a real answer.
//
// This exists because execution identity is derived: a caller cannot clear a terminally
// failed execution by resubmitting it, since the same semantic request resolves to the
// same record. Without this operation such an execution is stuck forever, which blocks
// anything that needs every case of a set to answer.
func runReattemptContract(t *testing.T, newStore func(*testing.T) ports.ExecutionStore) {
	t.Helper()

	t.Run("returns an unattempted execution to the queue", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, request)
		if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := store.Fail(t.Context(), "acme", request.ExecutionID, "plan_absent"); err != nil {
			t.Fatalf("Fail: %v", err)
		}

		// A claim BEFORE the reattempt, which finds nothing. This step is load-bearing
		// and its absence made the next assertion vacuous: an implementation may skip
		// terminal entries at the head of its queue permanently, and only a claim that
		// has already passed this entry can expose a reattempt that fails to bring it
		// back. Without this poll the entry was never passed, so the reattempt had
		// nothing to undo. Verified — removing the rewind left the test green.
		if _, found, err := store.Claim(t.Context(), time.Minute); err != nil || found {
			t.Fatalf("a terminally failed execution was claimable: found=%t err=%v", found, err)
		}

		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err != nil {
			t.Fatalf("Reattempt: %v", err)
		}

		record, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		if record.Status != ports.ExecutionPending {
			t.Fatalf("status = %s, want pending", record.Status)
		}
		if record.FailureReason != "" {
			t.Fatalf("failure reason = %q, want it cleared", record.FailureReason)
		}
		// The identity and the inputs are unchanged, because nothing about the semantic
		// request was wrong. A retry that produced a different execution would defeat the
		// derived identity entirely.
		if record.Request.ExecutionID != request.ExecutionID ||
			record.Request.RunID != request.RunID {
			t.Fatal("reattempting changed the execution's identity")
		}

		// AND IT MUST ACTUALLY BE CLAIMABLE. A reattempt that leaves the row unclaimable
		// is the quietest possible way for a retry to do nothing.
		claimed, ok, err := store.Claim(t.Context(), time.Minute)
		if err != nil || !ok {
			t.Fatalf("a reattempted execution was not claimable: ok=%t err=%v", ok, err)
		}
		if claimed.ExecutionID != request.ExecutionID {
			t.Fatalf("claimed %s, want the reattempted %s",
				claimed.ExecutionID, request.ExecutionID)
		}
	})

	t.Run("a reattempted execution can then complete normally", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, request)
		if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := store.Fail(t.Context(), "acme", request.ExecutionID, "internal_error"); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err != nil {
			t.Fatalf("Reattempt: %v", err)
		}
		if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := store.Complete(t.Context(),
			ExecutionResultFixture(request, ports.ExecutionSucceeded)); err != nil {
			t.Fatalf("Complete after reattempt: %v", err)
		}
		record, _, err := store.Get(t.Context(), "acme", request.ExecutionID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if record.Status != ports.ExecutionSucceeded || record.Result == nil {
			t.Fatalf("status = %s with result=%t, want succeeded with a result",
				record.Status, record.Result != nil)
		}
	})

	t.Run("refuses an execution that produced a real answer", func(t *testing.T) {
		// The distinction the whole operation rests on. A deterministic semantic
		// rejection is a completed execution carrying a result, and re-running it
		// reproduces that result byte for byte — so retrying it is a request for a
		// different answer to the same question, not a retry.
		for _, status := range []ports.ExecutionStatus{
			ports.ExecutionSucceeded, ports.ExecutionFailed,
		} {
			t.Run(string(status)+" with a result", func(t *testing.T) {
				store := newStore(t)
				request := ExecutionRequestFixture(t, "acme", "a")
				mustEnqueue(t, store, request)
				if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
					t.Fatalf("Claim: %v", err)
				}
				if err := store.Complete(t.Context(),
					ExecutionResultFixture(request, status)); err != nil {
					t.Fatalf("Complete: %v", err)
				}
				if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err == nil {
					t.Fatalf("a %s execution carrying a result was reattempted", status)
				}
				// And it is untouched.
				record, _, err := store.Get(t.Context(), "acme", request.ExecutionID)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if record.Status != status || record.Result == nil {
					t.Fatal("a refused reattempt still altered the execution")
				}
			})
		}
	})

	t.Run("refuses an execution still in flight", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, request)
		// Pending: already claimable, so there is nothing to return to the queue.
		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err == nil {
			t.Fatal("a pending execution was reattempted")
		}
		if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		// Running: an attempt is in progress and its lease governs recovery.
		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err == nil {
			t.Fatal("a running execution was reattempted")
		}
	})

	t.Run("reports an absent execution rather than reattempting one", func(t *testing.T) {
		store := newStore(t)
		other := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, other)

		if err := store.Reattempt(t.Context(), "acme",
			semantic.ExecutionID("sha256:"+repeat("9", 64))); err == nil {
			t.Fatal("an execution nobody enqueued was reattempted")
		}
		// Another tenant's execution is absent too, so a reattempt cannot reach it.
		if err := store.Reattempt(t.Context(), "globex", other.ExecutionID); err == nil {
			t.Fatal("another tenant's execution was reattempted")
		}
	})

	t.Run("is idempotent only in the sense that a second call refuses", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, request)
		if _, _, err := store.Claim(t.Context(), time.Minute); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := store.Fail(t.Context(), "acme", request.ExecutionID, "internal_error"); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err != nil {
			t.Fatalf("first Reattempt: %v", err)
		}
		// The execution is pending now, so a second call finds nothing to return. A
		// caller repeating the operation learns it already happened rather than
		// queueing the work twice.
		if err := store.Reattempt(t.Context(), "acme", request.ExecutionID); err == nil {
			t.Fatal("a second reattempt succeeded on an already-pending execution")
		}
	})

	t.Run("stops on a cancelled context", func(t *testing.T) {
		store := newStore(t)
		request := ExecutionRequestFixture(t, "acme", "a")
		mustEnqueue(t, store, request)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := store.Reattempt(ctx, "acme", request.ExecutionID); err == nil {
			t.Fatal("Reattempt succeeded on a cancelled context")
		}
	})
}

func waitPast(lease time.Duration) { time.Sleep(lease + 25*time.Millisecond) }

func executionKey(i int) string { return "exec-" + string(rune('a'+i%26)) + string(rune('0'+i/26)) }

func mustEnqueue(t *testing.T, store ports.ExecutionStore, request ports.ExecutionRequest) {
	t.Helper()
	if _, err := store.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

// ExecutionRequestFixture builds a queued execution whose identities are
// genuinely derived by the kernel rather than invented.
//
// That matters for a queue contract: identities derived from the input are what
// make enqueueing idempotent, so a fixture with fabricated identities would let
// a store pass the idempotence assertion without the property holding for real
// requests. key varies the pinned input, and therefore every derived identity.
func ExecutionRequestFixture(t *testing.T, tenant ports.TenantID, key string) ports.ExecutionRequest {
	t.Helper()

	schema, err := semantic.NewSchema([]semantic.EntityDeclaration{{
		Kind: "driver",
		Fields: []semantic.FieldDeclaration{
			{Name: "assignment_key", Kind: semantic.ValueString},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema:                   schema.Declaration(),
		CompilerSemanticsVersion: "storagecontract.executions.v1",
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("contract fixture did not compile")
	}

	lineage, err := semantic.NewInputLineageID("maiden-lane.storagecontract", key)
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	assignment, err := semantic.NewStringValue("assignment-" + key)
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	entity, err := semantic.NewEntity(semantic.EntityRef{
		Kind: "driver",
		ID:   semantic.SourceEntityID(lineage, "driver", key),
	}, map[semantic.FieldName]semantic.Value{"assignment_key": assignment})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	state, err := semantic.NewState(schema, lineage, []semantic.Entity{entity}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	world, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	executor, err := semantic.NewExecutorIdentity("go",
		"sha256:1c0d5a3e9b7f2c4d6a8e0b1f3d5c7a9e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}

	// Binding is what derives the run and execution identities. Using it rather
	// than fabricating them keeps the fixture honest about where identity comes
	// from.
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: state, World: world,
		ExecutorIdentity: executor, Policy: semantic.ChangesProvenance,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}

	return ports.ExecutionRequest{
		TenantID:    tenant,
		ExecutionID: binding.ExecutionID(),
		RunID:       binding.SemanticRunID(),
		PlanID:      plan.ID(),
		Input: ports.ExecutionInput{
			InitialState:     state,
			World:            world,
			ExecutorIdentity: executor,
			Policy:           semantic.ChangesProvenance,
		},
	}
}

// ExecutionResultFixture builds a plausible completed result for a request.
//
// The sealed bytes are deliberately arbitrary: a store's job is to return
// exactly the bytes it was given, and it must not care what they mean. What the
// contract checks is that they come back unchanged.
func ExecutionResultFixture(request ports.ExecutionRequest, status ports.ExecutionStatus) ports.ExecutionResult {
	return ports.ExecutionResult{
		TenantID:            request.TenantID,
		ExecutionID:         request.ExecutionID,
		Status:              status,
		SpineStatus:         "succeeded",
		FinalStateDigest:    request.Input.InitialState.Digest(),
		JournalPrefixDigest: semantic.JournalPrefixDigest("sha256:" + repeat("a", 64)),
		InputID:             semantic.InputID("sha256:" + repeat("b", 64)),
		WorldID:             request.Input.World.ID(),
		AcceptedRules:       []semantic.RuleID{"form_team.v1"},
		Checkpoints: []ports.SealedCheckpoint{{
			CheckpointKey:        "team_formed.v1",
			CheckpointID:         semantic.CheckpointID("sha256:" + repeat("c", 64)),
			CheckpointArtifactID: semantic.CheckpointArtifactID("sha256:" + repeat("d", 64)),
			Digest:               semantic.CheckpointArtifactDigest("sha256:" + repeat("e", 64)),
			StateDigest:          request.Input.InitialState.Digest(),
			CanonicalBytes:       []byte{0x00, 0x01, 0xff, 0x7f, 0x80},
			// Bytes chosen to be hostile to a lossy round trip: a NUL, a high
			// bit, and an invalid UTF-8 sequence. The witness is opaque binary,
			// so an adapter that treats it as text corrupts it here rather than
			// in production.
			InvariantResultDigest:         semantic.InvariantResultDigest("sha256:" + repeat("9", 64)),
			InvariantResultCanonicalBytes: []byte{0x00, 0xc3, 0x28, 0xff, 0xfe, 0x7f},
		}},
		Assessments: []ports.StoredAssessment{{
			AssessmentID:         semantic.AssessmentID("sha256:" + repeat("f", 64)),
			Digest:               semantic.AssessmentDigest("sha256:" + repeat("0", 64)),
			CheckpointArtifactID: semantic.CheckpointArtifactID("sha256:" + repeat("d", 64)),
			ProfileID:            semantic.ProfileID("sha256:" + repeat("1", 64)),
			ProfileKey:           "cm.v1",
			Verdict:              semantic.Ready,
			MissingRequirements:  nil,
			CanonicalBytes:       []byte{0x02, 0x03},
		}},
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for range n {
		out = append(out, s[0])
	}
	return string(out)
}
