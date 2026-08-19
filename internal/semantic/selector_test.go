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

// Production break caught: equal over BOOL operands answered false for every pair, because a
// bool result lives in .boolean and the comparison read .value, which is the zero Value for
// every bool-producing node. It compiled cleanly and returned a total, deterministic wrong
// answer -- the compiler's type lattice and the evaluator's kind tag were each correct against
// their own reference, and neither owned the fact that TypeBool has no value.
func TestEvaluateComparesBooleans(t *testing.T) {
	state := selectorState(t, "a")
	entity := state.Entities()[0]
	present := Expr{Kind: ExprExists, Field: "driver.hos_anchor"}
	absent := Expr{Kind: ExprExists, Field: "driver.nope"}

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
			got, err := evaluateBool(
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
	groups, err := guarded.Select(state)
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
		if _, err := evaluateExpr(expr, entity); err == nil {
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

	if _, err := evaluateExpr(node, entity); err == nil {
		t.Fatal("an invalid literal evaluated")
	}
	// The composition that produced the wrong answer, not merely the leaf.
	if _, err := evaluateExpr(
		Expr{Kind: ExprEqual, Args: []Expr{node, node}}, entity); err == nil {
		t.Fatal("equal over two invalid literals answered instead of refusing")
	}
	// And the compiler refuses it too, so the two halves agree.
	if _, err := CompileExpression(expressionSchema(t), testCompilerVersion, node); err == nil {
		t.Fatal("the compiler accepted an invalid literal, so the two halves now disagree")
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
