package semantic

import (
	"encoding/hex"
	"fmt"
	"slices"
	"testing"
)

// selectorState builds a state of drivers, some sharing an assignment key.
//
// The order this appends in is NOT observable: NewState sorts by compareEntityRefs, so the
// canonical order is a function of the entity IDs, which are digests. Tests that depend on
// encounter order differing from canonical order must check that themselves -- see
// firstEncounterOrder.
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

	selection, err := selector.Select(state)
	groups := selection.Groups()
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
		selection, err := selector.Select(state)
		groups := selection.Groups()
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
			if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
				t.Fatalf("groups are not in canonical key order: %v", keys)
			}
			// AND THE FIXTURE MUST ACTUALLY DISTINGUISH THE TWO ORDERS. An earlier version of
			// this test claimed "the keys were inserted c, a, b, so encounter order differs"
			// -- which is false: NewState sorts by compareEntityRefs, so the order this
			// helper appended in is not observable at all, and first-encounter order is a
			// sha256 permutation nobody pinned. It happened to differ, making the kill a
			// 5-in-6 accident that renaming a lineage string would silently undo.
			//
			// So the encounter order is computed here and required to differ. If it ever
			// coincides, this fails loudly telling the author to fix the fixture rather than
			// quietly ceasing to test anything.
			if encountered := firstEncounterOrder(t, state); slices.Equal(encountered, keys) {
				t.Fatalf("encounter order %v equals canonical order, so this fixture cannot "+
					"distinguish them; change the entity keys until it does", encountered)
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
	selection, err := selector.Select(state)
	groups := selection.Groups()
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
			selection, err := selector.Select(state)
			groups := selection.Groups()
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
	selection, err := selector.Select(state)
	groups := selection.Groups()
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
	selection, err := selector.Select(state)
	groups := selection.Groups()
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

// THE LOAD-BEARING HALF OF THE SAME RULE, pinned against a constant.
//
// compileSelectorExpr's `named != kind` is the check that actually gates authored input:
// Select filters entities by kind before evaluating, so boundField's identical guard is
// defence-in-depth and cannot fire in production.
//
// BEFORE THIS TEST EXISTED, every selector in the suite carrying a predicate or a grouping
// declared Kind "driver", so replacing `named != kind` with `named != "driver"` survived the
// whole suite: the fixture's value for the dimension under test was exactly the literal the
// broken code would hardcode. A previous round hardened the unreachable copy and left this
// one satisfiable by a constant. Both are pinned now, which is why the sentence above is in
// the past tense -- a comment claiming a live gap the same commit closes is the defect this
// suite exists to catch.
func TestSelectorBindsItsOwnKindWhicheverThatIs(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
		{Kind: "team", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	// A selector over a NON-driver kind, reading its own field, must compile. Under the
	// hardcoded mutant it is refused by a message that contradicts itself, naming the very
	// kind it claims is the only one bound.
	if _, err := CompileSelector(schema, testCompilerVersion, Selector{
		Kind:    "team",
		Where:   ptr(Expr{Kind: ExprExists, Field: "team.assignment_key"}),
		Members: Cardinality{Kind: CardinalityAny},
	}); err != nil {
		t.Fatalf("a team selector reading a team field was refused: %v", err)
	}

	// And the cross-kind direction from the other side: a team selector reading a DRIVER
	// path must be refused. Under the mutant it compiles, and Select then filters to teams
	// and refuses at evaluation -- a compiler admitting an input the evaluator rejects.
	if _, err := CompileSelector(schema, testCompilerVersion, Selector{
		Kind:    "team",
		Where:   ptr(Expr{Kind: ExprExists, Field: "driver.assignment_key"}),
		Members: Cardinality{Kind: CardinalityAny},
	}); err == nil {
		t.Fatal("a team selector accepted a predicate reading a driver path")
	}

	// THE OTHER DOOR. CompileSelector passes selector.Kind into compileSelectorExpr twice,
	// independently, once for the predicate and once for the grouping. Pinning the predicate
	// path leaves the grouping path satisfiable by a constant: hardcoding "driver" in the
	// GroupBy call alone survives the whole suite otherwise. Two call sites of one rule are
	// two things to pin, which is the same lesson as the compiler/evaluator pair one level up.
	if _, err := CompileSelector(schema, testCompilerVersion, Selector{
		Kind:    "team",
		GroupBy: ptr(field("team.assignment_key")),
		Members: Cardinality{Kind: CardinalityAny},
	}); err != nil {
		t.Fatalf("a team selector grouping on a team field was refused: %v", err)
	}
	if _, err := CompileSelector(schema, testCompilerVersion, Selector{
		Kind:    "team",
		GroupBy: ptr(field("driver.assignment_key")),
		Members: Cardinality{Kind: CardinalityAny},
	}); err == nil {
		t.Fatal("a team selector accepted a grouping reading a driver path")
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
		// GROUPED, because every other cardinality fixture here is ungrouped and the
		// ungrouped-unsatisfiability rule refuses exactly-zero independently. Without these,
		// narrowing the positive-count guard to at-least-only survives the whole suite, and a
		// grouped exactly-zero selector compiles and then selects nothing for every input.
		{"grouped exactly zero", Selector{
			Kind: "driver", GroupBy: ptr(groupKey()),
			Members: Cardinality{Kind: CardinalityExactly}}},
		{"grouped at least zero", Selector{
			Kind: "driver", GroupBy: ptr(groupKey()),
			Members: Cardinality{Kind: CardinalityAtLeast}}},
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
	selection, err := present.Select(state)
	groups := selection.Groups()
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
	if _, err := evaluateValue(expressionSchema(t), sum, huge); err == nil {
		t.Fatal("adding one to the maximum int64 produced an answer")
	}
	_ = schema
}

// Production break caught: equal over BOOL operands answered false for every pair, because a
// bool result lives in .boolean and the comparison read .value, which is the zero Value for
// every bool-producing node. It compiled cleanly and returned a total, deterministic wrong
// answer -- the compiler's type lattice and the evaluator's kind tag were each correct against
// their own reference, and neither owned the fact that TypeBool has no value.
func TestEvaluateComparesBooleans(t *testing.T) {
	// A DECLARED-BUT-ABSENT field, not an undeclared one. An earlier version used
	// "driver.nope" as its source of false -- a path TestCompileExpressionRefusals asserts is
	// meaningless. One test said the node has no meaning while another depended on it
	// evaluating to false, and the two only coexisted because the evaluator did not check
	// declaredness. Now that it does, this fixture has to say what it means.
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	anchor, err := NewAtomValue("T0")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}
	entity, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "partial")},
		map[FieldName]Value{"hos_anchor": anchor})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	_ = schema
	present := Expr{Kind: ExprExists, Field: "driver.hos_anchor"}
	absent := Expr{Kind: ExprExists, Field: "driver.assignment_key"}

	for _, test := range []struct {
		name  string
		left  Expr
		right Expr
		want  bool
	}{
		{"both true", present, present, true},
		{"both false", absent, absent, true},
		{"differing", present, absent, false},
		{"through not", Expr{Kind: ExprNot, Args: []Expr{present}}, absent, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := evaluateBool(expressionSchema(t),
				Expr{Kind: ExprEqual, Args: []Expr{test.left, test.right}}, entity)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got != test.want {
				t.Fatalf("equal = %v, want %v", got, test.want)
			}
		})
	}
}

