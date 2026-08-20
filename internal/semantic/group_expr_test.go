package semantic

import "testing"

// groupMembers builds drivers of one kind with the given anchor and hour values, for use as
// a group's members. Values are supplied per member so a test can vary exactly one dimension.
func groupMembers(t *testing.T, anchors []string, elapsed []int64) []Entity {
	t.Helper()
	if len(anchors) != len(elapsed) {
		t.Fatalf("fixture mismatch: %d anchors, %d hours", len(anchors), len(elapsed))
	}
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "group")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	members := make([]Entity, 0, len(anchors))
	for i, token := range anchors {
		fields := map[FieldName]Value{
			"hos_elapsed_hours": NewInt64Value(elapsed[i]),
			// Populated so the two-field LessOrEqualFields form is reachable, and set EQUAL
			// to elapsed rather than below it. Equality is the only input at which "a at most
			// b" differs from "a strictly below b", and the kernel predicate this refutes
			// accepts equality -- a fixture that can never produce it cannot tell the two
			// apart, so both a correct encoding and a strict one would pass.
			"hos_driving_hours": NewInt64Value(elapsed[i]),
		}
		if token != "" {
			anchor, err := NewAtomValue(token)
			if err != nil {
				t.Fatalf("NewAtomValue: %v", err)
			}
			fields["hos_anchor"] = anchor
		}
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", string(rune('a'+i)))},
			fields)
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		members = append(members, entity)
	}
	return members
}

func allMembers(pred Expr) Expr { return Expr{Kind: ExprAllMembers, Args: []Expr{pred}} }
func anyMembers(pred Expr) Expr { return Expr{Kind: ExprAnyMembers, Args: []Expr{pred}} }
func allEqual(path FieldPath) Expr {
	return Expr{Kind: ExprAllEqual, Field: path}
}

// THE REFUTATION CRITERION THE PLAN SET: whatever replaces the frozen aggregate predicates
// must be able to express all four of them. A design that cannot is refuted rather than
// merely awkward, so this test exists to fail if the vocabulary is inadequate.
//
// Three of the four are "for every member, <member-scoped predicate>" and use the quantifier;
// the fourth is genuinely cross-member and uses all_equal. Note the arities the kernel
// actually accepts: CompleteTuple is variadic, LessOrEqualFields is exactly two, and the
// other two take one -- an earlier draft of the plan said CompleteTuple took three, which was
// the fixture's number rather than the vocabulary's.
func TestGroupPredicatesExpressTheFrozenFour(t *testing.T) {
	schema := expressionSchema(t)
	complete := groupMembers(t, []string{"T0", "T0"}, []int64{10, 12})
	sparse := groupMembers(t, []string{"T0", ""}, []int64{10, 12})
	disagreeing := groupMembers(t, []string{"T0", "T1"}, []int64{10, 12})
	exceeding := membersWithDriving(t, 10, 11)

	for _, test := range []struct {
		name    string
		expr    Expr
		members []Entity
		want    bool
	}{
		{
			// CompleteTuple, variadic: every member holds all of these fields.
			name: "complete tuple holds",
			expr: allMembers(Expr{Kind: ExprAll, Args: []Expr{
				{Kind: ExprExists, Field: "driver.hos_anchor"},
				{Kind: ExprExists, Field: "driver.hos_elapsed_hours"},
			}}),
			members: complete, want: true,
		},
		{
			name: "complete tuple fails on a sparse member",
			expr: allMembers(Expr{Kind: ExprAll, Args: []Expr{
				{Kind: ExprExists, Field: "driver.hos_anchor"},
				{Kind: ExprExists, Field: "driver.hos_elapsed_hours"},
			}}),
			members: sparse, want: false,
		},
		{
			// NonNegativeInt: every member's value is at least zero, written as not-less-than.
			name: "non-negative holds",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(0)}},
			}}),
			members: complete, want: true,
		},
		{
			name: "non-negative fails",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(0)}},
			}}),
			members: groupMembers(t, []string{"T0"}, []int64{-1}), want: false,
		},
		{
			// LessOrEqualFields, exactly two DISTINCT paths: driving <= elapsed, written as
			// not(elapsed < driving). The fixture sets them EQUAL, which is the boundary a
			// strict encoding would get wrong.
			name: "a at most b holds at equality",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{
					field("driver.hos_elapsed_hours"), field("driver.hos_driving_hours")}},
			}}),
			members: complete, want: true,
		},
		{
			// And its failing counterpart, which the other three predicates had and this one
			// did not. One member has driving ABOVE elapsed, so a at most b is false.
			name: "a at most b fails when a exceeds b",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{
					field("driver.hos_elapsed_hours"), field("driver.hos_driving_hours")}},
			}}),
			members: exceeding, want: false,
		},
		{
			// NonNegativeInt at its own boundary: zero, where >= 0 differs from > 0.
			name: "non-negative holds at zero",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{field("driver.hos_elapsed_hours"), intLiteral(0)}},
			}}),
			members: groupMembers(t, []string{"T0"}, []int64{0}), want: true,
		},
		{
			// EqualFieldAcrossSources: the members agree. Genuinely cross-member, so it is
			// not a quantifier over a member-scoped predicate.
			name:    "all members agree",
			expr:    allEqual("driver.hos_anchor"),
			members: complete, want: true,
		},
		{
			name:    "members disagree",
			expr:    allEqual("driver.hos_anchor"),
			members: disagreeing, want: false,
		},
		{
			name:    "any member matches",
			expr:    anyMembers(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
			members: sparse, want: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := checkGroupExpr(schema, "driver", test.expr, 0); err != nil {
				t.Fatalf("checkGroupExpr: %v", err)
			}
			got, err := evaluateGroupExpr(schema, test.expr, test.members)
			if err != nil {
				t.Fatalf("evaluateGroupExpr: %v", err)
			}
			if got != test.want {
				t.Fatalf("= %v, want %v", got, test.want)
			}
		})
	}
}

