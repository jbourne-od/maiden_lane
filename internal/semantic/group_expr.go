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
// Group reductions (count, sum, min, max) compute aggregate values across the members of a group.
// They can appear in group guards (e.g. comparing sum or max against a threshold or count against
// group size) or in field assignment value expressions under select-and-assign.

// scope distinguishes where an expression is being checked.
//
// It is not carried on the node. Whether `driver.hours` is meaningful depends on the context
// the expression sits in, not on the node itself, so scope is a property of the check rather
// than of the tree -- which is also why the same member-scoped expression can appear under
// different group predicates without changing its bytes.
type scope uint8

const (
	// scopeInvalid is the zero value and admits nothing. Its three sibling enumerations in
	// this package all refuse in their zero state -- TypeInvalid, CardinalityInvalid, and
	// ExprKind's iota+1 -- and an earlier version of this one made memberScope the zero,
	// so a call that failed to state its scope silently got the permissive mode.
	scopeInvalid scope = iota
	// memberScope is a single bound entity: the selector's candidate, or one group member.
	memberScope
	// groupScope is a whole group, where no single entity is bound and a bare field path
	// therefore has no referent.
	groupScope
	// memberInGroupScope is a bound entity inside a qualifying group: used for assignment
	// value expressions, where both the member's own fields and group reductions are in scope.
	memberInGroupScope
)

