package semantic

import (
	"fmt"
	"strings"
)

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
		fieldT, err := valueKindType(declared)
		if err != nil {
			return TypeInvalid, err
		}
		if expr.Kind == ExprSum {
			if fieldT != TypeInt64 && fieldT != TypeDecimal && fieldT != TypeDuration {
				return TypeInvalid, fmt.Errorf("sum requires int64, decimal, or duration field, got %s", fieldT)
			}
			return fieldT, nil
		}
		if fieldT != TypeInt64 && fieldT != TypeDecimal && fieldT != TypeDuration && fieldT != TypeTimestamp && fieldT != TypeDate {
			return TypeInvalid, fmt.Errorf("%s requires comparable field, got %s", expr.Kind, fieldT)
		}
		return fieldT, nil

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
		if left != right {
			return TypeInvalid, fmt.Errorf("less compares %s with %s", left, right)
		}
		if left != TypeInt64 && left != TypeDecimal && left != TypeDuration && left != TypeTimestamp && left != TypeDate {
			return TypeInvalid, fmt.Errorf("less requires ordered type on both sides, got %s", left)
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
		if left == TypeInt64 && right == TypeInt64 {
			return TypeInt64, nil
		}
		if left == TypeDecimal && right == TypeDecimal {
			return TypeDecimal, nil
		}
		if left == TypeDuration && right == TypeDuration {
			return TypeDuration, nil
		}
		if (left == TypeTimestamp && right == TypeDuration) || (left == TypeDuration && right == TypeTimestamp) {
			return TypeTimestamp, nil
		}
		return TypeInvalid, fmt.Errorf("add unsupported operand types %s and %s", left, right)

	case ExprSubtract:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left == TypeInt64 && right == TypeInt64 {
			return TypeInt64, nil
		}
		if left == TypeDecimal && right == TypeDecimal {
			return TypeDecimal, nil
		}
		if left == TypeDuration && right == TypeDuration {
			return TypeDuration, nil
		}
		if left == TypeTimestamp && right == TypeTimestamp {
			return TypeDuration, nil
		}
		if left == TypeTimestamp && right == TypeDuration {
			return TypeTimestamp, nil
		}
		return TypeInvalid, fmt.Errorf("subtract unsupported operand types %s and %s", left, right)

	case ExprMultiply:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left == TypeInt64 && right == TypeInt64 {
			return TypeInt64, nil
		}
		if left == TypeDecimal && right == TypeDecimal {
			return TypeDecimal, nil
		}
		if (left == TypeDuration && right == TypeInt64) || (left == TypeInt64 && right == TypeDuration) {
			return TypeDuration, nil
		}
		return TypeInvalid, fmt.Errorf("multiply unsupported operand types %s and %s", left, right)

	case ExprDivide:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left == TypeInt64 && right == TypeInt64 {
			return TypeInt64, nil
		}
		if left == TypeDecimal && right == TypeDecimal {
			return TypeDecimal, nil
		}
		return TypeInvalid, fmt.Errorf("divide unsupported operand types %s and %s", left, right)

	case ExprModulo:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left == TypeInt64 && right == TypeInt64 {
			return TypeInt64, nil
		}
		return TypeInvalid, fmt.Errorf("modulo requires int64, got %s and %s", left, right)

	case ExprAbs:
		arg, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if arg == TypeInt64 || arg == TypeDecimal || arg == TypeDuration {
			return arg, nil
		}
		return TypeInvalid, fmt.Errorf("abs requires int64, decimal, or duration, got %s", arg)

	case ExprClamp:
		val, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		minVal, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		maxVal, err := checkExprInScope(schema, kind, expr.Args[2], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if val != minVal || val != maxVal {
			return TypeInvalid, fmt.Errorf("clamp arguments must have matching types, got %s, %s, %s", val, minVal, maxVal)
		}
		if val != TypeInt64 && val != TypeDecimal && val != TypeDuration && val != TypeTimestamp && val != TypeDate {
			return TypeInvalid, fmt.Errorf("clamp requires ordered type, got %s", val)
		}
		return val, nil

	case ExprTimestampAdd:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if (left == TypeTimestamp && right == TypeDuration) || (left == TypeDuration && right == TypeTimestamp) {
			return TypeTimestamp, nil
		}
		return TypeInvalid, fmt.Errorf("timestamp_add requires timestamp and duration, got %s and %s", left, right)

	case ExprTimestampDiff:
		left, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		right, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if left == TypeTimestamp && right == TypeTimestamp {
			return TypeDuration, nil
		}
		return TypeInvalid, fmt.Errorf("timestamp_diff requires timestamp on both sides, got %s and %s", left, right)

	case ExprDateExtract:
		unit := strings.ToLower(string(expr.Field))
		switch unit {
		case "year", "month", "day", "hour", "minute", "second", "day_of_week":
		default:
			return TypeInvalid, fmt.Errorf("unknown date extract unit %q", expr.Field)
		}
		arg, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if arg == TypeDate && (unit == "hour" || unit == "minute" || unit == "second") {
			return TypeInvalid, fmt.Errorf("date extract unit %q is not valid for date type", unit)
		}
		if arg != TypeTimestamp && arg != TypeDate {
			return TypeInvalid, fmt.Errorf("date_extract requires timestamp or date, got %s", arg)
		}
		return TypeInt64, nil

	case ExprConcat:
		for i := range expr.Args {
			arg, err := checkExprInScope(schema, kind, expr.Args[i], in, depth+1)
			if err != nil {
				return TypeInvalid, err
			}
			if arg != TypeString {
				return TypeInvalid, fmt.Errorf("concat requires string arguments, argument %d is %s", i, arg)
			}
		}
		return TypeString, nil

	case ExprSubstring:
		str, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		start, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		length, err := checkExprInScope(schema, kind, expr.Args[2], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if str != TypeString || start != TypeInt64 || length != TypeInt64 {
			return TypeInvalid, fmt.Errorf("substring requires (string, int64, int64), got (%s, %s, %s)", str, start, length)
		}
		return TypeString, nil

	case ExprTrim:
		arg, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if arg != TypeString {
			return TypeInvalid, fmt.Errorf("trim requires string, got %s", arg)
		}
		return TypeString, nil

	case ExprIf:
		cond, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if cond != TypeBool {
			return TypeInvalid, fmt.Errorf("if condition must be bool, got %s", cond)
		}
		thenT, err := checkExprInScope(schema, kind, expr.Args[1], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		elseT, err := checkExprInScope(schema, kind, expr.Args[2], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if thenT != elseT {
			return TypeInvalid, fmt.Errorf("if branches must have matching types, got %s and %s", thenT, elseT)
		}
		return thenT, nil

	case ExprCoalesce:
		firstT, err := checkExprInScope(schema, kind, expr.Args[0], in, depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		for i := 1; i < len(expr.Args); i++ {
			t, err := checkExprInScope(schema, kind, expr.Args[i], in, depth+1)
			if err != nil {
				return TypeInvalid, err
			}
			if t != firstT {
				return TypeInvalid, fmt.Errorf("coalesce arguments must have matching types, argument %d is %s, want %s", i, t, firstT)
			}
		}
		return firstT, nil

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

// evaluateGroupValue evaluates a group-scoped value expression (e.g. reductions, literals) over a group's members.
func evaluateGroupValue(schema Schema, expr Expr, members []Entity) (Value, error) {
	result, err := evaluateGroupNode(schema, expr, members, nil)
	if err != nil {
		return Value{}, err
	}
	if result.kind == TypeBool {
		return Value{}, fmt.Errorf("group value expression is bool, not a value")
	}
	if !result.value.Valid() {
		return Value{}, fmt.Errorf("group value expression yielded no value")
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
		var sumInt int64
		var sumDur int64
		var sumDec decimal
		var kind ValueKind
		for i, member := range members {
			value, declared, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("sum reads %q, absent on a member", expr.Field)
			}
			if i == 0 {
				kind = declared
			}
			switch kind {
			case ValueInt64:
				val, ok := value.Int64()
				if !ok {
					return evaluated{}, fmt.Errorf("sum reads %q, invalid int64", expr.Field)
				}
				next, err := addInt64(sumInt, val)
				if err != nil {
					return evaluated{}, fmt.Errorf("sum overflows int64")
				}
				sumInt = next
			case ValueDuration:
				val, ok := value.Duration()
				if !ok {
					return evaluated{}, fmt.Errorf("sum reads %q, invalid duration", expr.Field)
				}
				next, err := addInt64(sumDur, val)
				if err != nil {
					return evaluated{}, fmt.Errorf("sum overflows duration")
				}
				sumDur = next
			case ValueDecimal:
				d, ok := value.Decimal()
				if !ok {
					return evaluated{}, fmt.Errorf("sum reads %q, invalid decimal", expr.Field)
				}
				dec, _ := parseDecimal(d)
				next, err := sumDec.Add(dec)
				if err != nil {
					return evaluated{}, fmt.Errorf("sum overflows decimal: %w", err)
				}
				sumDec = next
			default:
				return evaluated{}, fmt.Errorf("sum reads unsupported kind %d", kind)
			}
		}
		switch kind {
		case ValueInt64:
			return evaluated{kind: TypeInt64, value: NewInt64Value(sumInt)}, nil
		case ValueDuration:
			return evaluated{kind: TypeDuration, value: NewDurationValue(sumDur)}, nil
		case ValueDecimal:
			val, _ := NewDecimalValue(sumDec.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		default:
			return evaluated{}, fmt.Errorf("sum reads unsupported kind %d", kind)
		}

	case ExprMin, ExprMax:
		var bestVal Value
		var bestType ExprType
		for i, member := range members {
			value, declared, present, err := boundField(schema, expr.Field, member)
			if err != nil {
				return evaluated{}, err
			}
			if !present {
				return evaluated{}, fmt.Errorf("%s reads %q, absent on a member", expr.Kind, expr.Field)
			}
			t, err := valueKindType(declared)
			if err != nil {
				return evaluated{}, err
			}
			if i == 0 {
				bestVal = value
				bestType = t
			} else {
				isLess, err := valueLess(value, bestVal, t)
				if err != nil {
					return evaluated{}, err
				}
				if expr.Kind == ExprMin && isLess {
					bestVal = value
				} else if expr.Kind == ExprMax && !isLess && !value.Equal(bestVal) {
					bestVal = value
				}
			}
		}
		return evaluated{kind: bestType, value: bestVal}, nil

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
		if left.kind != right.kind {
			return evaluated{}, fmt.Errorf("less compares %s with %s", left.kind, right.kind)
		}
		isLess, err := valueLess(left.value, right.value, left.kind)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: isLess}, nil

	case ExprAdd:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			sum, err := addInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(sum)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Add(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeDuration {
			sum, err := addInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(sum)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			res, err := ts.AddDuration(right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeTimestamp {
			ts, _ := parseTimestamp(right.value.text)
			res, err := ts.AddDuration(left.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("add unsupported on %s and %s", left.kind, right.kind)

	case ExprSubtract:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			diff, err := subInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(diff)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Sub(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeDuration {
			diff, err := subInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeTimestamp {
			ts1, _ := parseTimestamp(left.value.text)
			ts2, _ := parseTimestamp(right.value.text)
			diff, err := ts1.Diff(ts2)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			dur := right.value.integer
			if dur == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("duration %d overflows negation", dur)
			}
			res, err := ts.AddDuration(-dur)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("subtract unsupported on %s and %s", left.kind, right.kind)

	case ExprMultiply:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(prod)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Mul(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeInt64 {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(prod)}, nil
		}
		if left.kind == TypeInt64 && right.kind == TypeDuration {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(prod)}, nil
		}
		return evaluated{}, fmt.Errorf("multiply unsupported on %s and %s", left.kind, right.kind)

	case ExprDivide:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			if right.value.integer == 0 {
				return evaluated{}, fmt.Errorf("division by zero")
			}
			if left.value.integer == -9223372036854775808 && right.value.integer == -1 {
				return evaluated{}, fmt.Errorf("division overflows int64")
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(left.value.integer / right.value.integer)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Div(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("divide unsupported on %s and %s", left.kind, right.kind)

	case ExprModulo:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			if right.value.integer == 0 {
				return evaluated{}, fmt.Errorf("modulo by zero")
			}
			if left.value.integer == -9223372036854775808 && right.value.integer == -1 {
				return evaluated{}, fmt.Errorf("modulo overflows int64")
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(left.value.integer % right.value.integer)}, nil
		}
		return evaluated{}, fmt.Errorf("modulo requires int64")

	case ExprAbs:
		arg, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		switch arg.kind {
		case TypeInt64:
			if arg.value.integer == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("abs overflows int64")
			}
			v := arg.value.integer
			if v < 0 {
				v = -v
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(v)}, nil
		case TypeDuration:
			if arg.value.integer == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("abs overflows duration")
			}
			v := arg.value.integer
			if v < 0 {
				v = -v
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(v)}, nil
		case TypeDecimal:
			d, _ := parseDecimal(arg.value.text)
			res := d.Abs()
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		default:
			return evaluated{}, fmt.Errorf("abs unsupported on %s", arg.kind)
		}

	case ExprClamp:
		val, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		minVal, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		maxVal, err := evaluateGroupNode(schema, expr.Args[2], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if val.kind != minVal.kind || val.kind != maxVal.kind {
			return evaluated{}, fmt.Errorf("clamp types mismatch: %s, %s, %s", val.kind, minVal.kind, maxVal.kind)
		}
		switch val.kind {
		case TypeInt64, TypeDuration, TypeTimestamp, TypeDate:
			if maxVal.value.integer < minVal.value.integer {
				return evaluated{}, fmt.Errorf("clamp min %d is greater than max %d", minVal.value.integer, maxVal.value.integer)
			}
			clamped := val.value.integer
			if clamped < minVal.value.integer {
				return minVal, nil
			}
			if clamped > maxVal.value.integer {
				return maxVal, nil
			}
			return val, nil
		case TypeDecimal:
			dVal, _ := parseDecimal(val.value.text)
			dMin, _ := parseDecimal(minVal.value.text)
			dMax, _ := parseDecimal(maxVal.value.text)
			res, err := dVal.Clamp(dMin, dMax)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: v}, nil
		default:
			return evaluated{}, fmt.Errorf("clamp unsupported on %s", val.kind)
		}

	case ExprTimestampAdd:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			res, err := ts.AddDuration(right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: v}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeTimestamp {
			ts, _ := parseTimestamp(right.value.text)
			res, err := ts.AddDuration(left.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: v}, nil
		}
		return evaluated{}, fmt.Errorf("timestamp_add requires timestamp and duration")

	case ExprTimestampDiff:
		left, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		right, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeTimestamp && right.kind == TypeTimestamp {
			ts1, _ := parseTimestamp(left.value.text)
			ts2, _ := parseTimestamp(right.value.text)
			diff, err := ts1.Diff(ts2)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		return evaluated{}, fmt.Errorf("timestamp_diff requires timestamps")

	case ExprDateExtract:
		arg, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		unit := string(expr.Field)
		if arg.kind == TypeTimestamp {
			ts, _ := parseTimestamp(arg.value.text)
			extracted, err := ts.DateExtract(unit)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(extracted)}, nil
		}
		if arg.kind == TypeDate {
			d, _ := parseDate(arg.value.text)
			extracted, err := d.DateExtract(unit)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(extracted)}, nil
		}
		return evaluated{}, fmt.Errorf("date_extract requires timestamp or date")

	case ExprConcat:
		var buf strings.Builder
		for _, arg := range expr.Args {
			res, err := evaluateGroupNode(schema, arg, members, boundMember)
			if err != nil {
				return evaluated{}, err
			}
			if res.kind != TypeString {
				return evaluated{}, fmt.Errorf("concat requires string, got %s", res.kind)
			}
			buf.WriteString(res.value.text)
		}
		val, err := NewStringValue(buf.String())
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeString, value: val}, nil

	case ExprSubstring:
		strRes, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		startRes, err := evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		lenRes, err := evaluateGroupNode(schema, expr.Args[2], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if strRes.kind != TypeString || startRes.kind != TypeInt64 || lenRes.kind != TypeInt64 {
			return evaluated{}, fmt.Errorf("substring requires (string, int64, int64)")
		}
		start := startRes.value.integer
		length := lenRes.value.integer
		if start < 0 || length < 0 {
			return evaluated{}, fmt.Errorf("substring start and length must be non-negative")
		}
		runes := []rune(strRes.value.text)
		runeCount := int64(len(runes))
		if start >= runeCount {
			val, _ := NewStringValue("")
			return evaluated{kind: TypeString, value: val}, nil
		}
		end := runeCount
		if length < runeCount-start {
			end = start + length
		}
		val, _ := NewStringValue(string(runes[start:end]))
		return evaluated{kind: TypeString, value: val}, nil

	case ExprTrim:
		strRes, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if strRes.kind != TypeString {
			return evaluated{}, fmt.Errorf("trim requires string, got %s", strRes.kind)
		}
		val, _ := NewStringValue(strings.TrimSpace(strRes.value.text))
		return evaluated{kind: TypeString, value: val}, nil

	case ExprIf:
		cond, err := evaluateGroupNode(schema, expr.Args[0], members, boundMember)
		if err != nil {
			return evaluated{}, err
		}
		if cond.kind != TypeBool {
			return evaluated{}, fmt.Errorf("if condition must be bool")
		}
		if cond.boolean {
			return evaluateGroupNode(schema, expr.Args[1], members, boundMember)
		}
		return evaluateGroupNode(schema, expr.Args[2], members, boundMember)

	case ExprCoalesce:
		var lastErr error
		for _, arg := range expr.Args {
			res, err := evaluateGroupNode(schema, arg, members, boundMember)
			if err == nil {
				return res, nil
			}
			lastErr = err
		}
		return evaluated{}, fmt.Errorf("coalesce failed: %w", lastErr)

	default:
		return evaluated{}, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}

func valueLess(a, b Value, t ExprType) (bool, error) {
	switch t {
	case TypeInt64, TypeDuration, TypeTimestamp, TypeDate:
		return a.integer < b.integer, nil
	case TypeString:
		return a.text < b.text, nil
	case TypeDecimal:
		d1, _ := parseDecimal(a.text)
		d2, _ := parseDecimal(b.text)
		return d1.Less(d2), nil
	default:
		return false, fmt.Errorf("type %s is not ordered", t)
	}
}

// checkRelationGuard type-checks relation guard expressions over (fromKind, toKind) endpoints.
func checkRelationGuard(
	schema Schema, fromKind, toKind EntityKind, expr Expr, depth int,
) (ExprType, error) {
	if depth > maxExprDepth {
		return TypeInvalid, fmt.Errorf("expression nests deeper than %d", maxExprDepth)
	}

	// Validate and rewrite from. and to. aliases into concrete entity kinds
	var mapFieldPaths func(e Expr) (Expr, error)
	mapFieldPaths = func(e Expr) (Expr, error) {
		cloned := e
		if e.Field != "" {
			k, name := splitFieldPath(e.Field)
			if k == "" || name == "" {
				return Expr{}, fmt.Errorf("malformed field path %q", e.Field)
			}
			if k == "from" {
				realPath := FieldPath(string(fromKind) + "." + string(name))
				if _, isDeclared := schema.fieldKind(realPath); !isDeclared {
					return Expr{}, fmt.Errorf("from endpoint reads undeclared field %q", realPath)
				}
				cloned.Field = realPath
			} else if k == "to" {
				realPath := FieldPath(string(toKind) + "." + string(name))
				if _, isDeclared := schema.fieldKind(realPath); !isDeclared {
					return Expr{}, fmt.Errorf("to endpoint reads undeclared field %q", realPath)
				}
				cloned.Field = realPath
			} else if fromKind == toKind && k == fromKind {
				return Expr{}, fmt.Errorf(
					"field %q is ambiguous for same-kind relation endpoints (%s -> %s); use from.%s or to.%s",
					e.Field, fromKind, toKind, name, name)
			} else if fromKind != toKind {
				if k == fromKind {
					if _, isDeclared := schema.fieldKind(e.Field); !isDeclared {
						return Expr{}, fmt.Errorf("from endpoint reads undeclared field %q", e.Field)
					}
				} else if k == toKind {
					if _, isDeclared := schema.fieldKind(e.Field); !isDeclared {
						return Expr{}, fmt.Errorf("to endpoint reads undeclared field %q", e.Field)
					}
				} else {
					return Expr{}, fmt.Errorf("field %q does not match relation endpoints %s or %s", e.Field, fromKind, toKind)
				}
			} else {
				return Expr{}, fmt.Errorf("field %q does not match relation endpoints %s or %s", e.Field, fromKind, toKind)
			}
		}

		if len(e.Args) > 0 {
			newArgs := make([]Expr, len(e.Args))
			for i, arg := range e.Args {
				mapped, err := mapFieldPaths(arg)
				if err != nil {
					return Expr{}, err
				}
				newArgs[i] = mapped
			}
			cloned.Args = newArgs
		}
		return cloned, nil
	}

	mappedExpr, err := mapFieldPaths(expr)
	if err != nil {
		return TypeInvalid, err
	}

	return checkExprInScope(schema, "", mappedExpr, memberScope, depth)
}
