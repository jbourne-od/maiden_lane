package semantic

import (
	"slices"
	"testing"
)

// selectorRule builds a set-scoped rule reading one field and writing another.
//
// Everything is grouped by driver.depot, which is the point: decision 4's worry is that
// set-scoped rules over ONE entity kind overlap their read and write sets far more readily
// than rules naming their sources, because every such rule reads the grouping field.
func selectorRule(t *testing.T, id RuleID, guardOn FieldPath, target FieldPath, value Expr) TransformationDeclaration {
	t.Helper()
	groupBy := fieldExpr("driver.depot")
	reads := []FieldPath{"driver.depot", guardOn}
	reads = append(reads, readFieldPaths(value)...)
	slices.Sort(reads)
	reads = slices.Compact(reads)
	return TransformationDeclaration{
		ID:             id,
		Operator:       OperatorSelectAndAssign,
		DeclaredReads:  reads,
		DeclaredWrites: []FieldPath{target},
		SelectAssign: &SelectAssignDeclaration{
			Selector: Selector{
				Kind: "driver", GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityAtLeast, Count: 1},
			},
			Guard:       Expr{Kind: ExprAllMembers, Args: []Expr{{Kind: ExprExists, Field: guardOn}}},
			Assignments: []FieldAssignment{{Target: target, Value: value}},
		},
	}
}

