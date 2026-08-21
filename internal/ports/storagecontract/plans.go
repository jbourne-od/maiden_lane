// Package storagecontract holds the behavioural contract every PlanStore
// implementation must satisfy.
//
// It exists because the claim that a durable adapter can replace the in-process
// one is worth verifying rather than asserting in a comment. One suite, run
// against every adapter, is what makes substitutability a tested property: an
// adapter that passes behaves like every other adapter, by construction.
//
// The suite deliberately imports no fixture package. It builds its own minimal
// valid compilations from internal/semantic alone, for two reasons. Storage
// behaviour has nothing to do with any particular domain rule, so coupling the
// two would make a storage test fail for a reason that has nothing to do with
// storage. And a non-test package under internal/ports must not import the
// team-HOS fixture, which the fixture-isolation test enforces.
//
// This package is test support. It imports "testing" and is imported only by
// _test.go files; nothing here belongs in a running process.
package storagecontract

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// RunPlanStoreContract asserts every behaviour a ports.PlanStore must exhibit.
//
// newStore must return an empty store. It is called per subtest, so no subtest
// can observe state another one left behind; an adapter that shares state
// across calls will fail the isolation assertions, which is correct.
func RunPlanStoreContract(t *testing.T, newStore func(*testing.T) ports.PlanStore) {
	t.Helper()

	t.Run("isolates tenants sharing an identity", func(t *testing.T) {
		// Plan identities are content derived, so two tenants compiling the
		// same declarations hold the same identity. That is the normal case,
		// not an edge case, and a store keyed only on the identity would let
		// one tenant read the other's record.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")
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
	})

	t.Run("reports a foreign tenant's plan as absent", func(t *testing.T) {
		// Distinguishing "exists but not yours" from "does not exist" leaks the
		// artifact's existence to a caller with no right to know it, which is an
		// authorization failure even when the content is withheld.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")
		mustPut(t, store, record)

		foreign, foreignFound, err := store.GetPlan(t.Context(), "intruder", record.PlanID)
		if err != nil {
			t.Fatalf("GetPlan: %v", err)
		}
		absent, absentFound, err := store.GetPlan(t.Context(), "intruder", unknownPlanID)
		if err != nil {
			t.Fatalf("GetPlan: %v", err)
		}

		if foreignFound || absentFound {
			t.Fatal("a foreign or unknown identity resolved to a record")
		}
		if foreign.PlanID != absent.PlanID || foreign.TenantID != absent.TenantID {
			t.Fatalf("a foreign plan is distinguishable from an absent one: %+v vs %+v", foreign, absent)
		}
		if foreign.PlanID != "" || foreign.TenantID != "" {
			t.Fatalf("an absent lookup returned data: %+v", foreign)
		}
	})

	t.Run("stores plans idempotently", func(t *testing.T) {
		// Identity is content derived, so the same identity always denotes the
		// same plan. Re-submitting must not conflict or duplicate.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")

		mustPut(t, store, record)
		mustPut(t, store, record)

		got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
		if err != nil || !found {
			t.Fatalf("GetPlan: found=%t err=%v", found, err)
		}
		if got.PlanID != record.PlanID {
			t.Fatalf("planID = %s, want %s", got.PlanID, record.PlanID)
		}
	})

	t.Run("keeps distinct plans for one tenant", func(t *testing.T) {
		store := newStore(t)
		first := PlanRecordFixture(t, "acme", "contract.v1")
		second := PlanRecordFixture(t, "acme", "contract.v2")
		if first.PlanID == second.PlanID {
			t.Fatal("the fixture produced one identity for two different plans")
		}

		mustPut(t, store, first)
		mustPut(t, store, second)

		for _, want := range []ports.PlanRecord{first, second} {
			got, found, err := store.GetPlan(t.Context(), "acme", want.PlanID)
			if err != nil || !found {
				t.Fatalf("GetPlan(%s): found=%t err=%v", want.PlanID, found, err)
			}
			if got.PlanID != want.PlanID {
				t.Errorf("planID = %s, want %s", got.PlanID, want.PlanID)
			}
		}
	})

	t.Run("refuses incomplete records", func(t *testing.T) {
		// An incomplete record would create an entry keyed by an empty tenant,
		// which is exactly the key a missing scope check produces.
		store := newStore(t)
		complete := PlanRecordFixture(t, "acme", "contract.v1")

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

		if _, found, _ := store.GetPlan(t.Context(), "", complete.PlanID); found {
			t.Error("an empty tenant resolved to a stored record")
		}
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		// An adapter that ignored cancellation would keep working after its
		// caller gave up. A durable one never will, so an in-process one must
		// behave the same or the port's guarantees differ by implementation.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")
		mustPut(t, store, record)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := store.PutPlan(ctx, record); !errors.Is(err, context.Canceled) {
			t.Errorf("PutPlan on a cancelled context = %v, want context.Canceled", err)
		}
		if _, _, err := store.GetPlan(ctx, "acme", record.PlanID); !errors.Is(err, context.Canceled) {
			t.Errorf("GetPlan on a cancelled context = %v, want context.Canceled", err)
		}
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		// The store is shared by every in-flight request.
		store := newStore(t)
		var group sync.WaitGroup
		for i := range 8 {
			group.Go(func() {
				record := PlanRecordFixture(t, "acme", "contract.v1")
				if i%2 == 0 {
					record.TenantID = "globex"
				}
				if err := store.PutPlan(context.Background(), record); err != nil {
					t.Errorf("PutPlan: %v", err)
				}
				if _, _, err := store.GetPlan(context.Background(), record.TenantID, record.PlanID); err != nil {
					t.Errorf("GetPlan: %v", err)
				}
			})
		}
		group.Wait()
	})

	t.Run("round trip reproduces the stored identities", func(t *testing.T) {
		// This is the assertion only a durable adapter can fail, and it is the
		// reason the record carries an immutable compilation input rather than a
		// compilation: a Compilation cannot be serialized, so an adapter stores
		// the input, recompiles, and must arrive back at the same identities.
		//
		// An in-process adapter passes trivially. That is deliberate: the suite
		// is written so a durable adapter cannot pass it by accident, and a
		// lossy or re-encoding round trip fails here rather than silently
		// returning a plan under an identity it did not produce.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")
		mustPut(t, store, record)

		got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
		if err != nil || !found {
			t.Fatalf("GetPlan: found=%t err=%v", found, err)
		}

		recompiled, err := semantic.Compile(got.Input.Request())
		if err != nil {
			t.Fatalf("recompile the retrieved input: %v", err)
		}
		plan, ok := recompiled.Plan()
		if !ok {
			failure, _ := recompiled.Failure()
			t.Fatalf("the retrieved input did not compile: %v", failure.Diagnostics())
		}
		if plan.ID() != record.PlanID {
			t.Fatalf("recompiled planID = %s, want %s", plan.ID(), record.PlanID)
		}
		if recompiled.InputDigest() != record.Input.Digest() {
			t.Fatalf("recompiled input digest = %s, want %s",
				recompiled.InputDigest(), record.Input.Digest())
		}

		// The retrieved record must also report the identities directly, so a
		// caller need not recompile to learn what it holds.
		if got.Input.Digest() != record.Input.Digest() {
			t.Fatalf("retrieved input digest = %s, want %s", got.Input.Digest(), record.Input.Digest())
		}
		if storedPlan, ok := got.Compilation.Plan(); !ok || storedPlan.ID() != record.PlanID {
			t.Fatalf("retrieved compilation does not carry the stored plan")
		}
		if got.Schema.Digest() != record.Schema.Digest() {
			t.Fatalf("retrieved schema digest = %s, want %s", got.Schema.Digest(), record.Schema.Digest())
		}
	})

	t.Run("stored records are unreachable for mutation", func(t *testing.T) {
		// This property is what lets an adapter return records by ordinary
		// assignment without defensive copying of its own. It stopped being
		// true once a mutable compiler request was retained, so it is asserted
		// rather than assumed.
		store := newStore(t)
		record := PlanRecordFixture(t, "acme", "contract.v1")
		mustPut(t, store, record)

		// Mutate what was handed to the store after storing it.
		mutateEverythingReachable(record)

		got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
		if err != nil || !found {
			t.Fatalf("GetPlan: found=%t err=%v", found, err)
		}
		// Mutate what the store handed back.
		mutateEverythingReachable(got)

		final, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
		if err != nil || !found {
			t.Fatalf("GetPlan: found=%t err=%v", found, err)
		}
		recompiled, err := semantic.Compile(final.Input.Request())
		if err != nil {
			t.Fatalf("recompile: %v", err)
		}
		plan, ok := recompiled.Plan()
		if !ok || plan.ID() != record.PlanID {
			t.Fatalf("the stored plan was reachable for mutation: planID = %v", plan.ID())
		}
	})
}

