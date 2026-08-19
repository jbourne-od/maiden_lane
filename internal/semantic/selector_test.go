package semantic

import (
	"fmt"
	"testing"
)

// selectorState builds a state of drivers, some sharing an assignment key.
//
// Deliberately NOT insertion-ordered by assignment: the entities are created so that the
// canonical (kind, EntityID) order interleaves the groups, which is what makes the ordering
// guarantees observable rather than accidental.
func selectorState(t *testing.T, assignments ...string) State {
	t.Helper()
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	anchor, err := NewAtomValue("T0")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}

	entities := make([]Entity, 0, len(assignments))
	for i, assignment := range assignments {
		key, err := NewStringValue(assignment)
		if err != nil {
			t.Fatalf("NewStringValue: %v", err)
		}
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", fmt.Sprintf("d%d", i))},
			map[FieldName]Value{
				"assignment_key":    key,
				"hos_anchor":        anchor,
				"hos_elapsed_hours": NewInt64Value(int64(10 + i)),
				"hos_driving_hours": NewInt64Value(int64(8)),
			})
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		entities = append(entities, entity)
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state
}

func groupKey() Expr { return field("driver.assignment_key") }

func mustCompileSelector(t *testing.T, selector Selector) CompiledSelector {
	t.Helper()
	compiled, err := CompileSelector(expressionSchema(t), testCompilerVersion, selector)
	if err != nil {
		t.Fatalf("CompileSelector: %v", err)
	}
	return compiled
}

// The thing whose absence made a fleet inexpressible: ONE rule over MANY groups.
func TestSelectorGroupsEveryTeamFromOneDeclaration(t *testing.T) {
	state := selectorState(t, "a", "b", "a", "c", "b", "a")
	selector := mustCompileSelector(t, Selector{
		Kind:    "driver",
		GroupBy: ptr(groupKey()),
		Members: Cardinality{Kind: CardinalityAny},
	})

	groups, err := selector.Select(state)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	total := 0
	for _, group := range groups {
		total += len(group.Members())
	}
	if total != 6 {
		t.Fatalf("members across groups = %d, want 6", total)
	}
}

// ORDER IS AN IDENTITY PROBLEM. A set-scoped rule iterates, and nondeterminism there changes
// patch order, the journal, and every downstream identity. Groups are accumulated in a map,
// so this is the test that fails if the result ever follows map iteration.
//
// Repeated because Go randomizes map iteration per range: a single pass can agree with the
// canonical order by luck, and over enough passes it cannot.
func TestSelectorOrderIsCanonicalAndNotMapIteration(t *testing.T) {
	state := selectorState(t, "c", "a", "b", "a", "c", "b")
	selector := mustCompileSelector(t, Selector{
		Kind:    "driver",
		GroupBy: ptr(groupKey()),
		Members: Cardinality{Kind: CardinalityAny},
	})

	var first []string
	for pass := range 50 {
		groups, err := selector.Select(state)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		keys := make([]string, 0, len(groups))
		for _, group := range groups {
			text, ok := group.Key().String()
			if !ok {
				t.Fatalf("group key is not a string")
			}
			keys = append(keys, text)
		}
		if pass == 0 {
			first = keys
			// The keys were inserted c, a, b — so encounter order and canonical order differ,
			// and a result that merely echoed insertion would be visible here.
			if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
				t.Fatalf("groups are not in canonical key order: %v", keys)
			}
			continue
		}
		for i := range keys {
			if keys[i] != first[i] {
				t.Fatalf("pass %d ordered groups %v, pass 0 ordered them %v", pass, keys, first)
			}
		}
	}

	// Members inherit the state's canonical order rather than being sorted again here, so
	// assert that too: within a group, entity refs ascend.
	groups, err := selector.Select(state)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	for _, group := range groups {
		members := group.Members()
		for i := 1; i < len(members); i++ {
			if compareEntityRefs(members[i-1].Ref(), members[i].Ref()) >= 0 {
				t.Fatalf("members are not in canonical order within a group")
			}
		}
	}
}