func compileSelectorRules(t *testing.T, rules ...TransformationDeclaration) Compilation {
	t.Helper()
	compilation, err := Compile(CompileRequest{
		Schema:                   selectAssignSchema(t).Declaration(),
		Rules:                    RulesetDeclaration{Transformations: rules},
		CompilerSemanticsVersion: "semantics.v1",
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return compilation
}

// Decision 4's open question, answered by measurement rather than by argument.
//
// The plan deferred it to the first slice that lands a rule end to end, because answering it
// needs a set-scoped ruleset that actually reaches the compiler. It does now, so this records
// what the compiler does -- as a test, so the answer stops being true silently.
//
// THE ANSWER IS THAT SET-SCOPING DOES NOT CAUSE CYCLES BY ITSELF, and the feared mechanism is
// real but narrower than stated. Two set-scoped rules over one kind are ordered, not cyclic,
// whenever the writes flow one way. A cycle needs each rule to read what the other writes.
//
// What set-scoping DOES change is how easily that happens: a rule writing the grouping field
// gains an edge from every other rule over that kind at once, because every one of them reads
// it. That is the case the last subtest pins.
func TestSetScopedRulesCycleOnlyWhenWritesFlowBothWays(t *testing.T) {
	certified := stringLiteral(t, "certified")
	total := Expr{Kind: ExprAdd, Args: []Expr{
		fieldExpr("driver.driving_hours"), fieldExpr("driver.rest_hours"),
	}}

	t.Run("writes flowing one way are ordered and accepted", func(t *testing.T) {
		first := selectorRule(t, "certify.v1", "driver.violations", "driver.status", certified)
		second := selectorRule(t, "total.v1", "driver.status", "driver.shift_total", total)
		compilation := compileSelectorRules(t, first, second)
		plan, accepted := compilation.Plan()
		if !accepted {
			failure, _ := compilation.Failure()
			t.Fatalf("one-way ruleset refused: %v", failure.Diagnostics())
		}
		// The derived edge must exist, or this subtest proves only that two unrelated rules
		// compile -- which would be true of any two rules and would pin nothing.
		ordered := plan.MustTransformation("total.v1")
		if !slices.Contains(ordered.Dependencies(), RuleID("certify.v1")) {
			t.Fatalf("no derived dependency on the rule writing driver.status: %v", ordered.Dependencies())
		}
	})

	t.Run("writes flowing both ways are a cycle", func(t *testing.T) {
		first := selectorRule(t, "certify.v1", "driver.shift_total", "driver.status", certified)
		second := selectorRule(t, "total.v1", "driver.status", "driver.shift_total", total)
		compilation := compileSelectorRules(t, first, second)
		diagnostics := compilationDiagnostics(t, compilation)
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Code() == DependencyCycle {
				found = true
			}
		}
		if !found {
			t.Fatalf("diagnostics %v, want DEPENDENCY_CYCLE", diagnostics)
		}
	})

	// THE MECHANISM DECISION 4 NAMED, isolated. Writing the grouping field is what makes
	// set-scoping structurally different: every rule over the kind reads that field, so one
	// writer of it is upstream of all of them at once -- and a second writer of it is a
	// cycle, since each reads what the other writes.
	t.Run("two rules writing the grouping field cycle on it alone", func(t *testing.T) {
		first := selectorRule(t, "relabel_a.v1", "driver.violations", "driver.depot", stringLiteral(t, "north"))
		second := selectorRule(t, "relabel_b.v1", "driver.violations", "driver.depot", stringLiteral(t, "south"))
		compilation := compileSelectorRules(t, first, second)
		diagnostics := compilationDiagnostics(t, compilation)
		codes := make([]CompilationDiagnosticCode, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			codes = append(codes, diagnostic.Code())
		}
		// Either refusal is sound. Which one it is depends on compile.go:800, which skips
		// write-conflict analysis for a pair a dependency edge already orders -- so a pair
		// that is BOTH conflicting and cyclic reports the cycle. Recorded as a disjunction
		// because pinning the specific code here would pin that interaction by accident.
		if !slices.Contains(codes, DependencyCycle) && !slices.Contains(codes, WriteConflictUnresolved) {
			t.Fatalf("diagnostics %v, want a cycle or a write conflict", diagnostics)
		}
	})

	// And the reassuring half, which matters because the over-strictness decision 4 starts
	// from is real: two set-scoped rules writing DIFFERENT fields of the same kind, reading
	// nothing the other writes, are neither ordered nor refused.
	t.Run("disjoint writes over one kind are independent", func(t *testing.T) {
		first := selectorRule(t, "certify.v1", "driver.violations", "driver.status", certified)
		second := selectorRule(t, "total.v1", "driver.violations", "driver.shift_total", total)
		compilation := compileSelectorRules(t, first, second)
		plan, accepted := compilation.Plan()
		if !accepted {
			failure, _ := compilation.Failure()
			t.Fatalf("independent set-scoped rules refused: %v", failure.Diagnostics())
		}
		for _, id := range []RuleID{"certify.v1", "total.v1"} {
			if deps := plan.MustTransformation(id).Dependencies(); len(deps) != 0 {
				t.Fatalf("%s gained dependencies %v", id, deps)
			}
		}
	})
}

// The baseline this whole programme started from, now on the other side.
//
// TestMultiInstanceRulesetBaseline records that N rules of one shape, one per instance,
// produce C(N,2) unresolved write conflicts -- because nothing in a field path distinguishes
// one instance from another, so "one rule per team" was never a workaround for the missing
// selector. At ten instances that is forty-five refusals.
//
// ONE set-scoped rule covers the same ten groups with no conflict to analyse, because there
// is only one rule. This is the claim the programme was built to make true, so it is asserted
// against the same instance count the baseline uses, and it executes rather than merely
// compiling -- a ruleset that compiles for a fleet it cannot transform would be the same
// unreachability every earlier slice shipped.
func TestOneSetScopedRuleCoversAFleetTheBaselineCannotExpress(t *testing.T) {
	const depots = 10
	schema := selectAssignSchema(t)
	drivers := make([]driverFixture, 0, depots*2)
	for depot := 0; depot < depots; depot++ {
		name := string(rune('a' + depot))
		drivers = append(drivers,
			driverFixture{key: name + "1", depot: name, driving: 8, rest: 10, violations: 1},
			driverFixture{key: name + "2", depot: name, driving: 9, rest: 11, violations: 2},
		)
	}
	plan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
	state := selectAssignState(t, schema, drivers)
	outcome := mustAcceptedTransition(t, selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})

	if !outcome.HasPatch() {
		t.Fatal("the fleet transition carried no patch")
	}
	if got := len(outcome.Patch().Operations()); got != depots*2 {
		t.Fatalf("patch holds %d operations, want one per driver (%d)", got, depots*2)
	}
	certified := 0
	for _, entity := range outcome.State().Entities() {
		if value, present := entity.Field("status"); present {
			if text, _ := value.String(); text == "certified" {
				certified++
			}
		}
	}
	if certified != depots*2 {
		t.Fatalf("%d drivers certified, want %d", certified, depots*2)
	}
}