// SCOPE IS ENFORCED IN BOTH DIRECTIONS, which is what replaces the binder. A member-reading
// node in group scope would have to pick a member; a group predicate in member scope has no
// group to quantify over. Both are refused at compile time rather than resolved at runtime.
func TestGroupScopeIsEnforcedBothWays(t *testing.T) {
	schema := expressionSchema(t)
	present := Expr{Kind: ExprExists, Field: "driver.hos_anchor"}

	t.Run("member-reading nodes are refused in group scope", func(t *testing.T) {
		for _, expr := range []Expr{
			field("driver.hos_anchor"),
			present,
			{Kind: ExprEqual, Args: []Expr{
				field("driver.hos_elapsed_hours"), intLiteral(1)}},
			// Composition does not launder it: the arguments stay in group scope.
			{Kind: ExprAll, Args: []Expr{allMembers(present), present}},
		} {
			if _, err := checkGroupExpr(schema, "driver", expr, 0); err == nil {
				t.Errorf("%s was accepted in group scope", expr.Kind)
			}
		}
	})

	t.Run("group predicates are refused in member scope", func(t *testing.T) {
		for _, expr := range []Expr{
			allMembers(present),
			anyMembers(present),
			allEqual("driver.hos_anchor"),
			// Nesting is refused by the same rule rather than needing its own: the argument
			// of a quantifier is checked in member scope.
			allMembers(allMembers(present)),
		} {
			if _, err := CompileExpression(schema, testCompilerVersion, expr); err == nil {
				t.Errorf("%s was accepted in member scope", expr.Kind)
			}
		}
	})

	t.Run("the nested case is refused in group scope too", func(t *testing.T) {
		if _, err := checkGroupExpr(schema, "driver", allMembers(allMembers(present)), 0); err == nil {
			t.Fatal("a quantifier nested inside a quantifier compiled")
		}
	})
}