// unknownPlanID is a well-formed identity no fixture produces.
const unknownPlanID semantic.PlanID = "sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// PlanRecordFixture builds a valid record. version distinguishes otherwise
// identical plans, since plan identity is derived from the declarations and the
// pinned compiler semantics version.
//
// The compilation is deliberately domain-free -- storage behaviour has nothing to do with any
// particular rule, and a storage test that failed because a domain fixture changed would be
// reporting the wrong thing.
//
// IT IS NO LONGER EMPTY, and the difference matters. It previously compiled a request with NO
// TRANSFORMATIONS, so every store passed a contract that never asked whether a declaration
// survives being stored. It does not follow from "domain-free" that the fixture should carry
// no rule: what a store must round-trip is every declaration TYPE, and a ruleset with none
// exercises none of them. A Value reached a stored declaration for the first time with the
// select-and-assign operator, serialized to `{}` with no error, and made the plan unreadable
// after a successful write -- past a contract whose whole purpose is to keep the memory and
// SQL stores from drifting, and which could not see it.
//
// So the rule below is chosen to be shapeless and total rather than realistic: it reaches an
// expression tree, a literal of each value kind, a selector, a cardinality and an assignment,
// because those are the types storage has to carry.
func PlanRecordFixture(t *testing.T, tenant ports.TenantID, version string) ports.PlanRecord {
	t.Helper()

	schema, err := semantic.NewSchema([]semantic.EntityDeclaration{{
		Kind: "driver",
		Fields: []semantic.FieldDeclaration{
			{Name: "assignment_key", Kind: semantic.ValueString},
			{Name: "hos_anchor", Kind: semantic.ValueAtom},
			{Name: "hos_elapsed_hours", Kind: semantic.ValueInt64},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	anchor, err := semantic.NewAtomValue("T0")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}
	marker, err := semantic.NewStringValue("stored")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	groupBy := semantic.Expr{Kind: semantic.ExprField, Field: "driver.assignment_key"}
	hours := semantic.NewInt64Value(1)
	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema:                   schema.Declaration(),
		CompilerSemanticsVersion: semantic.CompilerSemanticsVersion(version),
		Rules: semantic.RulesetDeclaration{
			Transformations: []semantic.TransformationDeclaration{{
				ID:       "storage_contract.v1",
				Operator: semantic.OperatorSelectAndAssign,
				DeclaredReads: []semantic.FieldPath{
					"driver.assignment_key", "driver.hos_anchor", "driver.hos_elapsed_hours",
				},
				DeclaredWrites: []semantic.FieldPath{"driver.assignment_key", "driver.hos_anchor", "driver.hos_elapsed_hours"},
				SelectAssign: &semantic.SelectAssignDeclaration{
					Selector: semantic.Selector{
						Kind:    "driver",
						Where:   &semantic.Expr{Kind: semantic.ExprExists, Field: "driver.hos_anchor"},
						GroupBy: &groupBy,
						Members: semantic.Cardinality{Kind: semantic.CardinalityAtLeast, Count: 1},
					},
					Guard: semantic.Expr{Kind: semantic.ExprAllEqual, Field: "driver.assignment_key"},
					// One assignment per value kind, so no kind's text form is untested.
					Assignments: []semantic.FieldAssignment{
						{Target: "driver.assignment_key", Value: semantic.Expr{Kind: semantic.ExprLiteral, Literal: &marker}},
						{Target: "driver.hos_anchor", Value: semantic.Expr{Kind: semantic.ExprLiteral, Literal: &anchor}},
						{Target: "driver.hos_elapsed_hours", Value: semantic.Expr{Kind: semantic.ExprAdd, Args: []semantic.Expr{
							{Kind: semantic.ExprField, Field: "driver.hos_elapsed_hours"},
							{Kind: semantic.ExprLiteral, Literal: &hours},
						}}},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("contract fixture did not compile: %v", failure.Diagnostics())
	}

	return ports.PlanRecord{
		TenantID:    tenant,
		PlanID:      plan.ID(),
		Input:       compilation.Input(),
		Schema:      schema,
		Compilation: compilation,
	}
}

// corruptSelectAssign reaches every authored field of a selector-scoped payload, including the
// expression trees and the literals inside them.
func corruptSelectAssign(payload *semantic.SelectAssignDeclaration) {
	if payload == nil {
		return
	}
	corruptExpr := func(expr *semantic.Expr) {
		if expr == nil {
			return
		}
		var walk func(*semantic.Expr)
		walk = func(node *semantic.Expr) {
			node.Kind = semantic.ExprField
			node.Field = "corrupted.corrupted"
			if node.Literal != nil {
				corrupted := semantic.NewInt64Value(-1)
				*node.Literal = corrupted
			}
			for i := range node.Args {
				walk(&node.Args[i])
			}
		}
		walk(expr)
	}
	payload.Selector.Kind = "corrupted"
	payload.Selector.Members = semantic.Cardinality{Kind: semantic.CardinalityExactly, Count: 99}
	corruptExpr(payload.Selector.Where)
	corruptExpr(payload.Selector.GroupBy)
	corruptExpr(&payload.Guard)
	for i := range payload.Assignments {
		payload.Assignments[i].Target = "corrupted.corrupted"
		corruptExpr(&payload.Assignments[i].Value)
	}
}

// mutateEverythingReachable corrupts every part of a record an ordinary caller
// can reach through its public accessors.
func mutateEverythingReachable(record ports.PlanRecord) {
	request := record.Input.Request()
	request.CompilerSemanticsVersion = "corrupted"
	for i := range request.Rules.Transformations {
		request.Rules.Transformations[i].ID = "corrupted"
		// THE PAYLOAD SUBTREE, not just the ID. The fixture gained a select-and-assign rule so
		// that storage is tested against every declaration type, and a walker that reached
		// only the ID would have left the whole new subtree -- selector, filter, grouping,
		// guard, assignments, literals -- outside a function whose comment claims it corrupts
		// everything an ordinary caller can reach. A store that returned the record by value
		// while retaining the caller's SelectAssign pointer would then be invisible to the
		// subtest that exists to catch exactly that.
		corruptSelectAssign(request.Rules.Transformations[i].SelectAssign)
	}
	for i := range request.Rules.Checkpoints {
		request.Rules.Checkpoints[i].Key = "corrupted"
	}
	for i := range request.Profiles {
		request.Profiles[i].Key = "corrupted"
	}
	entities := request.Schema.EntityDeclarations()
	for i := range entities {
		entities[i].Kind = "corrupted"
		for j := range entities[i].Fields {
			entities[i].Fields[j].Name = "corrupted"
		}
	}

	declaration := record.Schema.Declaration()
	for _, entity := range declaration.EntityDeclarations() {
		for i := range entity.Fields {
			entity.Fields[i].Name = "corrupted"
		}
	}

	if plan, ok := record.Compilation.Plan(); ok {
		for _, transformation := range plan.Transformations() {
			corrupted := transformation.Declaration()
			corrupted.ID = "corrupted"
		}
		canonical := plan.CanonicalBytes()
		for i := range canonical {
			canonical[i] = 0
		}
	}
	for _, profile := range record.Compilation.Profiles() {
		corrupted := profile.Declaration()
		corrupted.Key = "corrupted"
	}
	canonical := record.Input.CanonicalBytes()
	for i := range canonical {
		canonical[i] = 0
	}
}

func mustPut(t *testing.T, store ports.PlanStore, record ports.PlanRecord) {
	t.Helper()
	if err := store.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
}
