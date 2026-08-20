package semantic

import "fmt"

// Group-scoped expressions: predicates about a whole group rather than one entity.
//
// WHY THIS NEEDS NO BINDER, which is the question slice 1 deferred and slice 2 constrained.
// Three of the four predicates the frozen aggregate operator hardcodes are of one shape --
// "for every member, <something about that member>":
//
//	CompleteTuple(p...)        every member has all of p present
//	NonNegativeInt(p)          every member's p is at least zero
//	LessOrEqualFields(a, b)    every member has a at most b
//
// Each inner predicate is an ordinary MEMBER-SCOPED expression, the language slices 1 and 2
// already have, evaluated with each member as the bound entity. The member IS the binding,
// exactly as a selector binds one entity, so nothing has to name it and no variable-reference
// node is required. A binder would have forced a decision about whether the author's chosen
// name enters the canonical bytes, and that decision is now never needed.
//
// The fourth predicate is genuinely cross-member -- EqualFieldAcrossSources asks whether the
// members AGREE -- so it cannot be a quantifier over a member-scoped predicate and gets its
// own node.
//
// Reductions (max, sum, count) are deliberately absent. They produce values rather than
// predicates, and nothing consumes a value from a group until a Transform exists. Recorded
// rather than assumed: adding them later is an append-only kind, which costs no identity.

// scope distinguishes where an expression is being checked.
//
// It is not carried on the node. Whether `driver.hours` is meaningful depends on the context
// the expression sits in, not on the node itself, so scope is a property of the check rather
// than of the tree -- which is also why the same member-scoped expression can appear under
// different group predicates without changing its bytes.
type scope uint8

const (
	// memberScope is a single bound entity: the selector's candidate, or one group member.
	memberScope scope = iota
	// groupScope is a whole group, where no single entity is bound and a bare field path
	// therefore has no referent.
	groupScope
)

func (s scope) String() string {
	if s == groupScope {
		return "group"
	}
	return "member"
}

// checkGroupExpr types a group-scoped node.
//
// Group predicates may only appear in group scope, and member-reading nodes may only appear
// in member scope. Boolean composition is legal in both, which is what lets an author write
// all(all_members(...), all_equal(...)) without a third vocabulary.
func checkGroupExpr(schema Schema, kind EntityKind, expr Expr, depth int) (ExprType, error) {
	return checkExprInScope(schema, kind, expr, groupScope, depth)
}