// Three guards that nothing exercised. Each is the sole reason for its refusal, and each
// on removal makes the CHECKER admit a tree the EVALUATOR cannot answer -- the disagreement
// that produced four of this branch's five wrong answers.
func TestGroupCheckerAndEvaluatorAgreeOnWhatIsAdmissible(t *testing.T) {
	schema := expressionSchema(t)
	present := Expr{Kind: ExprExists, Field: "driver.hos_anchor"}

	// all_equal inside member scope. The other member-scope refusals in this file go through
	// CompileExpression, which is a DIFFERENT copy of the rule, so this arm was untested.
	if _, err := checkGroupExpr(schema, "driver", allMembers(allEqual("driver.hos_anchor")), 0); err == nil {
		t.Error("all_equal was accepted inside a quantifier's member-scoped argument")
	}

	// A quantifier over a non-bool predicate. Every fixture predicate elsewhere is already
	// bool, so this requirement was never the reason for anything.
	if _, err := checkGroupExpr(
		schema, "driver", allMembers(field("driver.hos_elapsed_hours")), 0); err == nil {
		t.Error("a quantifier accepted an int64 predicate")
	}

	// A member-reading node at the top of a group evaluation. Without the default refusal it
	// answers a confident false, which is the failure-becomes-a-vacuous-answer shape the
	// empty-group guard three lines away exists to prevent.
	members := groupMembers(t, []string{"T0"}, []int64{1})
	if _, err := evaluateGroupExpr(schema, present, members); err == nil {
		t.Error("a member-reading node was evaluated over a group")
	}
	if _, err := evaluateGroupExpr(schema, field("driver.hos_anchor"), members); err == nil {
		t.Error("a field read was evaluated over a group")
	}
}

// The bound kind constrains a quantifier's inner predicate, not only all_equal. An earlier
// version consulted it in the all_equal arm alone, so this type-checked and then failed at
// evaluation inside boundField.
func TestQuantifierInnerPredicateIsBoundToTheGroupsKind(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
		{Kind: "team", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	// Both kinds, both directions, so neither can be satisfied by a constant.
	for _, test := range []struct {
		kind    EntityKind
		path    FieldPath
		wantErr bool
	}{
		{"driver", "driver.assignment_key", false},
		{"driver", "team.assignment_key", true},
		{"team", "team.assignment_key", false},
		{"team", "driver.assignment_key", true},
	} {
		inner := Expr{Kind: ExprExists, Field: test.path}
		_, err := checkGroupExpr(schema, test.kind, allMembers(inner), 0)
		if (err != nil) != test.wantErr {
			t.Errorf("all_members(exists(%q)) under a %q group: err=%v, wantErr=%v",
				test.path, test.kind, err, test.wantErr)
		}
	}
}

// all_equal makes the declared-kind agreement check its member-scoped sibling makes. Without
// it, two members each holding a string where the schema declares an atom compare equal and
// answer "the members agree on their atom" from data that is not of the declared type.
func TestAllEqualRequiresTheDeclaredKind(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "group")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	// hos_anchor is declared ValueAtom; these hold strings. NewState would refuse the state,
	// which is why the entities are built directly -- and why this function, which takes
	// entities rather than a state, has to check.
	members := make([]Entity, 0, 2)
	for i, name := range []string{"m", "n"} {
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", name)},
			map[FieldName]Value{"hos_anchor": mustString(t, "T0")})
		if err != nil {
			t.Fatalf("NewEntity %d: %v", i, err)
		}
		members = append(members, entity)
	}
	if _, err := evaluateGroupExpr(schema, allEqual("driver.hos_anchor"), members); err == nil {
		t.Fatal("all_equal answered over members whose stored kind contradicts the declaration")
	}

	// AND THE OTHER DIRECTION, over a differently-declared field. Every all_equal elsewhere in
	// this suite reads hos_anchor, which is declared ValueAtom, so `value.Kind() != declared`
	// could be replaced by `value.Kind() != ValueAtom` and survive -- the guard compared
	// against the one kind the fixture happens to use. assignment_key is declared ValueString.
	atomWhereStringDeclared := make([]Entity, 0, 2)
	for _, name := range []string{"p", "q"} {
		atom, err := NewAtomValue("T0")
		if err != nil {
			t.Fatalf("NewAtomValue: %v", err)
		}
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", name)},
			map[FieldName]Value{"assignment_key": atom})
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		atomWhereStringDeclared = append(atomWhereStringDeclared, entity)
	}
	if _, err := evaluateGroupExpr(
		schema, allEqual("driver.assignment_key"), atomWhereStringDeclared); err == nil {
		t.Fatal("all_equal answered over atoms where a string is declared")
	}

	// And the control: correctly-typed members of that same non-atom field must ANSWER, so the
	// guard is refusing on the mismatch rather than on the field.
	wellTyped := make([]Entity, 0, 2)
	for _, name := range []string{"r", "s"} {
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", name)},
			map[FieldName]Value{"assignment_key": mustString(t, "k")})
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		wellTyped = append(wellTyped, entity)
	}
	agreed, err := evaluateGroupExpr(schema, allEqual("driver.assignment_key"), wellTyped)
	if err != nil {
		t.Fatalf("all_equal refused correctly-typed strings: %v", err)
	}
	if !agreed {
		t.Fatal("all_equal reported disagreement between two identical strings")
	}
}

