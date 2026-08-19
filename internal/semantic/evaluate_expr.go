package semantic

import "fmt"

// Evaluation of an expression against ONE BOUND ENTITY.
//
// This is the smallest evaluation that means anything, and it is why selection and evaluation
// arrive together. Slice 1 settled that there is no ambient scope, so a Field path names an
// entity kind and no instance; until something binds an instance, no expression denotes a
// value. A selector binds one. Evaluating with no binding at all was the ordering this
// programme rejected, and this is not that: the bound entity IS the referent, supplied by the
// caller and never inferred.
//
// Everything here is total and deterministic. There is no clock, no map iteration, no
// ordering that depends on anything but the expression, and absence is refused rather than
// defaulted -- a missing field yields an error, never a zero.

// evaluated is one expression's result. It is not a Value because an expression may be bool
// and Value has no bool variant.
type evaluated struct {
	kind    ExprType
	value   Value
	boolean bool
}

// evaluateBool evaluates an expression that must be bool.
func evaluateBool(expr Expr, entity Entity) (bool, error) {
	result, err := evaluateExpr(expr, entity)
	if err != nil {
		return false, err
	}
	if result.kind != TypeBool {
		// Unreachable through a compiled selector, which checks the type. Refused rather than
		// coerced so that a future caller reaching this directly cannot get a silent answer.
		return false, fmt.Errorf("expression is %s, not bool", result.kind)
	}
	return result.boolean, nil
}

// evaluateValue evaluates an expression that must yield a Value.
func evaluateValue(expr Expr, entity Entity) (Value, error) {
	result, err := evaluateExpr(expr, entity)
	if err != nil {
		return Value{}, err
	}
	if result.kind == TypeBool {
		return Value{}, fmt.Errorf("expression is bool, not a value")
	}
	if !result.value.Valid() {
		// A zero value with no error is the zero-value-permits shape this codebase forbids.
		return Value{}, fmt.Errorf("expression yielded no value")
	}
	return result.value, nil
}

