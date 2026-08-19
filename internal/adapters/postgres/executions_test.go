package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
)

// The durable queue is held to exactly the same contract as the in-process one.
// The concurrent-partition assertion is the one that actually validates SKIP
// LOCKED: without it, several workers would either block on each other or hand
// the same execution out twice, and neither shows up single-threaded.
func TestStoreSatisfiesTheExecutionStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunExecutionStoreContract(t, func(t *testing.T) ports.ExecutionStore {
		return freshExecutionStore(t, url)
	})
}

// Production break caught: a row altered after it was written must not be
// returned as though it were intact. This is a narrower guarantee than the one
// plans get — it proves storage returned the bytes it was given, not that those
// bytes were correct — because a sealed artifact cannot be re-derived without
// re-executing.
func TestCorruptedExecutionRowsFailClosed(t *testing.T) {
	url := requireDatabase(t)

	tests := []struct {
		name     string
		corrupt  string
		complete bool
	}{
		{"request bytes altered", `UPDATE executions SET request = overlay(request placing '\x20'::bytea from 2 for 1)`, false},
		{"request truncated", `UPDATE executions SET request = substring(request from 1 for 10)`, false},
		{"result bytes altered", `UPDATE executions SET result = overlay(result placing '\x20'::bytea from 2 for 1)`, true},
		{"result hash cleared", `UPDATE executions SET result_hash = NULL`, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := freshExecutionStore(t, url)
			request := storagecontract.ExecutionRequestFixture(t, "acme", "corrupt-a")
			if _, err := store.Enqueue(t.Context(), request); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if test.complete {
				// Claimed first: reporting an outcome requires holding the execution.
				leased, found, err := store.Claim(t.Context(), time.Minute)
				if err != nil || !found {
					t.Fatalf("Claim: found=%t err=%v", found, err)
				}
				if err := store.Complete(t.Context(), leased.AttemptID,
					storagecontract.ExecutionResultFixture(request, ports.ExecutionSucceeded)); err != nil {
					t.Fatalf("Complete: %v", err)
				}
			}
			// Baseline read, so a later failure is attributable to the corruption.
			if _, found, err := store.Get(t.Context(), "acme", request.ExecutionID); err != nil || !found {
				t.Fatalf("baseline read failed: found=%t err=%v", found, err)
			}

			execute(t, url, test.corrupt, nil)

			got, found, err := store.Get(t.Context(), "acme", request.ExecutionID)
			if err == nil {
				t.Fatalf("a corrupted execution was returned: found=%t status=%s", found, got.Status)
			}
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("err = %v, want an integrity failure", err)
			}
			if found {
				t.Error("a failed read also reported the execution as found")
			}
		})
	}
}

// Production break caught: a claim must not be visible to another claimer even
// under real concurrency against one database. This exercises SKIP LOCKED
// directly rather than through the contract's in-process loop.
func TestConcurrentWorkersPartitionTheQueue(t *testing.T) {
	url := requireDatabase(t)
	store := freshExecutionStore(t, url)

	const executions = 30
	for i := range executions {
		request := storagecontract.ExecutionRequestFixture(t, "acme", "partition-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
		if _, err := store.Enqueue(t.Context(), request); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	// Each worker uses its own store, as separate processes would.
	//
	// The loop is bounded and the channel is not relied on for capacity. An
	// earlier version of this test buffered exactly twice the work and looped
	// until found was false, which reintroduced precisely the hang the shared
	// contract was fixed to eliminate: a store that re-hands leased work fills
	// the buffer, every worker blocks on the send, and CI reports a bare
	// timeout instead of naming the defect.
	const maxClaims = executions * 2
	var (
		attempts atomic.Int64
		mutex    sync.Mutex
		counts   = map[string]int{}
		group    sync.WaitGroup
	)
	for range 5 {
		group.Go(func() {
			// t.Fatalf from a non-test goroutine is documented misuse, so
			// failures are reported with t.Errorf and the goroutine returns.
			worker, err := Open(context.Background(), url)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer worker.Close()
			for {
				if attempts.Add(1) > maxClaims {
					t.Errorf("claims exceeded %d for %d executions; the queue is re-handing work",
						maxClaims, executions)
					return
				}
				leased, found, err := worker.Claim(context.Background(), time.Minute)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if !found {
					return
				}
				mutex.Lock()
				counts[string(leased.Request.ExecutionID)]++
				mutex.Unlock()
			}
		})
	}
	group.Wait()

	if len(counts) != executions {
		t.Fatalf("distinct executions claimed = %d, want %d", len(counts), executions)
	}
	for id, count := range counts {
		if count != 1 {
			t.Errorf("execution %s was claimed %d times", id, count)
		}
	}
}

// freshExecutionStore returns a store over an empty executions table.
func freshExecutionStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(t.Context(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	execute(t, url, `TRUNCATE executions`, nil)
	return store
}

// Production break caught: the identity columns sit outside the request blob, so
// covering only the blob would let an UPDATE alter execution_id or run_id while
// both hashes stayed valid. A worker would then execute a request bound to an
// identity the kernel never derived for that input, and would seal artifacts
// under it. This is the review finding that the plan's own constraint —
// identities are re-derived and compared, never read and trusted — had not been
// honoured for executions.
func TestTamperedIdentityColumnsFailClosed(t *testing.T) {
	url := requireDatabase(t)

	tests := []struct {
		name    string
		corrupt string
	}{
		{"run identity altered", `UPDATE executions SET run_id = 'sha256:` + repeatByte('1', 64) + `'`},
		{"plan identity altered", `UPDATE executions SET plan_id = 'sha256:` + repeatByte('2', 64) + `'`},
		{"unknown storage format", `UPDATE executions SET format = 99`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := freshExecutionStore(t, url)
			request := storagecontract.ExecutionRequestFixture(t, "acme", "tamper-a")
			if _, err := store.Enqueue(t.Context(), request); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			execute(t, url, test.corrupt, nil)

			if _, found, err := store.Get(t.Context(), "acme", request.ExecutionID); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("Get err = %v found = %t, want an integrity failure", err, found)
			}
			// Claiming must also refuse, and must retire the row rather than
			// leaving it to be re-selected every lease interval.
			if _, found, err := store.Claim(t.Context(), time.Minute); !errors.Is(err, ErrIntegrity) || found {
				t.Fatalf("Claim err = %v found = %t, want an integrity failure", err, found)
			}
			if _, found, err := store.Claim(t.Context(), time.Minute); err != nil || found {
				t.Fatalf("the poisoned row was offered again: found=%t err=%v", found, err)
			}
		})
	}
}

func repeatByte(c byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