// membersWithDriving builds one member per driving value, with elapsed held at a fixed level,
// so a test can put driving above elapsed -- which groupMembers cannot do.
func membersWithDriving(t *testing.T, elapsed int64, driving ...int64) []Entity {
	t.Helper()
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "group")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	anchor, err := NewAtomValue("T0")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}
	members := make([]Entity, 0, len(driving))
	for i, hours := range driving {
		entity, err := NewEntity(
			EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "x"+string(rune('a'+i)))},
			map[FieldName]Value{
				"hos_anchor":        anchor,
				"hos_elapsed_hours": NewInt64Value(elapsed),
				"hos_driving_hours": NewInt64Value(hours),
			})
		if err != nil {
			t.Fatalf("NewEntity: %v", err)
		}
		members = append(members, entity)
	}
	return members
}

// The group layer opened two new doors into the same Args[0] hazard that
// TestEvaluateIsTotalOnMalformedNodes covers one layer down, and shipped no equivalent.
// Removing either checkOperandShape call panics rather than refusing.
func TestGroupEntryPointsAreTotalOnMalformedNodes(t *testing.T) {
	schema := expressionSchema(t)
	members := groupMembers(t, []string{"T0"}, []int64{1})
	for _, expr := range []Expr{
		{Kind: ExprAllMembers},
		{Kind: ExprAnyMembers},
		{Kind: ExprAllMembers, Args: []Expr{intLiteral(1), intLiteral(2)}},
		{Kind: ExprAllEqual},
		{Kind: ExprNot},
		{},
	} {
		if _, err := checkGroupExpr(schema, "driver", expr, 0); err == nil {
			t.Errorf("checkGroupExpr accepted kind %d with %d args", expr.Kind, len(expr.Args))
		}
		if _, err := evaluateGroupExpr(schema, expr, members); err == nil {
			t.Errorf("evaluateGroupExpr accepted kind %d with %d args", expr.Kind, len(expr.Args))
		}
	}
}

// BOOLEAN COMPOSITION AT GROUP LEVEL, which the design comment advertises and nothing
// evaluated: all three arms of evaluateGroupExpr's composition were dead in the suite, so
// deleting them would have left the checker admitting `all(all_members(...), all_equal(...))`
// while the evaluator refused it. any_members was also never observed answering false.
func TestGroupCompositionEvaluates(t *testing.T) {
	schema := expressionSchema(t)
	agreeing := groupMembers(t, []string{"T0", "T0"}, []int64{10, 12})
	disagreeing := groupMembers(t, []string{"T0", "T1"}, []int64{10, 12})
	hasAnchor := allMembers(Expr{Kind: ExprExists, Field: "driver.hos_anchor"})

	for _, test := range []struct {
		name    string
		expr    Expr
		members []Entity
		want    bool
	}{
		// The exact composition the design comment names.
		{"all of two group predicates", Expr{Kind: ExprAll, Args: []Expr{
			hasAnchor, allEqual("driver.hos_anchor")}}, agreeing, true},
		{"all fails when one conjunct fails", Expr{Kind: ExprAll, Args: []Expr{
			hasAnchor, allEqual("driver.hos_anchor")}}, disagreeing, false},
		{"any succeeds when one disjunct holds", Expr{Kind: ExprAny, Args: []Expr{
			allEqual("driver.hos_anchor"), hasAnchor}}, disagreeing, true},
		// THE FALSE TERMINAL, which the case above never reaches: it returns from inside the
		// loop on the second disjunct. Group-level any and any_members are twins added in one
		// commit, and only the second had its terminal pinned -- so `return false` here could
		// become `return true` and a group failing every disjunct would be admitted.
		{"any is false when every disjunct fails", Expr{Kind: ExprAny, Args: []Expr{
			allEqual("driver.hos_anchor"),
			allMembers(Expr{Kind: ExprExists, Field: "driver.assignment_key"}),
		}}, disagreeing, false},
		{"not inverts a false operand", Expr{Kind: ExprNot, Args: []Expr{
			allEqual("driver.hos_anchor")}}, disagreeing, true},
		// And the other polarity, so not() is observed at both outcomes.
		{"not inverts a true operand", Expr{Kind: ExprNot, Args: []Expr{
			allEqual("driver.hos_anchor")}}, agreeing, false},
		// any_members answering FALSE, which no fixture reached: no member lacks the anchor.
		{"any_members is false when no member matches",
			anyMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprExists, Field: "driver.hos_anchor"}}}), agreeing, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := checkGroupExpr(schema, "driver", test.expr, 0); err != nil {
				t.Fatalf("checkGroupExpr: %v", err)
			}
			got, err := evaluateGroupExpr(schema, test.expr, test.members)
			if err != nil {
				t.Fatalf("evaluateGroupExpr: %v", err)
			}
			if got != test.want {
				t.Fatalf("= %v, want %v", got, test.want)
			}
		})
	}
}

