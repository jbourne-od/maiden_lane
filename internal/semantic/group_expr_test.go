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
		fields := map[FieldName]Value{"hos_elapsed_hours": NewInt64Value(elapsed[i])}
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
			// LessOrEqualFields, exactly two paths: every member has a at most b, written as
			// not(b < a).
			name: "a at most b holds",
			expr: allMembers(Expr{Kind: ExprNot, Args: []Expr{
				{Kind: ExprLess, Args: []Expr{
					field("driver.hos_elapsed_hours"), field("driver.hos_elapsed_hours")}},
			}}),
			members: complete, want: true,
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