func (s scope) String() string {
	switch s {
	case memberScope:
		return "member"
	case groupScope:
		return "group"
	case memberInGroupScope:
		return "member-in-group"
	default:
		return "invalid"
	}
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
	if in != memberScope && in != groupScope && in != memberInGroupScope {
		return TypeInvalid, fmt.Errorf("expression checked in %s scope", in)
	}
	if err := checkOperandShape(expr); err != nil {
		return TypeInvalid, err
	}

	switch expr.Kind {
	case ExprLiteral:
		return literalType(*expr.Literal)

	case ExprField:
		if in == groupScope {
			return TypeInvalid, fmt.Errorf(
				"field %q reads a single entity and cannot appear in group scope", expr.Field)
		}
		declared, isDeclared := schema.fieldKind(expr.Field)
		if !isDeclared {
			return TypeInvalid, fmt.Errorf("expression reads undeclared field %q", expr.Field)
		}
		if in == memberInGroupScope {
			if err := checkPathsBindKind(expr, kind); err != nil {
				return TypeInvalid, err
			}
		}
		return valueKindType(declared)

	case ExprExists:
		if in == groupScope {
			return TypeInvalid, fmt.Errorf(
				"exists reads a single entity and cannot appear in group scope")
		}
		if _, isDeclared := schema.fieldKind(expr.Field); !isDeclared {
			return TypeInvalid, fmt.Errorf("expression asks about undeclared field %q", expr.Field)
		}
		if in == memberInGroupScope {
			if err := checkPathsBindKind(expr, kind); err != nil {
				return TypeInvalid, err
			}
		}
		return TypeBool, nil

	case ExprAllMembers, ExprAnyMembers:
		if in != groupScope {
			return TypeInvalid, fmt.Errorf(
				"%s is a group predicate and cannot appear in %s scope", expr.Kind, in)
		}
		inner, err := checkExprInScope(schema, kind, expr.Args[0], memberScope, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if err := checkPathsBindKind(expr.Args[0], kind); err != nil {
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
		if err := checkPathsBindKind(expr, kind); err != nil {
			return TypeInvalid, err
		}
		if _, err := valueKindType(declared); err != nil {
			return TypeInvalid, err
		}
		return TypeBool, nil

	case ExprCount:
		if in == memberScope {
			return TypeInvalid, fmt.Errorf(
				"count is a group reduction and cannot appear in member scope")
		}
		return TypeInt64, nil

	case ExprSum, ExprMin, ExprMax:
		if in == memberScope {
			return TypeInvalid, fmt.Errorf(
				"%s is a group reduction and cannot appear in member scope", expr.Kind)
		}
		declared, isDeclared := schema.fieldKind(expr.Field)
		if !isDeclared {
			return TypeInvalid, fmt.Errorf("%s reads undeclared field %q", expr.Kind, expr.Field)
		}
		if err := checkPathsBindKind(expr, kind); err != nil {
			return TypeInvalid, err
		}
		if declared != ValueInt64 {
			return TypeInvalid, fmt.Errorf(
				"%s requires int64 field, got declared kind %d", expr.Kind, declared)
		}
		return TypeInt64, nil

	case ExprNot:
		argument, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if argument != TypeBool {
			return TypeInvalid, fmt.Errorf("not requires bool, got %s", argument)
		}
		return TypeBool, nil

	case ExprAll, ExprAny:
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

	case ExprEqual:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left != right {
			return TypeInvalid, fmt.Errorf("equal compares %s with %s", left, right)
		}
		return TypeBool, nil

	case ExprLess:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left != TypeInt64 || right != TypeInt64 {
			return TypeInvalid, fmt.Errorf("less requires int64 on both sides, got %s and %s", left, right)
		}
		return TypeBool, nil

	case ExprAdd:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left != TypeInt64 || right != TypeInt64 {
			return TypeInvalid, fmt.Errorf("add requires int64 on both sides, got %s and %s", left, right)
		}
		return TypeInt64, nil

	default:
		return TypeInvalid, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}

// evaluateGroupExpr evaluates a group-scoped expression over a group's members.
func evaluateGroupExpr(schema Schema, expr Expr, members []Entity) (bool, error) {
	result, err := evaluateGroupNode(schema, expr, members, nil)
	if err != nil {
		return false, err
	}
	if result.kind != TypeBool {
		return false, fmt.Errorf("expression is %s, not bool", result.kind)
	}
	return result.boolean, nil
}

// evaluateAssignmentValue evaluates an assignment expression for a member within a group.
func evaluateAssignmentValue(schema Schema, expr Expr, members []Entity, member Entity) (Value, error) {
	result, err := evaluateGroupNode(schema, expr, members, &member)
	if err != nil {
		return Value{}, err
	}
	if result.kind == TypeBool {
		return Value{}, fmt.Errorf("assignment expression is bool, not a value")
	}
	if !result.value.Valid() {
		return Value{}, fmt.Errorf("assignment expression yielded no value")
	}
	return result.value, nil
}

func evaluateGroupNode(
	schema Schema, expr Expr, members []Entity, boundMember *Entity,
) (evaluated, error) {
	if len(members) == 0 {
		return evaluated{}, fmt.Errorf("group predicate evaluated over an empty group")
	}
	if err := checkOperandShape(expr); err != nil {
		return evaluated{}, err
	}

	switch expr.Kind {
	case ExprLiteral:
		if expr.Literal == nil {
			return evaluated{}, fmt.Errorf("literal carries no value")
		}
		kind, err := literalType(*expr.Literal)
		if err != nil {
			return evaluated{}, fmt.Errorf("literal: %w", err)
		}
		return evaluated{kind: kind, value: *expr.Literal}, nil

	case ExprField:
		if boundMember == nil {
			return evaluated{}, fmt.Errorf("field %q reads a single entity and cannot appear in group scope", expr.Field)
		}
		return evaluateExpr(schema, expr, *boundMember)

	case ExprExists:
		if boundMember == nil {
			return evaluated{}, fmt.Errorf("exists reads a single entity and cannot appear in group scope")
		}
		return evaluateExpr(schema, expr, *boundMember)

	case ExprAllMembers:
		for _, member := range members {
			held, err := evaluateBool(schema, expr.Args[0], member)
			if err != nil {
				return evaluated{}, err
			}
			if !held {
				return evaluated{kind: TypeBool, boolean: false}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: true}, nil

	case ExprAnyMembers:
		for _, member := range members {
			held, err := evaluateBool(schema, expr.Args[0], member)
			if err != nil {
				return evaluated{}, err
			}
			if held {
				return evaluated{kind: TypeBool, boolean: true}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: false}, nil

	case ExprAllEqual:
		first, declared, present, err := boundField(schema, expr.Field, members[0])
		if err != nil {
			return evaluated{}, err
		}
		if !present {
			return evaluated{}, fmt.Errorf("all_equal reads %q, absent on a member", expr.Field)
		}
		for _, member := range members {
			value, _, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("all_equal reads %q, absent on a member", expr.Field)
			}
			if value.Kind() != declared {
				return evaluated{}, fmt.Errorf(
					"all_equal reads %q, which holds kind %d where %d is declared",
					expr.Field, value.Kind(), declared)
			}
			if !value.Equal(first) {
				return evaluated{kind: TypeBool, boolean: false}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: true}, nil

	case ExprCount:
		count := int64(len(members))
		return evaluated{kind: TypeInt64, value: NewInt64Value(count)}, nil

	case ExprSum:
		var sum int64
		for _, member := range members {
			value, declared, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("sum reads %q, absent on a member", expr.Field)
			}
			if declared != ValueInt64 || value.Kind() != ValueInt64 {
				return evaluated{}, fmt.Errorf("sum reads %q, which is not an int64", expr.Field)
			}
			intVal, ok := value.Int64()
			if !ok {
				return evaluated{}, fmt.Errorf("sum reads %q, invalid int64", expr.Field)
			}
			next := sum + intVal
			if (intVal > 0 && next < sum) || (intVal < 0 && next > sum) {
				return evaluated{}, fmt.Errorf("sum overflows int64")
			}
			sum = next
		}
		return evaluated{kind: TypeInt64, value: NewInt64Value(sum)}, nil

	case ExprMin:
		var minVal int64
		for i, member := range members {
			value, declared, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("min reads %q, absent on a member", expr.Field)
			}
			if declared != ValueInt64 || value.Kind() != ValueInt64 {
				return evaluated{}, fmt.Errorf("min reads %q, which is not an int64", expr.Field)
			}
			intVal, ok := value.Int64()
			if !ok {
				return evaluated{}, fmt.Errorf("min reads %q, invalid int64", expr.Field)
			}
			if i == 0 || intVal < minVal {
				minVal = intVal
			}
		}
		return evaluated{kind: TypeInt64, value: NewInt64Value(minVal)}, nil

	case ExprMax:
		var maxVal int64
		for i, member := range members {
			value, declared, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("max reads %q, absent on a member", expr.Field)
			}
			if declared != ValueInt64 || value.Kind() != ValueInt64 {
				return evaluated{}, fmt.Errorf("max reads %q, which is not an int64", expr.Field)
			}
			intVal, ok := value.Int64()
			if !ok {
				return evaluated{}, fmt.Errorf("max reads %q, invalid int64", expr.Field)
			}
			if i == 0 || intVal > maxVal {
				maxVal = intVal
			}
		}
		return evaluated{kind: TypeInt64, value: NewInt64Value(maxVal)}, nil

	case ExprNot:
		op, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if op.kind != TypeBool {
			return evaluated{}, fmt.Errorf("not requires bool, got %s", op.kind)
		}
		return evaluated{kind: TypeBool, boolean: !op.boolean}, nil

	case ExprAll:
		for i := range expr.Args {
			op, err := evaluateGroupNode(schema, expr.Args[i], members, boundMember)
			if err != nil {
				return evaluated{}, err
			}
			if op.kind != TypeBool {
				return evaluated{}, fmt.Errorf("boolean composition requires bool, argument %d is %s", i, op.kind)
			}
			if !op.boolean {
				return evaluated{kind: TypeBool, boolean: false}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: true}, nil

	case ExprAny:
		for i := range expr.Args {
			op, err := evaluateGroupNode(schema, expr.Args[i], members, boundMember)
			if err != nil {
				return evaluated{}, err
			}
			if op.kind != TypeBool {
				return evaluated{}, fmt.Errorf("boolean composition requires bool, argument %d is %s", i, op.kind)
			}
			if op.boolean {
				return evaluated{kind: TypeBool, boolean: true}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: false}, nil

	case ExprEqual:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind != right.kind {
			return evaluated{}, fmt.Errorf("equal compares %s with %s", left.kind, right.kind)
		}
		if left.kind == TypeBool {
			return evaluated{kind: TypeBool, boolean: left.boolean == right.boolean}, nil
		}
		return evaluated{kind: TypeBool, boolean: left.value.Equal(right.value)}, nil

	case ExprLess:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		leftInt, leftOK := left.value.Int64()
		rightInt, rightOK := right.value.Int64()
		if left.kind != TypeInt64 || right.kind != TypeInt64 || !leftOK || !rightOK {
			return evaluated{}, fmt.Errorf("less requires int64, got %s and %s", left.kind, right.kind)
		}
		return evaluated{kind: TypeBool, boolean: leftInt < rightInt}, nil

	case ExprAdd:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		leftInt, leftOK := left.value.Int64()
		rightInt, rightOK := right.value.Int64()
		if left.kind != TypeInt64 || right.kind != TypeInt64 || !leftOK || !rightOK {
			return evaluated{}, fmt.Errorf("add requires int64, got %s and %s", left.kind, right.kind)
		}
		sum := leftInt + rightInt
		if (rightInt > 0 && sum < leftInt) || (rightInt < 0 && sum > leftInt) {
			return evaluated{}, fmt.Errorf("add overflows int64")
		}
		return evaluated{kind: TypeInt64, value: NewInt64Value(sum)}, nil

	default:
		return evaluated{}, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}