// GROUP SCOPE HAS ITS OWN DEPTH BOUND AND NOTHING ELSE PROVIDES IT.
//
// In member scope a deep tree is refused by checkExpr's guard, reached through
// checkExprInScope's default arm. In group scope the composition arm recurses within
// checkExprInScope and the leaf is handled by its own arms, so checkExpr is never reached on
// any node -- removing this bound leaves authored nesting unbounded at group scope, and
// evaluateGroupExpr recurses to the same depth with no bound of its own.
func TestGroupScopeBoundsNesting(t *testing.T) {
	schema := expressionSchema(t)
	deep := allEqual("driver.hos_anchor")
	for range maxExprDepth + 2 {
		deep = Expr{Kind: ExprNot, Args: []Expr{deep}}
	}
	if _, err := checkGroupExpr(schema, "driver", deep, 0); err == nil {
		t.Fatal("a group expression nested past the bound type-checked")
	}

	// The control: a tree just inside the bound still compiles, so the refusal is about depth
	// rather than about composition being rejected outright.
	shallow := allEqual("driver.hos_anchor")
	for range 3 {
		shallow = Expr{Kind: ExprNot, Args: []Expr{shallow}}
	}
	if _, err := checkGroupExpr(schema, "driver", shallow, 0); err != nil {
		t.Fatalf("a shallow group expression was refused: %v", err)
	}
}

// An unstated scope admits nothing. Both call sites pass a valid scope today, so this guard is
// latent -- but its adjacent comment claims the design prevents a call that forgot its scope
// from silently getting the permissive member mode, and without the guard that claim is false:
// scopeInvalid would refuse group predicates and then fall through to checkExpr.
func TestUnstatedScopeAdmitsNothing(t *testing.T) {
	schema := expressionSchema(t)
	for _, expr := range []Expr{
		field("driver.hos_anchor"),
		{Kind: ExprExists, Field: "driver.hos_anchor"},
		allEqual("driver.hos_anchor"),
	} {
		if _, err := checkExprInScope(schema, "driver", expr, scopeInvalid, 0); err == nil {
			t.Errorf("kind %d was accepted under an unstated scope", expr.Kind)
		}
	}
	if got := scopeInvalid.String(); got != "invalid" {
		t.Fatalf("scopeInvalid.String() = %q, want \"invalid\"", got)
	}
}