// Cardinality is a declared property of the selector, which is where `len(sources) != 2`
// belongs — authored, not hard-coded in an executor.
func TestSelectorCardinalityFiltersGroups(t *testing.T) {
	state := selectorState(t, "pair", "solo", "pair", "trio", "trio", "trio")
	for _, test := range []struct {
		name    string
		members Cardinality
		want    int
	}{
		{"any", Cardinality{Kind: CardinalityAny}, 3},
		{"exactly two", Cardinality{Kind: CardinalityExactly, Count: 2}, 1},
		{"exactly three", Cardinality{Kind: CardinalityExactly, Count: 3}, 1},
		{"at least two", Cardinality{Kind: CardinalityAtLeast, Count: 2}, 2},
		{"exactly four", Cardinality{Kind: CardinalityExactly, Count: 4}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			selector := mustCompileSelector(t, Selector{
				Kind: "driver", GroupBy: ptr(groupKey()), Members: test.members,
			})
			groups, err := selector.Select(state)
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if len(groups) != test.want {
				t.Fatalf("groups = %d, want %d", len(groups), test.want)
			}
		})
	}
}

// An ungrouped selector applies per entity, not once over everything.
func TestSelectorUngroupedYieldsOneGroupPerEntity(t *testing.T) {
	state := selectorState(t, "a", "a", "a")
	selector := mustCompileSelector(t, Selector{
		Kind: "driver", Members: Cardinality{Kind: CardinalityAny},
	})
	groups, err := selector.Select(state)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3 (one per entity)", len(groups))
	}
	for _, group := range groups {
		if len(group.Members()) != 1 {
			t.Fatalf("ungrouped group holds %d members, want 1", len(group.Members()))
		}
	}
}