// checkExprInScope is the scope-aware type check.
func checkExprInScope(
	schema Schema, kind EntityKind, expr Expr, in scope, depth int,
) (ExprType, error) {
	if depth > maxExprDepth {
		return TypeInvalid, fmt.Errorf("expression nests deeper than %d", maxExprDepth)
	}
	if err := checkOperandShape(expr); err != nil {
		return TypeInvalid, err
	}

	switch expr.Kind {
	case ExprAllMembers, ExprAnyMembers:
		if in != groupScope {
			// Nesting falls out of this rather than needing its own rule: the argument of a
			// group predicate is checked in member scope, so all_members(all_members(x)) is
			// refused here on the inner node.
			return TypeInvalid, fmt.Errorf(
				"%s is a group predicate and cannot appear in %s scope", expr.Kind, in)
		}
		inner, err := checkExprInScope(schema, kind, expr.Args[0], memberScope, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if inner != TypeBool {
			return TypeInvalid, fmt.Errorf(
				"a group quantifier requires a bool predicate, got %s", inner)
		}
		return TypeBool, nil

	case ExprAllEqual:
		if in != groupScope {
			return TypeInvalid, fmt.Errorf(
				"all_equal is a group predicate and cannot appear in %s scope", in)
		}
		declared, isDeclared := schema.fieldKind(expr.Field)
		if !isDeclared {
			return TypeInvalid, fmt.Errorf("all_equal reads undeclared field %q", expr.Field)
		}
		if named, _ := splitFieldPath(expr.Field); named != kind {
			return TypeInvalid, fmt.Errorf(
				"all_equal reads %q, but this group holds %q", expr.Field, kind)
		}
		if _, err := valueKindType(declared); err != nil {
			return TypeInvalid, err
		}
		return TypeBool, nil

	case ExprNot, ExprAll, ExprAny:
		// Legal in both scopes, and its arguments stay in the scope it was found in.
		for i := range expr.Args {
			argument, err := checkExprInScope(schema, kind, expr.Args[i], in, depth+1)
			if err != nil {
				return TypeInvalid, err
			}
			if argument != TypeBool {
				return TypeInvalid, fmt.Errorf(
					"boolean composition requires bool arguments, argument %d is %s", i, argument)
			}
		}
		return TypeBool, nil

	default:
		if in == groupScope {
			// Everything else reads a bound entity, and in group scope there is none. This is
			// the no-ambient-scope decision applied one level up: a bare path here would have
			// to mean "some member", and picking one silently is exactly what slice 1 refused
			// to let a later change do.
			return TypeInvalid, fmt.Errorf(
				"%s reads a single entity and cannot appear in group scope", expr.Kind)
		}
		return checkExpr(schema, expr, depth)
	}
}

// evaluateGroupExpr evaluates a group-scoped expression over a group's members.
func evaluateGroupExpr(schema Schema, expr Expr, members []Entity) (bool, error) {
	if len(members) == 0 {
		// A GROUP IS NEVER EMPTY by construction: an accumulator exists because a member was
		// added to it. Refused rather than answered, because the vacuous answer is the
		// dangerous one -- `evaluateProfileOverState` already returns Ready for an empty
		// selection, justified by a fixture property that author rulesets will not have, and
		// this is the same trap one layer down. A quantifier over nothing is not true here;
		// it is a caller error.
		return false, fmt.Errorf("group predicate evaluated over an empty group")
	}
	if err := checkOperandShape(expr); err != nil {
		return false, err
	}

	switch expr.Kind {
	case ExprAllMembers:
		for _, member := range members {
			held, err := evaluateBool(schema, expr.Args[0], member)
			if err != nil {
				return false, err
			}
			if !held {
				return false, nil
			}
		}
		return true, nil

	case ExprAnyMembers:
		for _, member := range members {
			held, err := evaluateBool(schema, expr.Args[0], member)
			if err != nil {
				return false, err
			}
			if held {
				return true, nil
			}
		}
		return false, nil

	case ExprAllEqual:
		// Cross-member, so it cannot be a quantifier over a member-scoped predicate.
		//
		// Absence is refused rather than treated as a value that can agree or disagree, for
		// the same reason a member-scoped read refuses it: "all members agree" and "no member
		// has one" are different claims, and collapsing them would let a group of entities
		// missing the field entirely pass a check about that field.
		first, _, present, err := boundField(schema, expr.Field, members[0])
		if err != nil {
			return false, err
		}
		if !present {
			return false, fmt.Errorf("all_equal reads %q, absent on a member", expr.Field)
		}
		for _, member := range members[1:] {
			value, _, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return false, err
			}
			if !present {
				return false, fmt.Errorf("all_equal reads %q, absent on a member", expr.Field)
			}
			if !value.Equal(first) {
				return false, nil
			}
		}
		return true, nil

	case ExprNot:
		held, err := evaluateGroupExpr(schema, expr.Args[0], members)
		if err != nil {
			return false, err
		}
		return !held, nil

	case ExprAll:
		for i := range expr.Args {
			held, err := evaluateGroupExpr(schema, expr.Args[i], members)
			if err != nil {
				return false, err
			}
			if !held {
				return false, nil
			}
		}
		return true, nil

	case ExprAny:
		for i := range expr.Args {
			held, err := evaluateGroupExpr(schema, expr.Args[i], members)
			if err != nil {
				return false, err
			}
			if held {
				return true, nil
			}
		}
		return false, nil

	default:
		// Reached only by an un-compiled tree; a checked one cannot carry a member-reading
		// node here. Refused rather than delegated to the member evaluator, which would have
		// to pick a member.
		return false, fmt.Errorf(
			"%s reads a single entity and cannot be evaluated over a group", expr.Kind)
	}
}
