package postgres

import (
	"context"
	"errors"
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
				if err := store.Complete(t.Context(),
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
	claimed := make(chan string, executions*2)
	done := make(chan struct{})
	for range 5 {
		go func() {
			defer func() { done <- struct{}{} }()
			worker := freshConnection(t, url)
			for {
				request, found, err := worker.Claim(context.Background(), time.Minute)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if !found {
					return
				}
				claimed <- string(request.ExecutionID)
			}
		}()
	}
	for range 5 {
		<-done
	}
	close(claimed)

	counts := map[string]int{}
	for id := range claimed {
		counts[id]++
	}
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

// freshConnection returns an additional store over the same database without
// truncating, so a test can model several worker processes.
func freshConnection(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}