// ABSENCE MUST BE TOLERABLE, or a language cannot describe real inputs. Absence is refused,
// so all() and any() must short circuit for "present and below" to be writable at all --
// without it one sparse entity fails the entire selection.
func TestSelectorToleratesSparseEntitiesThroughShortCircuit(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	full, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "full")},
		map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(5)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	sparse, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "sparse")},
		map[FieldName]Value{"hos_driving_hours": NewInt64Value(1)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	state, err := NewState(schema, lineage, []Entity{full, sparse}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// "present and below ten", the predicate a real ruleset writes over sparse data.
	guarded := mustCompileSelector(t, Selector{
		Kind: "driver",
		Where: ptr(Expr{Kind: ExprAll, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_elapsed_hours"},
			{Kind: ExprLess, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(10)}},
		}}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	selection, err := guarded.Select(state)
	groups := selection.Groups()
	if err != nil {
		t.Fatalf("a guarded predicate failed over a sparse entity: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (the entity that has the field)", len(groups))
	}

	// And the unguarded form still refuses, so the guard is doing the work rather than the
	// evaluator having quietly started defaulting.
	unguarded := mustCompileSelector(t, Selector{
		Kind: "driver",
		Where: ptr(Expr{Kind: ExprLess, Args: []Expr{
			field("driver.hos_elapsed_hours"), intLiteral(10)}}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	if _, err := unguarded.Select(state); err == nil {
		t.Fatal("an unguarded predicate read an absent field without refusing")
	}
}

// The evaluator claims to be total. An earlier version panicked on any under-arity node while
// its sibling arms all guarded exactly that class of un-compiled input.
func TestEvaluateIsTotalOnMalformedNodes(t *testing.T) {
	state := selectorState(t, "a")
	entity := state.Entities()[0]
	for _, expr := range []Expr{
		{Kind: ExprNot},
		{Kind: ExprEqual},
		{Kind: ExprEqual, Args: []Expr{intLiteral(1)}},
		{Kind: ExprLess, Args: []Expr{intLiteral(1)}},
		{Kind: ExprAdd},
		{Kind: ExprLiteral},
		{},
	} {
		if _, err := evaluateExpr(expressionSchema(t), expr, entity); err == nil {
			t.Errorf("kind %d with %d args evaluated", expr.Kind, len(expr.Args))
		}
	}
}

// THE SELECTOR ENCODING IS AN IDENTITY and had no test at all. Slice 1 established why that
// matters and slice 2 shipped a second domain tag without one.
//
// These are behavioural rather than golden, because unlike the expression case the dimensions
// here CAN be varied independently by valid selectors: every component below is changed while
// holding the rest fixed, and each must move the bytes.
func TestSelectorIdentityCommitsToEveryComponent(t *testing.T) {
	schema := expressionSchema(t)
	base := Selector{
		Kind:    "driver",
		Where:   ptr(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
		GroupBy: ptr(groupKey()),
		Members: Cardinality{Kind: CardinalityExactly, Count: 2},
	}
	baseline := mustCompileSelector(t, base)

	other := Expr{Kind: ExprExists, Field: "driver.assignment_key"}
	for _, test := range []struct {
		name     string
		selector Selector
	}{
		{"cardinality kind", withMembers(base, Cardinality{Kind: CardinalityAtLeast, Count: 2})},
		{"cardinality count", withMembers(base, Cardinality{Kind: CardinalityExactly, Count: 3})},
		{"predicate", withWhere(base, &other)},
		{"grouping", withGroupBy(base, ptr(field("driver.hos_anchor")))},
		// Presence itself, in both slots: dropping an expression must move the bytes.
		{"no predicate", withWhere(base, nil)},
		// Cardinality changes too, because base's Exactly-2 is unsatisfiable ungrouped, so
		// this subtest alone does not isolate the groupBy presence byte. The isolated
		// comparison is below, against a selector differing in nothing else.
		{"no grouping", withMembers(withGroupBy(base, nil), Cardinality{Kind: CardinalityAny})},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompileSelector(schema, testCompilerVersion, test.selector)
			if err != nil {
				t.Fatalf("CompileSelector: %v", err)
			}
			if string(compiled.CanonicalBytes()) == string(baseline.CanonicalBytes()) {
				t.Fatal("changing this component did not change the identity")
			}
		})
	}

	// The two nullable slots cannot hold the SAME expression -- a predicate must be bool and
	// a group key must not be -- so the confusable-slots collision an earlier draft of this
	// test tried to construct is not constructible. What is testable is that presence is
	// encoded at all, which the two cases above cover, and that a selector with neither
	// expression differs from one with either.
	bare := mustCompileSelector(t, Selector{
		Kind: "driver", Members: Cardinality{Kind: CardinalityAny}})
	noGrouping := mustCompileSelector(t,
		withMembers(withGroupBy(base, nil), Cardinality{Kind: CardinalityAny}))
	noPredicate := mustCompileSelector(t,
		withMembers(withWhere(base, nil), Cardinality{Kind: CardinalityAny}))
	for name, other := range map[string]CompiledSelector{
		"predicate only": noGrouping, "grouping only": noPredicate} {
		if string(bare.CanonicalBytes()) == string(other.CanonicalBytes()) {
			t.Fatalf("a selector with no expressions encodes identically to a %s one", name)
		}
	}
	// The groupBy presence byte in isolation: these differ in nothing but whether GroupBy is
	// set, with cardinality and predicate held fixed.
	withGrouping := mustCompileSelector(t, Selector{
		Kind: "driver", GroupBy: ptr(groupKey()), Members: Cardinality{Kind: CardinalityAny}})
	if string(bare.CanonicalBytes()) == string(withGrouping.CanonicalBytes()) {
		t.Fatal("the groupBy presence byte is not encoded")
	}

	// And the schema participates, for the same reason it does in an expression: the
	// selector's field paths mean nothing without the schema they were checked against.
	narrower, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "hos_anchor", Kind: ValueAtom},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	underNarrower, err := CompileSelector(narrower, testCompilerVersion, Selector{
		Kind: "driver", Where: ptr(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
		GroupBy: ptr(groupKey()), Members: Cardinality{Kind: CardinalityExactly, Count: 2},
	})
	if err != nil {
		t.Fatalf("CompileSelector under the narrower schema: %v", err)
	}
	if string(underNarrower.CanonicalBytes()) == string(baseline.CanonicalBytes()) {
		t.Fatal("two selectors checked against different schemas share an identity")
	}
}

// A BARE SELECTOR REACHES NO OTHER VALIDATION, so it is the shape that finds a guard living
// in the wrong place. An earlier version validated the version only inside CompileExpression,
// which a selector with neither a predicate nor a grouping never calls.
func TestCompileSelectorRequiresAUsableVersion(t *testing.T) {
	schema := expressionSchema(t)
	bare := Selector{Kind: "driver", Members: Cardinality{Kind: CardinalityAny}}
	withExpr := Selector{
		Kind:    "driver",
		Where:   ptr(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
		Members: Cardinality{Kind: CardinalityAny},
	}

	for _, version := range []CompilerSemanticsVersion{"", CompilerSemanticsVersion([]byte{0xff})} {
		// Both shapes, because the point is that the two must not disagree: adding a
		// predicate is a change to a field the version check has no business depending on.
		for name, selector := range map[string]Selector{"bare": bare, "with predicate": withExpr} {
			if _, err := CompileSelector(schema, version, selector); err == nil {
				t.Errorf("%s selector compiled under version %q", name, version)
			}
		}
	}
}

// Production break caught, and it is the bool-equality defect one type down. An invalid
// literal produced a result whose kind tag did not match its payload -- TypeInvalid with a
// nil error -- so equal took the value path and Value.Equal's default returned false for two
// byte-identical operands.
//
// The compiler already refuses this node. The evaluator's contract is to refuse un-compiled
// input rather than answer it, so both halves must agree about the same input.
func TestEvaluateRefusesAnInvalidLiteral(t *testing.T) {
	state := selectorState(t, "a")
	entity := state.Entities()[0]
	invalid := Value{}
	node := Expr{Kind: ExprLiteral, Literal: &invalid}

	if _, err := evaluateExpr(expressionSchema(t), node, entity); err == nil {
		t.Fatal("an invalid literal evaluated")
	}
	// The composition that produced the wrong answer, not merely the leaf.
	if _, err := evaluateExpr(expressionSchema(t),
		Expr{Kind: ExprEqual, Args: []Expr{node, node}}, entity); err == nil {
		t.Fatal("equal over two invalid literals answered instead of refusing")
	}
	// And the compiler refuses it too, so the two halves agree.
	if _, err := CompileExpression(expressionSchema(t), testCompilerVersion, node); err == nil {
		t.Fatal("the compiler accepted an invalid literal, so the two halves now disagree")
	}
}

// THE THIRD INSTANCE OF THE SHAPE, in a different guise. Not a kind/payload mismatch but a
// REFERENT mismatch: boundField's entity-kind check is the only thing stopping a `team.` path
// from reading a driver's field. Remove it and nothing panics, nothing errors, and the result
// is correctly typed — it just answers a question about a different entity.
//
// The compiler refuses this through compileSelectorExpr, and TestSelectorRefusesAPathOutsideItsKind
// covers that. This covers the evaluator, which the guard's own comment says is "reachable
// without one" — the reachability the two previous wrong-answer defects both lived in.
func TestEvaluateRefusesAPathNamingAnotherKind(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
		{Kind: "team", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	key, err := NewStringValue("k")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	driver, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "d")},
		map[FieldName]Value{"assignment_key": key})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	// THE TWO-KIND SCHEMA, not expressionSchema. This test built one and then discarded it,
	// so `team.assignment_key` was refused by the DECLAREDNESS check -- expressionSchema does
	// not declare `team` at all -- and the kind guard this test names was never reached.
	// Deleting that guard left the whole suite green. A guard is only tested when the fixture
	// reaches the state where that guard is the sole reason for refusal.

	// The driver's own path resolves; the team's must not, even though both kinds declare a
	// field of that name and the driver holds a value for it.
	if _, err := evaluateExpr(schema, field("driver.assignment_key"), driver); err != nil {
		t.Fatalf("the bound kind's own path did not resolve: %v", err)
	}
	if _, err := evaluateExpr(schema, field("team.assignment_key"), driver); err == nil {
		t.Fatal("a team path read a driver's field")
	}
	if _, err := evaluateExpr(schema,
		Expr{Kind: ExprExists, Field: "team.assignment_key"}, driver); err == nil {
		t.Fatal("exists on a team path answered against a driver")
	}

	// evaluateBool's type refusal has the same shape: on removal it returns .boolean, which
	// is false for a value-typed result, so a non-bool operand answers false rather than
	// refusing.
	if _, err := evaluateBool(schema, field("driver.assignment_key"), driver); err == nil {
		t.Fatal("a string-typed expression was accepted as a bool")
	}

	// AND THE SYMMETRIC CASE, because binding only a driver lets the guard be satisfied by a
	// constant. Before this block, replacing `kind != entity.Ref().Kind` with
	// `kind != "driver"` refused exactly the inputs above and survived the whole suite, while
	// being wrong for every entity of any other kind. A guard compared against the one value
	// the fixture happens to hold is not tested. Past tense deliberately: this block kills
	// that mutant.
	team, err := NewEntity(
		EntityRef{Kind: "team", ID: SourceEntityID(lineage, "team", "t")},
		map[FieldName]Value{"assignment_key": key})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	if _, err := evaluateExpr(schema, field("team.assignment_key"), team); err != nil {
		t.Fatalf("the team's own path did not resolve against a team: %v", err)
	}
	if _, err := evaluateExpr(schema, field("driver.assignment_key"), team); err == nil {
		t.Fatal("a driver path read a team's field")
	}
}

// The negative branch of the overflow check. An earlier test covered only maxInt64 + 1, which
// is the first disjunct; deleting the second let the int64 minimum plus -1 wrap silently.
func TestEvaluateRefusesNegativeInt64Overflow(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	floor, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "floor")},
		map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(-1 << 63)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	_ = schema

	sum := Expr{Kind: ExprAdd, Args: []Expr{
		field("driver.hos_elapsed_hours"), intLiteral(-1)}}
	if got, err := evaluateValue(expressionSchema(t), sum, floor); err == nil {
		value, _ := got.Int64()
		t.Fatalf("MinInt64 + -1 produced %d instead of refusing", value)
	}
}

// A cardinality violation is REPORTED, not dropped. The thing cardinality replaces --
// `len(sources) != 2` in execute_form.go -- calls rejectInvariant, an observable attributable
// failure. Dropping the group instead would mean a three-driver team never forms and nothing
// records why, and an empty explicit selection assesses vacuously Ready.
func TestSelectorReportsCardinalityViolations(t *testing.T) {
	state := selectorState(t, "pair", "pair", "trio", "trio", "trio", "solo")
	selector := mustCompileSelector(t, Selector{
		Kind: "driver", GroupBy: ptr(groupKey()),
		Members: Cardinality{Kind: CardinalityExactly, Count: 2},
	})
	selection, err := selector.Select(state)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(selection.Groups()) != 1 {
		t.Fatalf("satisfying groups = %d, want 1", len(selection.Groups()))
	}
	if len(selection.Violations()) != 2 {
		t.Fatalf("violations = %d, want 2 (the trio and the solo)", len(selection.Violations()))
	}
	// Violations carry their members, so a consumer can say what was wrong rather than only
	// that something was.
	total := 0
	for _, group := range selection.Violations() {
		total += len(group.Members())
	}
	if total != 4 {
		t.Fatalf("members across violations = %d, want 4", total)
	}
}

// THE FOURTH INSTANCE, and it was inside the fix for the second. An earlier round collapsed
// both literal call sites onto valueKindType, which switches on the KIND and never consults
// Valid(). The compiler's mapping is literalType: Valid() and then valueKindType. So a Value
// with a recognised kind and invalid content was refused by the compiler and accepted by the
// evaluator, and equal compared kind and text and answered TRUE.
func TestEvaluateRefusesALiteralValidInKindOnly(t *testing.T) {
	schema := expressionSchema(t)
	state := selectorState(t, "a")
	entity := state.Entities()[0]

	// Constructible only in-package, which is why the compiler/evaluator disagreement was
	// invisible: every external caller goes through a validating constructor.
	malformed := Value{kind: ValueString, text: "\xff"}
	if malformed.Valid() {
		t.Fatal("the fixture value is valid, so this test proves nothing")
	}
	node := Expr{Kind: ExprLiteral, Literal: &malformed}

	if _, err := CompileExpression(schema, testCompilerVersion, node); err == nil {
		t.Fatal("the compiler accepted it, so there is no disagreement to test")
	}
	if _, err := evaluateExpr(schema, node, entity); err == nil {
		t.Fatal("the evaluator accepted a literal the compiler refuses")
	}
	if _, err := evaluateBool(
		schema, Expr{Kind: ExprEqual, Args: []Expr{node, node}}, entity); err == nil {
		t.Fatal("equal over two malformed literals answered instead of refusing")
	}
}

// Declaredness is the other half of boundField's sentence. Without it, absence of a
// DECLARATION collapses into absence of a VALUE, so not(exists(driver.typo)) is true for
// every entity while the compiler refuses the identical node.
func TestEvaluateRefusesAnUndeclaredPath(t *testing.T) {
	schema := expressionSchema(t)
	state := selectorState(t, "a")
	entity := state.Entities()[0]

	for _, expr := range []Expr{
		{Kind: ExprExists, Field: "driver.nope"},
		field("driver.nope"),
	} {
		if _, err := evaluateExpr(schema, expr, entity); err == nil {
			t.Errorf("kind %d over an undeclared path answered instead of refusing", expr.Kind)
		}
		// The compiler refuses the same node, so the two halves agree.
		if _, err := CompileExpression(schema, testCompilerVersion, expr); err == nil {
			t.Errorf("the compiler accepted kind %d over an undeclared path", expr.Kind)
		}
	}
}

// An unconstructed selector must refuse rather than report a successful empty selection.
// Without the guard the schema comparison is "" != "", which passes, and a zero selector on a
// zero state returns Selection{}, nil.
func TestZeroSelectorRefuses(t *testing.T) {
	var zero CompiledSelector
	selection, err := zero.Select(State{})
	if err == nil {
		t.Fatal("a zero selector produced a selection")
	}
	if selection.Ran() {
		t.Fatal("a refused selection reports that it ran")
	}
}

// The type introduced to stop a violation becoming an absence must not have "nothing was
// there" as its own zero value.
func TestSelectionDistinguishesNotRunFromEmpty(t *testing.T) {
	var never Selection
	if never.Ran() {
		t.Fatal("the zero Selection reports that it ran")
	}

	state := selectorState(t, "a")
	unmatchable, err := NewStringValue("nobody")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	empty := mustCompileSelector(t, Selector{
		Kind: "driver",
		Where: ptr(Expr{Kind: ExprEqual, Args: []Expr{
			field("driver.assignment_key"), {Kind: ExprLiteral, Literal: &unmatchable}}}),
		Members: Cardinality{Kind: CardinalityAny},
	})
	selection, err := empty.Select(state)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	// Same observable groups and violations as the zero value; only Ran distinguishes them.
	if len(selection.Groups()) != 0 || len(selection.Violations()) != 0 {
		t.Fatalf("expected an empty selection, got %d groups and %d violations",
			len(selection.Groups()), len(selection.Violations()))
	}
	if !selection.Ran() {
		t.Fatal("a successful empty selection reports that it did not run")
	}
}

// Ungrouped order is the state's canonical order. Nothing observed it before this test.
//
// Note what this test CANNOT do: it cannot distinguish whether that order comes from the
// comparator's tiebreak or from pdqsort happening to preserve all-ties input. Removing the
// tiebreak leaves this green at every size tried. The tiebreak is documented at the sort as
// unfalsifiable for that reason; this test pins the observable order, not its mechanism.
func TestSelectorUngroupedOrderIsTheStatesOrder(t *testing.T) {
	state := selectorState(t, "e", "b", "d", "a", "c", "f")
	selector := mustCompileSelector(t, Selector{
		Kind: "driver", Members: Cardinality{Kind: CardinalityAny}})

	var first []EntityRef
	for pass := range 30 {
		selection, err := selector.Select(state)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		refs := make([]EntityRef, 0, len(selection.Groups()))
		for _, group := range selection.Groups() {
			refs = append(refs, group.Members()[0].Ref())
		}
		if pass == 0 {
			first = refs
			// The state's own order, ascending by (kind, EntityID).
			for i := 1; i < len(refs); i++ {
				if compareEntityRefs(refs[i-1], refs[i]) >= 0 {
					t.Fatalf("ungrouped selection is not in the state's canonical order")
				}
			}
			continue
		}
		for i := range refs {
			if refs[i] != first[i] {
				t.Fatalf("pass %d ordered ungrouped groups differently from pass 0", pass)
			}
		}
	}
}

// equal's operand-type refusal. Without it, bool-vs-string reaches the bool arm and compares
// left.boolean against right.boolean -- which is the zero false for a string result -- so it
// answers false, totally and deterministically. That is the round-eleven defect's shape in
// the arm immediately below the one written to fix it.
func TestEvaluateRefusesEqualAcrossTypes(t *testing.T) {
	schema := expressionSchema(t)
	state := selectorState(t, "a")
	entity := state.Entities()[0]

	crossType := Expr{Kind: ExprEqual, Args: []Expr{
		{Kind: ExprExists, Field: "driver.hos_anchor"}, // bool, and true for this entity
		field("driver.assignment_key"),                 // string
	}}
	if _, err := evaluateExpr(schema, crossType, entity); err == nil {
		t.Fatal("equal compared a bool with a string instead of refusing")
	}
	// The compiler refuses the same node, so the two halves agree.
	if _, err := CompileExpression(schema, testCompilerVersion, crossType); err == nil {
		t.Fatal("the compiler accepted a cross-type equal")
	}
}

// ExprAny's short circuit, which only ExprAll was covering. The comment justifying short
// circuiting names any(not(exists(f)), less(f, 10)) as one of its two motivating forms, and
// nothing exercised it.
func TestEvaluateAnyShortCircuits(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	sparse, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "sparse")},
		map[FieldName]Value{"hos_driving_hours": NewInt64Value(1)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}

	// The first disjunct is true, so the second -- which reads an absent field and would
	// error -- must never run.
	tolerant := Expr{Kind: ExprAny, Args: []Expr{
		{Kind: ExprNot, Args: []Expr{{Kind: ExprExists, Field: "driver.hos_elapsed_hours"}}},
		{Kind: ExprLess, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(10)}},
	}}
	got, err := evaluateBool(schema, tolerant, sparse)
	if err != nil {
		t.Fatalf("any did not short circuit past an absent field: %v", err)
	}
	if !got {
		t.Fatal("any returned false when its first disjunct was true")
	}
}