// A group predicate reads only the kind the group holds, for the same reason a selector's
// predicate does: the members are entities of one kind and a path naming another has no
// referent among them.
func TestGroupPredicateReadsOnlyTheGroupsKind(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{Kind: "driver", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
		{Kind: "team", Fields: []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	// Both directions over both kinds, so neither check can be satisfied by a constant.
	for _, test := range []struct {
		kind    EntityKind
		path    FieldPath
		wantErr bool
	}{
		{"driver", "driver.assignment_key", false},
		{"driver", "team.assignment_key", true},
		{"team", "team.assignment_key", false},
		{"team", "driver.assignment_key", true},
	} {
		_, err := checkGroupExpr(schema, test.kind, allEqual(test.path), 0)
		if (err != nil) != test.wantErr {
			t.Errorf("all_equal(%q) under a %q group: err=%v, wantErr=%v",
				test.path, test.kind, err, test.wantErr)
		}
	}
}

// A QUANTIFIER OVER NOTHING IS A CALLER ERROR, not true.
//
// Groups are never empty by construction, so this cannot arise through Select. It is refused
// anyway because the vacuous answer is the dangerous one: evaluateProfileOverState already
// returns Ready for an empty selection, justified by a fixture property author rulesets will
// not have, and answering true here would put the same trap one layer down where a Transform
// would build on it.
func TestGroupPredicateRefusesAnEmptyGroup(t *testing.T) {
	schema := expressionSchema(t)
	for _, expr := range []Expr{
		allMembers(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
		anyMembers(Expr{Kind: ExprExists, Field: "driver.hos_anchor"}),
		allEqual("driver.hos_anchor"),
	} {
		if _, err := evaluateGroupExpr(schema, expr, nil); err == nil {
			t.Errorf("%s answered over an empty group", expr.Kind)
		}
	}
}

// all_equal refuses an absent field rather than treating absence as a value that can agree.
// "All members agree" and "no member has one" are different claims, and collapsing them would
// let a group missing the field entirely pass a check about that field.
func TestAllEqualRefusesAbsence(t *testing.T) {
	schema := expressionSchema(t)
	for _, name := range []string{"absent on the first member", "absent on a later member"} {
		t.Run(name, func(t *testing.T) {
			anchors := []string{"", "T0"}
			if name == "absent on a later member" {
				anchors = []string{"T0", ""}
			}
			members := groupMembers(t, anchors, []int64{1, 2})
			if _, err := evaluateGroupExpr(schema, allEqual("driver.hos_anchor"), members); err == nil {
				t.Fatal("all_equal answered over a member missing the field")
			}
		})
	}
}

// The quantifiers short circuit over MEMBERS, which is observable only through a member whose
// predicate would error. An earlier version of this test used a member whose predicate merely
// returned false, which demonstrates nothing: the quantifier would answer the same either way.
func TestGroupQuantifiersShortCircuitOverMembers(t *testing.T) {
	schema := expressionSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "group")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	// The first member decides the answer. The second holds NO hours at all, so reading them
	// errors -- it is reached only if the quantifier fails to stop.
	deciding, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "a")},
		map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(5)})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	unreadable, err := NewEntity(
		EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "b")},
		map[FieldName]Value{"assignment_key": mustString(t, "x")})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	members := []Entity{deciding, unreadable}
	below := Expr{Kind: ExprLess, Args: []Expr{
		field("driver.hos_elapsed_hours"), intLiteral(0)}}

	// all_members: the first member is 5, not below zero, so the answer is false and the
	// second member is never read.
	got, err := evaluateGroupExpr(schema, allMembers(below), members)
	if err != nil {
		t.Fatalf("all_members read past the deciding member: %v", err)
	}
	if got {
		t.Fatal("all_members = true for a member holding 5")
	}

	// any_members: the first member decides true when the test is inverted, so again the
	// second is never read.
	notBelow := Expr{Kind: ExprNot, Args: []Expr{below}}
	got, err = evaluateGroupExpr(schema, anyMembers(notBelow), members)
	if err != nil {
		t.Fatalf("any_members read past the deciding member: %v", err)
	}
	if !got {
		t.Fatal("any_members = false when the first member satisfies the predicate")
	}

	// And the control: without a deciding first member, the unreadable one IS reached and the
	// quantifier refuses rather than defaulting. Otherwise the two assertions above would pass
	// for a quantifier that silently skipped members it could not evaluate.
	if _, err := evaluateGroupExpr(schema, allMembers(below), []Entity{unreadable}); err == nil {
		t.Fatal("a member whose predicate cannot be evaluated was skipped rather than refused")
	}
}