// Selecting nothing is a successful selection over an empty result, not an error: a rule whose
// predicate matched nothing HAS run and found nothing, which is a different fact from a rule
// that could not run.
func TestSelectorMatchingNothingSucceeds(t *testing.T) {
	state := selectorState(t, "a", "b")
	unmatchable, err := NewStringValue("nobody")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	selector := mustCompileSelector(t, Selector{
		Kind: "driver",
		Where: ptr(Expr{Kind: ExprEqual, Args: []Expr{
			field("driver.assignment_key"), {Kind: ExprLiteral, Literal: &unmatchable}}}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	groups, err := selector.Select(state)
	if err != nil {
		t.Fatalf("Select over an empty match returned an error: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %d, want 0", len(groups))
	}
}

// THE QUESTION SLICE 1 COULD NOT ASK. An expression type-checks equal(driver.x, team.y)
// because ExprType carries only the scalar kind; under a selector the answer is available,
// because the selection binds one entity of one kind and a path naming another has no
// referent.
func TestSelectorRefusesAPathOutsideItsKind(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
		{Kind: "team", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	crossKind := Expr{Kind: ExprEqual, Args: []Expr{
		field("driver.assignment_key"), field("team.assignment_key")}}

	// It compiles as an expression, which is the point: slice 1 cannot see the problem.
	if _, err := CompileExpression(schema, testCompilerVersion, crossKind); err != nil {
		t.Fatalf("the cross-kind expression did not compile, so this test proves nothing: %v", err)
	}
	if _, err := CompileSelector(schema, testCompilerVersion, Selector{
		Kind: "driver", Where: ptr(crossKind), Members: Cardinality{Kind: CardinalityAny},
	}); err == nil {
		t.Fatal("a selector accepted a predicate reading a kind it does not bind")
	}
}

// Every refusal a malformed selector must produce.
func TestCompileSelectorRefusals(t *testing.T) {
	schema := expressionSchema(t)
	always := Expr{Kind: ExprExists, Field: "driver.hos_anchor"}

	for _, test := range []struct {
		name     string
		selector Selector
	}{
		{"zero value", Selector{}},
		{"no cardinality", Selector{Kind: "driver"}},
		{"undeclared kind", Selector{
			Kind: "truck", Members: Cardinality{Kind: CardinalityAny}}},
		{"predicate is not bool", Selector{
			Kind: "driver", Where: ptr(field("driver.hos_elapsed_hours")),
			Members: Cardinality{Kind: CardinalityAny}}},
		{"grouping by a bool", Selector{
			Kind: "driver", GroupBy: ptr(always),
			Members: Cardinality{Kind: CardinalityAny}}},
		{"any with a count", Selector{
			Kind: "driver", Members: Cardinality{Kind: CardinalityAny, Count: 2}}},
		{"exactly zero", Selector{
			Kind: "driver", Members: Cardinality{Kind: CardinalityExactly}}},
		{"at least zero", Selector{
			Kind: "driver", Members: Cardinality{Kind: CardinalityAtLeast}}},
		// Unsatisfiable rather than merely odd: an ungrouped selector yields one member per
		// group, so these select nothing at all, and doing that silently is the failure.
		{"ungrouped exactly two", Selector{
			Kind: "driver", Members: Cardinality{Kind: CardinalityExactly, Count: 2}}},
		{"ungrouped at least two", Selector{
			Kind: "driver", Members: Cardinality{Kind: CardinalityAtLeast, Count: 2}}},
		{"predicate reads an undeclared field", Selector{
			Kind: "driver", Where: ptr(Expr{Kind: ExprExists, Field: "driver.nope"}),
			Members: Cardinality{Kind: CardinalityAny}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CompileSelector(schema, testCompilerVersion, test.selector); err == nil {
				t.Fatal("compiled a selector that should be refused")
			}
		})
	}
}

// A selector carries the schema it was checked against, so applying it to a state built on a
// different one is refused rather than re-checked. Re-checking here would make Select a second
// compiler, with its own opportunity to disagree with the first.
func TestSelectorRefusesAForeignSchema(t *testing.T) {
	other, err := NewSchema([]EntityDeclaration{{
		Kind:   "driver",
		Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	foreign, err := NewState(other, lineage, nil, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	selector := mustCompileSelector(t, Selector{
		Kind: "driver", Members: Cardinality{Kind: CardinalityAny}})
	if _, err := selector.Select(foreign); err == nil {
		t.Fatal("a selector applied to a state built on another schema")
	}
}

// ABSENCE IS REFUSED, NOT DEFAULTED. Yielding a zero for a missing field would make
// less(driver.hours, 10) true for an entity with no hours at all — a claim about the world
// nobody made.
func TestSelectorRefusesAnAbsentField(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	sparse, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "sparse")},
		map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(10)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	state, err := NewState(schema, lineage, []Entity{sparse}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	selector := mustCompileSelector(t, Selector{
		Kind:    "driver",
		Where:   ptr(Expr{Kind: ExprEqual, Args: []Expr{groupKey(), groupKey()}}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	if _, err := selector.Select(state); err == nil {
		t.Fatal("a predicate reading an absent field yielded an answer")
	}

	// exists() is how an author asks the question the refusal forces them to ask explicitly.
	present := mustCompileSelector(t, Selector{
		Kind:    "driver",
		Where:   ptr(Expr{Kind: ExprExists, Field: "driver.assignment_key"}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	groups, err := present.Select(state)
	if err != nil {
		t.Fatalf("exists over an absent field errored: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %d, want 0", len(groups))
	}
}

// Arithmetic overflow is refused rather than wrapped. Go wraps int64 silently, and a wrapped
// sum is a wrong answer the kernel would seal into a checkpoint and hash.
func TestEvaluateRefusesInt64Overflow(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	huge, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "huge")},
		map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(1<<63 - 1)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	sum := Expr{Kind: ExprAdd, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(1)}}
	if _, err := evaluateValue(sum, huge); err == nil {
		t.Fatal("adding one to the maximum int64 produced an answer")
	}
	_ = schema
}

func ptr[T any](value T) *T { return &value }