// evaluateExpr walks one expression against the bound entity.
func evaluateExpr(expr Expr, entity Entity) (evaluated, error) {
	// TOTALITY, enforced rather than claimed. The header says this function is total, and an
	// earlier version was not: Expr{Kind: ExprNot} indexed Args[0] and panicked, as did every
	// binary kind, while the sibling arms all guarded exactly this class of un-compiled input.
	// checkOperandShape is the compiler's own check, reused here so the two cannot disagree
	// about what a kind's operands are.
	if err := checkOperandShape(expr); err != nil {
		return evaluated{}, err
	}
	switch expr.Kind {
	case ExprLiteral:
		if expr.Literal == nil {
			return evaluated{}, fmt.Errorf("literal carries no value")
		}
		return evaluated{kind: literalKindOf(*expr.Literal), value: *expr.Literal}, nil

	case ExprField:
		value, present, err := boundField(expr.Field, entity)
		if err != nil {
			return evaluated{}, err
		}
		if !present {
			// ABSENCE IS REFUSED, NOT DEFAULTED. Yielding a zero here would make
			// less(driver.hours, 10) true for an entity with no hours at all, which is a
			// claim about the world nobody made. An author who means "present and below"
			// writes all(exists(f), less(f, 10)), which works because all short circuits --
			// see ExprAll. The refusal keeps that distinction the author's to make; the
			// short circuit keeps it expressible.
			return evaluated{}, fmt.Errorf("field %q is absent", expr.Field)
		}
		return evaluated{kind: literalKindOf(value), value: value}, nil

	case ExprExists:
		_, present, err := boundField(expr.Field, entity)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: present}, nil

	case ExprNot:
		operand, err := evaluateBool(expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: !operand}, nil

	case ExprAll:
		// SHORT CIRCUITS, and an earlier version deliberately did not. That version argued
		// determinism: an absent field should be refused wherever it appears rather than
		// depending on the order the author wrote the conjuncts in.
		//
		// The argument was wrong twice over. Short-circuit evaluation is deterministic --
		// it depends on authored order, which is content this kernel already treats as
		// semantic and preserves in the canonical bytes, not on anything ambient. And
		// without it, absence-tolerant predicates are UNWRITABLE: absence is refused, so
		// all(exists(f), less(f, 10)) evaluates less() on the absent field and errors, and
		// so does any(not(exists(f)), less(f, 10)). One sparse entity then fails the whole
		// selection. Real inputs are sparse, so a language that cannot say "present and
		// below" is not a language for them.
		for i := range expr.Args {
			operand, err := evaluateBool(expr.Args[i], entity)
			if err != nil {
				return evaluated{}, err
			}
			if !operand {
				return evaluated{kind: TypeBool, boolean: false}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: true}, nil

	case ExprAny:
		for i := range expr.Args {
			operand, err := evaluateBool(expr.Args[i], entity)
			if err != nil {
				return evaluated{}, err
			}
			if operand {
				return evaluated{kind: TypeBool, boolean: true}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: false}, nil

	case ExprEqual:
		left, right, err := evaluatePair(expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind != right.kind {
			return evaluated{}, fmt.Errorf("equal compares %s with %s", left.kind, right.kind)
		}
		// Bool operands compare through .boolean, not .value. A bool result carries the zero
		// Value, so comparing values here answered false for every pair of booleans -- a
		// total, deterministic wrong answer: equal(exists(f), exists(f)) was false even when
		// f was present. Two checks each correct against their own reference, and neither
		// owning the fact that TypeBool has no value.
		if left.kind == TypeBool {
			return evaluated{kind: TypeBool, boolean: left.boolean == right.boolean}, nil
		}
		return evaluated{kind: TypeBool, boolean: left.value.Equal(right.value)}, nil

	case ExprLess:
		left, right, err := evaluateInt64Pair(expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: left < right}, nil

	case ExprAdd:
		left, right, err := evaluateInt64Pair(expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		// OVERFLOW IS REFUSED. Go wraps on int64 overflow, and a wrapped sum is a wrong
		// answer the kernel would seal into a checkpoint and hash. HLD §8 says bounded
		// arithmetic; this is where the bound is enforced rather than assumed.
		sum := left + right
		if (right > 0 && sum < left) || (right < 0 && sum > left) {
			return evaluated{}, fmt.Errorf("add overflows int64")
		}
		return evaluated{kind: TypeInt64, value: NewInt64Value(sum)}, nil

	default:
		return evaluated{}, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}

// evaluatePair evaluates both operands of a binary node.
func evaluatePair(expr Expr, entity Entity) (evaluated, evaluated, error) {
	left, err := evaluateExpr(expr.Args[0], entity)
	if err != nil {
		return evaluated{}, evaluated{}, err
	}
	right, err := evaluateExpr(expr.Args[1], entity)
	if err != nil {
		return evaluated{}, evaluated{}, err
	}
	return left, right, nil
}

// evaluateInt64Pair evaluates both operands and requires them to be int64.
func evaluateInt64Pair(expr Expr, entity Entity) (int64, int64, error) {
	left, right, err := evaluatePair(expr, entity)
	if err != nil {
		return 0, 0, err
	}
	leftInt, leftOK := left.value.Int64()
	rightInt, rightOK := right.value.Int64()
	if left.kind != TypeInt64 || right.kind != TypeInt64 || !leftOK || !rightOK {
		return 0, 0, fmt.Errorf("arithmetic requires int64, got %s and %s", left.kind, right.kind)
	}
	return leftInt, rightInt, nil
}

// boundField reads a field from the bound entity, refusing a path that names another kind.
//
// A compiled selector has already established that every path names the selected kind, so
// this cannot fire through that route. It is checked anyway because the evaluator is reachable
// without one and must not read a field off an entity the path does not describe.
func boundField(path FieldPath, entity Entity) (Value, bool, error) {
	kind, name := splitFieldPath(path)
	if kind == "" || name == "" {
		return Value{}, false, fmt.Errorf("malformed field path %q", path)
	}
	if kind != entity.Ref().Kind {
		return Value{}, false, fmt.Errorf(
			"path %q does not name the bound entity kind %q", path, entity.Ref().Kind)
	}
	value, present := entity.Field(name)
	return value, present, nil
}

// literalKindOf maps a value's kind into the expression vocabulary, without the error path
// that valueKindType needs at compile time: a Value that reached evaluation was already
// validated when its entity or literal was built.
func literalKindOf(value Value) ExprType {
	switch value.Kind() {
	case ValueString:
		return TypeString
	case ValueAtom:
		return TypeAtom
	case ValueInt64:
		return TypeInt64
	default:
		return TypeInvalid
	}
}
