package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Production break caught: a store that keyed only on the artifact identity
// would let one tenant read another's plan, because plan identities are
// content derived and therefore identical for identical declarations. Two
// tenants compiling the same rules is the normal case, not an edge case.
func TestPlanStoreIsolatesTenantsSharingAnIdentity(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")
	other := record
	other.TenantID = "globex"

	mustPut(t, store, record)
	mustPut(t, store, other)

	for _, tenant := range []ports.TenantID{"acme", "globex"} {
		got, found, err := store.GetPlan(t.Context(), tenant, record.PlanID)
		if err != nil {
			t.Fatalf("GetPlan(%s): %v", tenant, err)
		}
		if !found {
			t.Fatalf("tenant %s cannot read its own plan", tenant)
		}
		if got.TenantID != tenant {
			t.Errorf("tenant %s read a record owned by %s", tenant, got.TenantID)
		}
	}
}

// Production break caught: reporting another tenant's artifact as anything but
// absent leaks its existence, which is an authorization failure even when the
// body is withheld.
func TestGetPlanReportsAnotherTenantsPlanAsAbsent(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")
	mustPut(t, store, record)

	got, found, err := store.GetPlan(t.Context(), "intruder", record.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if found {
		t.Fatal("a foreign tenant read the plan")
	}
	if got.PlanID != "" || got.TenantID != "" {
		t.Fatalf("absent lookup returned data: %+v", got)
	}
}

// Production break caught: plan identity is content derived, so re-submitting
// identical declarations must be idempotent rather than an error or a
// duplicate.
func TestPutPlanIsIdempotent(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")

	mustPut(t, store, record)
	mustPut(t, store, record)

	got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
	if err != nil || !found {
		t.Fatalf("GetPlan: found=%t err=%v", found, err)
	}
	if got.PlanID != record.PlanID {
		t.Fatalf("planID = %s, want %s", got.PlanID, record.PlanID)
	}
}

// Production break caught: handing out a value whose getters share backing
// arrays with the stored record would let one reader corrupt every later
// reader's view of a supposedly immutable artifact.
func TestStoredRecordSurvivesCallerMutationOfRetrievedValues(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")
	mustPut(t, store, record)

	first, _, err := store.GetPlan(t.Context(), "acme", record.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	plan, ok := first.Compilation.Plan()
	if !ok {
		t.Fatal("stored compilation carries no plan")
	}
	// The kernel's getters clone, so mutating what they return must not reach
	// the store. Prove it rather than trusting it.
	transformations := plan.Transformations()
	for i := range transformations {
		transformations[i] = semantic.CompiledTransformation{}
	}
	checkpoints := plan.Checkpoints()
	for i := range checkpoints {
		checkpoints[i] = semantic.CheckpointDeclaration{}
	}
	canonical := plan.CanonicalBytes()
	for i := range canonical {
		canonical[i] = 0
	}

	second, _, err := store.GetPlan(t.Context(), "acme", record.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	secondPlan, ok := second.Compilation.Plan()
	if !ok {
		t.Fatal("second read carries no plan")
	}
	if secondPlan.ID() != plan.ID() {
		t.Fatalf("plan identity changed after caller mutation: %s", secondPlan.ID())
	}
	if got := len(secondPlan.Transformations()); got != len(transformations) {
		t.Fatalf("transformations = %d, want %d", got, len(transformations))
	}
	if len(secondPlan.CanonicalBytes()) == 0 {
		t.Fatal("stored canonical bytes were emptied by a caller")
	}
	for _, b := range secondPlan.CanonicalBytes() {
		if b != 0 {
			return
		}
	}
	t.Fatal("stored canonical bytes were zeroed by a caller")
}

// Production break caught: an incomplete record would create an unreachable
// entry keyed by an empty tenant, which is the one key a missing scope check
// would produce.
func TestPutPlanRejectsIncompleteRecords(t *testing.T) {
	store := memory.NewStore()
	complete := planRecordFixture(t, "acme")

	missingTenant := complete
	missingTenant.TenantID = ""
	if err := store.PutPlan(t.Context(), missingTenant); err == nil {
		t.Error("PutPlan accepted a record with no tenant")
	}

	missingID := complete
	missingID.PlanID = ""
	if err := store.PutPlan(t.Context(), missingID); err == nil {
		t.Error("PutPlan accepted a record with no plan identity")
	}

	if _, found, err := store.GetPlan(t.Context(), "", complete.PlanID); err == nil && found {
		t.Error("an empty tenant resolved to a stored record")
	}
}

// Production break caught: an adapter that ignored cancellation would keep
// working after its caller gave up, which a durable replacement never will.
// Behaving like a real adapter now keeps the port honest for the swap.
func TestStoreHonorsContextCancellation(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")
	mustPut(t, store, record)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.PutPlan(ctx, record); !errors.Is(err, context.Canceled) {
		t.Errorf("PutPlan on a cancelled context = %v, want context.Canceled", err)
	}
	if _, _, err := store.GetPlan(ctx, "acme", record.PlanID); !errors.Is(err, context.Canceled) {
		t.Errorf("GetPlan on a cancelled context = %v, want context.Canceled", err)
	}
}

// Production break caught: the store is shared by every in-flight request, so
// an unsynchronized map would crash the process under ordinary concurrency.
func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	store := memory.NewStore()
	record := planRecordFixture(t, "acme")

	var group sync.WaitGroup
	for i := range 8 {
		group.Go(func() {
			scoped := record
			if i%2 == 0 {
				scoped.TenantID = "globex"
			}
			if err := store.PutPlan(context.Background(), scoped); err != nil {
				t.Errorf("PutPlan: %v", err)
			}
			if _, _, err := store.GetPlan(context.Background(), scoped.TenantID, scoped.PlanID); err != nil {
				t.Errorf("GetPlan: %v", err)
			}
		})
	}
	group.Wait()
}

func planRecordFixture(t *testing.T, tenant ports.TenantID) ports.PlanRecord {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	schema, err := semantic.NewSchema(
		inputs.Compilation.Schema.EntityDeclarations(),
		inputs.Compilation.Schema.RelationDeclarations(),
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return ports.PlanRecord{
		TenantID:    tenant,
		PlanID:      plan.ID(),
		Schema:      schema,
		Compilation: compilation,
	}
}

func mustPut(t *testing.T, store *memory.Store, record ports.PlanRecord) {
	t.Helper()
	if err := store.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
}