// ONE DERIVATION OF A FIELD'S TYPE, from the declaration, which is what the compiler uses.
//
// This is constructible because NewEntity takes no schema: only NewState enforces that a
// stored value's kind matches its declaration. So an Entity built directly -- the "reachable
// without a compiled selector" class the evaluator's own comments claim to defend -- can hold
// a string in an int64-declared field, and that is the only input distinguishing "type from
// the declaration" from "type from the value".
func TestEvaluateTypesAFieldFromItsDeclaration(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "selector")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	wrongKind, err := NewStringValue("not-an-int")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	// hos_elapsed_hours is declared ValueInt64; this holds a string. NewState would refuse
	// the state, which is why this entity is built directly.
	mismatched, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "mismatched")},
		map[FieldName]Value{"hos_elapsed_hours": wrongKind})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	if _, err := NewState(schema, lineage, []Entity{mismatched}, nil); err == nil {
		t.Fatal("NewState accepted the mismatch, so this entity is not the unreachable case")
	}

	if _, err := evaluateExpr(schema, field("driver.hos_elapsed_hours"), mismatched); err == nil {
		t.Fatal("a field whose stored kind contradicts its declaration was typed and returned")
	}
}

// ── golden canonical vector ─────────────────────────────────────────────────

// One vector, for one property the behavioural tests above cannot reach: THE DOMAIN TAG.
//
// Every selector in this suite carries it, so removing it shifts all bytes equally and every
// relative comparison in TestSelectorIdentityCommitsToEveryComponent still holds. The valid
// state space cannot vary "is the tag written" independently of everything else, which is the
// condition under which a golden vector is the right instrument rather than ceremony. Verified
// by mutation: dropping the tag survives the whole suite without this, and fails it with.
//
// Everything else the encoding commits to is pinned behaviourally above, because those
// dimensions CAN be varied by valid selectors, and a behavioural test does not go stale the
// way a digest constant does.
func TestSelectorCanonicalGoldenVector(t *testing.T) {
	const wantHex = "00000000000000176d616964656e2d6c616e652e73656c6563746f722e7631" +
		"00000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631" +
		"b9ceee82852bb8e562a9b4b9866659833af65e8e11ada88d370be23cc83fd56d" +
		"000000000000000664726976657202000000000000000200010200000000000000156472697665722e61737369676e6d656e745f6b6579"

	compiled := mustCompileSelector(t, Selector{
		Kind:    "driver",
		GroupBy: ptr(groupKey()),
		Members: Cardinality{Kind: CardinalityExactly, Count: 2},
	})
	if got := hex.EncodeToString(compiled.CanonicalBytes()); got != wantHex {
		t.Fatalf("canonical bytes =\n%s\nwant\n%s", got, wantHex)
	}
}

// firstEncounterOrder reports the distinct assignment keys in the order Select would meet
// them while walking the state, which is what a result echoing encounter order would show.
func firstEncounterOrder(t *testing.T, state State) []string {
	t.Helper()
	var order []string
	seen := map[string]bool{}
	for _, entity := range state.Entities() {
		value, present := entity.Field("assignment_key")
		if !present {
			continue
		}
		text, ok := value.String()
		if !ok {
			t.Fatalf("assignment_key is not a string")
		}
		if !seen[text] {
			seen[text] = true
			order = append(order, text)
		}
	}
	return order
}

func withMembers(s Selector, members Cardinality) Selector { s.Members = members; return s }
func withWhere(s Selector, where *Expr) Selector           { s.Where = where; return s }
func withGroupBy(s Selector, groupBy *Expr) Selector       { s.GroupBy = groupBy; return s }

func ptr[T any](value T) *T { return &value }
